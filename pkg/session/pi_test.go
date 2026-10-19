package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piHome writes a pi home with one session file, in the real layout:
// <home>/agent/sessions/<encoded cwd>/<timestamp>_<uuid>.jsonl.
func piHome(t *testing.T, entries []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, "agent", "sessions", "--tmp-repo--")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}

	var body []byte
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, b...), '\n')
	}
	p := filepath.Join(d, "2026-10-19T19-36-36-239Z_f029accf-17c8-4706-b53c-0ab5906f86b7.jsonl")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func piMsg(ms int64, m map[string]any) map[string]any {
	return map[string]any{"type": "message", "id": "e", "timestamp": "2026-10-19T19:37:00.000Z",
		"message": m}
}

// A whole session, end to end: the header, the model, a prompt, an answer with
// its reasoning, a tool call and the result folded back into it.
func TestPiReadsASession(t *testing.T) {
	home := piHome(t, []map[string]any{
		{"type": "session", "version": 3, "id": "abc123",
			"timestamp": "2026-10-19T19:36:36.239Z", "cwd": "/tmp/repo"},
		{"type": "model_change", "id": "m1", "timestamp": "2026-10-19T19:36:36.257Z",
			"provider": "anthropic", "modelId": "claude-sonnet-4-6"},
		piMsg(0, map[string]any{"role": "user", "timestamp": 1_700_000_000_000,
			"content": []map[string]any{{"type": "text", "text": "fix the handler"}}}),
		piMsg(0, map[string]any{"role": "assistant", "timestamp": 1_700_000_001_000,
			"model": "claude-sonnet-4-6", "provider": "anthropic",
			"usage": map[string]any{"input": 2251, "output": 155, "cacheRead": 10,
				"cacheWrite": 5, "cost": map[string]any{"total": 0.0132}},
			"content": []map[string]any{
				{"type": "thinking", "thinking": "The handler never checks the typed error."},
				{"type": "text", "text": "I'll read it first."},
				{"type": "toolCall", "id": "call-1", "name": "read",
					"arguments": map[string]any{"path": "/tmp/repo/handler.go"}},
			}}),
		piMsg(0, map[string]any{"role": "toolResult", "timestamp": 1_700_000_002_500,
			"toolCallId": "call-1", "toolName": "read", "isError": false,
			"content": []map[string]any{{"type": "text", "text": "84 lines"}}}),
	})

	sessions, err := ParsePiDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(sessions))
	}
	s := sessions[0]

	if s.Info.ID != "pi-abc123" || s.Info.Source != "pi" {
		t.Errorf("ID = %q, Source = %q", s.Info.ID, s.Info.Source)
	}
	if s.Info.CWD != "/tmp/repo" || s.Info.ProjectName != "repo" {
		t.Errorf("CWD = %q, project = %q", s.Info.CWD, s.Info.ProjectName)
	}
	if s.Info.Model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("Model = %q", s.Info.Model)
	}
	if s.Info.InputTokens != 2251 || s.Info.OutputTokens != 155 ||
		s.Info.CacheReadTokens != 10 || s.Info.CacheWriteTokens != 5 {
		t.Errorf("tokens = %d/%d/%d/%d", s.Info.InputTokens, s.Info.OutputTokens,
			s.Info.CacheReadTokens, s.Info.CacheWriteTokens)
	}
	if s.Info.CostUSD != 0.0132 {
		t.Errorf("CostUSD = %v, want the recorded 0.0132", s.Info.CostUSD)
	}

	// One event per thing that happened, and the result folded into its call.
	if len(s.Events) != 4 {
		t.Fatalf("emitted %d events, want prompt, thinking, text, tool", len(s.Events))
	}
	tool := s.Events[3]
	if tool.Type != EventToolUse || tool.ToolName != "Read" {
		t.Fatalf("last event is %v %q, want a Read", tool.Type, tool.ToolName)
	}
	if tool.ToolOutput != "84 lines" {
		t.Errorf("the result did not reach its call: %q", tool.ToolOutput)
	}
	if tool.DurationMs != 1500 {
		t.Errorf("DurationMs = %d, want 1500 from the two timestamps", tool.DurationMs)
	}
	if fp, _ := stringInput(tool.ToolInput, "file_path"); fp != "/tmp/repo/handler.go" {
		t.Errorf("path was not renamed to file_path: %v", tool.ToolInput)
	}
}

// pi is the only source on disk that keeps its reasoning: Claude Code's
// thinking blocks are signed but empty and Codex keeps a headline at best.
// Dropping it here would throw away the only answer to "why" any transcript
// has.
func TestPiKeepsItsThinkingWhole(t *testing.T) {
	const reasoning = "The user wants the 404 path covered.\n\nTwo tests, then the route."
	home := piHome(t, []map[string]any{
		{"type": "session", "id": "s", "cwd": "/tmp/repo",
			"timestamp": "2026-10-19T19:36:36.239Z"},
		piMsg(0, map[string]any{"role": "assistant", "timestamp": 1_700_000_001_000,
			"content": []map[string]any{
				{"type": "thinking", "thinking": reasoning},
				{"type": "thinking", "thinking": "   "},
			}}),
	})

	sessions, _ := ParsePiDir(home)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	var got []string
	for _, e := range sessions[0].Events {
		if e.Type == EventThinking {
			got = append(got, e.Thinking)
		}
	}
	if len(got) != 1 {
		t.Fatalf("kept %d thinking blocks, want the one with text in it", len(got))
	}
	if got[0] != reasoning {
		t.Errorf("thinking was altered:\n%q", got[0])
	}
}

// pi records the diff it DISPLAYED, not a unified one: no @@ headers, a line
// number per line, and an elision between runs. The number field is
// right-aligned, so a three-digit line has no space after the marker — a
// pattern expecting one silently skips every hundredth line onward.
func TestPiDiffParsesIncludingWideLineNumbers(t *testing.T) {
	diff := strings.Join([]string{
		"     ...",
		"  98   keep this",
		"- 99   gone",
		"+ 99   replaced",
		"+100   and another",
		"-101   also gone",
		"     ...",
		" 400   a later run",
		"+401   added there",
	}, "\n")

	hunks := parsePiDiff(diff)
	if len(hunks) != 2 {
		t.Fatalf("parsed %d hunks, want 2 — the elision ends a run", len(hunks))
	}

	first := hunks[0]
	if first.OldStart != 98 || first.NewStart != 98 {
		t.Errorf("hunk starts at old %d new %d, want 98", first.OldStart, first.NewStart)
	}
	want := []string{" " + "  keep this", "-  gone", "+  replaced", "+  and another", "-  also gone"}
	if len(first.Lines) != len(want) {
		t.Fatalf("hunk has %d lines, want %d: %q", len(first.Lines), len(want), first.Lines)
	}
	for i := range want {
		if first.Lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, first.Lines[i], want[i])
		}
	}
	if first.OldLines != 3 || first.NewLines != 3 {
		t.Errorf("hunk counts old %d new %d, want 3 and 3", first.OldLines, first.NewLines)
	}

	// The three-digit lines are in there, indentation intact.
	joined := strings.Join(first.Lines, "\n")
	if !strings.Contains(joined, "+  and another") || !strings.Contains(joined, "-  also gone") {
		t.Errorf("a line numbered past 99 was dropped:\n%s", joined)
	}

	if hunks[1].OldStart != 400 {
		t.Errorf("second hunk starts at %d, want 400", hunks[1].OldStart)
	}
}

// A write has no before-state, so its content rides on the call — which is
// what the replay types out.
func TestPiWriteCarriesItsContentAndEditCarriesHunks(t *testing.T) {
	home := piHome(t, []map[string]any{
		{"type": "session", "id": "s", "cwd": "/tmp/repo",
			"timestamp": "2026-10-19T19:36:36.239Z"},
		piMsg(0, map[string]any{"role": "assistant", "timestamp": 1_700_000_001_000,
			"content": []map[string]any{
				{"type": "toolCall", "id": "w1", "name": "write",
					"arguments": map[string]any{"path": "/tmp/repo/new.go",
						"content": "package main\n"}},
				{"type": "toolCall", "id": "e1", "name": "edit",
					"arguments": map[string]any{"path": "/tmp/repo/old.go",
						"oldText": "gone", "newText": "replaced"}},
			}}),
		piMsg(0, map[string]any{"role": "toolResult", "timestamp": 1_700_000_002_000,
			"toolCallId": "w1", "toolName": "write",
			"content": []map[string]any{{"type": "text", "text": "written"}}}),
		piMsg(0, map[string]any{"role": "toolResult", "timestamp": 1_700_000_002_000,
			"toolCallId": "e1", "toolName": "edit",
			"details": map[string]any{"diff": "  10   ctx\n- 11   gone\n+ 11   replaced",
				"firstChangedLine": 11},
			"content": []map[string]any{{"type": "text", "text": "replaced"}}}),
	})

	sessions, _ := ParsePiDir(home)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	s := sessions[0]

	var write, edit Event
	for _, e := range s.Events {
		switch e.ToolName {
		case "Write":
			write = e
		case "Edit":
			edit = e
		}
	}

	if _, code := stepCode(write); code != "package main\n" {
		t.Errorf("stepCode = %q, want the written file's content", code)
	}
	if write.Result == nil || len(write.Result.StructuredPatch) != 0 {
		t.Error("a write must carry no patch: there is no before-state")
	}
	if len(s.Info.FilesCreated) != 1 {
		t.Errorf("FilesCreated = %v", s.Info.FilesCreated)
	}

	if edit.Result == nil || len(edit.Result.StructuredPatch) != 1 {
		t.Fatalf("the edit's diff did not become a patch: %+v", edit.Result)
	}
	if edit.LinesAdded != 1 || edit.LinesRemoved != 1 {
		t.Errorf("churn = +%d -%d, want +1 -1", edit.LinesAdded, edit.LinesRemoved)
	}
}

// A command run from pi's own shell is still a command that ran in this
// directory; reading the session without it leaves an unexplained change.
func TestPiBashExecutionIsACommand(t *testing.T) {
	home := piHome(t, []map[string]any{
		{"type": "session", "id": "s", "cwd": "/tmp/repo",
			"timestamp": "2026-10-19T19:36:36.239Z"},
		piMsg(0, map[string]any{"role": "bashExecution", "timestamp": 1_700_000_001_000,
			"command": "npm install -g pi", "output": "added 1 package\n", "exitCode": 0}),
	})

	sessions, _ := ParsePiDir(home)
	if len(sessions) != 1 || len(sessions[0].Events) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	e := sessions[0].Events[0]
	if e.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", e.ToolName)
	}
	if cmd, _ := stringInput(e.ToolInput, "command"); cmd != "npm install -g pi" {
		t.Errorf("command = %q", cmd)
	}
	if sessions[0].Info.BashCommands != 1 {
		t.Errorf("BashCommands = %d, want 1", sessions[0].Info.BashCommands)
	}
}

// A failed call is counted and marked, and its text is the failure.
func TestPiToolErrorIsCounted(t *testing.T) {
	home := piHome(t, []map[string]any{
		{"type": "session", "id": "s", "cwd": "/tmp/repo",
			"timestamp": "2026-10-19T19:36:36.239Z"},
		piMsg(0, map[string]any{"role": "assistant", "timestamp": 1_700_000_001_000,
			"content": []map[string]any{{"type": "toolCall", "id": "r1", "name": "read",
				"arguments": map[string]any{"path": "/tmp/repo"}}}}),
		piMsg(0, map[string]any{"role": "toolResult", "timestamp": 1_700_000_002_000,
			"toolCallId": "r1", "toolName": "read", "isError": true,
			"content": []map[string]any{{"type": "text",
				"text": "EISDIR: illegal operation on a directory, read"}}}),
	})

	sessions, _ := ParsePiDir(home)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if sessions[0].Info.Errors != 1 {
		t.Errorf("Errors = %d, want 1", sessions[0].Info.Errors)
	}
	if !sessions[0].Events[0].IsError {
		t.Error("the failed call is not marked")
	}
}

// A pi that has never run yields nothing and no error.
func TestPiEmptyHomeIsNotAnError(t *testing.T) {
	sessions, err := ParsePiDir(t.TempDir())
	if err != nil {
		t.Errorf("an empty home errored: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("parsed %d sessions from an empty home", len(sessions))
	}
}
