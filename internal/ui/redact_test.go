package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

// redactHome is an isolated HOME holding one transcript, so a test of the
// footer cannot reach the reader's own sessions.
func redactHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows resolves home from %USERPROFILE%, and os.UserHomeDir is what
	// every production path uses.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	path := filepath.Join(home, ".claude", "projects", "-repo", "aaa.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","timestamp":"2026-01-02T10:00:00.000Z",` +
		`"cwd":"/repo","message":{"role":"user","content":"ship it"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// redactModel is a sessions list over that home, with redaction on or off.
func redactModel(t *testing.T, on bool) Model {
	t.Helper()
	redactHome(t)
	store, err := session.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.SetRedaction(on)
	if err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	m := Model{store: store, width: 160, height: 40, version: "0.0.0", mode: viewSessions}
	m.refreshSessions()
	return m
}

// Believing redaction is on when it is not is the whole failure mode, so the
// footer has to say so — and has to go on saying so when the terminal is too
// narrow for everything else.
func TestTheFooterSaysWhenSecretsAreMasked(t *testing.T) {
	m := redactModel(t, true)

	foot := stripAnsi(lastRow(m.View()))
	if !strings.Contains(foot, "redacted") {
		t.Errorf("footer = %q, want it to say the view is redacted", foot)
	}

	// It must outlive the clock and the version, which are conveniences.
	narrow := m
	narrow.width = 70
	foot = stripAnsi(lastRow(narrow.View()))
	if !strings.Contains(foot, "redacted") {
		t.Errorf("narrow footer = %q, dropped the one part that matters", foot)
	}
}

func TestTheFooterIsSilentWhenNothingIsMasked(t *testing.T) {
	m := redactModel(t, false)
	if foot := stripAnsi(lastRow(m.View())); strings.Contains(foot, "redacted") {
		t.Errorf("footer = %q, claims redaction that is not happening", foot)
	}
}

// The indicator adds columns to a row that already fills the terminal, and a
// footer one column too wide wraps, costs the frame a row and strands output
// on screen. Every width, with it on.
func TestTheRedactedFooterStillFitsTheTerminal(t *testing.T) {
	m := redactModel(t, true)

	for _, width := range []int{40, 60, 70, 80, 100, 120, 160, 200} {
		m.width = width
		frame := m.View()
		for i, row := range visibleLines(frame) {
			if got := terminalColumns(row); got > usableWidth(width) {
				t.Errorf("width %d: row %d is %d columns, over the %d allowed: %q",
					width, i, got, usableWidth(width), stripAnsi(row))
			}
		}
		if foot := stripAnsi(lastRow(frame)); width >= 60 && !strings.Contains(foot, "redacted") {
			t.Errorf("width %d: footer lost the indicator: %q", width, foot)
		}
	}
}

// The indicator takes its columns out of the keybindings' budget rather than
// overflowing into the clamp. Without that the row is built too wide and cut
// at the end — and the end is where `q quit` lives, the one binding the
// footer is never allowed to drop.
func TestTheRedactedFooterStillKeepsQuit(t *testing.T) {
	m := redactModel(t, true)
	for _, width := range []int{60, 70, 80, 100} {
		m.width = width
		foot := stripAnsi(lastRow(m.View()))
		if !strings.Contains(foot, "q quit") {
			t.Errorf("width %d: footer = %q, want q quit on it", width, foot)
		}
	}
}
