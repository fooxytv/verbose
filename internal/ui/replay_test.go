package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// The footer shares one row with the keybindings, and a row wider than the
// terminal wraps and pushes the footer off the screen entirely.
func TestFooterFitsAndRightAligns(t *testing.T) {
	sess := &session.Session{Info: session.SessionInfo{
		Title: "t", LastUpdate: time.Now().Add(-90 * time.Second),
	}}

	for _, w := range []int{20, 40, 80, 120, 200} {
		m := Model{width: w, version: "0.12.2", selectedSession: sess}
		keys := []helpKey{{"space", "play"}, {"→/←", "step"}, {"q", "quit"}}
		got := m.footer("", keys)

		if n := visibleLen(got); n >= w {
			t.Errorf("width %d: footer occupies %d columns, want at most %d\n  %q",
				w, n, w-1, stripAnsi(got))
		}
		plain := stripAnsi(got)
		// The age earns its place first: it is what tells a live session from
		// one that stopped. The version is dropped before it is.
		if w >= 80 && !strings.Contains(plain, "1m ago") {
			t.Errorf("width %d: footer lost the session age: %q", w, plain)
		}
		if w >= 120 && !strings.HasSuffix(strings.TrimRight(plain, " "), "v0.12.2") {
			t.Errorf("width %d: version should end the row: %q", w, plain)
		}
	}
}

// With no session open there is no age to show, but the clock and version stay.
func TestFooterWithoutSession(t *testing.T) {
	m := Model{width: 80, version: "1.2.3"}
	plain := stripAnsi(m.footer("", []helpKey{{"q", "quit"}}))
	if strings.Contains(plain, "updated") {
		t.Errorf("no session open, so nothing was updated: %q", plain)
	}
	if !strings.Contains(plain, "v1.2.3") {
		t.Errorf("version missing: %q", plain)
	}
}

// The status is dropped least-useful first, so a narrow terminal keeps the
// session age and loses the version rather than the other way round.
func TestFooterStatusDegradesByUsefulness(t *testing.T) {
	m := Model{width: 200, version: "9.9.9", selectedSession: &session.Session{
		Info: session.SessionInfo{LastUpdate: time.Now().Add(-30 * time.Second)},
	}}

	full := m.footerStatus(100)
	for _, want := range []string{"30s ago", "v9.9.9"} {
		if !strings.Contains(full, want) {
			t.Errorf("with room, status = %q, missing %q", full, want)
		}
	}

	tight := m.footerStatus(20)
	if !strings.Contains(tight, "30s ago") {
		t.Errorf("tight status = %q, should keep the age", tight)
	}
	if strings.Contains(tight, "v9.9.9") {
		t.Errorf("tight status = %q, should have dropped the version", tight)
	}
	if len(tight) > 20 {
		t.Errorf("tight status is %d columns, over its 20 budget: %q", len(tight), tight)
	}

	if got := m.footerStatus(3); got != "" {
		t.Errorf("with no room at all, status = %q, want empty", got)
	}
}

func TestAgoShort(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s ago"},
		{5 * time.Second, "5s ago"},
		{90 * time.Second, "1m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := agoShort(c.d); got != c.want {
			t.Errorf("agoShort(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// The whole-view invariant, which is what actually decides whether the screen
// stays intact: no row wider than the terminal, and no more rows than it has.
//
// A row that overflows is wrapped by the terminal into two, so the frame takes
// more rows than it claims. The terminal then scrolls, and because the renderer
// positions the cursor on the assumption that it did not, rows are left behind
// — the header and footer appear twice, and a line of code being typed appears
// repeated down the screen.
//
// The footer was the real offender: the sessions help line is 129 columns, so it
// wrapped on any terminal narrower than that.
func TestViewNeverOverflowsTerminal(t *testing.T) {
	m, _ := deleteFixture(t)
	sess := m.store.GetSession(m.sessions[0].ID)
	if sess == nil {
		t.Skip("fixture has no parseable session")
	}

	modes := []struct {
		name  string
		setup func(m Model) Model
	}{
		{"sessions", func(m Model) Model { m.mode = viewSessions; return m }},
		{"detail", func(m Model) Model {
			m.mode = viewDetail
			m.selectedSession = sess
			return m
		}},
		{"overview", func(m Model) Model {
			m.mode = viewOverview
			m.selectedSession = sess
			return m
		}},
		{"replay", func(m Model) Model {
			m.startReplay(sess)
			m.replayPlaying = true
			m.replayTyped = 20
			return m
		}},
		{"replay typing mid-step", func(m Model) Model {
			m.startReplay(sess)
			m.replayPlaying = true
			if len(m.replaySteps) > 1 {
				m.replayIndex = 1
			}
			m.replayTyped = 3
			return m
		}},
		{"replay goto prompt", func(m Model) Model {
			m.startReplay(sess)
			m.replayGotoTyping = true
			m.replayGotoDraft = "123"
			return m
		}},
		{"tree", func(m Model) Model {
			m.openTree(sess)
			m.treeCursor = 3
			return m
		}},
		{"tree changed only", func(m Model) Model {
			m.openTree(sess)
			m.treeChangedOnly = true
			m.replayPlaying = true
			return m
		}},
		{"replay with tree sidebar", func(m Model) Model {
			m.startReplay(sess)
			m.loadTreeFor(sess)
			m.treeSplit = true
			m.replayPlaying = true
			m.replayTyped = 30
			return m
		}},
		{"sidebar, changed only", func(m Model) Model {
			m.startReplay(sess)
			m.loadTreeFor(sess)
			m.treeSplit = true
			m.treeChangedOnly = true
			return m
		}},
		{"status message", func(m Model) Model {
			m.mode = viewSessions
			m.statusMsg = "Moved to Trash: a-fairly-long-transcript-file-name.jsonl"
			return m
		}},
	}

	for _, size := range []struct{ w, h int }{{40, 12}, {80, 24}, {100, 30}, {120, 40}, {200, 50}} {
		for _, mode := range modes {
			v := mode.setup(m)
			v.width, v.height = size.w, size.h
			out := v.View()

			rows := visibleLines(out)
			if len(rows) > size.h {
				t.Errorf("%s at %dx%d: %d rows, want at most %d",
					mode.name, size.w, size.h, len(rows), size.h)
			}
			// Strictly less than the width: a row that fills the last
			// column puts the terminal into pending-wrap and costs an extra
			// row, which is what stranded rows on screen.
			for i, row := range rows {
				if n := terminalColumns(row); n >= size.w {
					t.Errorf("%s at %dx%d: row %d draws %d columns, want at most %d\n  %q",
						mode.name, size.w, size.h, i, n, size.w-1, stripAnsi(row))
				}
			}
		}
	}
}

// Dropping keys is preferable to wrapping, but the first ones must survive and
// the omission must be visible.
func TestRenderHelpFitDropsFromTheEnd(t *testing.T) {
	keys := []helpKey{
		{"a", "first"}, {"b", "second"}, {"c", "third"}, {"d", "fourth"},
	}
	full := renderHelp(keys)

	if got := renderHelpFit(keys, visibleLen(full)); got != full {
		t.Error("with room for everything, nothing should be dropped")
	}

	narrow := renderHelpFit(keys, 30)
	if n := visibleLen(narrow); n > 30 {
		t.Errorf("fitted help is %d columns, want at most 30", n)
	}
	plain := stripAnsi(narrow)
	if !strings.Contains(plain, "a first") {
		t.Errorf("the first key must survive: %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("a dropped key must be marked: %q", plain)
	}

	// Absurdly narrow: still must not exceed the width.
	if n := visibleLen(renderHelpFit(keys, 4)); n > 4 {
		t.Errorf("help is %d columns at width 4", n)
	}
	if got := renderHelpFit(nil, 80); got != "" {
		t.Errorf("no keys should render nothing, got %q", got)
	}
}

// terminalColumns counts the columns a terminal actually draws, expanding any
// surviving tab to the terminal's own default stop of 8. visibleLen counts a
// tab as one, so it cannot be used to check this.
func terminalColumns(s string) int {
	n, inEsc := 0, false
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
		if r == '\t' {
			n += 8 - (n % 8)
			continue
		}
		n++
	}
	return n
}

func TestExpandTabs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\tb", "a   b"},        // to the next stop of 4
		{"\tx", "    x"},         // a full stop from column 0
		{"ab\tc", "ab  c"},       //
		{"abcd\te", "abcd    e"}, // already on a stop: a whole one follows
		{"no tabs here", "no tabs here"},
	}
	for _, c := range cases {
		if got := expandTabs(c.in); got != c.want {
			t.Errorf("expandTabs(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Colour escapes must not shift the stops: the visible text decides them.
	coloured := "\x1b[31ma\x1b[0m\tb"
	if got, want := expandTabs(coloured), "\x1b[31ma\x1b[0m   b"; got != want {
		t.Errorf("expandTabs(coloured) = %q, want %q", got, want)
	}
}

// The real invariant, measured the way a terminal measures: a tabbed line of
// code must not draw past the edge. visibleLen counts a tab as one column
// while a terminal draws it as up to eight, so code indented with tabs
// overflowed and wrapped, stranding rows on screen — including while merely
// scrolling a paused step.
func TestViewNeverOverflowsWithTabbedCode(t *testing.T) {
	code := "package main\n\nfunc main() {\n\tfor i := 0; i < 10; i++ {\n" +
		"\t\tif x := compute(i); x > 0 {\n" +
		"\t\t\tfmt.Println(\"a reasonably long line of output here\", i, x)\n" +
		"\t\t}\n\t}\n}"
	sess := &session.Session{
		Info: session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{
			Type:     session.EventToolUse,
			ToolName: "Write",
			ToolInput: map[string]interface{}{
				"file_path": "/repo/main.go", "content": code,
			},
			Result: &session.ToolResult{FilePath: "/repo/main.go"},
		}, {
			// The same shape through a diff, which renders by another path.
			Type:      session.EventToolUse,
			ToolName:  "Edit",
			ToolInput: map[string]interface{}{"file_path": "/repo/b.go"},
			Result: &session.ToolResult{FilePath: "/repo/b.go",
				StructuredPatch: []session.PatchHunk{{Lines: []string{
					"+\t\t\tif err := doSomethingWithAVeryLongName(ctx, x); err != nil {",
					"-\t\t\treturn fmt.Errorf(\"wrapping an error message here: %w\", err)",
					" \t\t\tcontext line with tabs",
				}}}},
		}},
	}

	m := Model{mode: viewReplay, version: "0.0.0", selectedSession: sess}
	m.replaySteps = session.BuildReplay(sess)

	for _, w := range []int{40, 60, 80, 100} {
		for i := range m.replaySteps {
			for _, typed := range []int{-1, 0, 11, 60} {
				v := m
				v.width, v.height = w, 20
				v.replayIndex = i
				v.replayTyped = typed
				v.replayPlaying = typed >= 0

				for n, row := range visibleLines(v.View()) {
					if got := terminalColumns(row); got >= w {
						t.Fatalf("width %d, step %d, typed %d: row %d draws %d columns\n  %q",
							w, i, typed, n, got, stripAnsi(row))
					}
				}
			}
		}
	}
}

// treeModel builds a real project directory and a session that worked in it, so
// the tree tests exercise actual rows rather than skipping.
func treeModel(t *testing.T) Model {
	t.Helper()
	root := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	main := write("src/main.go")
	util := write("src/util.go")
	write("src/untouched.go")
	write("README.md")

	sess := &session.Session{
		Info: session.SessionInfo{ID: "t1", Title: "tree fixture", CWD: root},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "do the work"},
			{Type: session.EventToolUse, ToolName: "Read",
				ToolInput: map[string]interface{}{"file_path": main}},
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": main},
				Result: &session.ToolResult{FilePath: main,
					StructuredPatch: []session.PatchHunk{{Lines: []string{"+a", "-b"}}}}},
			{Type: session.EventToolUse, ToolName: "Write",
				ToolInput: map[string]interface{}{"file_path": util, "content": "y\n"},
				Result:    &session.ToolResult{FilePath: util}},
			{Type: session.EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "echo done"}},
		},
	}

	m := Model{width: 100, height: 30, version: "0.0.0"}
	m.replaySteps = session.BuildReplay(sess)
	m.openTree(sess)
	return m
}

func TestTreeCursorNavigationAndCollapse(t *testing.T) {
	m := treeModel(t)
	rows := m.treeRows()
	if len(rows) < 2 {
		t.Fatalf("fixture tree has only %d rows", len(rows))
	}

	// Down then up returns to where it started.
	res, _ := m.handleTreeKey(namedKey(tea.KeyDown), "down")
	m = res.(Model)
	if m.treeCursor != 1 {
		t.Fatalf("cursor = %d after down, want 1", m.treeCursor)
	}
	res, _ = m.handleTreeKey(namedKey(tea.KeyUp), "up")
	m = res.(Model)
	if m.treeCursor != 0 {
		t.Errorf("cursor = %d after up, want 0", m.treeCursor)
	}

	// Collapsing the root hides everything beneath it.
	root := rows[0].node
	if !root.IsDir {
		t.Skip("root is not a directory")
	}
	res, _ = m.handleTreeKey(namedKey(tea.KeyLeft), "left")
	m = res.(Model)
	if !m.treeCollapsed[root.Path] {
		t.Fatal("left should collapse the directory under the cursor")
	}
	if n := len(m.treeRows()); n != 1 {
		t.Errorf("collapsed root leaves %d rows, want 1", n)
	}

	res, _ = m.handleTreeKey(namedKey(tea.KeyRight), "right")
	m = res.(Model)
	if m.treeCollapsed[root.Path] {
		t.Error("right should open it again")
	}
}

// The cursor must never point past the rows, including after the filter cuts
// most of them away.
func TestTreeCursorStaysInRangeAcrossFilter(t *testing.T) {
	m := treeModel(t)
	m.treeCursor = len(m.treeRows()) - 1

	res, _ := m.handleTreeKey(namedKey(tea.KeyTab), "tab")
	m = res.(Model)
	if !m.treeChangedOnly {
		t.Fatal("tab should turn on the changed-only filter")
	}
	if rows := m.treeRows(); m.treeCursor > max(0, len(rows)-1) {
		t.Errorf("cursor %d is past the %d filtered rows", m.treeCursor, len(rows))
	}
	// Rendering with a stale cursor must not panic either.
	m.treeCursor = 9999
	if out := renderTree(m.treeViewState(usableWidth(m.width), m.height)); out == "" {
		t.Error("empty render with an out-of-range cursor")
	}
}

// The tree is drawn as of the replay position, so rewinding hides later work.
func TestTreeFollowsReplayPosition(t *testing.T) {
	m := treeModel(t)
	if len(m.replaySteps) < 3 {
		t.Fatalf("fixture has only %d replay steps", len(m.replaySteps))
	}

	m.treeChangedOnly = true
	m.replayIndex = len(m.replaySteps) - 1
	atEnd := len(m.treeRows())

	m.replayIndex = 0
	atStart := len(m.treeRows())

	if atStart > atEnd {
		t.Errorf("tree shows %d changed rows at the first step but %d at the last: "+
			"it should grow as the replay advances, not shrink", atStart, atEnd)
	}
	if m.treeUpto() != m.replaySteps[0].EventIndex {
		t.Errorf("treeUpto = %d, want the first step's event index %d",
			m.treeUpto(), m.replaySteps[0].EventIndex)
	}
}

func TestTreeFadeStageRamps(t *testing.T) {
	if got := treeFadeStage(-time.Second); got != 0 {
		t.Errorf("a step reached in the future should be at the start, got %d", got)
	}
	if got := treeFadeStage(0); got != 0 {
		t.Errorf("stage at 0 = %d, want 0", got)
	}
	if got := treeFadeStage(treeFadeDuration * 2); got != treeFadeStages {
		t.Errorf("long past the ramp = %d, want %d (settled)", got, treeFadeStages)
	}
	// Monotonic: a highlight never gets brighter as time passes.
	last := -1
	for d := time.Duration(0); d < treeFadeDuration*2; d += treeFadeDuration / 6 {
		s := treeFadeStage(d)
		if s < last {
			t.Fatalf("fade went backwards at %v: %d after %d", d, s, last)
		}
		last = s
	}
}

// The split exists to be read, so the code pane must keep enough width to read
// code in. Below the floor it refuses and shows the replay full-width rather
// than two unusable columns.
func TestSidebarWidthRefusesNarrowTerminals(t *testing.T) {
	for _, w := range []int{20, 60, 80, splitMinWidth - 1} {
		if got := sidebarWidth(w); got != 0 {
			t.Errorf("sidebarWidth(%d) = %d, want 0: too narrow to split", w, got)
		}
	}
	for _, w := range []int{100, 120, 160, 300} {
		got := sidebarWidth(w)
		if got < treeSidebarMin || got > treeSidebarMax {
			t.Errorf("sidebarWidth(%d) = %d, want between %d and %d",
				w, got, treeSidebarMin, treeSidebarMax)
		}
		// Whatever is left has to be worth reading code in.
		if main := usableWidth(w) - got - 3; main < 50 {
			t.Errorf("width %d leaves only %d columns for code", w, main)
		}
	}
}

// Every row of a split frame has to be exactly the terminal's width minus one,
// because a short row lets the right pane slide left on that line alone and a
// long one pushes it off the screen.
func TestSplitRowsAlignExactly(t *testing.T) {
	m := treeModel(t)
	m.treeSplit = true
	m.replayPlaying = true

	for _, w := range []int{100, 120, 160, 200} {
		for _, typed := range []int{-1, 0, 25} {
			v := m
			v.width, v.height = w, 24
			v.replayTyped = typed

			side := sidebarWidth(w)
			if side == 0 {
				t.Fatalf("width %d should split", w)
			}
			want := side + 3 + (usableWidth(w) - side - 3)

			rows := visibleLines(renderSplit(v))
			for i, r := range rows {
				if got := terminalColumns(r); got != want {
					t.Fatalf("width %d typed %d: split row %d is %d columns, want exactly %d\n  %q",
						w, typed, i, got, want, stripAnsi(r))
				}
			}
		}
	}
}

// Toggling the sidebar on a terminal too narrow for it must say so rather than
// silently doing nothing.
func TestSplitToggleExplainsWhenTooNarrow(t *testing.T) {
	m := treeModel(t)
	m.mode = viewReplay
	m.width = 80

	res, _ := m.handleReplayKey(runeKey("T"), "T")
	m = res.(Model)
	if m.treeSplit {
		t.Error("the split should be refused at 80 columns")
	}
	if !strings.Contains(m.statusMsg, "columns") {
		t.Errorf("statusMsg = %q, want it to explain the width needed", m.statusMsg)
	}

	// Wide enough: it turns on, and off again.
	m.width = 160
	m.statusMsg = ""
	res, _ = m.handleReplayKey(runeKey("T"), "T")
	m = res.(Model)
	if !m.treeSplit {
		t.Fatal("T should turn the sidebar on at 160 columns")
	}
	res, _ = m.handleReplayKey(runeKey("T"), "T")
	m = res.(Model)
	if m.treeSplit {
		t.Error("T again should turn it off")
	}
}

func TestPadPaneExactWidth(t *testing.T) {
	cases := []string{"", "short", "a\tb", "\x1b[31mcoloured\x1b[0m",
		strings.Repeat("long ", 40)}
	for _, in := range cases {
		out := padPane(in, 20)
		if got := terminalColumns(out); got != 20 {
			t.Errorf("padPane(%q) is %d columns, want 20", stripAnsi(in), got)
		}
	}
}

// A project tree is far longer than the pane showing it, so the file being
// changed has to be scrolled to. Without this, 74 of the 90 steps that lit a
// file in one real session lit one below the fold and the highlight was never
// seen at all.
func TestTreeFollowsTheChangeIntoView(t *testing.T) {
	root := t.TempDir()
	// Enough files that the tree cannot fit in the pane.
	var paths []string
	for i := 0; i < 60; i++ {
		p := filepath.Join(root, "src", fmt.Sprintf("file%02d.go", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	// Edit them in order, so the change marches down the tree.
	var events []session.Event
	for _, p := range paths {
		events = append(events, session.Event{
			Type: session.EventToolUse, ToolName: "Edit",
			ToolInput: map[string]interface{}{"file_path": p},
			Result: &session.ToolResult{FilePath: p,
				StructuredPatch: []session.PatchHunk{{Lines: []string{"+a"}}}},
		})
	}
	sess := &session.Session{
		Info:   session.SessionInfo{ID: "t", Title: "t", CWD: root},
		Events: events,
	}

	m := Model{width: 140, height: 20, version: "0.0.0"}
	m.replaySteps = session.BuildReplay(sess)
	m.loadTreeFor(sess)
	m.treeSplit = true

	side := sidebarWidth(m.width)
	if side == 0 {
		t.Fatal("140 columns should split")
	}

	for i := range m.replaySteps {
		m.replayIndex = i
		v := m.treeViewState(side, m.height-1)
		at := m.treeFocusRow(v.rows, m.treeUpto())
		if at < 0 {
			t.Fatalf("step %d: no focusable change, but every step edits a file", i)
		}
		visible := treeVisibleRows(v.height)
		if at < v.scroll || at >= v.scroll+visible {
			t.Fatalf("step %d: focused row %d is outside the visible window [%d,%d)",
				i, at, v.scroll, v.scroll+visible)
		}
	}
}

// Moving the cursor in the full-screen tree takes over; the split has no cursor
// of its own to fight with, so it always follows.
func TestTreeFollowStopsOnManualNavigation(t *testing.T) {
	m := treeModel(t)
	m.replayPlaying = true
	if !m.treeFollowing() {
		t.Fatal("a playing tree should follow the change")
	}

	res, _ := m.handleTreeKey(namedKey(tea.KeyDown), "down")
	m = res.(Model)
	if m.treeFollowing() {
		t.Error("moving the cursor should hand control to the reader")
	}

	// Asking it to play is asking to watch, so following resumes. (It was
	// already playing, so space pauses first.)
	res, _ = m.handleTreeKey(tea.KeyMsg{Type: tea.KeySpace}, " ")
	m = res.(Model)
	if m.replayPlaying {
		t.Fatal("the first space should pause")
	}
	res, _ = m.handleTreeKey(tea.KeyMsg{Type: tea.KeySpace}, " ")
	m = res.(Model)
	if !m.replayPlaying || !m.treeFollowing() {
		t.Error("playing again should resume following")
	}

	// The sidebar follows too, but it is no longer exempt: it has a cursor of
	// its own now, so the reader can take it over there as well.
	m.treeFollow = true
	m.replayPlaying = false
	m.treeSplit = true
	if !m.treeFollowing() {
		t.Error("an untouched sidebar should follow")
	}
	m.treeFollow = false
	if m.treeFollowing() {
		t.Error("a sidebar the reader has taken over should stay put")
	}
}

// The flash has to leave a mark. A change that has settled is still coloured as
// a change, not returned to neutral.
func TestTreeFlashSettlesIntoAPersistentMark(t *testing.T) {
	path := "/repo/a.go"
	mk := func(kind session.TouchKind) treeView {
		return treeView{
			activity: map[string]*session.FileActivity{
				path: {Path: path, Touches: []session.FileTouch{{EventIndex: 3, Kind: kind}}},
			},
			upto: 3, justNow: 3,
		}
	}
	row := treeRow{node: &session.TreeNode{Name: "a.go", Path: path}}

	for _, kind := range []session.TouchKind{
		session.TouchCreate, session.TouchEdit, session.TouchWrite, session.TouchDelete,
	} {
		v := mk(kind)

		// Fresh: lit.
		v.fade = 0
		fresh, _ := treeRowStyle(v, row)
		if !fresh.GetReverse() {
			t.Errorf("%v: a change should be lit when it first lands", kind)
		}

		// Settled: still marked, and in the colour for its kind.
		v.fade = treeFadeStages
		settled, _ := treeRowStyle(v, row)
		if settled.GetReverse() {
			t.Errorf("%v: the flash should not stay reversed", kind)
		}
		if settled.GetForeground() == mutedStyle.GetForeground() {
			t.Errorf("%v: a settled change must keep a mark, not go neutral", kind)
		}
	}

	// An untouched file in the same tree stays neutral.
	v := mk(session.TouchEdit)
	other := treeRow{node: &session.TreeNode{Name: "b.go", Path: "/repo/b.go"}}
	style, _ := treeRowStyle(v, other)
	if style.GetForeground() != mutedStyle.GetForeground() {
		t.Error("a file the session never touched should stay dim")
	}
}

// In the split every plain arrow belongs to the replay, which left the tree
// unreachable while it was running. ctrl with an arrow drives the tree instead.
func TestSidebarKeysDriveTheTree(t *testing.T) {
	m := treeModel(t)
	m.mode = viewReplay
	m.treeSplit = true
	m.width, m.height = 160, 24
	m.replayPlaying = true

	before := m.replayIndex

	// "]" and "[" are the bindings that work everywhere: ctrl with the arrows
	// is swallowed by macOS before the terminal sees it.
	res, _ := m.handleReplayKey(runeKey("]"), "]")
	m = res.(Model)
	if m.treeCursor != 1 {
		t.Errorf("tree cursor = %d after \"]\", want 1", m.treeCursor)
	}
	if m.replayIndex != before {
		t.Error("ctrl+down must not move the replay")
	}
	if m.treeFollowing() {
		t.Error("driving the tree by hand should stop the follow")
	}

	res, _ = m.handleReplayKey(runeKey("["), "[")
	m = res.(Model)
	if m.treeCursor != 0 {
		t.Errorf("tree cursor = %d after \"[\", want 0", m.treeCursor)
	}

	// The ctrl bindings remain as aliases for anyone who has remapped their
	// system shortcuts.
	res, _ = m.handleReplayKey(namedKey(tea.KeyCtrlDown), "ctrl+down")
	m = res.(Model)
	if m.treeCursor != 1 {
		t.Errorf("tree cursor = %d after ctrl+down, want 1", m.treeCursor)
	}
	res, _ = m.handleReplayKey(runeKey("["), "[")
	m = res.(Model)

	// ctrl+f gives the follow back.
	res, _ = m.handleReplayKey(namedKey(tea.KeyCtrlF), "ctrl+f")
	m = res.(Model)
	if !m.treeFollowing() {
		t.Error("ctrl+f should hand the follow back to the replay")
	}

	// A plain arrow still belongs to the replay, so nothing changes meaning
	// when the sidebar opens.
	m.replayPlaying = true
	cursorBefore := m.treeCursor
	res, _ = m.handleReplayKey(namedKey(tea.KeyRight), "right")
	m = res.(Model)
	if m.treeCursor != cursorBefore {
		t.Error("a plain arrow must not move the tree")
	}
}

// ctrl+left and ctrl+right open and close directories in the sidebar.
func TestSidebarKeysCollapseDirectories(t *testing.T) {
	m := treeModel(t)
	m.mode = viewReplay
	m.treeSplit = true
	m.width, m.height = 160, 24

	root := m.treeRows()[0].node
	if !root.IsDir {
		t.Skip("root is not a directory")
	}

	res, _ := m.handleReplayKey(runeKey("{"), "{")
	m = res.(Model)
	if !m.treeCollapsed[root.Path] {
		t.Fatal("\"{\" should close the directory under the cursor")
	}
	if n := len(m.treeRows()); n != 1 {
		t.Errorf("closed root leaves %d rows, want 1", n)
	}

	res, _ = m.handleReplayKey(runeKey("}"), "}")
	m = res.(Model)
	if m.treeCollapsed[root.Path] {
		t.Error("\"}\" should open it again")
	}
}

// The cursor must stay on screen in the sidebar's pane, which is shorter than
// the terminal by the footer.
func TestSidebarScrollsCursorIntoView(t *testing.T) {
	m := treeModel(t)
	m.mode = viewReplay
	m.treeSplit = true
	m.width, m.height = 160, 14

	for i := 0; i < 40; i++ {
		res, _ := m.handleReplayKey(runeKey("]"), "]")
		m = res.(Model)

		v := m.treeViewState(sidebarWidth(m.width), m.height-1)
		visible := treeVisibleRows(v.height)
		if v.cursor < v.scroll || v.cursor >= v.scroll+visible {
			t.Fatalf("after %d moves the cursor %d is outside [%d,%d)",
				i+1, v.cursor, v.scroll, v.scroll+visible)
		}
	}
}

// "/" means search on the timeline and go-to-step in a replay. They are
// different views, so both are live — but the replay intercepts every key
// while it is open, so a careless change to where that interception happens
// would silently take the timeline's search away.
func TestSlashMeansSearchOnTimelineAndGotoInReplay(t *testing.T) {
	m, _ := deleteFixture(t)
	sess := m.store.GetSession(m.sessions[0].ID)
	if sess == nil {
		t.Skip("fixture has no parseable session")
	}
	m.width, m.height = 120, 30

	// On the timeline it opens search.
	timeline := m
	timeline.mode = viewDetail
	timeline.selectedSession = sess
	res, _ := timeline.handleKey(runeKey("/"))
	timeline = res.(Model)
	if !timeline.searchTyping {
		t.Error("/ on the timeline should open search")
	}
	if timeline.replayGotoTyping {
		t.Error("/ on the timeline must not open the go-to-step prompt")
	}

	// In a replay it opens go-to-step.
	replay := m
	replay.startReplay(sess)
	res, _ = replay.handleKey(runeKey("/"))
	replay = res.(Model)
	if !replay.replayGotoTyping {
		t.Error("/ in a replay should open go-to-step")
	}
	if replay.searchTyping {
		t.Error("/ in a replay must not open search")
	}

	// And the footers say so.
	timeline.searchTyping = false
	if foot := stripAnsi(lastRow(timeline.View())); !strings.Contains(foot, "/ search") {
		t.Errorf("timeline footer should offer search: %q", foot)
	}
	replay.replayGotoTyping = false
	if foot := stripAnsi(lastRow(replay.View())); !strings.Contains(foot, "/ go to step") {
		t.Errorf("replay footer should offer go to step: %q", foot)
	}
}

func lastRow(frame string) string {
	rows := visibleLines(frame)
	return rows[len(rows)-1]
}

// Typing a query on the timeline still filters the events, which is the part
// that would actually be missed.
func TestTimelineSearchStillFilters(t *testing.T) {
	m, _ := deleteFixture(t)
	sess := m.store.GetSession(m.sessions[0].ID)
	if sess == nil {
		t.Skip("fixture has no parseable session")
	}
	m.mode = viewDetail
	m.selectedSession = sess
	m.width, m.height = 120, 30

	all := len(m.visibleEvents())

	res, _ := m.handleKey(runeKey("/"))
	m = res.(Model)
	for _, r := range "zzzznotpresentzzzz" {
		res, _ = m.handleKey(runeKey(string(r)))
		m = res.(Model)
	}
	res, _ = m.handleKey(namedKey(tea.KeyEnter))
	m = res.(Model)

	if m.searchQuery != "zzzznotpresentzzzz" {
		t.Fatalf("searchQuery = %q, want the typed text", m.searchQuery)
	}
	if got := len(m.visibleEvents()); got >= all && all > 0 {
		t.Errorf("search matched %d of %d events; it should have filtered them out", got, all)
	}
}

// wheel builds the message bubbletea delivers for a scroll at a column.
func wheel(up bool, x int) tea.MouseMsg {
	btn := tea.MouseButtonWheelDown
	if up {
		btn = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{Button: btn, Action: tea.MouseActionPress, X: x}
}

// The tree and the replay were the only views the wheel did nothing in, while
// the README claimed every view supported it.
func TestMouseScrollsTreeAndReplay(t *testing.T) {
	m := treeModel(t)
	m.mode = viewTree
	m.width, m.height = 120, 24

	res, _ := m.handleMouse(wheel(false, 0))
	m = res.(Model)
	if m.treeCursor != 1 {
		t.Errorf("tree cursor = %d after a wheel down, want 1", m.treeCursor)
	}
	res, _ = m.handleMouse(wheel(true, 0))
	m = res.(Model)
	if m.treeCursor != 0 {
		t.Errorf("tree cursor = %d after a wheel up, want 0", m.treeCursor)
	}

	// In the replay the wheel scrolls the step, and pauses, as the arrows do.
	r := treeModel(t)
	r.mode = viewReplay
	r.width, r.height = 120, 24
	r.replayPlaying = true
	res, _ = r.handleMouse(wheel(false, 60))
	r = res.(Model)
	if r.replayScroll != 1 {
		t.Errorf("replayScroll = %d after a wheel down, want 1", r.replayScroll)
	}
	if r.replayPlaying {
		t.Error("scrolling the replay should pause it, like the arrow keys")
	}
}

// With the sidebar open the pointer decides which pane moves.
func TestMouseScrollRoutesByPaneInSplit(t *testing.T) {
	m := treeModel(t)
	m.mode = viewReplay
	m.treeSplit = true
	m.width, m.height = 160, 24

	side := sidebarWidth(m.width)
	if side == 0 {
		t.Fatal("160 columns should split")
	}

	// Over the tree.
	res, _ := m.handleMouse(wheel(false, side/2))
	over := res.(Model)
	if over.treeCursor != 1 {
		t.Errorf("tree cursor = %d with the pointer over the tree, want 1", over.treeCursor)
	}
	if over.replayScroll != 0 {
		t.Error("the replay must not scroll when the pointer is over the tree")
	}

	// Over the code.
	res, _ = m.handleMouse(wheel(false, side+20))
	overCode := res.(Model)
	if overCode.replayScroll != 1 {
		t.Errorf("replayScroll = %d with the pointer over the code, want 1", overCode.replayScroll)
	}
	if overCode.treeCursor != 0 {
		t.Error("the tree must not move when the pointer is over the code")
	}
}

// treeFocusRow walks a map, whose order Go randomises, and one shell command
// can mention several files — so several touches share an event index. Picking
// whichever came last would return a different row on every frame and the tree
// would jitter thirty times a second.
func TestTreeFocusIsDeterministicAndPrefersChanges(t *testing.T) {
	rows := []treeRow{
		{node: &session.TreeNode{Name: "a.go", Path: "/r/a.go"}},
		{node: &session.TreeNode{Name: "b.go", Path: "/r/b.go"}},
		{node: &session.TreeNode{Name: "c.go", Path: "/r/c.go"}},
	}
	// All three touched by the same step: two reads and one edit.
	m := Model{treeActivity: map[string]*session.FileActivity{
		"/r/a.go": {Path: "/r/a.go", Touches: []session.FileTouch{
			{EventIndex: 4, Kind: session.TouchRead}}},
		"/r/b.go": {Path: "/r/b.go", Touches: []session.FileTouch{
			{EventIndex: 4, Kind: session.TouchEdit}}},
		"/r/c.go": {Path: "/r/c.go", Touches: []session.FileTouch{
			{EventIndex: 4, Kind: session.TouchRead}}},
	}}

	// The edit wins, every time.
	for i := 0; i < 200; i++ {
		if got := m.treeFocusRow(rows, 10); got != 1 {
			t.Fatalf("iteration %d: focus = %d, want 1 (the edited file)", i, got)
		}
	}

	// With only reads at that step, the earlier row wins — and keeps winning.
	m.treeActivity["/r/b.go"].Touches[0].Kind = session.TouchRead
	first := m.treeFocusRow(rows, 10)
	for i := 0; i < 200; i++ {
		if got := m.treeFocusRow(rows, 10); got != first {
			t.Fatalf("iteration %d: focus moved from %d to %d between identical calls",
				i, first, got)
		}
	}
	if first != 0 {
		t.Errorf("focus = %d, want the earliest row when nothing outranks", first)
	}
}

// A later step always outranks an earlier one, whatever the kinds.
func TestTreeFocusPrefersTheMostRecentStep(t *testing.T) {
	rows := []treeRow{
		{node: &session.TreeNode{Name: "old.go", Path: "/r/old.go"}},
		{node: &session.TreeNode{Name: "new.go", Path: "/r/new.go"}},
	}
	m := Model{treeActivity: map[string]*session.FileActivity{
		"/r/old.go": {Touches: []session.FileTouch{{EventIndex: 2, Kind: session.TouchEdit}}},
		"/r/new.go": {Touches: []session.FileTouch{{EventIndex: 9, Kind: session.TouchRead}}},
	}}

	if got := m.treeFocusRow(rows, 20); got != 1 {
		t.Errorf("focus = %d, want the more recent touch even though it is only a read", got)
	}
	// Rewound before it, the older change is the most recent thing that happened.
	if got := m.treeFocusRow(rows, 5); got != 0 {
		t.Errorf("focus = %d at step 5, want the older change", got)
	}
	if got := m.treeFocusRow(rows, 1); got != -1 {
		t.Errorf("focus = %d before anything happened, want -1", got)
	}
}

// The diff panel must obey the same layout invariants as everything else, in
// both views it can open from and at both the two-column and unified widths.
func TestDiffPanelNeverOverflows(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("a very long line of code that will not fit ", 4)
	sess := &session.Session{
		Info: session.SessionInfo{ID: "t", Title: "t", CWD: root},
		Events: []session.Event{
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": path},
				Result: &session.ToolResult{FilePath: path,
					StructuredPatch: []session.PatchHunk{{
						OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4,
						Lines: []string{
							" \tcontext with a tab",
							"-" + long,
							"+" + long + " changed",
							"+extra added line",
							"-removed with no partner",
						},
					}}},
			},
		},
	}

	m := Model{version: "0.0.0"}
	m.replaySteps = session.BuildReplay(sess)
	m.loadTreeFor(sess)
	m.replayIndex = len(m.replaySteps) - 1
	m.diffPath = path

	for _, mode := range []viewMode{viewReplay, viewTree} {
		for _, split := range []bool{false, true} {
			for _, size := range []struct{ w, h int }{{60, 12}, {100, 24}, {160, 30}, {220, 40}} {
				v := m
				v.mode = mode
				v.treeSplit = split
				v.width, v.height = size.w, size.h

				rows := visibleLines(v.View())
				if len(rows) > size.h {
					t.Errorf("mode %v split=%v at %dx%d: %d rows, want at most %d",
						mode, split, size.w, size.h, len(rows), size.h)
				}
				for i, r := range rows {
					if n := terminalColumns(r); n >= size.w {
						t.Fatalf("mode %v split=%v at %dx%d: row %d draws %d columns\n  %q",
							mode, split, size.w, size.h, i, n, stripAnsi(r))
					}
				}
			}
		}
	}
}

// Two columns need width; below the threshold the panel says so and falls back
// to a single column rather than showing two unreadable ones.
func TestDiffFallsBackToUnifiedWhenNarrow(t *testing.T) {
	m := treeModel(t)
	m.diffPath = m.treeRows()[1].node.Path

	wide := m.diffViewState(diffSideBySideMin, 24)
	if wide.unified {
		t.Error("at the threshold the panel should still be two columns")
	}
	narrow := m.diffViewState(diffSideBySideMin-1, 24)
	if !narrow.unified {
		t.Error("below the threshold it should fall back to one column")
	}
}

// The diff is bounded by the replay position, exactly as the tree's colours
// are, and says so rather than looking empty.
func TestDiffIsBoundedByReplayPosition(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{
		Info: session.SessionInfo{CWD: root},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "go"},
			{Type: session.EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "echo hi"}},
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": path},
				Result: &session.ToolResult{FilePath: path,
					StructuredPatch: []session.PatchHunk{{Lines: []string{"+new"}}}}},
		},
	}
	m := Model{width: 160, height: 24, version: "0.0.0"}
	m.replaySteps = session.BuildReplay(sess)
	m.loadTreeFor(sess)
	m.diffPath = path

	m.replayIndex = 0
	early := m.diffViewState(100, 24)
	if len(early.changes) != 0 {
		t.Errorf("at the first step the diff shows %d changes, want none", len(early.changes))
	}
	if early.later != 1 {
		t.Errorf("later = %d, want 1 so the panel can explain itself", early.later)
	}
	if out := renderFileDiff(early); !strings.Contains(stripAnsi(out), "Not changed yet") {
		t.Errorf("empty panel should say the change has not happened yet:\n%s", stripAnsi(out))
	}

	m.replayIndex = len(m.replaySteps) - 1
	late := m.diffViewState(100, 24)
	if len(late.changes) != 1 {
		t.Errorf("at the end the diff shows %d changes, want 1", len(late.changes))
	}
}
