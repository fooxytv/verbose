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

	// Codex is tracked by HOME rather than by database, because a session is
	// split across two of them and only the pair is meaningful.
	cxDirs      map[string]time.Time // tracked Codex homes: dir → newest mtime
	cxExtraDirs []string             // explicitly specified Codex homes

	// Copilot is tracked by home too: the database carries the metadata and
	// the transcript is a file per session beside it.
	cpDirs      map[string]time.Time // tracked Copilot homes: dir → last mtime
	cpExtraDirs []string             // explicitly specified Copilot homes

	// pi keeps a file per session like Claude Code, but under its own home.
	piDirs      map[string]time.Time // tracked pi homes: dir → newest mtime
	piExtraDirs []string             // explicitly specified pi homes

	// relinks rewrite the paths of sessions whose folder was renamed or moved,
	// applied as each one loads. See relink.go.
	relinks Relinks

	// redact masks secrets in every session as it is read. Set before Scan
	// and never changed after, which is what makes it safe to read without
	// the lock.
	redact bool
}

// prepare is everything that happens to a session between parsing it and
// filing it in the store.
//
// It exists because the relink rewrite alone was repeated at eight call sites
// — one per source, plus load and reload — and a SECOND thing to do per
// session is a second chance to miss one. It deliberately does not touch
// s.sessions, so it is safe to call with or without the lock held, which the
// call sites differ on.
func (s *Store) prepare(sess *Session) {
	s.relinks.Apply(sess)
	if s.redact {
		RedactSession(sess)
	}
}

// SetRedaction masks secrets in every session as it is read. Call it before
// Scan: sessions already in the store are not revisited, and the flag is read
// without the lock on the strength of that.
func (s *Store) SetRedaction(on bool) { s.redact = on }

// Redacting reports whether secrets are being masked, so a view can say so.
// Believing it is on when it is not is the whole failure mode.
func (s *Store) Redacting() bool { return s.redact }

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
		cxDirs:   make(map[string]time.Time),
		cpDirs:   make(map[string]time.Time),
		piDirs:   make(map[string]time.Time),
	}
	// A broken links file must not stop verbose opening: the sessions are
	// still readable, only with their recorded paths.
	s.relinks, _ = LoadRelinks()

	return s, nil
}

// AddOpenCodeDB adds an explicit OpenCode database path to scan.
func (s *Store) AddOpenCodeDB(path string) {
	s.ocExtraDBs = append(s.ocExtraDBs, path)
}

// AddCodexDir adds an explicit Codex home to scan.
func (s *Store) AddCodexDir(path string) {
	s.cxExtraDirs = append(s.cxExtraDirs, path)
}

// AddCopilotDir adds an explicit Copilot home to scan.
func (s *Store) AddCopilotDir(path string) {
	s.cpExtraDirs = append(s.cpExtraDirs, path)
}

// AddPiDir adds an explicit pi home to scan.
func (s *Store) AddPiDir(path string) {
	s.piExtraDirs = append(s.piExtraDirs, path)
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

	// And for Codex homes
	s.scanCodexDirs()

	// And for Copilot homes
	s.scanCopilotDirs()

	// And for pi homes
	s.scanPiDirs()

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
func (s *Store) linkSubagents() { s.linkSubagentsOf("") }

// linkSubagentsOf splices subagent runs into their parents. With a parent id it
// rebuilds just that one timeline, which is what a single file changing needs;
// with an empty id it does every parent, which is what a full scan needs.
func (s *Store) linkSubagentsOf(only string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Group children by parent so one parent is spliced in a single pass.
	children := make(map[string][]*Session)
	for _, sess := range s.sessions {
		if sess.Info.IsAgent && sess.Info.ParentSessionID != "" {
			if only != "" && sess.Info.ParentSessionID != only {
				continue
			}
			children[sess.Info.ParentSessionID] = append(
				children[sess.Info.ParentSessionID], sess)
		}
	}

	// Splice in a stable order, so a parent's timeline does not depend on map
	// iteration order from one reload to the next.
	for _, kids := range children {
		sort.Slice(kids, func(i, j int) bool { return kids[i].Info.ID < kids[j].Info.ID })
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

// reload re-parses one transcript after it changed, and restores the subagent
// links that a fresh parse drops.
//
// A parsed file holds only its own events, so re-parsing a parent throws away
// the subagent turns that were spliced into it, and re-parsing a subagent
// leaves its parent holding a stale copy. Either way the affected parent is
// rebuilt from disk and all of its children are spliced in again, which is
// cheap — one parent and its agents — and idempotent.
func (s *Store) reload(path string) {
	sess, err := ParseSessionFile(path)
	if err != nil || len(sess.Events) == 0 {
		return
	}
	s.prepare(sess)

	s.mu.Lock()
	s.sessions[sess.Info.ID] = sess

	// Which timeline this change belongs to: a subagent's events live in its
	// parent's, so that is the one to rebuild.
	parentID := sess.Info.ID
	parentPath := path
	if sess.Info.IsAgent && sess.Info.ParentSessionID != "" {
		parentID = sess.Info.ParentSessionID
		if p := s.sessions[parentID]; p != nil {
			parentPath = p.Info.FilePath
		}
	}
	s.mu.Unlock()

	// Re-parse the parent so it starts from a clean timeline. If it has gone
	// from disk, the subagent still stands on its own.
	if parentPath != path {
		if parent, err := ParseSessionFile(parentPath); err == nil && len(parent.Events) > 0 {
			s.prepare(parent)
			s.mu.Lock()
			s.sessions[parent.Info.ID] = parent
			s.mu.Unlock()
		}
	}

	s.linkSubagentsOf(parentID)

	// Signal update (non-blocking)
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// load parses one transcript into the store, ignoring files that fail to parse
// or carry no events.
func (s *Store) load(path string) {
	sess, err := ParseSessionFile(path)
	if err != nil || len(sess.Events) == 0 {
		return
	}
	s.prepare(sess)
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
			s.prepare(sess)
			s.sessions[sess.Info.ID] = sess
		}
		s.mu.Unlock()
	}
}

// scanCodexDirs discovers and parses Codex homes. Codex keeps one pair of
// databases under a single home covering every thread, so there is nothing
// per-project to look for.
func (s *Store) scanCodexDirs() {
	dirs := make(map[string]bool)
	for _, d := range s.cxExtraDirs {
		dirs[d] = true
	}
	for _, d := range DefaultCodexDirs() {
		dirs[d] = true
	}

	for dir := range dirs {
		s.loadCodexDir(dir)
	}
}

// loadCodexDir parses one Codex home into the store and records what it was
// read at. It reports whether anything was loaded, which is what the watcher
// turns into an update signal.
func (s *Store) loadCodexDir(dir string) bool {
	mtime, ok := codexMtime(dir)
	if !ok {
		return false
	}
	sessions, err := ParseCodexDir(dir)
	if err != nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cxDirs[dir] = mtime
	for _, sess := range sessions {
		s.prepare(sess)
		s.sessions[sess.Info.ID] = sess
	}
	return true
}

// codexMtime is the newest modification time of a home's two databases. Either
// one can be the one that moved: the metadata is written when a thread is
// created or renamed, the history as it runs.
func codexMtime(dir string) (time.Time, bool) {
	state, history, ok := CodexDBs(dir)
	if !ok {
		return time.Time{}, false
	}
	var newest time.Time
	for _, p := range []string{state, history} {
		info, err := os.Stat(p)
		if err != nil {
			return time.Time{}, false
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, true
}

// scanCopilotDirs discovers and parses Copilot homes.
func (s *Store) scanCopilotDirs() {
	dirs := make(map[string]bool)
	for _, d := range s.cpExtraDirs {
		dirs[d] = true
	}
	for _, d := range DefaultCopilotDirs() {
		dirs[d] = true
	}

	for dir := range dirs {
		s.loadCopilotDir(dir)
	}
}

// loadCopilotDir parses one Copilot home into the store and records what it
// was read at.
func (s *Store) loadCopilotDir(dir string) bool {
	mtime, ok := copilotMtime(dir)
	if !ok {
		return false
	}
	sessions, err := ParseCopilotDir(dir)
	if err != nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cpDirs[dir] = mtime
	for _, sess := range sessions {
		s.prepare(sess)
		s.sessions[sess.Info.ID] = sess
	}
	return true
}

// copilotMtime is the newest modification time of the database and of the
// session-state directory. The database alone is not enough: a live session
// appends to its events file for minutes before the database row is updated,
// and that is precisely the session worth reloading.
func copilotMtime(dir string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(dir, "session-store.db"))
	if err != nil {
		return time.Time{}, false
	}
	newest := info.ModTime()

	state := filepath.Join(dir, "session-state")
	if entries, err := os.ReadDir(state); err == nil {
		for _, e := range entries {
			fi, err := os.Stat(filepath.Join(state, e.Name(), "events.jsonl"))
			if err != nil {
				continue
			}
			if fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
	}
	return newest, true
}

// scanPiDirs discovers and parses pi homes.
func (s *Store) scanPiDirs() {
	dirs := make(map[string]bool)
	for _, d := range s.piExtraDirs {
		dirs[d] = true
	}
	for _, d := range DefaultPiDirs() {
		dirs[d] = true
	}

	for dir := range dirs {
		s.loadPiDir(dir)
	}
}

// loadPiDir parses one pi home into the store.
func (s *Store) loadPiDir(dir string) bool {
	mtime, ok := piMtime(dir)
	if !ok {
		return false
	}
	sessions, err := ParsePiDir(dir)
	if err != nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.piDirs[dir] = mtime
	for _, sess := range sessions {
		s.prepare(sess)
		s.sessions[sess.Info.ID] = sess
	}
	return true
}

// piMtime is the newest modification time among a home's session files. The
// sessions directory's own mtime only moves when a file is added, not when a
// live session is appended to — and a live session is the one worth noticing.
func piMtime(dir string) (time.Time, bool) {
	root := filepath.Join(dir, "agent", "sessions")
	projects, err := os.ReadDir(root)
	if err != nil {
		return time.Time{}, false
	}

	var newest time.Time
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
	}
	return newest, true
}

// How long to wait after a write before re-parsing, and the longest a file may
// stay dirty while being written to continuously. Without the second bound, an
// agent that writes more often than the debounce interval would never trigger a
// reload at all.
const (
	watchDebounce = 500 * time.Millisecond
	watchMaxDelay = 2 * time.Second
)

// Watch starts watching for file changes and re-parses modified sessions.
// Returns a channel that receives a signal whenever sessions are updated.
func (s *Store) Watch() <-chan struct{} {
	go func() {
		// One debounce timer PER FILE. A single shared timer meant a write to
		// any transcript cancelled the pending re-parse of another, so with two
		// sessions being written at once — or an agent writing its own file
		// beside its parent's — only the last one to be touched ever reloaded.
		debounce := make(map[string]*time.Timer)
		// When a file was first seen dirty, so that sustained writing cannot
		// postpone its reload indefinitely.
		firstDirty := make(map[string]time.Time)
		var mu sync.Mutex

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

				path := event.Name
				mu.Lock()
				if _, seen := firstDirty[path]; !seen {
					firstDirty[path] = time.Now()
				}
				// An agent writing steadily resets the timer on every line, so
				// a pure "quiet for 500ms" rule can starve: reload anyway once
				// the file has been dirty for watchMaxDelay.
				waited := time.Since(firstDirty[path])
				if t := debounce[path]; t != nil && waited < watchMaxDelay {
					t.Stop()
				}
				if waited < watchMaxDelay {
					debounce[path] = time.AfterFunc(watchDebounce, func() {
						mu.Lock()
						delete(firstDirty, path)
						delete(debounce, path)
						mu.Unlock()
						s.reload(path)
					})
					mu.Unlock()
					continue
				}
				delete(firstDirty, path)
				delete(debounce, path)
				mu.Unlock()
				s.reload(path)

			case _, ok := <-s.watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Poll OpenCode databases for changes
	go s.watchOpenCode()

	// And Codex homes, which are SQLite too and poll for the same reason
	go s.watchCodex()

	// And Copilot homes
	go s.watchCopilot()

	// And pi homes, whose files fsnotify would report but whose directory is
	// outside the watched tree
	go s.watchPi()

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
				s.prepare(sess)
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

// watchCodex polls tracked Codex homes for changes, the way OpenCode's
// databases are polled: SQLite does not notify usefully, and the write that
// matters lands in a -wal file the watcher would have to know about anyway.
func (s *Store) watchCodex() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		tracked := make(map[string]time.Time, len(s.cxDirs))
		for dir, mtime := range s.cxDirs {
			tracked[dir] = mtime
		}
		s.mu.RUnlock()

		changed := false
		for dir, lastMtime := range tracked {
			mtime, ok := codexMtime(dir)
			if !ok || !mtime.After(lastMtime) {
				continue
			}
			if s.loadCodexDir(dir) {
				changed = true
			}
		}

		if changed {
			select {
			case s.updates <- struct{}{}:
			default:
			}
		}
	}
}

// watchCopilot polls tracked Copilot homes, for the same reason the other two
// are polled: the state that changes is a SQLite database and a file being
// appended to, neither of which fsnotify reports usefully here.
func (s *Store) watchCopilot() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		tracked := make(map[string]time.Time, len(s.cpDirs))
		for dir, mtime := range s.cpDirs {
			tracked[dir] = mtime
		}
		s.mu.RUnlock()

		changed := false
		for dir, lastMtime := range tracked {
			mtime, ok := copilotMtime(dir)
			if !ok || !mtime.After(lastMtime) {
				continue
			}
			if s.loadCopilotDir(dir) {
				changed = true
			}
		}

		if changed {
			select {
			case s.updates <- struct{}{}:
			default:
			}
		}
	}
}

// watchPi polls tracked pi homes.
func (s *Store) watchPi() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		tracked := make(map[string]time.Time, len(s.piDirs))
		for dir, mtime := range s.piDirs {
			tracked[dir] = mtime
		}
		s.mu.RUnlock()

		changed := false
		for dir, lastMtime := range tracked {
			mtime, ok := piMtime(dir)
			if !ok || !mtime.After(lastMtime) {
				continue
			}
			if s.loadPiDir(dir) {
				changed = true
			}
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

	// Newest-first on its own scatters a session's agents among unrelated work,
	// so a subagent is pulled up under the parent that spawned it.
	return GroupSubagents(infos)
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
		proj.TotalTokensReported += info.TokensTotal
		proj.TotalCostUSD += info.CostUSD

		// Which tool did the work. An empty Source is Claude Code, which
		// predates the field.
		src := info.Source
		if src == "" {
			src = "claude"
		}
		if proj.SourceSessions == nil {
			proj.SourceSessions = make(map[string]int)
		}
		proj.SourceSessions[src]++
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

	// Sort sessions by LastUpdate descending, then pull each subagent under the
	// parent that spawned it, the same as the main list.
	sort.Slice(proj.Sessions, func(i, j int) bool {
		return proj.Sessions[i].LastUpdate.After(proj.Sessions[j].LastUpdate)
	})
	proj.Sessions = GroupSubagents(proj.Sessions)

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

	// Sorted, so the markers and the counts read in the same order every time.
	for src := range proj.SourceSessions {
		proj.Sources = append(proj.Sources, src)
	}
	sort.Strings(proj.Sources)

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
