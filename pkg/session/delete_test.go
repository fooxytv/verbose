package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedHome points HOME at a scratch directory so a test can never reach the
// real ~/.claude or the real Trash. It returns the fake home.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// trashDir consults XDG_DATA_HOME first on non-darwin platforms.
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	return home
}

// writeFile creates a file and the directories leading to it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveToTrash(t *testing.T) {
	home := isolatedHome(t)

	src := filepath.Join(home, "work", "transcript.jsonl")
	writeFile(t, src, "line one\n")

	dest, err := moveToTrash(src)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("original still present after trashing")
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("trashed file unreadable: %v", err)
	}
	if string(body) != "line one\n" {
		t.Errorf("trashed content = %q", body)
	}
	if filepath.Base(dest) != "transcript.jsonl" {
		t.Errorf("trashed name = %q, want transcript.jsonl", filepath.Base(dest))
	}
}

func TestMoveToTrashNameCollision(t *testing.T) {
	home := isolatedHome(t)

	// Two different sessions can share a basename; neither may clobber the other.
	for i, dir := range []string{"a", "b"} {
		src := filepath.Join(home, dir, "same.jsonl")
		writeFile(t, src, dir)

		dest, err := moveToTrash(src)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(dest)
		if string(body) != dir {
			t.Errorf("round %d: content = %q, want %q", i, body, dir)
		}
	}

	dir, err := trashDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("trash holds %v, want two distinct files", names)
	}
}

func TestClaudeSessionFilesIncludesTodos(t *testing.T) {
	home := isolatedHome(t)

	transcript := filepath.Join(home, ".claude", "projects", "-repo", "abc123.jsonl")
	writeFile(t, transcript, "{}")
	writeFile(t, filepath.Join(home, ".claude", "todos", "abc123-agent-abc123.json"), "[]")
	// Belongs to a different session and must be left alone.
	writeFile(t, filepath.Join(home, ".claude", "todos", "zzz999-agent-zzz999.json"), "[]")

	got := claudeSessionFiles(SessionInfo{ID: "abc123", FilePath: transcript})
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2: %v", len(got), got)
	}
	for _, p := range got {
		if strings.Contains(p, "zzz999") {
			t.Errorf("picked up another session's todo file: %s", p)
		}
	}
}

// storeWith returns a store holding one session, with HOME already isolated.
func storeWith(t *testing.T, sess *Session) *Store {
	t.Helper()
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.sessions[sess.Info.ID] = sess
	return store
}

func TestDeleteSessionTrashesClaudeTranscript(t *testing.T) {
	home := isolatedHome(t)

	transcript := filepath.Join(home, ".claude", "projects", "-repo", "abc123.jsonl")
	writeFile(t, transcript, "{}")
	todo := filepath.Join(home, ".claude", "todos", "abc123-agent-abc123.json")
	writeFile(t, todo, "[]")

	store := storeWith(t, &Session{Info: SessionInfo{
		ID: "abc123", FilePath: transcript, Source: "claude",
	}})

	out, err := store.DeleteSession("abc123")
	if err != nil {
		t.Fatal(err)
	}

	if out.Permanent {
		t.Error("a Claude session went to the trash, so it is not permanent")
	}
	if len(out.Trashed) != 2 {
		t.Errorf("trashed %d files, want 2: %v", len(out.Trashed), out.Trashed)
	}
	for _, p := range []string{transcript, todo} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the delete", p)
		}
	}
	for _, p := range out.Trashed {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("trashed file missing at %s: %v", p, err)
		}
	}

	// The store must forget it, since nothing else prunes removed sessions.
	if store.GetSession("abc123") != nil {
		t.Error("deleted session still in the store")
	}
	for _, info := range store.GetSessions() {
		if info.ID == "abc123" {
			t.Error("deleted session still listed")
		}
	}
}

func TestDeleteSessionUnknownID(t *testing.T) {
	isolatedHome(t)
	store := storeWith(t, &Session{Info: SessionInfo{ID: "abc123"}})

	if _, err := store.DeleteSession("nope"); err == nil {
		t.Error("deleting an unknown session should fail")
	}
}

func TestDeleteSessionMissingFiles(t *testing.T) {
	home := isolatedHome(t)
	store := storeWith(t, &Session{Info: SessionInfo{
		ID:       "gone",
		FilePath: filepath.Join(home, ".claude", "projects", "-repo", "gone.jsonl"),
		Source:   "claude",
	}})

	// Nothing on disk to move: report it rather than claiming success.
	if _, err := store.DeleteSession("gone"); err == nil {
		t.Error("expected an error when no files exist for the session")
	}
	if store.GetSession("gone") == nil {
		t.Error("a failed delete must leave the session in the store")
	}
}

func TestDeleteOpenCodeSessionNeedsCLI(t *testing.T) {
	isolatedHome(t)
	// An empty PATH makes the opencode binary unfindable.
	t.Setenv("PATH", "")

	store := storeWith(t, &Session{Info: SessionInfo{
		ID: "oc-ses_abc", Source: "opencode",
	}})

	_, err := store.DeleteSession("oc-ses_abc")
	if err == nil {
		t.Fatal("expected an error when the opencode CLI is unavailable")
	}
	if !strings.Contains(err.Error(), "opencode CLI not found") {
		t.Errorf("error = %q, want it to name the missing CLI", err)
	}
	if store.GetSession("oc-ses_abc") == nil {
		t.Error("a failed delete must leave the session in the store")
	}
}

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()

	first := uniqueName(dir, "a.jsonl")
	if filepath.Base(first) != "a.jsonl" {
		t.Errorf("first = %q, want a.jsonl", filepath.Base(first))
	}

	writeFile(t, first, "x")
	second := uniqueName(dir, "a.jsonl")
	if filepath.Base(second) != "a 2.jsonl" {
		t.Errorf("second = %q, want \"a 2.jsonl\"", filepath.Base(second))
	}

	// A name without an extension still gets a suffix.
	writeFile(t, filepath.Join(dir, "plain"), "x")
	if got := filepath.Base(uniqueName(dir, "plain")); got != "plain 2" {
		t.Errorf("extensionless = %q, want \"plain 2\"", got)
	}
}

func TestCopyThenRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	writeFile(t, src, "payload")

	if err := copyThenRemove(src, dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "payload" {
		t.Fatalf("copy = %q, err = %v", body, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source survived the move")
	}
}

// stubOpenCode puts a fake `opencode` on PATH so the CLI handoff can be tested
// without touching a real OpenCode database.
func stubOpenCode(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDeleteOpenCodeSessionSucceeds(t *testing.T) {
	isolatedHome(t)
	// Record the arguments so the ID handed to the CLI can be checked.
	argsFile := filepath.Join(t.TempDir(), "args")
	stubOpenCode(t, "echo \"$@\" > "+argsFile+"; exit 0")

	store := storeWith(t, &Session{Info: SessionInfo{
		ID: "oc-ses_abc123", Source: "opencode",
	}})

	out, err := store.DeleteSession("oc-ses_abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Permanent {
		t.Error("an OpenCode delete is permanent and must say so")
	}
	if len(out.Trashed) != 0 {
		t.Errorf("nothing can be trashed for OpenCode, got %v", out.Trashed)
	}
	if store.GetSession("oc-ses_abc123") != nil {
		t.Error("deleted session still in the store")
	}

	// The oc- prefix is verbose's own; OpenCode must receive its raw ID.
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != "session delete ses_abc123" {
		t.Errorf("CLI args = %q, want \"session delete ses_abc123\"", got)
	}
}

func TestDeleteOpenCodeSessionReportsCLIFailure(t *testing.T) {
	isolatedHome(t)
	stubOpenCode(t, "echo 'Error: Session not found: ses_abc123' >&2; exit 1")

	store := storeWith(t, &Session{Info: SessionInfo{
		ID: "oc-ses_abc123", Source: "opencode",
	}})

	_, err := store.DeleteSession("oc-ses_abc123")
	if err == nil {
		t.Fatal("a failing CLI must surface an error")
	}
	if !strings.Contains(err.Error(), "Session not found") {
		t.Errorf("error = %q, want the CLI's own message", err)
	}
	// A failed delete must not silently drop the row from the list.
	if store.GetSession("oc-ses_abc123") == nil {
		t.Error("session removed from the store despite the failure")
	}
}
