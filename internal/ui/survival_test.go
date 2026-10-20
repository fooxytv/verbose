package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

func lostEdit(path string, lines ...string) session.Event {
	return session.Event{
		Type: session.EventToolUse, ToolName: "Edit",
		ToolInput: map[string]interface{}{"file_path": path},
		Result: &session.ToolResult{FilePath: path,
			StructuredPatch: []session.PatchHunk{{Lines: lines}}},
	}
}

// A project with two edits: one whose lines are still in the file, one whose
// are not.
func lostFixture(t *testing.T) (Model, *session.Session, string) {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kept := write("kept.go", "package app\n\nfunc Kept() {}\n")
	lost := write("retry.go", "package app\n\nfunc Retry() {}\n")

	sess := &session.Session{
		Info: session.SessionInfo{ID: "s1", Title: "retry work", CWD: root},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "make the retries smarter"},
			lostEdit(kept, "+func Kept() {}"),
			lostEdit(lost, "+func exponentialBackoff(attempt int) time.Duration {",
				"+\treturn time.Second << attempt", "+}"),
		},
	}
	m := Model{width: 120, height: 40, version: "0.0.0"}
	m.startReplay(sess)
	return m, sess, lost
}

func TestLostWorkFilterNarrowsToWhatIsGone(t *testing.T) {
	m, _, lost := lostFixture(t)
	before := len(m.replaySteps)
	if before != 3 {
		t.Fatalf("fixture built %d steps, want 3", before)
	}

	m, _ = press(t, m, "u")

	if !m.replayLostOnly {
		t.Fatal("u did not open the lost-work filter")
	}
	if len(m.replaySteps) != 1 {
		t.Fatalf("got %d steps, want only the one whose work is gone", len(m.replaySteps))
	}
	if m.replaySteps[0].FilePath != lost {
		t.Errorf("kept the wrong step: %s, want %s", m.replaySteps[0].FilePath, lost)
	}
	if want := fmt.Sprintf("%d of %d", 1, before); !strings.Contains(m.statusMsg, want) {
		t.Errorf("status %q does not count the steps as %q", m.statusMsg, want)
	}
}

func TestLostWorkFilterTogglesBackToEveryStep(t *testing.T) {
	m, _, _ := lostFixture(t)

	m, _ = press(t, m, "u")
	m, _ = press(t, m, "u")

	if m.replayLostOnly {
		t.Error("the filter is still open after a second u")
	}
	if len(m.replaySteps) != 3 {
		t.Errorf("got %d steps back, want all 3", len(m.replaySteps))
	}
	if m.replayLostFates != nil {
		t.Error("the fates outlived the filter, so a step would still claim it lost work")
	}
	if m.replayLostLine() != "" {
		t.Errorf("a line about lost work survived the toggle: %q", m.replayLostLine())
	}
}

// Entering a filter with nothing in it would blank the screen, which reads as
// the replay breaking rather than as an answer.
func TestLostWorkFilterWithNothingLostLeavesTheListAlone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "kept.go")
	if err := os.WriteFile(path, []byte("package app\n\nfunc Kept() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{
		Info: session.SessionInfo{ID: "s", Title: "t", CWD: root},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "keep it"},
			lostEdit(path, "+func Kept() {}"),
		},
	}
	m := Model{width: 120, height: 40, version: "0.0.0"}
	m.startReplay(sess)

	m, _ = press(t, m, "u")

	if m.replayLostOnly {
		t.Error("the filter opened with nothing to show in it")
	}
	if len(m.replaySteps) != 2 {
		t.Errorf("got %d steps, want the list untouched", len(m.replaySteps))
	}
	if !strings.Contains(m.statusMsg, "still in the files") {
		t.Errorf("status = %q, want it to say the work survived", m.statusMsg)
	}
}

// "Nothing was checked" and "nothing was lost" are different answers, and
// saying the second when the first is true claims knowledge there is none of.
func TestLostWorkFilterSaysWhenThereWasNothingToCheck(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "scratch.md")
	sess := &session.Session{
		Info: session.SessionInfo{ID: "s", Title: "t", CWD: t.TempDir()},
		Events: []session.Event{
			{Type: session.EventUserPrompt, UserText: "scratch something"},
			lostEdit(outside, "+a note that is not in the project"),
		},
	}
	m := Model{width: 120, height: 40, version: "0.0.0"}
	m.startReplay(sess)

	m, _ = press(t, m, "u")

	if m.replayLostOnly {
		t.Error("the filter opened on a session with nothing to check")
	}
	if !strings.Contains(m.statusMsg, "Nothing to check") {
		t.Errorf("status = %q, want it to say nothing could be checked", m.statusMsg)
	}
}

// The filter narrows; the line is what says WHY this step is in the list.
func TestLostWorkLineNamesTheFileOnScreen(t *testing.T) {
	m, _, lost := lostFixture(t)
	m, _ = press(t, m, "u")

	out := stripAnsi(renderReplay(m.replayViewState(m.width, m.height)))
	if !strings.Contains(out, filepath.Base(lost)) {
		t.Errorf("the frame does not name the file whose work is gone:\n%s", out)
	}
	if !strings.Contains(out, lostMarker) {
		t.Errorf("the frame has no %s marker on the line:\n%s", lostMarker, out)
	}
	if !strings.Contains(out, "lost work") {
		t.Errorf("the header does not say the filter is open:\n%s", out)
	}
}

// The line costs a row, and replayBodyRows has to have counted it. When it
// does not, the frame is one row taller than the terminal — which wraps, and
// strands rows on screen.
func TestLostWorkLineGivesTheBodyBackARow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "long.go")
	if err := os.WriteFile(path, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A body far longer than any pane, so the frame is full either way and the
	// row the line takes has to come out of the body.
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("+\tstep%d()", i))
	}
	sess := &session.Session{
		Info:   session.SessionInfo{ID: "s", Title: "t", CWD: root},
		Events: []session.Event{lostEdit(path, lines...)},
	}

	m := Model{width: 100, height: 30, version: "0.0.0"}
	m.startReplay(sess)
	v := m.replayViewState(m.width, m.height)

	plain := visibleLines(renderReplay(v))
	v.lost = "Not in long.go any more — something replaced it after this session"
	v.lostOnly = true
	withLine := visibleLines(renderReplay(v))

	if len(plain) != len(withLine) {
		t.Errorf("frame is %d rows with the line and %d without: the body did not give a row back",
			len(withLine), len(plain))
	}
	if len(withLine) > m.height-1 {
		t.Errorf("%d rows at height %d", len(withLine), m.height)
	}
}

// An empty list in the filter has to explain itself, the same way the code-only
// filter does. A rebuild can empty it after the key has been pressed.
func TestLostWorkEmptyStateExplainsItself(t *testing.T) {
	out := stripAnsi(renderReplay(replayView{
		sess: &session.Session{}, lostOnly: true, width: 100, height: 30,
	}))
	if !strings.Contains(out, "still in the files") {
		t.Errorf("the empty state does not say why it is empty:\n%s", out)
	}
	if !strings.Contains(out, "u") {
		t.Errorf("the empty state does not say how to get back:\n%s", out)
	}
}
