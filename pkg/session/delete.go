package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DeleteOutcome reports what happened to a deleted session's data, so the
// caller can tell the user whether anything is recoverable.
type DeleteOutcome struct {
	Trashed   []string // files moved to the trash, by their new location
	Permanent bool     // the data was destroyed outright and cannot be restored
}

// DeleteSession removes a session from the machine and from the store.
//
// Claude Code sessions are plain files, so they are moved to the trash and can
// be restored. OpenCode keeps every session in one shared SQLite database, so
// deletion is delegated to `opencode session delete` — writing to that database
// underneath a running OpenCode is not safe — and is permanent.
func (s *Store) DeleteSession(id string) (DeleteOutcome, error) {
	s.mu.RLock()
	sess := s.sessions[id]
	s.mu.RUnlock()

	if sess == nil {
		return DeleteOutcome{}, fmt.Errorf("no such session: %s", id)
	}

	var out DeleteOutcome
	var err error
	if sess.Info.Source == "opencode" {
		err = deleteOpenCodeSession(sess.Info)
		out.Permanent = true
	} else {
		out.Trashed, err = trashClaudeSession(sess.Info)
	}
	if err != nil {
		return out, err
	}

	// Neither the file watcher nor the database poller removes sessions that
	// have gone away, so drop it here.
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()

	return out, nil
}

// trashClaudeSession moves a session's transcript, and any todo lists belonging
// to it, to the trash.
func trashClaudeSession(info SessionInfo) ([]string, error) {
	var moved []string

	for _, path := range claudeSessionFiles(info) {
		dest, err := moveToTrash(path)
		if err != nil {
			// Report what did move, so a partial failure is not silent.
			return moved, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		moved = append(moved, dest)
	}

	if len(moved) == 0 {
		return nil, fmt.Errorf("no files found for session %s", info.ID)
	}
	return moved, nil
}

// claudeSessionFiles lists everything on disk belonging to a Claude Code
// session: the transcript itself and the todo lists keyed by its ID.
func claudeSessionFiles(info SessionInfo) []string {
	var paths []string

	if info.FilePath != "" {
		if _, err := os.Stat(info.FilePath); err == nil {
			paths = append(paths, info.FilePath)
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	todosDir := filepath.Join(home, ".claude", "todos")
	entries, err := os.ReadDir(todosDir)
	if err != nil {
		return paths
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, info.ID) || !strings.HasSuffix(name, ".json") {
			continue
		}
		paths = append(paths, filepath.Join(todosDir, name))
	}

	return paths
}

// deleteOpenCodeSession asks the OpenCode CLI to delete the session. Its
// database holds every project's sessions at once and may be open by a running
// OpenCode, so its own tooling is the only safe way to remove a row.
func deleteOpenCodeSession(info SessionInfo) error {
	id := strings.TrimPrefix(info.ID, "oc-")

	bin, err := exec.LookPath("opencode")
	if err != nil {
		return errors.New("opencode CLI not found on PATH — needed to delete OpenCode sessions")
	}

	cmd := exec.Command(bin, "session", "delete", id)
	// Run from the session's own directory when it still exists, so OpenCode
	// resolves the same project context it recorded.
	if info.CWD != "" {
		if fi, err := os.Stat(info.CWD); err == nil && fi.IsDir() {
			cmd.Dir = info.CWD
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return fmt.Errorf("opencode session delete: %w", err)
		}
		return fmt.Errorf("opencode session delete: %s", firstLineOf(detail))
	}
	return nil
}

// moveToTrash moves a file to the platform's trash and returns where it landed.
func moveToTrash(path string) (string, error) {
	dir, err := trashDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	dest := uniqueName(dir, filepath.Base(path))

	// A rename is atomic and cheap, but only within one filesystem.
	if err := os.Rename(path, dest); err == nil {
		return dest, nil
	}
	return dest, copyThenRemove(path, dest)
}

// trashDir returns the directory the platform recovers deleted files from.
func trashDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash"), nil
	}
	// The freedesktop.org location. Files land there without the usual
	// .trashinfo metadata, so a file manager may not offer "restore" — the file
	// itself is still recoverable by hand.
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "Trash", "files"), nil
	}
	return filepath.Join(home, ".local", "share", "Trash", "files"), nil
}

// uniqueName returns a path in dir that no file occupies yet, suffixing the
// name the way a file manager does when two deleted files collide.
func uniqueName(dir, name string) string {
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		dest = filepath.Join(dir, fmt.Sprintf("%s %d%s", stem, i, ext))
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			return dest
		}
	}
	return dest
}

// copyThenRemove is the fallback for a move that crosses filesystems. The
// original is removed only once the copy is safely on disk.
func copyThenRemove(path, dest string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dest)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(dest)
		return err
	}

	return os.Remove(path)
}

// firstLineOf returns the first non-empty line, for one-line error reporting.
func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return s
}
