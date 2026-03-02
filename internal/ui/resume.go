package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// resumeSessionCmd returns a tea.Cmd that resumes a Claude session.
// Detection priority: tmux (split pane) > iTerm2 (new tab) > Terminal.app > in-place.
func resumeSessionCmd(sessionID, cwd string) tea.Cmd {
	// If inside tmux, split the current window — stays in context.
	if os.Getenv("TMUX") != "" {
		return resumeInTmux(sessionID, cwd)
	}

	// iTerm2 without tmux — open a new tab.
	if os.Getenv("ITERM_SESSION_ID") != "" {
		return resumeInITermTab(sessionID, cwd)
	}

	terminal := os.Getenv("TERM_PROGRAM")
	if terminal == "Apple_Terminal" {
		return resumeInTerminalApp(sessionID, cwd)
	}

	return resumeInPlace(sessionID, cwd)
}

// resumeInITermTab opens a new iTerm2 tab and runs claude --resume there.
// Uses "write text" which types into the shell, so the tab stays open even if claude exits.
func resumeInITermTab(sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := fmt.Sprintf("claude --resume %s", sessionID)
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && claude --resume %s", shellQuote(cwd), sessionID)
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

// resumeInTmux splits the current tmux window horizontally and runs claude --resume.
// Uses send-keys so the shell stays alive if claude exits.
func resumeInTmux(sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		// Split horizontally (side by side) — verbose stays on the left, claude on the right
		splitArgs := []string{"split-window", "-h"}
		if cwd != "" {
			splitArgs = append(splitArgs, "-c", cwd)
		}
		cmd := exec.Command("tmux", splitArgs...)
		if err := cmd.Run(); err != nil {
			return resumeStartedMsg{sessionID: sessionID}
		}

		// Send the command into the new pane
		sendCmd := fmt.Sprintf("claude --resume %s", sessionID)
		cmd = exec.Command("tmux", "send-keys", sendCmd, "Enter")
		cmd.Run()

		return resumeStartedMsg{sessionID: sessionID, newTab: true}
	}
}

// resumeInTerminalApp opens a new Terminal.app window and runs the command.
func resumeInTerminalApp(sessionID, cwd string) tea.Cmd {
	return func() tea.Msg {
		shellCmd := fmt.Sprintf("claude --resume %s", sessionID)
		if cwd != "" {
			shellCmd = fmt.Sprintf("cd %s && claude --resume %s", shellQuote(cwd), sessionID)
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

// resumeInPlace suspends the TUI and runs claude --resume in the current terminal.
func resumeInPlace(sessionID, cwd string) tea.Cmd {
	c := exec.Command("claude", "--resume", sessionID)
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
