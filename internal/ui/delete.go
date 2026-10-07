package ui

import (
	"fmt"
	"path/filepath"

	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// sessionDeletedMsg reports the result of a delete the user confirmed.
type sessionDeletedMsg struct {
	id      string
	outcome session.DeleteOutcome
	err     error
}

// deleteSessionCmd removes a session from the machine in the background.
func deleteSessionCmd(store *session.Store, id string) tea.Cmd {
	return func() tea.Msg {
		outcome, err := store.DeleteSession(id)
		return sessionDeletedMsg{id: id, outcome: outcome, err: err}
	}
}

// deletePrompt is the confirmation bar shown in place of the help line. It
// states plainly whether the deletion can be undone, because that differs by
// source and is the whole reason to stop and ask.
func deletePrompt(info session.SessionInfo) string {
	label := shortSessionID(info.ID)
	if info.ProjectName != "" {
		label += " · " + info.ProjectName
	}

	var consequence string
	if info.Source == "opencode" {
		consequence = deleteWarnStyle.Render(" permanent — OpenCode has no trash ")
	} else {
		consequence = mutedStyle.Render(" moves to Trash ")
	}

	return deletePromptStyle.Render(" delete ") + " " +
		normalStyle.Render(label) + consequence +
		keyStyle.Render("y") + mutedStyle.Render(" confirm  ") +
		keyStyle.Render("n/esc") + mutedStyle.Render(" cancel")
}

// deleteResultMessage describes a finished delete for the status bar.
func deleteResultMessage(msg sessionDeletedMsg) string {
	if msg.err != nil {
		return "Delete failed: " + msg.err.Error()
	}
	if msg.outcome.Permanent {
		return fmt.Sprintf("Deleted %s permanently", shortSessionID(msg.id))
	}

	switch len(msg.outcome.Trashed) {
	case 0:
		return fmt.Sprintf("Deleted %s", shortSessionID(msg.id))
	case 1:
		return "Moved to Trash: " + filepath.Base(msg.outcome.Trashed[0])
	default:
		return fmt.Sprintf("Moved %d files to Trash for %s",
			len(msg.outcome.Trashed), shortSessionID(msg.id))
	}
}

// shortSessionID abbreviates an ID the same way the sessions list does.
func shortSessionID(id string) string {
	if len(id) > 10 {
		return truncateRunes(id, 8) + ".."
	}
	return id
}

// deleteTarget returns the session the delete key applies to in the current
// view, or nil when the view has no session under the cursor.
func (m Model) deleteTarget() *session.SessionInfo {
	switch m.mode {
	case viewSessions:
		if m.cursor < len(m.sessions) {
			s := m.sessions[m.cursor]
			return &s
		}
	case viewDetail, viewOverview:
		if m.selectedSession != nil {
			s := m.selectedSession.Info
			return &s
		}
	case viewProject:
		if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
			s := m.selectedProject.Sessions[m.projectCursor]
			return &s
		}
	}
	return nil
}

// handleDeleteConfirmKey consumes every keystroke while a delete is pending, so
// no other binding can fire with a confirmation on screen.
func (m Model) handleDeleteConfirmKey(key string) (tea.Model, tea.Cmd) {
	target := m.confirmDelete
	switch key {
	case "y", "Y":
		m.confirmDelete = nil
		m.deleting = true
		m.statusMsg = "Deleting " + shortSessionID(target.ID) + "…"
		return m, deleteSessionCmd(m.store, target.ID)
	default:
		// Anything else cancels: only an explicit "y" deletes.
		m.confirmDelete = nil
		m.statusMsg = "Delete cancelled"
		return m, clearStatusAfter()
	}
}
