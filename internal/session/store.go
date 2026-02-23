package session

import (
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
	}

	return s, nil
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

	return nil
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

	return s.updates
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

// Close cleans up the file watcher.
func (s *Store) Close() error {
	return s.watcher.Close()
}
