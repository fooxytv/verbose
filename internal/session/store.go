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

	ocDBs       map[string]time.Time // tracked OpenCode DBs: path → last mtime
	ocExtraDBs  []string             // explicitly specified OpenCode DB paths
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
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}

			path := filepath.Join(projectDir, f.Name())
			sess, err := ParseSessionFile(path)
			if err != nil {
				continue
			}
			if len(sess.Events) == 0 {
				continue
			}

			s.mu.Lock()
			s.sessions[sess.Info.ID] = sess
			s.mu.Unlock()
		}
	}

	// Scan for OpenCode databases
	s.scanOpenCodeDBs()

	return nil
}

// scanOpenCodeDBs discovers and parses OpenCode databases.
func (s *Store) scanOpenCodeDBs() {
	candidates := make(map[string]bool)

	// Check explicitly provided paths
	for _, p := range s.ocExtraDBs {
		candidates[p] = true
	}

	// Check CWD of existing Claude sessions for co-located OpenCode DBs
	s.mu.RLock()
	for _, sess := range s.sessions {
		if sess.Info.CWD != "" {
			dbPath := filepath.Join(sess.Info.CWD, ".opencode", "opencode.db")
			candidates[dbPath] = true
		}
	}
	s.mu.RUnlock()

	// Check the current working directory
	if cwd, err := os.Getwd(); err == nil {
		dbPath := filepath.Join(cwd, ".opencode", "opencode.db")
		candidates[dbPath] = true
	}

	for dbPath := range candidates {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}

		s.ocDBs[dbPath] = info.ModTime()

		sessions, err := ParseOpenCodeDB(dbPath)
		if err != nil {
			continue
		}

		s.mu.Lock()
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
		for dbPath, lastMtime := range s.ocDBs {
			info, err := os.Stat(dbPath)
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMtime) {
				continue
			}

			s.ocDBs[dbPath] = info.ModTime()

			sessions, err := ParseOpenCodeDB(dbPath)
			if err != nil {
				continue
			}

			s.mu.Lock()
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

	editCounts := make(map[string]int)
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

		if proj.FirstSession.IsZero() || info.StartTime.Before(proj.FirstSession) {
			proj.FirstSession = info.StartTime
		}
		if info.LastUpdate.After(proj.LastSession) {
			proj.LastSession = info.LastUpdate
		}

		if proj.ProjectName == "" {
			proj.ProjectName = info.ProjectName
		}
		if encodedDir == "" && info.FilePath != "" {
			encodedDir = filepath.Base(filepath.Dir(info.FilePath))
		}

		// Count file edits
		for _, fp := range info.FilesWritten {
			editCounts[fp]++
		}
		for _, fp := range info.FilesCreated {
			editCounts[fp]++
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
	for fp, count := range editCounts {
		proj.MostEditedFiles = append(proj.MostEditedFiles, FileEditCount{Path: fp, Count: count})
	}
	sort.Slice(proj.MostEditedFiles, func(i, j int) bool {
		return proj.MostEditedFiles[i].Count > proj.MostEditedFiles[j].Count
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

// GetSessionTodos reads todo items for a session from ~/.claude/todos/.
func (s *Store) GetSessionTodos(sessionID string) []TodoItem {
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
