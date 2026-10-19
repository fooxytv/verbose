package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// launchInTerminal opens a CLI command in the best available terminal.
// Detection priority: tmux (split pane) > iTerm2 (new tab) > Terminal.app > in-place.
func launchInTerminal(cli string, args []string, cwd string) tea.Cmd {
	if os.Getenv("TMUX") != "" {
		return launchInTmux(cli, args, cwd)
	}
	if os.Getenv("ITERM_SESSION_ID") != "" {
		return launchInITermTab(cli, args, cwd)
	}
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return launchInTerminalApp(cli, args, cwd)
	}
	return launchInPlace(cli, args, cwd)
}

// resumeSessionCmd returns a tea.Cmd that resumes a session. Every CLI spells
// it differently: claude takes --resume, opencode takes --session, and codex
// takes a resume subcommand with the id as a bare argument.
func resumeSessionCmd(sessionID, cwd string) tea.Cmd {
	cli, args := resumeArgs(sessionID)
	return launchInTerminal(cli, args, cwd)
}

// resumeArgs is kept apart from launching so that what gets run can be
// asserted rather than taken on trust.
func resumeArgs(sessionID string) (cli string, args []string) {
	if id, ok := strings.CutPrefix(sessionID, "oc-"); ok {
		return "opencode", []string{"--session", id}
	}
	if id, ok := strings.CutPrefix(sessionID, "cx-"); ok {
		return "codex", []string{"resume", id}
	}
	if id, ok := strings.CutPrefix(sessionID, "cp-"); ok {
		return "copilot", []string{"--resume", id}
	}
	if id, ok := strings.CutPrefix(sessionID, "pi-"); ok {
		return "pi", []string{"--session", id}
	}
	return "claude", []string{"--resume", sessionID}
}

// newSessionCmd starts a fresh CLI session (no --resume) in the same CWD.
func newSessionCmd(source, cwd string) tea.Cmd {
	cli := "claude"
	switch source {
	case "opencode":
		cli = "opencode"
	case "codex":
		cli = "codex"
	case "copilot":
		cli = "copilot"
	case "pi":
		cli = "pi"
	}
	return launchInTerminal(cli, nil, cwd)
}

// forkSessionCmd starts a new CLI session with the last prompt as the first message.
func forkSessionCmd(source, cwd, lastPrompt string) tea.Cmd {
	prompt := "Continue from previous session:\n\n" + lastPrompt
	switch source {
	case "opencode":
		return launchInTerminal("opencode", []string{"--prompt", prompt}, cwd)
	case "codex":
		return launchInTerminal("codex", []string{prompt}, cwd)
	case "copilot":
		// --interactive runs the prompt and STAYS; --prompt would run it and
		// exit, which is not what forking a session into a new one means.
		return launchInTerminal("copilot", []string{"--interactive", prompt}, cwd)
	case "pi":
		return launchInTerminal("pi", []string{prompt}, cwd)
	}
	return launchInTerminal("claude", []string{prompt}, cwd)
}

// yankPromptCmd copies text to clipboard using pbcopy.
func yankPromptCmd(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		err := cmd.Run()
		return yankDoneMsg{err: err}
	}
}

// yankDoneMsg is sent when clipboard copy completes.
type yankDoneMsg struct {
	err error
}

// launchInTmux splits the current tmux window horizontally and runs the command.
func launchInTmux(cli string, args []string, cwd string) tea.Cmd {
	return func() tea.Msg {
		splitArgs := []string{"split-window", "-h"}
		if cwd != "" {
			splitArgs = append(splitArgs, "-c", cwd)
		}
		cmd := exec.Command("tmux", splitArgs...)
		if err := cmd.Run(); err != nil {
			return resumeStartedMsg{}
		}

		sendCmd := cli
		if len(args) > 0 {
			sendCmd = fmt.Sprintf("%s %s", cli, shellJoinArgs(args))
		}
		cmd = exec.Command("tmux", "send-keys", sendCmd, "Enter")
		cmd.Run()

		return resumeStartedMsg{newTab: true}
	}
}

// launchInITermTab opens a new iTerm2 tab and runs the command.
func launchInITermTab(cli string, args []string, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := cli
		if len(args) > 0 {
			shellCmd = fmt.Sprintf("%s %s", cli, shellJoinArgs(args))
		}
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(cwd), shellCmd)
		}

		script := fmt.Sprintf(`
tell application "iTerm2"
	tell current window
		create tab with default profile
		tell the current session
			write text %s
		end tell
	end tell
end tell
`, appleScriptString(shellCmd))

		cmd := exec.Command("osascript", "-e", script)
		cmd.Run()
		return resumeStartedMsg{newTab: true}
	}
}

// launchInTerminalApp opens a new Terminal.app window and runs the command.
func launchInTerminalApp(cli string, args []string, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := cli
		if len(args) > 0 {
			shellCmd = fmt.Sprintf("%s %s", cli, shellJoinArgs(args))
		}
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(cwd), shellCmd)
		}

		script := fmt.Sprintf(`
tell application "Terminal"
	activate
	do script %s
end tell
`, appleScriptString(shellCmd))

		cmd := exec.Command("osascript", "-e", script)
		cmd.Run()
		return resumeStartedMsg{newTab: true}
	}
}

// launchInPlace suspends the TUI and runs the command in the current terminal.
func launchInPlace(cli string, args []string, cwd string) tea.Cmd {
	c := exec.Command(cli, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return resumeDoneMsg{err: err}
	})
}

// missingCWD explains why a session cannot be launched in cwd, or returns empty
// when it can. A folder renamed or moved since the session ran leaves a cwd
// that no longer exists, and starting a process there fails before the CLI
// runs at all — on Windows that failure used to be the only sign, and it was
// dropped. An empty cwd is not missing: it launches where verbose was started.
func missingCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if info, err := os.Stat(cwd); err == nil && info.IsDir() {
		return ""
	}
	return "The folder this session ran in no longer exists. If it was renamed " +
		"or moved, run `verbose relink` in its new location. Was: " + cwd
}

// resumeStartedMsg is sent when a session was launched in a new tab/pane.
type resumeStartedMsg struct {
	newTab bool
}

// resumeDoneMsg is sent when an in-place launch finishes and the TUI should restore.
type resumeDoneMsg struct {
	err error
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// shellJoinArgs joins args, quoting each one for safe shell usage.
func shellJoinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// appleScriptString returns a properly quoted AppleScript string literal.
func appleScriptString(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}
