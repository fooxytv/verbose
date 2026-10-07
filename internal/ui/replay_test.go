package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

// visibleLines counts rows the way padToHeight does, which is what decides
// whether the footer stays on screen.
func visibleLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// A line wider than the terminal is wrapped into two rows, so the frame occupies
// more rows than padToHeight counted: the footer is pushed off the bottom and
// the screen appears to duplicate as the terminal scrolls. Nothing this renderer
// emits may exceed the width it was given.
func TestRenderReplayNeverExceedsTerminalWidth(t *testing.T) {
	long := "/Users/someone/workspace/a-very-long-project-name/src/deeply/nested/module.go"
	hunk := session.PatchHunk{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
		Lines: []string{" " + strings.Repeat("ctx", 60), "+" + strings.Repeat("add", 60)}}

	sess := &session.Session{
		Info: session.SessionInfo{
			Title: strings.Repeat("a long session title ", 10),
			CWD:   "/nowhere",
		},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: strings.Repeat("word ", 200)},
			// A diff whose file lives outside the session directory: the path
			// cannot be shortened away, so the summary line must be clipped.
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": long},
				Result: &session.ToolResult{FilePath: long,
					StructuredPatch: []session.PatchHunk{hunk}}},
			{Type: session.EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "cat > " + long +
					" <<'EOF'\n" + strings.Repeat("x", 300) + "\nEOF"},
				Result: &session.ToolResult{Stdout: strings.Repeat("out ", 300)}},
			// A subagent step carries an extra mark and a time offset beside an
			// already-long title.
			{Type: session.EventToolUse, ToolName: "Grep", IsSidechain: true,
				ToolInput: map[string]interface{}{"pattern": strings.Repeat("p", 200)}},
		},
	}
	steps := session.BuildReplay(sess)

	for _, w := range []int{40, 60, 80, 120} {
		for i := range steps {
			for _, typed := range []int{-1, 0, 7, TypedLength(steps[i], sess)} {
				for _, waiting := range []bool{false, true} {
					out := renderReplay(replayView{sess: sess, steps: steps, idx: i,
						typed: typed, playing: true, waiting: waiting, codeOnly: true,
						delay: replayDefaultDelay, width: w, height: 24})
					for n, line := range visibleLines(out) {
						if got := visibleLen(line); got > w {
							t.Fatalf("width %d, step %d, typed %d: row %d is %d wide\n  %q",
								w, i, typed, n, got, stripAnsi(line))
						}
					}
				}
			}
		}
	}
}

// The frame must also fit the rows it was given, or padToHeight silently trims
// content that was meant to be read.
func TestRenderReplayFitsTerminalHeight(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "+line of code")
	}
	sess := &session.Session{
		Info: session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{
			Type: session.EventToolUse, ToolName: "Edit",
			ToolInput: map[string]interface{}{"file_path": "/repo/a.go"},
			Result: &session.ToolResult{FilePath: "/repo/a.go",
				StructuredPatch: []session.PatchHunk{{Lines: lines}}},
		}},
	}
	steps := session.BuildReplay(sess)

	for _, h := range []int{10, 24, 40} {
		for _, waiting := range []bool{false, true} {
			out := renderReplay(replayView{sess: sess, steps: steps, idx: 0, typed: -1,
				playing: true, waiting: waiting, delay: replayDefaultDelay,
				width: 80, height: h})
			if n := len(visibleLines(out)); n > h-1 {
				t.Errorf("height %d (waiting=%v): frame is %d rows, want at most %d",
					h, waiting, n, h-1)
			}
		}
	}
}

// key builds the KeyMsg the model expects for a run of characters.
func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func namedKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// typeGoto feeds a string to the "go to step" prompt and returns the model.
func typeGoto(m Model, digits string) Model {
	for _, r := range digits {
		mm, _ := m.handleReplayGotoKey(runeKey(string(r)), string(r))
		m = mm.(Model)
	}
	return m
}

func replayModelWithSteps(n int) Model {
	var events []session.Event
	for i := 0; i < n; i++ {
		events = append(events, session.Event{
			Type: session.EventToolUse, ToolName: "Bash",
			ToolInput: map[string]interface{}{"command": "echo " + strconv.Itoa(i)},
		})
	}
	sess := &session.Session{Info: session.SessionInfo{Title: "t"}, Events: events}
	m := Model{selectedSession: sess, mode: viewReplay, replayDelay: replayDefaultDelay}
	m.replaySteps = session.BuildReplay(sess)
	return m
}

func TestReplayGotoJumpsToStepNumber(t *testing.T) {
	m := replayModelWithSteps(50)
	m.replayGotoTyping = true
	m.replayPlaying = true

	m = typeGoto(m, "24")
	if m.replayGotoDraft != "24" {
		t.Fatalf("draft = %q, want %q", m.replayGotoDraft, "24")
	}

	res, _ := m.handleReplayGotoKey(namedKey(tea.KeyEnter), "enter")
	m = res.(Model)

	// Steps are numbered from 1 on screen, so "24" is index 23.
	if m.replayIndex != 23 {
		t.Errorf("replayIndex = %d, want 23", m.replayIndex)
	}
	if m.replayGotoTyping {
		t.Error("prompt should close after jumping")
	}
	if m.replayPlaying {
		t.Error("jumping is a deliberate look, so playback should pause")
	}
	if m.replayTyped != -1 {
		t.Error("the step jumped to should be fully revealed")
	}
}

func TestReplayGotoClampsAndReportsOutOfRange(t *testing.T) {
	m := replayModelWithSteps(10)
	m.replayGotoTyping = true

	m = typeGoto(m, "999")
	res, _ := m.handleReplayGotoKey(namedKey(tea.KeyEnter), "enter")
	m = res.(Model)

	if m.replayIndex != 9 {
		t.Errorf("replayIndex = %d, want 9 (the last step)", m.replayIndex)
	}
	if !strings.Contains(m.statusMsg, "Only 10 steps") {
		t.Errorf("statusMsg = %q, want it to say how many steps there are", m.statusMsg)
	}
}

func TestReplayGotoIgnoresNonDigitsAndCancels(t *testing.T) {
	m := replayModelWithSteps(10)
	m.replayGotoTyping = true

	m = typeGoto(m, "1a2z")
	if m.replayGotoDraft != "12" {
		t.Errorf("draft = %q, want %q: letters are not part of a step number",
			m.replayGotoDraft, "12")
	}

	res, _ := m.handleReplayGotoKey(namedKey(tea.KeyEsc), "esc")
	m = res.(Model)
	if m.replayGotoTyping || m.replayGotoDraft != "" {
		t.Error("esc should close the prompt and discard what was typed")
	}
	if m.replayIndex != 0 {
		t.Error("cancelling must not move the reader")
	}
}

// Following a live session: the transcript grows, the total goes up, and the
// reader stays on the step they were reading.
//
// The index alone cannot do this. linkSubagents splices a subagent's turns into
// the MIDDLE of its parent timeline, so a new background agent shifts every
// index after it — anchoring on the number would silently jump the reader.
func TestRebuildReplayKeepsPositionWhenEarlierStepsAppear(t *testing.T) {
	mk := func(uuid, cmd string) session.Event {
		return session.Event{Type: session.EventToolUse, ToolName: "Bash", UUID: uuid,
			ToolInput: map[string]interface{}{"command": cmd}}
	}

	before := &session.Session{Events: []session.Event{
		mk("a", "one"), mk("b", "two"), mk("c", "three"),
	}}
	m := Model{selectedSession: before, mode: viewReplay}
	m.replaySteps = session.BuildReplay(before)
	m.replayIndex = 2 // reading "three"

	anchor := m.replayStepUUID()
	if anchor != "c" {
		t.Fatalf("anchor = %q, want %q", anchor, "c")
	}

	// A subagent run splices in ahead of the reader, and the session grows.
	after := &session.Session{Events: []session.Event{
		mk("a", "one"), mk("x", "sub-1"), mk("y", "sub-2"), mk("b", "two"),
		mk("c", "three"), mk("d", "four"),
	}}
	m.selectedSession = after
	m.rebuildReplay(anchor)

	if len(m.replaySteps) != 6 {
		t.Fatalf("got %d steps, want 6", len(m.replaySteps))
	}
	if m.replayIndex != 4 {
		t.Errorf("replayIndex = %d, want 4: the reader should still be on \"three\"", m.replayIndex)
	}
	if got := m.replayStepUUID(); got != "c" {
		t.Errorf("anchor after rebuild = %q, want %q", got, "c")
	}
}

// OpenCode events carry no UUID, but their order is fixed when the database is
// parsed, so the index is the right fallback there.
func TestRebuildReplayFallsBackToIndexWithoutUUIDs(t *testing.T) {
	mk := func(cmd string) session.Event {
		return session.Event{Type: session.EventToolUse, ToolName: "Bash",
			ToolInput: map[string]interface{}{"command": cmd}}
	}
	sess := &session.Session{Events: []session.Event{mk("one"), mk("two"), mk("three")}}
	m := Model{selectedSession: sess, mode: viewReplay}
	m.replaySteps = session.BuildReplay(sess)
	m.replayIndex = 1

	grown := &session.Session{Events: append(sess.Events, mk("four"), mk("five"))}
	m.selectedSession = grown
	m.rebuildReplay("")

	if len(m.replaySteps) != 5 {
		t.Fatalf("got %d steps, want 5", len(m.replaySteps))
	}
	if m.replayIndex != 1 {
		t.Errorf("replayIndex = %d, want 1 (held by index)", m.replayIndex)
	}
}
