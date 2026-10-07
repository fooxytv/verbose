package ui

import (
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

func TestRevealUnlimitedShowsEverything(t *testing.T) {
	rv := newReveal(-1)
	for _, in := range []string{"one", "two", "three"} {
		out, ok := rv.line(in)
		if !ok || out != in {
			t.Fatalf("line(%q) = %q, %v; want it shown whole", in, out, ok)
		}
	}
	if rv.exhausted() {
		t.Error("an unlimited reveal is never exhausted")
	}
}

func TestRevealStopsMidLineWithCursor(t *testing.T) {
	rv := newReveal(5) // "abc" (3) + 2 of "defgh"

	if out, ok := rv.line("abc"); !ok || out != "abc" {
		t.Fatalf("first line = %q, %v; want it whole", out, ok)
	}
	out, ok := rv.line("defgh")
	if !ok {
		t.Fatal("the partial line must still be drawn")
	}
	if out != "de▌" {
		t.Errorf("partial line = %q, want %q", out, "de▌")
	}
	if !rv.exhausted() {
		t.Error("reveal should be exhausted once the cursor is drawn")
	}
	if _, ok := rv.line("ignored"); ok {
		t.Error("nothing may be drawn after the cursor")
	}
}

// A zero budget is the first frame of the animation: a cursor and nothing else.
func TestRevealZeroBudgetDrawsOnlyCursor(t *testing.T) {
	rv := newReveal(0)
	out, ok := rv.line("anything")
	if !ok || out != "▌" {
		t.Errorf("line = %q, %v; want just the cursor", out, ok)
	}
}

// Counting runes, not bytes: a budget mid-way through a multi-byte character
// must not split it.
func TestRevealCountsRunesNotBytes(t *testing.T) {
	rv := newReveal(3)
	out, _ := rv.line("héllo→")
	if out != "hél▌" {
		t.Errorf("out = %q, want %q", out, "hél▌")
	}
}

func TestRevealHunksDropsUnreachedHunks(t *testing.T) {
	hunks := []session.PatchHunk{
		{Lines: []string{"+aaa", "+bbb"}},
		{Lines: []string{"+ccc", "+ddd"}},
	}

	// 4 runes per line: the whole first line, then the cursor parked at the
	// start of the next — which is where an editor's caret sits after you
	// finish a line.
	shown := revealHunks(hunks, newReveal(4))
	if len(shown) != 1 {
		t.Fatalf("got %d hunks, want 1; the second was never reached", len(shown))
	}
	if got := shown[0].Lines; len(got) != 2 || got[0] != "+aaa" || got[1] != "▌" {
		t.Errorf("lines = %v, want [+aaa ▌]", got)
	}

	// One rune in, only a partial first line exists.
	early := revealHunks(hunks, newReveal(1))
	if len(early) != 1 || early[0].Lines[0] != "+▌" {
		t.Errorf("early reveal = %v, want [[+▌]]", early)
	}

	// Everything.
	all := revealHunks(hunks, newReveal(-1))
	if len(all) != 2 || len(all[1].Lines) != 2 {
		t.Errorf("unlimited reveal lost hunks: %v", all)
	}
}

// The budget and the renderer must agree on what counts as typed, or playback
// would either stall short of the end or finish before the diff is drawn.
func TestTypedLengthMatchesWhatTheRevealConsumes(t *testing.T) {
	sess := &session.Session{
		Events: []session.Event{{
			Type:      session.EventToolUse,
			ToolName:  "Edit",
			ToolInput: map[string]interface{}{"command": "make test"},
			Result: &session.ToolResult{
				FilePath: "/repo/a.go",
				StructuredPatch: []session.PatchHunk{
					{Lines: []string{"+one", "-two"}},
					{Lines: []string{" ctx"}},
				},
			},
		}},
	}
	step := session.ReplayStep{EventIndex: 0}

	// "make test"(9) + "+one"(4) + "-two"(4) + " ctx"(4)
	if got := TypedLength(step, sess); got != 21 {
		t.Fatalf("TypedLength = %d, want 21", got)
	}

	// At exactly that budget nothing is left over: no cursor is drawn.
	rv := newReveal(TypedLength(step, sess))
	rv.line("make test")
	revealHunks(sess.Events[0].Result.StructuredPatch, rv)
	if rv.exhausted() {
		t.Error("the full budget should cover the content exactly, with no cursor")
	}
}

func TestTypedLengthIsZeroForProseSteps(t *testing.T) {
	sess := &session.Session{
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "a long prompt that is not typed out"},
			{Type: session.EventText, Text: "narration"},
		},
	}
	for i := range sess.Events {
		if got := TypedLength(session.ReplayStep{EventIndex: i}, sess); got != 0 {
			t.Errorf("event %d TypedLength = %d, want 0; only code is typed", i, got)
		}
	}
}

// Following the cursor is the point: a mid-animation frame must show the end of
// what has been written, not the top of the file.
func TestRenderReplayFollowsTheTypingCursor(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "+line")
	}
	sess := &session.Session{
		Info: session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{
			Type:      session.EventToolUse,
			ToolName:  "Write",
			ToolInput: map[string]interface{}{"file_path": "/repo/big.go"},
			Result: &session.ToolResult{
				FilePath:        "/repo/big.go",
				StructuredPatch: []session.PatchHunk{{Lines: lines}},
			},
		}},
	}
	steps := session.BuildReplay(sess)
	total := TypedLength(steps[0], sess)

	mid := renderReplay(replayView{sess: sess, steps: steps, idx: 0, scroll: 0, typed: total / 2, playing: true, delay: replayDefaultDelay, width: 80, height: 24})
	if !strings.Contains(mid, "▌") {
		t.Error("a mid-animation frame should show the cursor; the view is not following it")
	}
	// Paused, the same step starts at the top and shows everything.
	done := renderReplay(replayView{sess: sess, steps: steps, idx: 0, scroll: 0, typed: -1, playing: false, delay: replayDefaultDelay, width: 80, height: 24})
	if strings.Contains(done, "▌") {
		t.Error("a paused step should show the whole diff, with no cursor")
	}
}

func TestCodeStepsKeepsOnlyStepsThatWroteSomething(t *testing.T) {
	sess := &session.Session{
		Info: session.SessionInfo{CWD: "/repo"},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "build it"},
			{Type: session.EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "go test ./..."}},
			{Type: session.EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "cat > a.go <<'EOF'\nx\nEOF"}},
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": "/repo/b.go"},
				Result: &session.ToolResult{FilePath: "/repo/b.go",
					StructuredPatch: []session.PatchHunk{{Lines: []string{"+y"}}}}},
		},
	}
	all := session.BuildReplay(sess)
	code := codeSteps(all, sess)

	if len(all) != 4 {
		t.Fatalf("got %d steps, want 4", len(all))
	}
	if len(code) != 2 {
		t.Fatalf("got %d code steps, want 2 (the shell write and the edit)", len(code))
	}
	if code[0].Title != "Wrote a.go" {
		t.Errorf("first code step = %q, want %q", code[0].Title, "Wrote a.go")
	}
}

// Recovered code is what gets typed; the command that carried it is a wrapper.
func TestTypedLengthUsesRecoveredCode(t *testing.T) {
	sess := &session.Session{
		Events: []session.Event{{
			Type:      session.EventToolUse,
			ToolName:  "Bash",
			ToolInput: map[string]interface{}{"command": "cat > a.go <<'EOF'\nabcd\nEOF"},
		}},
	}
	steps := session.BuildReplay(sess)
	// "abcd" is the body; the long cat invocation is not typed.
	if got := TypedLength(steps[0], sess); got != 4 {
		t.Errorf("TypedLength = %d, want 4 (the heredoc body only)", got)
	}
}

func TestRenderReplayStreamsRecoveredCode(t *testing.T) {
	sess := &session.Session{
		Info: session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{
			Type:     session.EventToolUse,
			ToolName: "Bash",
			ToolInput: map[string]interface{}{
				"command": "cat > /repo/a.js <<'EOF'\nconst answer = 42\nEOF"},
		}},
	}
	steps := session.BuildReplay(sess)

	out := renderReplay(replayView{sess: sess, steps: steps, idx: 0, typed: -1,
		delay: replayDefaultDelay, width: 80, height: 24})
	if !strings.Contains(out, "Writing a.js") {
		t.Errorf("header missing the file being written:\n%s", out)
	}
	// Code is syntax highlighted, so the escapes have to come out before the
	// text can be compared.
	if !strings.Contains(stripAnsi(out), "const answer = 42") {
		t.Errorf("the recovered code did not render:\n%s", out)
	}
}

// stripAnsi removes colour escapes so a test can assert on what is actually
// readable on screen.
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// The empty state has to distinguish "nothing happened" from "the filter hid
// everything", or a filtered session looks broken.
func TestRenderReplayEmptyCodeOnlyExplainsTheFilter(t *testing.T) {
	out := renderReplay(replayView{sess: &session.Session{}, codeOnly: true,
		typed: -1, delay: replayDefaultDelay, width: 80, height: 24})
	if !strings.Contains(out, "No step in this session wrote code") {
		t.Errorf("filtered empty state did not explain itself:\n%s", out)
	}
	if !strings.Contains(out, "tab") {
		t.Error("the way out of the filter should be stated")
	}
}
