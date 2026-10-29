package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A subagent started after the scan lives in a directory that did not exist
// when the watches were set up. It must still be picked up, while the store is
// running, without the parent transcript being written to.
func TestWatchFindsSubagentStartedAfterScan(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "C--work-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(project, "parent.jsonl")
	writeFile(t, parent, `{"type":"user","uuid":"u1","sessionId":"parent","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"start"}}`+"\n")

	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// What Scan does for this one project, without reading the real home.
	s.baseDir = base
	if err := s.watcher.Add(project); err != nil {
		t.Fatal(err)
	}
	s.load(parent)
	s.Watch()

	agentDir := filepath.Join(project, "parent", "subagents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(agentDir, "agent-abc.jsonl"),
		`{"type":"user","uuid":"a1","sessionId":"parent","isSidechain":true,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"look around"}}`+"\n")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := s.GetSession("agent-abc"); sess != nil {
			if !sess.Info.IsAgent || sess.Info.ParentSessionID != "parent" {
				t.Fatalf("loaded as IsAgent=%v parent=%q", sess.Info.IsAgent, sess.Info.ParentSessionID)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("subagent started after the scan was never loaded")
}

// A session in a folder nothing has run in before creates its project
// directory, which did not exist at the scan either.
func TestWatchFindsProjectStartedAfterScan(t *testing.T) {
	base := t.TempDir()

	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// What Scan does for an empty projects directory.
	s.baseDir = base
	if err := s.watcher.Add(base); err != nil {
		t.Fatal(err)
	}
	s.Watch()

	project := filepath.Join(base, "C--work-fresh-worktree")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, "fresh.jsonl"),
		`{"type":"user","uuid":"u1","sessionId":"fresh","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"start"}}`+"\n")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.GetSession("fresh") != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("session in a project created after the scan was never loaded")
}
