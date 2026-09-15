package ui

import (
	"testing"

	"github.com/fooxytv/verbose/internal/session"
)

func testSession() *session.Session {
	return &session.Session{
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "fix the terraform plan"},
			{Type: session.EventThinking},
			{Type: session.EventToolUse, ToolName: "Read",
				ToolInput: map[string]interface{}{"file_path": "/repo/config.py"}},
			{Type: session.EventToolUse, ToolName: "Bash", IsError: true,
				ToolInput: map[string]interface{}{"command": "terraform plan"},
				Result:    &session.ToolResult{Raw: "Error: Exit code 1"}},
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": "/repo/main.tf"}},
			{Type: session.EventText, Text: "done"},
			{Type: session.EventToolDenied, DenialKind: "user-rejected"},
		},
	}
}

func TestFilterSelectsExpectedEvents(t *testing.T) {
	sess := testSession()
	for _, tc := range []struct {
		filter eventFilter
		want   int
	}{
		{filterAll, 7},
		{filterOperations, 3},
		{filterFailures, 2}, // the failing Bash and the denial
		{filterFileOps, 2},  // Read and Edit
		{filterPrompts, 1},
	} {
		if got := len(visibleEvents(sess, tc.filter, "")); got != tc.want {
			t.Errorf("filter %s matched %d events, want %d", tc.filter.label(), got, tc.want)
		}
	}
}

func TestSearchMatchesInputsAndOutput(t *testing.T) {
	sess := testSession()
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"config.py", 1},   // a tool input path
		{"terraform", 2},   // the prompt and the bash command
		{"exit code 1", 1}, // the structured failure text
		{"CONFIG.PY", 1},   // case-insensitive
		{"nomatch", 0},
	} {
		if got := len(visibleEvents(sess, filterAll, tc.query)); got != tc.want {
			t.Errorf("search %q matched %d events, want %d", tc.query, got, tc.want)
		}
	}
}

func TestSearchAndFilterCompose(t *testing.T) {
	sess := testSession()
	if got := len(visibleEvents(sess, filterOperations, "terraform")); got != 1 {
		t.Errorf("tools+terraform matched %d, want 1 (the prompt must be excluded)", got)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	// 20 runes, 60 bytes. Byte-slicing kept only 12 and could split a rune.
	bar := "▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪▪"
	if got := truncate(bar, 30); got != bar {
		t.Errorf("truncate(30) shortened a 20-rune string: %q", got)
	}
	got := truncate(bar, 10)
	if r := []rune(got); len(r) != 10 {
		t.Errorf("truncate(10) = %d runes, want 10", len(r))
	}
	for _, r := range truncate("café/détail.go", 8) {
		if r == '�' {
			t.Error("truncate produced an invalid rune")
		}
	}
}

func TestTruncateRunesDoesNotSplitRunes(t *testing.T) {
	for n := 1; n < 12; n++ {
		for _, r := range truncateRunes("✎ café/détail.go", n) {
			if r == '�' {
				t.Fatalf("truncateRunes(%d) produced an invalid rune", n)
			}
		}
	}
}
