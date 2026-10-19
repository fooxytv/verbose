package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/fooxytv/verbose/pkg/session"
)

// Each CLI resumes differently and verbose cannot verify the spelling by
// launching one, so the argument lists are asserted directly: claude takes
// --resume, opencode --session, and codex a resume subcommand with the thread
// id as a bare argument.
func TestResumeArgsPerSource(t *testing.T) {
	cases := []struct {
		id   string
		cli  string
		args []string
	}{
		{"b23ceacd-ed93", "claude", []string{"--resume", "b23ceacd-ed93"}},
		{"oc-ses_abc", "opencode", []string{"--session", "ses_abc"}},
		{"cx-01a0397e", "codex", []string{"resume", "01a0397e"}},
	}
	for _, c := range cases {
		cli, args := resumeArgs(c.id)
		if cli != c.cli || !slices.Equal(args, c.args) {
			t.Errorf("resumeArgs(%q) = %q %v, want %q %v", c.id, cli, args, c.cli, c.args)
		}
	}
}

// The continue panel adds the reply to the same command line. Codex takes it
// as a second bare argument; a flag there would be read as a prompt.
func TestContinueArgsPerSource(t *testing.T) {
	cases := []struct {
		id, prompt string
		cli        string
		args       []string
	}{
		{"abc", "carry on", "claude", []string{"--resume", "abc", "carry on"}},
		{"oc-abc", "carry on", "opencode", []string{"--session", "abc", "--prompt", "carry on"}},
		{"cx-abc", "carry on", "codex", []string{"resume", "abc", "carry on"}},
		{"cx-abc", "", "codex", []string{"resume", "abc"}},
	}
	for _, c := range cases {
		cli, args := continueArgs(c.id, c.prompt)
		if cli != c.cli || !slices.Equal(args, c.args) {
			t.Errorf("continueArgs(%q, %q) = %q %v, want %q %v",
				c.id, c.prompt, cli, args, c.cli, c.args)
		}
	}
}

// The marker beside a Codex session must be width 1 and must not be
// ambiguous-width: visibleLen counts runes, not columns, so an ambiguous glyph
// is a layout bug waiting for the wrong terminal. ● and ◈ are ambiguous and
// were already on screen before the rule existed; nothing new may be.
func TestCodexMarkerIsNotAmbiguousWidth(t *testing.T) {
	marker := formatSessionLine(session.SessionInfo{Source: "codex"}, 80, "")
	if !strings.Contains(marker, "✦") {
		t.Fatalf("a codex session is not marked with ✦:\n%s", marker)
	}
	for _, r := range []rune("✦") {
		if w := runewidth.RuneWidth(r); w != 1 {
			t.Errorf("U+%04X is %d columns wide, want 1", r, w)
		}
		if runewidth.IsAmbiguousWidth(r) {
			t.Errorf("U+%04X is ambiguous-width and must not be used", r)
		}
	}
}
