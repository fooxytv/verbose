package ui

import (
	"os"
	"path/filepath"
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
		{"cp-91fff27a", "copilot", []string{"--resume", "91fff27a"}},
		{"pi-f029accf", "pi", []string{"--session", "f029accf"}},
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
		// --interactive runs the reply and stays; --prompt would run it and
		// exit, which is the opposite of continuing.
		{"cp-abc", "carry on", "copilot", []string{"--resume", "abc", "--interactive", "carry on"}},
		{"cp-abc", "", "copilot", []string{"--resume", "abc"}},
		// pi names the session with a flag and takes messages as bare args.
		{"pi-abc", "carry on", "pi", []string{"--session", "abc", "carry on"}},
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
func TestSourceMarkersAreNotAmbiguousWidth(t *testing.T) {
	for source, want := range map[string]string{"codex": "✦", "copilot": "▪", "pi": "▴"} {
		row := formatSessionLine(session.SessionInfo{Source: source}, 80, "")
		if !strings.Contains(row, want) {
			t.Errorf("a %s session is not marked with %s:\n%s", source, want, row)
		}
	}
	for _, r := range []rune("✦▪▴") {
		if w := runewidth.RuneWidth(r); w != 1 {
			t.Errorf("U+%04X is %d columns wide, want 1", r, w)
		}
		if runewidth.IsAmbiguousWidth(r) {
			t.Errorf("U+%04X is ambiguous-width and must not be used", r)
		}
	}
}

// The project header has to say which tools worked in the directory, because
// one directory here holds both OpenCode and Copilot sessions. The glyphs must
// be the same ones the session list uses, or a marker means two things.
func TestProjectViewShowsItsSources(t *testing.T) {
	proj := &session.ProjectInfo{
		ProjectName: "codex", ProjectDir: "/repo", CWD: "/repo",
		TotalSessions: 3,
		Sources:       []string{"copilot", "opencode"},
		SourceSessions: map[string]int{
			"copilot": 2, "opencode": 1,
		},
		TotalTokensReported: 4321,
	}

	out := stripAnsi(renderProjectView(proj, 0, 0, 120, 40))

	// The header row specifically. Looking anywhere in the view passes on the
	// Sources line alone, which is how the first version of this test missed
	// the header markers being dropped entirely.
	header := strings.SplitN(out, "\n", 2)[0]
	for _, want := range []string{"▪", "◈"} {
		if !strings.Contains(header, want) {
			t.Errorf("the project header does not show %s: %q", want, header)
		}
	}
	if !strings.Contains(out, "copilot 2") || !strings.Contains(out, "opencode 1") {
		t.Error("the Sources line does not count the sessions per tool")
	}
	// The glyphs come from the same place the list row uses.
	if sourceMarker("copilot") != "▪" || sourceMarker("opencode") != "◈" {
		t.Error("the project view and the session list disagree about a marker")
	}

	// A project whose only source reports no breakdown still shows a total,
	// and must not show four rows of zero or an empty bar.
	if !strings.Contains(out, "4,321") {
		t.Error("the reported token total did not reach the project view")
	}
	if strings.Contains(out, "Cache Write") {
		t.Error("a project with no token breakdown showed the breakdown rows")
	}
}

// The tree belongs to the session being replayed. Opening a different session
// while the sidebar is open left it marking the new session's files with the
// old one's activity: the split toggle only loads a tree when there is none,
// and startReplay did not touch it at all.
func TestOpeningAnotherSessionReloadsTheTree(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, second := write("first.go"), write("second.go")

	edit := func(path string) session.Event {
		return session.Event{Type: session.EventToolUse, ToolName: "Edit",
			ToolInput: map[string]interface{}{"file_path": path},
			Result: &session.ToolResult{FilePath: path,
				StructuredPatch: []session.PatchHunk{{Lines: []string{"+x"}}}}}
	}
	sessionWith := func(id, path string) *session.Session {
		return &session.Session{
			Info:   session.SessionInfo{ID: id, CWD: root},
			Events: []session.Event{{Type: session.EventUserPrompt, UserText: "go"}, edit(path)},
		}
	}

	m := Model{width: 160, height: 40, version: "0.0.0"}
	m.startReplay(sessionWith("a", first))
	m.openTree(m.selectedSession)
	if m.treeActivity[first] == nil {
		t.Fatal("the first session's file is not marked")
	}

	// Now open a different session, with the tree still on screen.
	m.treeSplit = true
	m.startReplay(sessionWith("b", second))

	if m.treeActivity[second] == nil {
		t.Error("the tree did not pick up the new session's file")
	}
	if m.treeActivity[first] != nil {
		t.Error("the tree is still marking the previous session's file")
	}
}

// Nothing on screen says what a marker means, so a list with more than one
// agent in it needs the key — and a machine running one agent does not, since
// the legend would only repeat what every row already says.
func TestSourceLegendOnlyAppearsWhenItSaysSomething(t *testing.T) {
	claudeOnly := []session.SessionInfo{{ID: "a"}, {ID: "b", Source: "claude"}}
	if got := sourceLegend(claudeOnly); got != "" {
		t.Errorf("one source produced a legend: %q", got)
	}

	mixed := []session.SessionInfo{
		{ID: "a"}, {ID: "b", Source: "claude"},
		{ID: "c", Source: "codex"},
		{ID: "d", Source: "copilot"}, {ID: "e", Source: "copilot"},
	}
	legend := stripAnsi(sourceLegend(mixed))
	for _, want := range []string{"● claude 2", "✦ codex 1", "▪ copilot 2"} {
		if !strings.Contains(legend, want) {
			t.Errorf("legend %q is missing %q", legend, want)
		}
	}
	if strings.Contains(legend, "opencode") {
		t.Errorf("legend names a source that is not in the list: %q", legend)
	}
	// A session with no Source is Claude Code, which is what the first one is.
	if strings.Contains(legend, "claude 1") {
		t.Errorf("an empty source was not counted as claude: %q", legend)
	}
}

// The summary view had no source field at all, so a Codex thread and a Claude
// session were told apart only by the model line — and Copilot runs Claude
// models, so even that did not separate them.
func TestSummaryNamesTheAgent(t *testing.T) {
	for source, want := range map[string]string{
		"":        "● Claude",
		"codex":   "✦ Codex",
		"copilot": "▪ Copilot",
	} {
		sess := &session.Session{Info: session.SessionInfo{
			ID: "s", Source: source, ProjectName: "p", Model: "claude-haiku-4.5"}}
		out := stripAnsi(renderSessionOverview(sess, nil, false, 0, 120, 40))
		if !strings.Contains(out, want) {
			t.Errorf("source %q: the summary does not say %q", source, want)
		}
	}
}
