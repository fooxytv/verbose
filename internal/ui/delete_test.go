package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// deleteFixture builds a model backed by a single real transcript under an
// isolated HOME, so nothing here can reach the user's own sessions.
func deleteFixture(t *testing.T) (Model, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	transcript := filepath.Join(home, ".claude", "projects", "-repo", "abc123.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z",` +
		`"cwd":"/repo","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := session.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Scan(); err != nil {
		t.Fatal(err)
	}

	m := NewModel(store, "", "test")
	m.width, m.height = 120, 40
	m.refreshSessions()
	if len(m.sessions) != 1 {
		t.Fatalf("fixture has %d sessions, want 1", len(m.sessions))
	}
	return m, transcript
}

func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model), cmd
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	m, transcript := deleteFixture(t)

	// "d" only arms the prompt — it must not touch the disk.
	m, cmd := press(t, m, "d")
	if m.confirmDelete == nil {
		t.Fatal("d did not arm the delete prompt")
	}
	if cmd != nil {
		t.Error("d returned a command; it must not act before confirmation")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("transcript disturbed by an unconfirmed delete: %v", err)
	}

	// The prompt takes over the help line and says what will happen.
	if view := m.View(); !strings.Contains(view, "delete") || !strings.Contains(view, "Trash") {
		t.Errorf("confirmation not shown in view:\n%s", view)
	}
}

func TestDeleteCancelled(t *testing.T) {
	m, transcript := deleteFixture(t)

	m, _ = press(t, m, "d")
	m, _ = press(t, m, "n")

	if m.confirmDelete != nil {
		t.Error("prompt still armed after cancelling")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Errorf("cancelled delete removed the transcript: %v", err)
	}
	if m.statusMsg == "" {
		t.Error("cancelling gave no feedback")
	}
}

// Any key that is not "y" cancels, so a stray keystroke can never delete.
func TestDeleteOnlyYConfirms(t *testing.T) {
	for _, key := range []string{"n", "j", "q", "esc", "enter", "D"} {
		m, transcript := deleteFixture(t)

		m, _ = press(t, m, "d")
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = next.(Model)

		if m.confirmDelete != nil {
			t.Errorf("%q left the prompt armed", key)
		}
		// The cancel branch is the only one that sets this message, so seeing it
		// proves no delete was dispatched.
		if m.statusMsg != "Delete cancelled" {
			t.Errorf("%q took the confirm path: status = %q", key, m.statusMsg)
		}
		if _, err := os.Stat(transcript); err != nil {
			t.Errorf("%q removed the transcript: %v", key, err)
		}
	}
}

func TestDeleteConfirmedTrashesTranscript(t *testing.T) {
	m, transcript := deleteFixture(t)

	m, _ = press(t, m, "d")
	m, cmd := press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirming produced no delete command")
	}

	msg, ok := cmd().(sessionDeletedMsg)
	if !ok {
		t.Fatalf("confirming produced %T, want sessionDeletedMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("delete failed: %v", msg.err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Error("transcript still on disk after a confirmed delete")
	}

	// Feeding the result back must drop the row and report the outcome.
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if len(m.sessions) != 0 {
		t.Errorf("session list still holds %d rows", len(m.sessions))
	}
	if !strings.Contains(m.statusMsg, "Trash") {
		t.Errorf("status = %q, want it to mention the Trash", m.statusMsg)
	}
}

// A delete that fails must be reported and must leave the list intact.
func TestDeleteFailureReported(t *testing.T) {
	m, transcript := deleteFixture(t)
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}

	m, _ = press(t, m, "d")
	m, cmd := press(t, m, "y")

	msg, ok := cmd().(sessionDeletedMsg)
	if !ok {
		t.Fatalf("got %T, want sessionDeletedMsg", cmd())
	}
	if msg.err == nil {
		t.Fatal("deleting a session with no files should fail")
	}

	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.statusMsg, "Delete failed") {
		t.Errorf("status = %q, want a failure message", m.statusMsg)
	}
	if len(m.sessions) != 1 {
		t.Errorf("failed delete dropped the row: %d sessions left", len(m.sessions))
	}
}

// While a delete is pending no other binding may fire — in particular the
// prompt must not be dismissed by a key that also does something else.
func TestDeletePromptSwallowsKeys(t *testing.T) {
	m, _ := deleteFixture(t)
	m.mode = viewSessions

	m, _ = press(t, m, "d")
	before := m.mode

	// "p" would normally open the project view.
	m, _ = press(t, m, "p")
	if m.mode != before {
		t.Errorf("mode changed to %v while a delete was pending", m.mode)
	}
}

// The footer shows one thing at a time. Appending the status after the help
// line pushed it past the right edge on any normal terminal, so a completed
// delete looked like it had done nothing.
func TestStatusMessageReplacesHelpLine(t *testing.T) {
	m, _ := deleteFixture(t)
	// Wide enough for the whole sessions help line, which is 129 columns. On a
	// narrower terminal the footer drops its last keys rather than wrapping, so
	// "d delete" would legitimately be absent.
	m.width = 160

	helpOnly := m.View()
	if !strings.Contains(helpOnly, "d delete") {
		t.Fatal("help line missing from the default footer")
	}

	m.statusMsg = "Moved to Trash: aaa11111-1111-1111-1111-111111111111.jsonl"
	view := m.View()

	if !strings.Contains(view, "Moved to Trash") {
		t.Error("status message absent from the view")
	}
	// With the help line gone, the message fits on screen.
	if strings.Contains(view, "d delete") {
		t.Error("help line still rendered alongside the status message")
	}

	// Every line must fit the terminal, or the message is cut off again.
	for _, line := range strings.Split(view, "\n") {
		if w := visibleLen(line); w > m.width {
			t.Errorf("line overflows %d columns (%d): %q", m.width, w, line)
		}
	}
}

// A pending delete outranks any status message left over from before.
func TestDeletePromptOutranksStatus(t *testing.T) {
	m, _ := deleteFixture(t)
	m.statusMsg = "Copied last prompt to clipboard"

	m, _ = press(t, m, "d")

	view := m.View()
	if strings.Contains(view, "Copied last prompt") {
		t.Error("stale status still shown under a delete confirmation")
	}
	if !strings.Contains(view, "confirm") {
		t.Error("confirmation prompt not shown")
	}
}

// Confirming reports progress immediately: the OpenCode CLI takes about a
// second, which otherwise reads as a freeze.
func TestConfirmShowsProgress(t *testing.T) {
	m, _ := deleteFixture(t)

	m, _ = press(t, m, "d")
	m, _ = press(t, m, "y")

	if !m.deleting {
		t.Error("in-flight delete not recorded")
	}
	if !strings.Contains(m.statusMsg, "Deleting") {
		t.Errorf("status = %q, want progress feedback", m.statusMsg)
	}
	if !strings.Contains(m.View(), "Deleting") {
		t.Error("progress not visible in the view")
	}
}

func TestPadToHeight(t *testing.T) {
	if got := padToHeight("a\nb", 5); got != "a\nb\n\n\n" {
		t.Errorf("short content = %q, want it padded to 5 lines", got)
	}
	// Trailing newlines must not count as extra lines.
	if got := padToHeight("a\n", 3); got != "a\n\n" {
		t.Errorf("trailing newline mishandled: %q", got)
	}
	// Content that would push the footer off screen is trimmed.
	if got := padToHeight("a\nb\nc\nd", 2); got != "a\nb" {
		t.Errorf("long content = %q, want it trimmed to 2 lines", got)
	}
	// A degenerate height leaves the content alone rather than erasing it.
	if got := padToHeight("a\nb", 0); got != "a\nb" {
		t.Errorf("zero height = %q, want the content untouched", got)
	}
}

// The footer belongs on the last row of the terminal in every view, whatever
// the content length — otherwise it floats mid-screen on a short list.
func TestFooterPinnedToBottom(t *testing.T) {
	m, _ := deleteFixture(t)
	m.width = 110

	views := map[string]viewMode{
		"sessions": viewSessions,
		"timeline": viewDetail,
		"overview": viewOverview,
	}
	sess := m.store.GetSession(m.sessions[0].ID)

	for name, mode := range views {
		for _, height := range []int{12, 20, 40} {
			mm := m
			mm.height = height
			mm.mode = mode
			if mode != viewSessions {
				mm.selectedSession = sess
			}

			lines := strings.Split(mm.View(), "\n")
			if len(lines) != height {
				t.Errorf("%s at height %d rendered %d lines", name, height, len(lines))
				continue
			}
			if strings.TrimSpace(lines[height-1]) == "" {
				t.Errorf("%s at height %d: last row is blank, footer floated", name, height)
			}
		}
	}
}

// The churn figures are coloured, so clipping a row by rune count ate them.
func TestTruncateVisibleKeepsStyledColumns(t *testing.T) {
	styled := "plain " + diffAddStyle.Render("+746") + " " + diffRemoveStyle.Render("-149")

	if got := truncateVisible(styled, 80); got != styled {
		t.Error("a row that fits must be returned untouched")
	}

	clipped := truncateVisible(styled, 6)
	if visibleLen(clipped) > 6 {
		t.Errorf("clipped to %d visible columns, want 6: %q", visibleLen(clipped), clipped)
	}
	if !strings.HasSuffix(clipped, "\x1b[0m") {
		t.Error("clipping must close the style it cut through")
	}
}

// Every row must fit the terminal at any width, label included.
func TestSessionRowsFitTerminalWidth(t *testing.T) {
	info := session.SessionInfo{
		ProjectName: "powerbi-fabric-workspace-lifecycle",
		ID:          "oc-ses_f8485cb45ffehIf3PUilR35b7x",
		Title:       "Review WorkspaceLifecycle.Tests email specificity across the whole solution",
		Source:      "opencode",
		LinesAdded:  3736, LinesRemoved: 1384,
	}

	for _, width := range []int{80, 100, 120, 155, 200} {
		row := formatSessionLine(info, width)
		if w := visibleLen(row); w > width-2 {
			t.Errorf("width %d: row is %d columns: %q", width, w, row)
		}
		// The label must still carry information at every size.
		if !strings.Contains(row, "Review") {
			t.Errorf("width %d: label missing from row: %q", width, row)
		}
	}
}

// With no title to show, the ID is better than an empty column.
func TestSessionRowFallsBackToID(t *testing.T) {
	row := formatSessionLine(session.SessionInfo{ID: "abc12345-6789", ProjectName: "repo"}, 120)
	if !strings.Contains(row, "abc12345") {
		t.Errorf("untitled session lost its identifier: %q", row)
	}
}
