package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fooxytv/verbose/pkg/session"
)

// Each CLI takes an opening prompt differently, and verbose cannot verify the
// spelling by launching one, so the argument lists are asserted directly.
func TestLaunchArgsPerAgent(t *testing.T) {
	cases := []struct {
		agent, mode, prompt string
		cli                 string
		args                []string
	}{
		{"claude", "agent", "fix it", "claude", []string{"fix it"}},
		{"claude", "agent", "  ", "claude", nil},
		{"claude", "orchestrator", "fix it", "claude",
			[]string{"--append-system-prompt-file", "brief.md", "fix it"}},
		{"claude", "orchestrator", "", "claude",
			[]string{"--append-system-prompt-file", "brief.md"}},
		{"codex", "agent", "fix it", "codex", []string{"fix it"}},
		{"pi", "agent", "fix it", "pi", []string{"fix it"}},
		{"opencode", "agent", "fix it", "opencode", []string{"--prompt", "fix it"}},
		// --interactive, never --prompt: that would run the prompt and exit.
		{"copilot", "agent", "fix it", "copilot", []string{"--interactive", "fix it"}},
		{"copilot", "agent", "", "copilot", nil},
	}
	for _, c := range cases {
		cli, args := launchArgs(c.agent, c.mode, c.prompt, "brief.md")
		if cli != c.cli || !slices.Equal(args, c.args) {
			t.Errorf("launchArgs(%q, %q, %q) = %q %q, want %q %q",
				c.agent, c.mode, c.prompt, cli, args, c.cli, c.args)
		}
	}
}

func TestOrchestratorNeedsClaude(t *testing.T) {
	f := launchForm{folders: []string{"x"}, mode: 1}
	for i, a := range launchAgents {
		f.agent = i
		got := launchUnsupported(f)
		if (a == "claude") != (got == "") {
			t.Errorf("orchestrator with %s: unsupported = %q", a, got)
		}
	}
	if launchUnsupported(launchForm{}) == "" {
		t.Error("a form with no folder must not launch")
	}
}

// A bare ";" starts another wt command, so a prompt containing one would be
// cut in two and the rest run as a wt subcommand.
func TestWTArgsEscapeSemicolons(t *testing.T) {
	cwd := t.TempDir()
	got := wtArgs("no-such-cli-xyz", []string{"build it; then test"}, cwd)
	want := []string{"-w", "0", "new-tab", "--title", "no-such-cli-xyz", "-d", cwd,
		"no-such-cli-xyz", `build it\; then test`}
	if !slices.Equal(got, want) {
		t.Errorf("wtArgs = %q, want %q", got, want)
	}
}

// wt joins its command line back together without re-escaping, so quotes and
// the backslashes in front of them, and trailing backslashes on an argument it
// will quote, are escaped once more. Each expectation here was checked by
// launching a real tab and reading back the argv it received.
func TestEscapeQuotesForArgv(t *testing.T) {
	cases := map[string]string{
		`plain words`:              `plain words`,
		`fix the "login" bug`:      `fix the \"login\" bug`,
		`say \"hi\"`:               `say \\\"hi\\\"`,
		`C:\path with space\`:      `C:\path with space\\`,
		`nospace\`:                 `nospace\`,
		`a\\b c`:                   `a\\b c`,
		`"quoted" and trailing \\`: `\"quoted\" and trailing \\\\`,
	}
	for in, want := range cases {
		if got := escapeQuotesForArgv(in); got != want {
			t.Errorf("escapeQuotesForArgv(%q) = %q, want %q", in, got, want)
		}
	}
}

// Folders come most recent first, once each, without subagents or folders
// that are no longer on disk.
func TestLaunchFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	gone := filepath.Join(a, "deleted")
	sessions := []session.SessionInfo{
		{CWD: b},
		{CWD: gone},
		{CWD: a, IsAgent: true},
		{CWD: b},
		{CWD: ""},
		{CWD: a},
	}
	got := launchFolders(sessions)
	if !slices.Equal(got, []string{b, a}) {
		t.Errorf("launchFolders = %q, want %q", got, []string{b, a})
	}
}

func launchKey(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func pressLaunch(m Model, keys ...string) Model {
	for _, k := range keys {
		msg := launchKey(k)
		next, _ := m.handleLaunchKey(msg, msg.String())
		m = next.(Model)
	}
	return m
}

// The panel opens on the prompt, so typing straight away is the prompt; the
// choices above it change with the arrows and wrap at both ends.
func TestLaunchPanelKeys(t *testing.T) {
	m := Model{launchOpen: true, launch: launchForm{
		folders: []string{"a", "b", "c"}, field: launchFieldPrompt}}

	m = pressLaunch(m, "h", "i", "backspace", "!")
	if m.launch.draft != "h!" {
		t.Fatalf("draft = %q, want typing to reach the prompt", m.launch.draft)
	}
	// Letters that move choices elsewhere are text in the prompt.
	m = pressLaunch(m, "l")
	if m.launch.draft != "h!l" {
		t.Fatalf("draft = %q", m.launch.draft)
	}

	m = pressLaunch(m, "tab") // wraps to Folder
	if m.launch.field != launchFieldFolder {
		t.Fatalf("field = %d, want folder", m.launch.field)
	}
	m = pressLaunch(m, "left")
	if !m.launch.newFolder() {
		t.Errorf("folder = %d, want left to wrap to the last entry, a new folder", m.launch.folder)
	}
	// A new folder brings in Under and Name, and the name takes typing.
	m = pressLaunch(m, "tab", "tab", "a", "p", "p", "l", "h")
	if m.launch.field != launchFieldName || m.launch.name != "applh" {
		t.Fatalf("field %d name %q, want typing to reach the name", m.launch.field, m.launch.name)
	}
	m = pressLaunch(m, "backspace", "backspace", "shift+tab", "shift+tab", "right")
	if m.launch.newFolder() || m.launch.folder != 0 {
		t.Fatalf("folder = %d, want right to wrap back to the first", m.launch.folder)
	}
	// With an existing folder chosen, tab skips the hidden Under and Name.
	m = pressLaunch(m, "tab", "right", "right")
	if m.launch.agentName() != launchAgents[2] {
		t.Errorf("agent = %s", m.launch.agentName())
	}
	m = pressLaunch(m, "tab", "right")
	if m.launch.modeName() != "orchestrator" {
		t.Errorf("mode = %s", m.launch.modeName())
	}

	m = pressLaunch(m, "esc")
	if m.launchOpen || m.launch.draft != "" {
		t.Error("esc must close the panel and drop the draft")
	}
}

// Enter on a form that cannot launch keeps the panel open and says why.
func TestLaunchRefusesOrchestratorForOtherAgents(t *testing.T) {
	m := Model{launchOpen: true, launch: launchForm{
		folders: []string{"a"}, agent: 1, mode: 1, field: launchFieldPrompt}}
	next, cmd := m.handleLaunchKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	m = next.(Model)
	if !m.launchOpen {
		t.Error("the panel closed on a launch it refused")
	}
	if !strings.Contains(m.statusMsg, "orchestrator") {
		t.Errorf("status = %q, want the reason", m.statusMsg)
	}
	if cmd == nil {
		t.Error("want the status cleared later")
	}
}

// A typed name goes under the chosen parent; ~ and an absolute path are taken
// as typed; ".." cannot climb out.
func TestNewFolderPath(t *testing.T) {
	parent := t.TempDir()
	home, _ := os.UserHomeDir()
	abs := filepath.Join(t.TempDir(), "elsewhere")
	for name, want := range map[string]string{
		"app":            filepath.Join(parent, "app"),
		"  app  ":        filepath.Join(parent, "app"),
		"group/app":      filepath.Join(parent, "group", "app"),
		"~/code/app":     filepath.Join(home, "code", "app"),
		abs:              abs,
		"":               "",
		"../app":         "",
		"a/../../escape": "",
	} {
		got, err := newFolderPath(parent, name)
		if want == "" {
			if err == nil {
				t.Errorf("newFolderPath(%q) = %q, want refused", name, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("newFolderPath(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

// Parents come from where the projects are, most recent first, then home;
// never the folder home sits in.
func TestLaunchParents(t *testing.T) {
	home, _ := os.UserHomeDir()
	work := filepath.Join(home, "work")
	got := launchParents([]string{filepath.Join(work, "a"), home, filepath.Join(work, "b"), filepath.Join(home, "c")})
	if want := []string{work, home}; !slices.Equal(got, want) {
		t.Errorf("launchParents = %q, want %q", got, want)
	}
}

// A new folder whose name is still empty cannot launch, and says why.
func TestNewFolderNeedsAName(t *testing.T) {
	f := launchForm{folders: []string{"a"}, folder: 1, parents: []string{t.TempDir()}}
	if why := launchUnsupported(f); !strings.Contains(why, "name") {
		t.Errorf("unsupported = %q, want it to ask for a name", why)
	}
	f.name = "app"
	if why := launchUnsupported(f); why != "" {
		t.Errorf("unsupported = %q with a name", why)
	}
}

func TestRenderLaunchFitsItsWidth(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("deeply-nested-", 12), "project")
	for _, w := range []int{30, 60, 120} {
		for _, f := range []launchForm{
			{folders: []string{long}, field: launchFieldFolder, draft: strings.Repeat("word ", 80)},
			{folders: nil, mode: 1, agent: 1},
			{folders: []string{long}, folder: 1, parents: []string{long}, name: strings.Repeat("name-", 20), field: launchFieldName},
		} {
			out := renderLaunch(f, w, 20)
			for _, line := range strings.Split(out, "\n") {
				if n := terminalColumns(line); n > w {
					t.Errorf("width %d: line is %d columns: %q", w, n, line)
				}
			}
		}
	}
}
