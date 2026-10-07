package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Store manages discovery and watching of Claude Code sessions.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session // keyed by session ID
	baseDir  string
	watcher  *fsnotify.Watcher
	updates  chan struct{} // signals that sessions have changed

	ocDBs      map[string]time.Time // tracked OpenCode DBs: path → last mtime
	ocExtraDBs []string             // explicitly specified OpenCode DB paths
}

// NewStore creates a session store that scans ~/.claude/projects/.
func NewStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	baseDir := filepath.Join(homeDir, ".claude", "projects")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	s := &Store{
		sessions: make(map[string]*Session),
		baseDir:  baseDir,
		watcher:  watcher,
		updates:  make(chan struct{}, 1),
		ocDBs:    make(map[string]time.Time),
	}

	return s, nil
}

// AddOpenCodeDB adds an explicit OpenCode database path to scan.
func (s *Store) AddOpenCodeDB(path string) {
	s.ocExtraDBs = append(s.ocExtraDBs, path)
}

// Scan discovers all sessions from the Claude projects directory.
func (s *Store) Scan() error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectDir := filepath.Join(s.baseDir, entry.Name())

		// Watch this project directory for changes
		_ = s.watcher.Add(projectDir)

		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		for _, f := range files {
			// A project directory holds its sessions as flat .jsonl files, and
			// one directory per session carrying that session's subagent runs.
			if f.IsDir() {
				s.scanSubagents(filepath.Join(projectDir, f.Name()))
				continue
			}
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}

			path := filepath.Join(projectDir, f.Name())
			s.load(path)
		}
	}

	// Scan for OpenCode databases
	s.scanOpenCodeDBs()

	// Every transcript is loaded by now, so subagent runs can be anchored back
	// into the sessions that launched them.
	s.linkSubagents()

	return nil
}

// linkSubagents splices each subagent run into its parent's timeline, at the
// Task call that launched it.
//
// Claude Code writes a background agent's turns to its own file rather than
// inline, so a parent transcript on its own shows a Task call and nothing about
// what the agent then did. Folding the turns in gives one timeline per piece of
// work — the same shape OpenCode sessions already get.
//
// The subagent also stays in the session list in its own right: it is useful to
// see what one run cost. Only its events are copied, never its tokens or cost,
// so a project total still counts them exactly once.
func (s *Store) linkSubagents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Group children by parent so one parent is spliced in a single pass.
	children := make(map[string][]*Session)
	for _, sess := range s.sessions {
		if sess.Info.IsAgent && sess.Info.ParentSessionID != "" {
			children[sess.Info.ParentSessionID] = append(
				children[sess.Info.ParentSessionID], sess)
		}
	}

	for parentID, kids := range children {
		parent := s.sessions[parentID]
		if parent == nil {
			continue // the parent transcript is gone; the run stands alone
		}

		// agent id -> the Task tool_use that started it.
		anchors := make(map[string]string, len(parent.SubagentLaunches))
		for toolUseID, agentID := range parent.SubagentLaunches {
			anchors[agentID] = toolUseID
		}

		type insert struct {
			at     int
			events []Event
		}
		var inserts []insert

		for _, kid := range kids {
			agentID := strings.TrimPrefix(kid.Info.ID, "agent-")
			if alreadySpliced(parent, agentID) {
				continue
			}

			events := make([]Event, len(kid.Events))
			copy(events, kid.Events)
			for i := range events {
				events[i].IsSidechain = true
				if events[i].AgentID == "" {
					events[i].AgentID = agentID
				}
			}

			at := spliceIndex(parent, anchors[agentID], kid)
			inserts = append(inserts, insert{at: at, events: events})
			parent.Info.SubagentEvents += len(events)
		}
		if len(inserts) == 0 {
			continue
		}

		// Insert from the back so an earlier insertion cannot shift a later
		// index out from under us.
		sort.Slice(inserts, func(i, j int) bool { return inserts[i].at > inserts[j].at })
		for _, ins := range inserts {
			rest := append([]Event{}, parent.Events[ins.at:]...)
			parent.Events = append(parent.Events[:ins.at], ins.events...)
			parent.Events = append(parent.Events, rest...)
		}
		parent.Info.EventCount = len(parent.Events)
	}
}

// alreadySpliced reports whether this agent's turns are in the parent already,
// so a rescan cannot duplicate them.
func alreadySpliced(parent *Session, agentID string) bool {
	for i := range parent.Events {
		if parent.Events[i].IsSidechain && parent.Events[i].AgentID == agentID {
			return true
		}
	}
	return false
}

// spliceIndex is where a subagent's turns belong in its parent's timeline:
// immediately after the Task call that launched it, or failing that, in
// timestamp order.
func spliceIndex(parent *Session, toolUseID string, kid *Session) int {
	if toolUseID != "" {
		for i := range parent.Events {
			if parent.Events[i].Type == EventToolUse &&
				parent.Events[i].ToolID == toolUseID {
				return i + 1
			}
		}
	}

	// No anchor — the result that names the agent may be missing. Fall back to
	// chronology rather than guessing a position.
	start := kid.Info.StartTime
	for i := range parent.Events {
		if parent.Events[i].Timestamp.After(start) {
			return i
		}
	}
	return len(parent.Events)
}

// scanSubagents reads the subagent runs recorded under one session's
// directory. Each is a transcript in its own right — the agent's prompt, every
// tool call it made, what it returned — so it is parsed and stored like any
// other session, keyed by its own agent id.
func (s *Store) scanSubagents(sessionDir string) {
	subagentDir := filepath.Join(sessionDir, "subagents")
	entries, err := os.ReadDir(subagentDir)
	if err != nil {
		return // no subagents for this session, which is the common case
	}

	_ = s.watcher.Add(subagentDir)

	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		s.load(filepath.Join(subagentDir, f.Name()))
	}
}

// load parses one transcript into the store, ignoring files that fail to parse
// or carry no events.
func (s *Store) load(path string) {
	sess, err := ParseSessionFile(path)
	if err != nil || len(sess.Events) == 0 {
		return
	}
	s.mu.Lock()
	s.sessions[sess.Info.ID] = sess
	s.mu.Unlock()
}

// scanOpenCodeDBs discovers and parses OpenCode databases. OpenCode keeps a
// single database under its data directory covering every project, so the
// global locations are the ones that matter; per-project databases are still
// checked for older layouts.
func (s *Store) scanOpenCodeDBs() {
	candidates := make(map[string]bool)

	for _, p := range s.ocExtraDBs {
		candidates[p] = true
	}
	for _, p := range DefaultOpenCodeDBs() {
		candidates[p] = true
	}

	// Legacy per-project databases, alongside the projects already known.
	s.mu.RLock()
	for _, sess := range s.sessions {
		if sess.Info.CWD != "" {
			candidates[filepath.Join(sess.Info.CWD, ".opencode", "opencode.db")] = true
		}
	}
	s.mu.RUnlock()

	if cwd, err := os.Getwd(); err == nil {
		candidates[filepath.Join(cwd, ".opencode", "opencode.db")] = true
	}

	for dbPath := range candidates {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}

		sessions, err := ParseOpenCodeDB(dbPath)
		if err != nil {
			continue
		}

		s.mu.Lock()
		s.ocDBs[dbPath] = info.ModTime()
		for _, sess := range sessions {
			s.sessions[sess.Info.ID] = sess
		}
		s.mu.Unlock()
	}
}

// Watch starts watching for file changes and re-parses modified sessions.
// Returns a channel that receives a signal whenever sessions are updated.
func (s *Store) Watch() <-chan struct{} {
	go func() {
		// Debounce timer to avoid re-parsing on every write
		var debounce *time.Timer

		for {
			select {
			case event, ok := <-s.watcher.Events:
				if !ok {
					return
				}
				if !strings.HasSuffix(event.Name, ".jsonl") {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}

				// Debounce: wait 500ms after last write before re-parsing
				if debounce != nil {
					debounce.Stop()
				}
				path := event.Name
				debounce = time.AfterFunc(500*time.Millisecond, func() {
					sess, err := ParseSessionFile(path)
					if err != nil || len(sess.Events) == 0 {
						return
					}
					s.mu.Lock()
					s.sessions[sess.Info.ID] = sess
					s.mu.Unlock()

					// Signal update (non-blocking)
					select {
					case s.updates <- struct{}{}:
					default:
					}
				})

			case _, ok := <-s.watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Poll OpenCode databases for changes
	go s.watchOpenCode()

	return s.updates
}

// watchOpenCode polls tracked OpenCode databases for mtime changes.
func (s *Store) watchOpenCode() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		changed := false

		s.mu.RLock()
		tracked := make(map[string]time.Time, len(s.ocDBs))
		for dbPath, mtime := range s.ocDBs {
			tracked[dbPath] = mtime
		}
		s.mu.RUnlock()

		for dbPath, lastMtime := range tracked {
			info, err := os.Stat(dbPath)
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMtime) {
				continue
			}

			sessions, err := ParseOpenCodeDB(dbPath)
			if err != nil {
				continue
			}

			s.mu.Lock()
			s.ocDBs[dbPath] = info.ModTime()
			for _, sess := range sessions {
				s.sessions[sess.Info.ID] = sess
			}
			s.mu.Unlock()
			changed = true
		}

		if changed {
			select {
			case s.updates <- struct{}{}:
			default:
			}
		}
	}
}

// GetSessions returns all sessions sorted by last update time (newest first).
func (s *Store) GetSessions() []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(s.sessions))
	for _, sess := range s.sessions {
		infos = append(infos, sess.Info)
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].LastUpdate.After(infos[j].LastUpdate)
	})

	return infos
}

// GetSession returns the full parsed session by ID.
func (s *Store) GetSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// GetProjectInfo returns aggregated project information for a given project directory.
func (s *Store) GetProjectInfo(projectDir string) *ProjectInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	proj := &ProjectInfo{
		ProjectDir: projectDir,
	}

	editCounts := make(map[string]*FileEditCount)
	proj.ToolCounts = make(map[string]int)
	var encodedDir string

	for _, sess := range s.sessions {
		info := sess.Info
		if info.ProjectDir != projectDir {
			continue
		}

		proj.TotalSessions++
		proj.TotalToolCalls += info.ToolCallCount
		proj.TotalUserPrompts += info.UserPrompts
		proj.TotalErrors += info.Errors
		proj.TotalInputTokens += info.InputTokens
		proj.TotalOutputTokens += info.OutputTokens
		proj.TotalCacheReadTokens += info.CacheReadTokens
		proj.TotalCacheWriteTokens += info.CacheWriteTokens
		proj.TotalCostUSD += info.CostUSD
		proj.TotalLinesAdded += info.LinesAdded
		proj.TotalLinesRemoved += info.LinesRemoved
		proj.TotalSubagentCalls += info.SubagentCalls
		proj.TotalDenials += info.Denials
		proj.TotalInterruptions += info.Interruptions
		for name, n := range info.ToolCounts {
			proj.ToolCounts[name] += n
		}

		if proj.FirstSession.IsZero() || info.StartTime.Before(proj.FirstSession) {
			proj.FirstSession = info.StartTime
		}
		if info.LastUpdate.After(proj.LastSession) {
			proj.LastSession = info.LastUpdate
		}

		if proj.ProjectName == "" {
			proj.ProjectName = info.ProjectName
		}
		// ProjectDir is decoded from the on-disk directory name, which cannot
		// distinguish path separators from hyphens. The CWD recorded in the
		// transcript is the real path.
		if proj.CWD == "" && info.CWD != "" {
			proj.CWD = info.CWD
		}
		if encodedDir == "" && info.FilePath != "" {
			encodedDir = filepath.Base(filepath.Dir(info.FilePath))
		}

		// Count file edits, carrying line churn through from each session
		for _, c := range info.FileChurns {
			e, ok := editCounts[c.Path]
			if !ok {
				e = &FileEditCount{Path: c.Path}
				editCounts[c.Path] = e
			}
			e.Count += c.Edits
			e.LinesAdded += c.LinesAdded
			e.LinesRemoved += c.LinesRemoved
		}

		proj.Sessions = append(proj.Sessions, info)
	}

	if proj.TotalSessions == 0 {
		return nil
	}

	proj.EncodedDir = encodedDir

	// Sort sessions by LastUpdate descending
	sort.Slice(proj.Sessions, func(i, j int) bool {
		return proj.Sessions[i].LastUpdate.After(proj.Sessions[j].LastUpdate)
	})

	// Build MostEditedFiles sorted desc by count
	for _, e := range editCounts {
		proj.MostEditedFiles = append(proj.MostEditedFiles, *e)
	}
	sort.Slice(proj.MostEditedFiles, func(i, j int) bool {
		a, b := proj.MostEditedFiles[i], proj.MostEditedFiles[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.LinesAdded+a.LinesRemoved > b.LinesAdded+b.LinesRemoved
	})
	if len(proj.MostEditedFiles) > 10 {
		proj.MostEditedFiles = proj.MostEditedFiles[:10]
	}

	// Read MEMORY.md
	if encodedDir != "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			memPath := filepath.Join(homeDir, ".claude", "projects", encodedDir, "memory", "MEMORY.md")
			data, err := os.ReadFile(memPath)
			if err == nil {
				proj.Memory = string(data)
			}
		}
	}

	return proj
}

// GetSessionTodos returns a session's todo items. OpenCode records them in the
// session database, so they are already attached; Claude Code keeps them in
// ~/.claude/todos/ and they are read from there.
func (s *Store) GetSessionTodos(sessionID string) []TodoItem {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess != nil && len(sess.Todos) > 0 {
		return sess.Todos
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	todosDir := filepath.Join(homeDir, ".claude", "todos")
	entries, err := os.ReadDir(todosDir)
	if err != nil {
		return nil
	}

	var todos []TodoItem
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, sessionID) || !strings.HasSuffix(name, ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(todosDir, name))
		if err != nil {
			continue
		}

		var items []TodoItem
		if err := json.Unmarshal(data, &items); err != nil {
			continue
		}
		todos = append(todos, items...)
	}

	return todos
}

// Close cleans up the file watcher.
func (s *Store) Close() error {
	return s.watcher.Close()
}
