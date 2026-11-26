package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// Starting a new agent from verbose.
//
// "n" used to open a bare CLI in the selected session's folder. The panel keeps
// that as its default (enter with nothing typed does exactly that) and adds
// the three choices worth making before an agent starts: where, which agent,
// and whether it runs as an orchestrator. Like the continue panel, the agent
// runs beside verbose rather than inside it.

// launchAgents is every CLI verbose can start, in the order the panel offers them.
var launchAgents = []string{"claude", "codex", "opencode", "copilot", "pi"}

// launchModes: an agent does the work itself; an orchestrator splits it up and
// hands it to subagents. Only Claude Code can be told to be an orchestrator.
var launchModes = []string{"agent", "orchestrator"}

// The panel's fields, in the order tab moves through them. Under and Name are
// only there while the folder is a new one (see fields).
const (
	launchFieldFolder = iota
	launchFieldUnder
	launchFieldName
	launchFieldAgent
	launchFieldMode
	launchFieldPrompt
	launchFieldCount
)

// orchestratorBrief is appended to Claude Code's system prompt for an
// orchestrator run. It describes a way of working, not a task: the task is the
// prompt typed into the panel.
const orchestratorBrief = `You are running as an orchestrator. Your job is to get the task done through subagents, not to do the work yourself.

- Start by reading enough of the repository to plan. Split the task into pieces that can run independently, and say what the pieces are before starting them.
- Hand each piece to a subagent with a self-contained brief: the goal, the files that matter, what is already known, and what done looks like. Run independent pieces in parallel.
- Keep your own context for planning, checking and joining results. Read what each subagent reports and verify the important claims (run the tests, read the diff) rather than trusting them.
- When the pieces are in, have a separate subagent review the combined change with fresh eyes, then fix what it finds.
- Stop and ask when a decision is genuinely the user's: scope changes, anything destructive, anything that leaves this machine. Otherwise keep going.
- Finish with a short report: what changed, how it was verified, and anything left open.`

// launchForm is the panel's state.
type launchForm struct {
	folders []string // most recently used first
	folder  int      // len(folders) is "+ new folder"
	parents []string // where a new folder can go, for the Under field
	parent  int
	name    string // the new folder's name
	agent   int
	mode    int
	field   int
	draft   string
}

func (f launchForm) agentName() string { return launchAgents[f.agent] }
func (f launchForm) modeName() string  { return launchModes[f.mode] }

// newFolder is true when the panel is creating a folder rather than picking one.
func (f launchForm) newFolder() bool { return f.folder >= len(f.folders) }

// fields lists the fields tab moves through, in order.
func (f launchForm) fields() []int {
	if f.newFolder() {
		return []int{launchFieldFolder, launchFieldUnder, launchFieldName, launchFieldAgent, launchFieldMode, launchFieldPrompt}
	}
	return []int{launchFieldFolder, launchFieldAgent, launchFieldMode, launchFieldPrompt}
}

func (f launchForm) parentPath() string {
	if f.parent < len(f.parents) {
		return f.parents[f.parent]
	}
	home, _ := os.UserHomeDir()
	return home
}

// folderPath is where the agent will run: the chosen folder, or the new one.
func (f launchForm) folderPath() (string, error) {
	if !f.newFolder() {
		return f.folders[f.folder], nil
	}
	return newFolderPath(f.parentPath(), f.name)
}

// newFolderPath resolves a typed name: under parent, or as typed when it is
// absolute or starts with ~. ".." is refused so a name cannot climb out of
// the folder shown beside it.
func newFolderPath(parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("Type a name for the new folder.")
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("A folder name cannot contain \"..\".")
		}
	}
	switch {
	case name == "~" || strings.HasPrefix(name, "~/") || strings.HasPrefix(name, `~\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, name[1:]), nil
	case filepath.IsAbs(name):
		return filepath.Clean(name), nil
	}
	return filepath.Join(parent, name), nil
}

// launchParents offers the folders the projects live in, most recent first,
// then the home folder. The home folder's own parent and the root are left out:
// nobody keeps a project in /home or C:\Users.
func launchParents(folders []string) []string {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] || (home != "" && p == filepath.Dir(home)) || filepath.Dir(p) == p {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, d := range folders {
		if d != home {
			add(filepath.Dir(d))
		}
	}
	add(home)
	return out
}

// launchFolders lists the folders sessions have run in, most recent first,
// keeping only the ones still on disk: a folder that was renamed or deleted
// cannot be started in.
func launchFolders(sessions []session.SessionInfo) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sessions {
		if s.IsAgent || s.CWD == "" || seen[s.CWD] {
			continue
		}
		seen[s.CWD] = true
		if info, err := os.Stat(s.CWD); err == nil && info.IsDir() {
			out = append(out, s.CWD)
		}
	}
	return out
}

// launchArgs builds the command line for a new session, kept apart from
// launching it so that what gets run can be asserted. Each CLI takes an
// opening prompt differently, the same way forkSessionCmd spells them.
//
// The orchestrator brief goes in by file (briefPath), not as an argument: it is
// several lines long, and a newline survives neither tmux send-keys (it is a
// keypress) nor wt's re-quoting of its command line.
func launchArgs(agent, mode, prompt, briefPath string) (cli string, args []string) {
	prompt = strings.TrimSpace(prompt)
	if len([]rune(prompt)) > continueMaxReply {
		prompt = string([]rune(prompt)[:continueMaxReply])
	}
	switch agent {
	case "opencode":
		if prompt != "" {
			args = []string{"--prompt", prompt}
		}
		return "opencode", args
	case "copilot":
		// --interactive runs the prompt and stays; --prompt would exit.
		if prompt != "" {
			args = []string{"--interactive", prompt}
		}
		return "copilot", args
	case "codex", "pi":
		if prompt != "" {
			args = []string{prompt}
		}
		return agent, args
	}
	if mode == "orchestrator" {
		args = append(args, "--append-system-prompt-file", briefPath)
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return "claude", args
}

// openLaunch opens the panel, starting on the folder of the session under the
// cursor (if any) and the agent that ran it. A session in the home folder is
// almost never where new work should start, so that, or having no folders at
// all, opens on a new folder with the cursor on its name.
func (m *Model) openLaunch(current *session.SessionInfo) {
	folders := launchFolders(m.store.GetSessions())
	f := launchForm{folders: folders, parents: launchParents(folders), field: launchFieldPrompt}
	home, _ := os.UserHomeDir()
	if current != nil {
		for i, d := range f.folders {
			if d == current.CWD {
				f.folder = i
			}
		}
		for i, a := range launchAgents {
			if a == current.Source {
				f.agent = i
			}
		}
	}
	if len(f.folders) == 0 || f.folders[f.folder] == home {
		f.folder = len(f.folders)
		f.field = launchFieldName
	}
	m.launch = f
	m.launchOpen = true
}

func (m *Model) closeLaunch() {
	m.launchOpen = false
	m.launch = launchForm{}
}

// launchUnsupported explains why the form cannot launch, or returns empty.
func launchUnsupported(f launchForm) string {
	if _, err := f.folderPath(); err != nil {
		return err.Error()
	}
	if f.modeName() == "orchestrator" && f.agentName() != "claude" {
		return "Only Claude Code can run as an orchestrator. Choose claude, or the agent mode."
	}
	return ""
}

// handleLaunchKey consumes keys while the panel is open.
func (m Model) handleLaunchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	f := &m.launch
	switch key {
	case "esc", "ctrl+c":
		m.closeLaunch()
		return m, nil
	case "tab", "down":
		f.field = f.step(1)
		return m, nil
	case "shift+tab", "up":
		f.field = f.step(-1)
		return m, nil
	case "enter":
		if why := launchUnsupported(*f); why != "" {
			m.warn(why)
			return m, clearStatusAfter()
		}
		cwd, _ := f.folderPath()
		created := false
		if f.newFolder() {
			if info, err := os.Stat(cwd); err == nil && !info.IsDir() {
				m.fail(cwd + " is a file, not a folder.")
				return m, clearStatusAfter()
			} else if err != nil {
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					m.fail("Could not create the folder: " + err.Error())
					return m, clearStatusAfter()
				}
				created = true
			}
		}
		brief := ""
		label := f.agentName()
		if f.modeName() == "orchestrator" {
			label = "orchestrator"
			var err error
			if brief, err = writeOrchestratorBrief(); err != nil {
				m.fail("Could not write the orchestrator brief: " + err.Error())
				return m, clearStatusAfter()
			}
		}
		cli, args := launchArgs(f.agentName(), f.modeName(), f.draft, brief)
		m.closeLaunch()
		m.statusMsg = "Starting " + label + " in " + filepath.Base(cwd) + "…"
		if created {
			m.statusMsg = "Created " + cwd + " · starting " + label + "…"
		}
		return m, tea.Batch(launchInTerminal(cli, args, cwd), clearStatusAfter())
	}

	if f.field == launchFieldName {
		f.name = editText(f.name, msg, key, 255)
		return m, nil
	}
	if f.field != launchFieldPrompt {
		step := 0
		switch key {
		case "right", "l", " ":
			step = 1
		case "left", "h":
			step = -1
		}
		if isSpace(msg) {
			step = 1
		}
		if step != 0 {
			switch f.field {
			case launchFieldFolder:
				f.folder = cycle(f.folder, step, len(f.folders)+1)
			case launchFieldUnder:
				f.parent = cycle(f.parent, step, len(f.parents))
			case launchFieldAgent:
				f.agent = cycle(f.agent, step, len(launchAgents))
			case launchFieldMode:
				f.mode = cycle(f.mode, step, len(launchModes))
			}
		}
		return m, nil
	}

	f.draft = editText(f.draft, msg, key, continueMaxReply)
	return m, nil
}

// step moves to the next or previous field that is showing, wrapping.
func (f launchForm) step(dir int) int {
	fields := f.fields()
	at := 0
	for i, fl := range fields {
		if fl == f.field {
			at = i
		}
	}
	return fields[cycle(at, dir, len(fields))]
}

// editText applies one key to a text field of at most limit characters.
func editText(s string, msg tea.KeyMsg, key string, limit int) string {
	switch key {
	case "backspace":
		if r := []rune(s); len(r) > 0 {
			return string(r[:len(r)-1])
		}
		return s
	case "ctrl+u":
		return ""
	}
	if isSpace(msg) {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	}
	if msg.Type == tea.KeyRunes && len([]rune(s))+len(msg.Runes) <= limit {
		return s + string(msg.Runes)
	}
	return s
}

// writeOrchestratorBrief writes the brief where Claude Code can read it and
// returns the path. It is rewritten on every launch, so a newer verbose's brief
// replaces an older one.
func writeOrchestratorBrief() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "verbose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "orchestrator.md")
	return path, os.WriteFile(path, []byte(orchestratorBrief+"\n"), 0o644)
}

func cycle(i, step, n int) int {
	if n == 0 {
		return 0
	}
	return ((i+step)%n + n) % n
}

// renderLaunch draws the panel.
func renderLaunch(f launchForm, width, height int) string {
	var b strings.Builder
	b.WriteString(clampWidth(titleStyle.Render("New agent"), width) + "\n")
	b.WriteString(clampWidth("  "+mutedStyle.Render("opens beside verbose · enter with no prompt starts it empty"), width) + "\n\n")

	choice := func(field int, label, value string, of, n int) {
		marker := "  "
		val := mutedStyle.Render(value)
		if f.field == field {
			marker = searchPromptStyle.Render("▐") + " "
			val = selectedStyle.Render(" " + value + " ")
		}
		pos := ""
		if n > 1 {
			pos = mutedStyle.Render(fmt.Sprintf("  ‹ %d/%d ›", of+1, n))
		}
		b.WriteString(clampWidth(marker+headerLabelStyle.Render(padRight(label, 8))+" "+val+pos, width) + "\n")
	}

	folder := "+ new folder"
	if !f.newFolder() {
		folder = shortenMiddle(f.folders[f.folder], max(10, width-30))
	}
	choice(launchFieldFolder, "Folder", folder, f.folder, len(f.folders)+1)
	if f.newFolder() {
		choice(launchFieldUnder, "Under", shortenMiddle(f.parentPath(), max(10, width-30)), f.parent, len(f.parents))
		marker := "  "
		name := mutedStyle.Render("type a name")
		if f.name != "" {
			name = textStyle.Render(f.name)
		}
		if f.field == launchFieldName {
			marker = searchPromptStyle.Render("▐") + " "
			name = selectedStyle.Render(" " + f.name + "▌ ")
		}
		b.WriteString(clampWidth(marker+headerLabelStyle.Render(padRight("Name", 8))+" "+name, width) + "\n")
		if path, err := f.folderPath(); err == nil {
			what := "creates "
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				what = "already exists, starts in "
			}
			b.WriteString(clampWidth("           "+mutedStyle.Render(what+shortenMiddle(path, max(10, width-40))), width) + "\n")
		}
	}
	choice(launchFieldAgent, "Agent", f.agentName(), f.agent, len(launchAgents))
	choice(launchFieldMode, "Mode", f.modeName(), f.mode, len(launchModes))
	b.WriteString("\n")

	marker := "  "
	if f.field == launchFieldPrompt {
		marker = searchPromptStyle.Render("▐") + " "
	}
	b.WriteString(clampWidth(marker+headerLabelStyle.Render("Prompt"), width) + "\n")
	if f.draft == "" {
		hint := "what should it do? (optional)"
		if f.field == launchFieldPrompt {
			hint = "▌ " + hint
		}
		b.WriteString(clampWidth("  "+mutedStyle.Render(hint), width) + "\n")
	} else {
		text := f.draft
		if f.field == launchFieldPrompt {
			text += "▌"
		}
		lines := wrapProse(text, max(10, width-6), "  ")
		visible := max(1, height-14)
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, l := range lines {
			b.WriteString(clampWidth(textStyle.Render(l), width) + "\n")
		}
	}

	b.WriteString("\n")
	if why := launchUnsupported(f); why != "" {
		b.WriteString(clampWidth("  "+toolErrorStyle.Render(why), width) + "\n")
	} else if f.modeName() == "orchestrator" {
		b.WriteString(clampWidth("  "+mutedStyle.Render("Claude plans the work, hands the pieces to subagents, checks"), width) + "\n")
		b.WriteString(clampWidth("  "+mutedStyle.Render("what they report and has the result reviewed before it finishes."), width) + "\n")
	}
	return b.String()
}

func padRight(s string, n int) string {
	if r := len([]rune(s)); r < n {
		return s + strings.Repeat(" ", n-r)
	}
	return s
}
