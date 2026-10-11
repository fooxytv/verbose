package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeJSONL writes one event per line, the way Milo's logs are appended.
func writeJSONL(t *testing.T, path string, events []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}

func miloFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ProjectMilo", "sessions")
	writeJSONL(t, filepath.Join(dir, "abc123.jsonl"), []map[string]any{
		{"type": "session_start", "model": "qwen3:8b", "ts": 1760000000.0},
		{"type": "user", "content": "what is eating disk?", "model": "qwen3:8b", "ts": 1760000001.0},
		{"type": "tool_call", "name": "run_shell", "args": map[string]any{"command": "df -h"}, "ts": 1760000002.0},
		{"type": "tool_result", "name": "run_shell", "result": "exit=0\n/dev/nvme0n1p2 90%", "ts": 1760000004.5},
		{"type": "thinking", "content": "mostly models", "ts": 1760000005.0},
		{"type": "message", "content": "90% full, mostly models.", "ts": 1760000006.0},
		{"type": "confirm", "name": "write_file", "args": map[string]any{"path": "notes.md"}, "ts": 1760000007.0},
		{"type": "confirm_response", "name": "write_file", "approved": false, "ts": 1760000008.0},
		{"type": "tool_call", "name": "read_file", "args": map[string]any{"path": "models.yaml"}, "ts": 1760000009.0},
		{"type": "tool_result", "name": "read_file", "result": "error: nope", "ts": 1760000010.0},
	})

	usage := map[string]map[string]int{"qwen3:8b": {"prompt": 1200, "completion": 300}}
	b, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc123.usage.json"), b, 0o644); err != nil {
		t.Fatalf("write usage: %v", err)
	}
	return dir
}

func TestParseMiloSession(t *testing.T) {
	dir := miloFixture(t)
	sess, err := ParseMiloSession(filepath.Join(dir, "abc123.jsonl"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sess == nil {
		t.Fatal("parse returned no session")
	}

	if sess.Info.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", sess.Info.ID)
	}
	if sess.Info.Source != SourceMilo {
		t.Errorf("Source = %q, want %q", sess.Info.Source, SourceMilo)
	}
	if got, want := len(sess.Events), 10; got != want {
		t.Fatalf("events = %d, want %d (every logged line should be accounted for)", got, want)
	}

	byType := map[EventType]int{}
	for _, e := range sess.Events {
		byType[e.Type]++
	}
	for _, tc := range []struct {
		typ  EventType
		want int
		name string
	}{
		{EventUserPrompt, 1, "user prompt"},
		{EventText, 1, "assistant text"},
		{EventThinking, 1, "thinking"},
		{EventToolUse, 2, "tool use"},
		{EventToolResult, 2, "tool result"},
		{EventToolDenied, 1, "denial"},
	} {
		if byType[tc.typ] != tc.want {
			t.Errorf("%s events = %d, want %d", tc.name, byType[tc.typ], tc.want)
		}
	}

	if sess.Info.UserPrompts != 1 {
		t.Errorf("UserPrompts = %d, want 1", sess.Info.UserPrompts)
	}
	if sess.Info.ToolCallCount != 2 {
		t.Errorf("ToolCallCount = %d, want 2", sess.Info.ToolCallCount)
	}
	if sess.Info.BashCommands != 1 {
		t.Errorf("BashCommands = %d, want 1", sess.Info.BashCommands)
	}
	// A denial is the absence of a tool_call, so it has to come from the
	// confirm_response or it is lost entirely.
	if sess.Info.Denials != 1 {
		t.Errorf("Denials = %d, want 1", sess.Info.Denials)
	}
	if sess.Info.Errors != 1 {
		t.Errorf("Errors = %d, want 1 (result starting 'error:')", sess.Info.Errors)
	}
	if len(sess.Info.FilesRead) != 1 || sess.Info.FilesRead[0] != "models.yaml" {
		t.Errorf("FilesRead = %v, want [models.yaml]", sess.Info.FilesRead)
	}
}

func TestParseMiloSessionUsageAndCost(t *testing.T) {
	dir := miloFixture(t)
	sess, err := ParseMiloSession(filepath.Join(dir, "abc123.jsonl"))
	if err != nil || sess == nil {
		t.Fatalf("parse: %v", err)
	}
	// Tokens are not in the JSONL — they only arrive if the sibling
	// <id>.usage.json is read.
	if sess.Info.InputTokens != 1200 || sess.Info.OutputTokens != 300 {
		t.Fatalf("tokens = %d/%d, want 1200/300 from the usage file",
			sess.Info.InputTokens, sess.Info.OutputTokens)
	}
	if sess.Info.Model != "qwen3:8b" {
		t.Errorf("Model = %q, want qwen3:8b", sess.Info.Model)
	}
}

func TestParseMiloSessionMissingUsageIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nousage.jsonl")
	writeJSONL(t, path, []map[string]any{
		{"type": "user", "content": "hello", "ts": 1760000000.0},
	})

	sess, err := ParseMiloSession(path)
	if err != nil {
		t.Fatalf("a session that never reached a model must still parse: %v", err)
	}
	if sess == nil {
		t.Fatal("parse returned no session")
	}
	if sess.Info.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", sess.Info.InputTokens)
	}
}

func TestParseMiloSessionToolDuration(t *testing.T) {
	dir := miloFixture(t)
	sess, err := ParseMiloSession(filepath.Join(dir, "abc123.jsonl"))
	if err != nil || sess == nil {
		t.Fatalf("parse: %v", err)
	}
	var shell *Event
	for i := range sess.Events {
		if sess.Events[i].Type == EventToolUse && sess.Events[i].ToolName == "run_shell" {
			shell = &sess.Events[i]
			break
		}
	}
	if shell == nil {
		t.Fatal("no run_shell tool use found")
	}
	// 1760000002.0 -> 1760000004.5
	if shell.DurationMs != 2500 {
		t.Errorf("DurationMs = %d, want 2500 (paired with the next matching result)",
			shell.DurationMs)
	}
}

func TestParseMiloSessionSkipsPartialLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.jsonl")
	writeJSONL(t, path, []map[string]any{
		{"type": "user", "content": "hi", "ts": 1760000000.0},
	})
	// A log being appended to can end mid-line; that must not lose the rest.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(`{"type": "message", "cont`); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	sess, err := ParseMiloSession(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sess == nil {
		t.Fatal("parse returned no session")
	}
	if len(sess.Events) != 1 {
		t.Errorf("events = %d, want 1 (the truncated line is skipped)", len(sess.Events))
	}
}

func TestParseMiloRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "sessions", "runs", "run_20261011_ab12cd")
	writeJSONL(t, filepath.Join(runDir, "events.jsonl"), []map[string]any{
		{"type": "run_created", "goal": "Add rate limiting", "ts": 1760000000.0},
		{"type": "stage", "stage": "plan", "status": "done", "model": "anthropic:claude-sonnet-4-6", "ts": 1760000010.0},
		{"type": "stage", "stage": "test", "status": "failed", "exit_code": 1, "ts": 1760000020.0},
		{"type": "gate_opened", "approval_id": "apr_x", "ts": 1760000030.0},
		{"type": "gate_decided", "approval_id": "apr_x", "decision": "approve", "comment": "ship it", "ts": 1760000040.0},
	})
	runJSON := map[string]any{
		"goal":   "Add rate limiting",
		"status": "approved",
		"stages": []map[string]any{{"name": "plan", "model": "anthropic:claude-sonnet-4-6"}},
	}
	b, err := json.Marshal(runJSON)
	if err != nil {
		t.Fatalf("marshal run.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), b, 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}

	sess, err := ParseMiloRun(runDir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sess == nil {
		t.Fatal("parse returned no session")
	}
	if sess.Info.ID != "run_20261011_ab12cd" {
		t.Errorf("ID = %q", sess.Info.ID)
	}
	// The goal is the run's prompt — it is what makes a run readable in the
	// same list as a conversation.
	if sess.Info.Title != "Add rate limiting" {
		t.Errorf("Title = %q, want the goal", sess.Info.Title)
	}
	if len(sess.Events) == 0 || sess.Events[0].Type != EventUserPrompt {
		t.Fatalf("first event should be the goal as a prompt, got %+v", sess.Events)
	}
	if sess.Info.Model != "anthropic:claude-sonnet-4-6" {
		t.Errorf("Model = %q, want the stage model", sess.Info.Model)
	}
	if sess.Info.Source != SourceMilo {
		t.Errorf("Source = %q", sess.Info.Source)
	}
}

func TestParseMiloDirFindsSessionsAndRuns(t *testing.T) {
	dir := miloFixture(t)
	runDir := filepath.Join(dir, "runs", "run_20261011_ffffff")
	writeJSONL(t, filepath.Join(runDir, "events.jsonl"), []map[string]any{
		{"type": "run_created", "goal": "Migrate to SQLite", "ts": 1760000100.0},
	})

	sessions, err := ParseMiloDir(dir)
	if err != nil {
		t.Fatalf("parse dir: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (one chat, one run)", len(sessions))
	}

	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.Info.ID] = true
	}
	if !ids["abc123"] {
		t.Error("chat session missing from the directory scan")
	}
	if !ids["run_20261011_ffffff"] {
		t.Error("run missing from the directory scan")
	}
	// Newest first, the same order the sessions list expects.
	if !sessions[0].Info.LastUpdate.After(sessions[1].Info.LastUpdate) {
		t.Error("sessions should be sorted newest first")
	}
}

func TestParseMiloDirMissingIsAnError(t *testing.T) {
	// A machine with no Milo on it is the normal case, and the store treats a
	// miss as silent — but the parser itself still reports it.
	if _, err := ParseMiloDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("want an error for a directory that does not exist")
	}
}
