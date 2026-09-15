package session

import (
	"os"
	"path/filepath"
	"testing"
)

// Claude Code writes one JSONL line per content block, all sharing a single
// message ID and repeating that message's usage verbatim. Every block must
// produce an event, while the tokens are counted only once.
const splitMessageTranscript = `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z","cwd":"/repo","gitBranch":"main","message":{"role":"user","content":"do the thing"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T10:00:01.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"thinking","thinking":"planning"}],"usage":{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}
{"type":"assistant","uuid":"a2","timestamp":"2026-01-01T10:00:02.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/repo/a.go"}}],"usage":{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}
{"type":"assistant","uuid":"a3","timestamp":"2026-01-01T10:00:03.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/a.go"}}],"usage":{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}
{"type":"assistant","uuid":"a4","timestamp":"2026-01-01T10:00:04.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T10:00:05.500Z","toolUseResult":{"filePath":"/repo/a.go","structuredPatch":[{"oldStart":1,"oldLines":2,"newStart":1,"newLines":3,"lines":["-old line","+new line","+another new line"," context"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}
{"type":"user","uuid":"u3","timestamp":"2026-01-01T10:00:06.000Z","toolUseResult":"Error: Exit code 127","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"command not found"}]}}
`

func writeTranscript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseKeepsEveryBlockOfASplitMessage(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := sess.Info.ToolCallCount, 3; got != want {
		t.Errorf("ToolCallCount = %d, want %d (blocks of one message must not collapse)", got, want)
	}
	for name, want := range map[string]int{"Read": 1, "Edit": 1, "Bash": 1} {
		if got := sess.Info.ToolCounts[name]; got != want {
			t.Errorf("ToolCounts[%q] = %d, want %d", name, got, want)
		}
	}

	var thinking int
	for _, e := range sess.Events {
		if e.Type == EventThinking {
			thinking++
		}
	}
	if thinking != 1 {
		t.Errorf("thinking events = %d, want 1", thinking)
	}
}

func TestParseCountsUsageOncePerMessage(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}

	i := sess.Info
	if i.InputTokens != 10 || i.OutputTokens != 200 || i.CacheReadTokens != 1000 || i.CacheWriteTokens != 50 {
		t.Errorf("usage = in %d/out %d/cr %d/cw %d, want 10/200/1000/50 (repeated usage must not be summed)",
			i.InputTokens, i.OutputTokens, i.CacheReadTokens, i.CacheWriteTokens)
	}
}

func TestParseAttributesChurnAndDuration(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}

	if sess.Info.LinesAdded != 2 || sess.Info.LinesRemoved != 1 {
		t.Errorf("churn = +%d/-%d, want +2/-1", sess.Info.LinesAdded, sess.Info.LinesRemoved)
	}

	var edit *Event
	for i := range sess.Events {
		if sess.Events[i].Type == EventToolUse && sess.Events[i].ToolID == "t2" {
			edit = &sess.Events[i]
		}
	}
	if edit == nil {
		t.Fatal("edit tool call not found")
	}
	// t2 issued at :03.000, result at :05.500
	if edit.DurationMs != 2500 {
		t.Errorf("Edit DurationMs = %d, want 2500", edit.DurationMs)
	}
}

func TestParseDetectsFailureFromStringResult(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}
	// The failing Bash call carries is_error:false but a string toolUseResult.
	if sess.Info.Errors != 1 {
		t.Errorf("Errors = %d, want 1", sess.Info.Errors)
	}
}

func TestParseCapturesSessionMetadata(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Info.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want %q", sess.Info.GitBranch, "main")
	}
	if sess.Info.CWD != "/repo" {
		t.Errorf("CWD = %q, want %q", sess.Info.CWD, "/repo")
	}
	if sess.Info.UserPrompts != 1 {
		t.Errorf("UserPrompts = %d, want 1", sess.Info.UserPrompts)
	}
}

func TestParseSidechainAndSubagentAccounting(t *testing.T) {
	body := `{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T10:00:00.000Z","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Task","input":{"description":"scan repo","subagent_type":"Explore"}}],"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","uuid":"a2","isSidechain":true,"timestamp":"2026-01-01T10:00:01.000Z","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Grep","input":{"pattern":"func"}}],"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Info.SubagentCalls != 1 {
		t.Errorf("SubagentCalls = %d, want 1", sess.Info.SubagentCalls)
	}
	if sess.Info.SubagentEvents != 1 {
		t.Errorf("SubagentEvents = %d, want 1", sess.Info.SubagentEvents)
	}
	for _, e := range sess.Events {
		if e.ToolID == "t2" && !e.IsSidechain {
			t.Error("subagent tool call not flagged as sidechain")
		}
	}
}

func TestParseRecordsToolDenial(t *testing.T) {
	body := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z","toolDenialKind":"user-rejected","userFeedback":"not that file","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"denied"}]}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Info.Denials != 1 {
		t.Fatalf("Denials = %d, want 1", sess.Info.Denials)
	}
	var found bool
	for _, e := range sess.Events {
		if e.Type == EventToolDenied {
			found = true
			if e.DenialKind != "user-rejected" || e.UserFeedback != "not that file" {
				t.Errorf("denial = %q / %q", e.DenialKind, e.UserFeedback)
			}
		}
	}
	if !found {
		t.Error("no EventToolDenied emitted")
	}
}

func TestParseSurfacesUserFileEdits(t *testing.T) {
	body := `{"type":"attachment","uuid":"x1","timestamp":"2026-01-01T10:00:00.000Z","attachment":{"type":"edited_text_file","filename":"/repo/b.go","snippet":"func main() {}"}}
{"type":"attachment","uuid":"x2","timestamp":"2026-01-01T10:00:01.000Z","attachment":{"type":"total_tokens_reminder"}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Events) != 1 {
		t.Fatalf("events = %d, want 1 (noise attachments must be dropped)", len(sess.Events))
	}
	if sess.Events[0].Type != EventUserFileEdit || sess.Events[0].FilePath != "/repo/b.go" {
		t.Errorf("unexpected event: %+v", sess.Events[0])
	}
}

func TestParseFoldsResultsIntoTheirCall(t *testing.T) {
	sess, err := ParseSessionFile(writeTranscript(t, splitMessageTranscript))
	if err != nil {
		t.Fatal(err)
	}

	// t2 (Edit) and t3 (Bash) have results; t1 (Read) does not.
	var results int
	byID := map[string]*Event{}
	for i := range sess.Events {
		e := &sess.Events[i]
		if e.Type == EventToolResult {
			results++
		}
		if e.Type == EventToolUse {
			byID[e.ToolID] = e
		}
	}
	if results != 0 {
		t.Errorf("standalone tool results = %d, want 0 (matched results must fold into their call)", results)
	}

	edit := byID["t2"]
	if edit == nil {
		t.Fatal("edit call missing")
	}
	if edit.Result == nil || edit.Result.FilePath != "/repo/a.go" {
		t.Errorf("edit call did not absorb its result: %+v", edit.Result)
	}

	bash := byID["t3"]
	if bash == nil {
		t.Fatal("bash call missing")
	}
	if !bash.IsError {
		t.Error("failing bash call not marked as an error after folding")
	}

	// The unmatched Read keeps its sentinel duration.
	if read := byID["t1"]; read == nil || read.DurationMs != -1 {
		t.Errorf("unmatched call should keep DurationMs -1, got %v", read)
	}
}

func TestParseKeepsUnmatchedResults(t *testing.T) {
	// A result whose call is absent (e.g. the transcript was truncated) must
	// still appear, otherwise the operation vanishes entirely.
	body := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"orphan","content":"output"}]}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Events) != 1 || sess.Events[0].Type != EventToolResult {
		t.Errorf("orphan result was dropped: %+v", sess.Events)
	}
}

func TestParseKeepsRedactedThinking(t *testing.T) {
	// Extended thinking is signed but not stored; the step must still show up.
	body := `{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T10:00:00.000Z","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"","signature":"abc"}],"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Events) != 1 || sess.Events[0].Type != EventThinking {
		t.Fatalf("redacted thinking dropped: %+v", sess.Events)
	}
}
