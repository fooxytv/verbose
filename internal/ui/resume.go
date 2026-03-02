package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// resumeSessionCmd returns a tea.Cmd that resumes a session.
// Detection priority: tmux (split pane) > iTerm2 (new tab) > Terminal.app > in-place.
func resumeSessionCmd(sessionID, cwd string) tea.Cmd {
	// Determine CLI command and actual session ID
	cli := "claude"
	resumeID := sessionID
	resumeArgs := []string{"--resume", resumeID}

	if strings.HasPrefix(sessionID, "oc-") {
		cli = "opencode"
		resumeID = strings.TrimPrefix(sessionID, "oc-")
		resumeArgs = []string{"--resume", resumeID}
	}

	// If inside tmux, split the current window — stays in context.
	if os.Getenv("TMUX") != "" {
		return resumeInTmux(cli, resumeArgs, sessionID, cwd)
	}

	// iTerm2 without tmux — open a new tab.
	if os.Getenv("ITERM_SESSION_ID") != "" {
		return resumeInITermTab(cli, resumeArgs, sessionID, cwd)
	}

	terminal := os.Getenv("TERM_PROGRAM")
	if terminal == "Apple_Terminal" {
		return resumeInTerminalApp(cli, resumeArgs, sessionID, cwd)
	}

	return resumeInPlace(cli, resumeArgs, sessionID, cwd)
}

// resumeInITermTab opens a new iTerm2 tab and runs the resume command there.
// Uses "write text" which types into the shell, so the tab stays open even if the CLI exits.
func resumeInITermTab(cli string, resumeArgs []string, sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := fmt.Sprintf("%s %s", cli, strings.Join(resumeArgs, " "))
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cwd), cli, strings.Join(resumeArgs, " "))
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
		return resumeStartedMsg{sessionID: sessionID, newTab: true}
	}
}

// resumeInTmux splits the current tmux window horizontally and runs the resume command.
// Uses send-keys so the shell stays alive if the CLI exits.
func resumeInTmux(cli string, resumeArgs []string, sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		// Split horizontally (side by side) — verbose stays on the left, CLI on the right
		splitArgs := []string{"split-window", "-h"}
		if cwd != "" {
			splitArgs = append(splitArgs, "-c", cwd)
		}
		cmd := exec.Command("tmux", splitArgs...)
		if err := cmd.Run(); err != nil {
			return resumeStartedMsg{sessionID: sessionID}
		}

		// Send the command into the new pane
		sendCmd := fmt.Sprintf("%s %s", cli, strings.Join(resumeArgs, " "))
		cmd = exec.Command("tmux", "send-keys", sendCmd, "Enter")
		cmd.Run()

		return resumeStartedMsg{sessionID: sessionID, newTab: true}
	}
}

// resumeInTerminalApp opens a new Terminal.app window and runs the command.
func resumeInTerminalApp(cli string, resumeArgs []string, sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := fmt.Sprintf("%s %s", cli, strings.Join(resumeArgs, " "))
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cwd), cli, strings.Join(resumeArgs, " "))
		}

		script := fmt.Sprintf(`
tell application "Terminal"
	activate
	do script %s
end tell
`, appleScriptString(shellCmd))

		cmd := exec.Command("osascript", "-e", script)
		cmd.Run()
		return resumeStartedMsg{sessionID: sessionID, newTab: true}
	}
}

// resumeInPlace suspends the TUI and runs the resume command in the current terminal.
func resumeInPlace(cli string, resumeArgs []string, sessionID, cwd string) tea.Cmd {
	c := exec.Command(cli, resumeArgs...)
	if cwd != "" {
		c.Dir = cwd
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return resumeDoneMsg{err: err}
	})
}

// resumeStartedMsg is sent when a session resume was launched in a new tab.
type resumeStartedMsg struct {
	sessionID string
	newTab    bool
}

// resumeDoneMsg is sent when an in-place resume finishes and the TUI should restore.
type resumeDoneMsg struct {
	err error
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// appleScriptString returns a properly quoted AppleScript string literal.
func appleScriptString(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}
