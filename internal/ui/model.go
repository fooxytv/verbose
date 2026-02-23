package ui

import (
	"fmt"

	"verbose/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

type viewMode int

const (
	viewSessions viewMode = iota
	viewDetail            // event timeline (default when opening a session)
	viewOverview          // session summary (opt-in via "s")
	viewEvent             // single event drill-down
)

// sessionsUpdatedMsg signals that the session store has new data.
type sessionsUpdatedMsg struct{}

// Model is the main bubbletea model.
type Model struct {
	store   *session.Store
	updates <-chan struct{}

	mode viewMode

	// Sessions list
	sessions []session.SessionInfo
	cursor   int

	// Session detail + overview
	selectedSession *session.Session
	detailCursor    int
	overviewScroll  int

	// Event detail
	selectedEvent *session.Event
	eventScroll   int

	// Auto-follow: scroll to bottom on updates
	autoFollow bool

	width  int
	height int

	// Optional project filter
	projectFilter string
}

// NewModel creates a new TUI model.
func NewModel(store *session.Store, projectFilter string) Model {
	return Model{
		store:         store,
		projectFilter: projectFilter,
	}
}

// SetUpdates sets the channel for receiving session update notifications.
func (m *Model) SetUpdates(ch <-chan struct{}) {
	m.updates = ch
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadSessions,
		m.watchForUpdates,
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case sessionsUpdatedMsg:
		m.refreshSessions()
		// Auto-scroll to bottom when in detail view (follow live output)
		if m.mode == viewDetail && m.selectedSession != nil && m.autoFollow {
			m.detailCursor = max(0, len(m.selectedSession.Events)-1)
		}
		return m, m.watchForUpdates
	}

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var content string
	var help string

	switch m.mode {
	case viewSessions:
		content = renderSessionsList(m.sessions, m.cursor, m.width, m.height)
		help = renderHelp([]helpKey{
			{"↑/↓", "navigate"},
			{"→/enter/space", "open"},
			{"s", "summary"},
			{"r", "refresh"},
			{"q", "quit"},
		})

	case viewDetail:
		if m.selectedSession != nil {
			content = renderSessionDetail(m.selectedSession, m.detailCursor, m.width, m.height)
		}
		followLabel := "follow"
		if m.autoFollow {
			followLabel = "follow ●"
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "navigate"},
			{"→/enter/space", "expand"},
			{"←", "back"},
			{"s", "summary"},
			{"f", followLabel},
			{"q", "quit"},
		})

	case viewOverview:
		if m.selectedSession != nil {
			content = renderSessionOverview(m.selectedSession, m.overviewScroll, m.width, m.height)
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "scroll"},
			{"←/esc", "back"},
			{"q", "quit"},
		})

	case viewEvent:
		if m.selectedEvent != nil {
			content = renderEventDetail(*m.selectedEvent, m.eventScroll, m.width, m.height)
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "scroll"},
			{"←", "back"},
			{"q", "quit"},
		})
	}

	return content + "\n" + help
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Normalize space to "enter" so it works as a selection key
	if msg.Type == tea.KeySpace {
		key = "enter"
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc", "left":
		switch m.mode {
		case viewDetail:
			m.mode = viewSessions
			m.selectedSession = nil
			m.detailCursor = 0
			m.autoFollow = false
		case viewOverview:
			// Go back to timeline if we came from there, otherwise sessions
			if m.selectedSession != nil {
				m.mode = viewDetail
				m.overviewScroll = 0
			} else {
				m.mode = viewSessions
				m.overviewScroll = 0
			}
		case viewEvent:
			m.mode = viewDetail
			m.selectedEvent = nil
			m.eventScroll = 0
		}

	case "j", "down":
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case viewOverview:
			m.overviewScroll++
		case viewDetail:
			if m.selectedSession != nil && m.detailCursor < len(m.selectedSession.Events)-1 {
				m.detailCursor++
			}
		case viewEvent:
			m.eventScroll++
		}

	case "k", "up":
		switch m.mode {
		case viewSessions:
			if m.cursor > 0 {
				m.cursor--
			}
		case viewOverview:
			if m.overviewScroll > 0 {
				m.overviewScroll--
			}
		case viewDetail:
			if m.detailCursor > 0 {
				m.detailCursor--
				m.autoFollow = false
			}
		case viewEvent:
			if m.eventScroll > 0 {
				m.eventScroll--
			}
		}

	case "g", "home":
		switch m.mode {
		case viewSessions:
			m.cursor = 0
		case viewDetail:
			m.detailCursor = 0
		case viewOverview:
			m.overviewScroll = 0
		case viewEvent:
			m.eventScroll = 0
		}

	case "G", "end":
		switch m.mode {
		case viewSessions:
			if len(m.sessions) > 0 {
				m.cursor = len(m.sessions) - 1
			}
		case viewDetail:
			if m.selectedSession != nil && len(m.selectedSession.Events) > 0 {
				m.detailCursor = len(m.selectedSession.Events) - 1
			}
		}

	case "enter", "right":
		switch m.mode {
		case viewSessions:
			// Go directly to timeline
			if m.cursor < len(m.sessions) {
				info := m.sessions[m.cursor]
				sess := m.store.GetSession(info.ID)
				if sess != nil {
					m.selectedSession = sess
					m.detailCursor = max(0, len(sess.Events)-1)
					m.autoFollow = true
					m.mode = viewDetail
				}
			}
		case viewDetail:
			if m.selectedSession != nil && m.detailCursor < len(m.selectedSession.Events) {
				evt := m.selectedSession.Events[m.detailCursor]
				m.selectedEvent = &evt
				m.eventScroll = 0
				m.mode = viewEvent
			}
		}

	case "s":
		// Open summary from sessions list or timeline
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				info := m.sessions[m.cursor]
				sess := m.store.GetSession(info.ID)
				if sess != nil {
					m.selectedSession = sess
					m.overviewScroll = 0
					m.mode = viewOverview
				}
			}
		case viewDetail:
			if m.selectedSession != nil {
				m.overviewScroll = 0
				m.mode = viewOverview
			}
		}

	case "f":
		if m.mode == viewDetail {
			m.autoFollow = !m.autoFollow
			if m.autoFollow && m.selectedSession != nil {
				m.detailCursor = max(0, len(m.selectedSession.Events)-1)
			}
		}

	case "r":
		m.refreshSessions()

	case "shift+up", "pgup":
		pageSize := m.pageSize()
		switch m.mode {
		case viewSessions:
			m.cursor = max(0, m.cursor-pageSize)
		case viewDetail:
			m.detailCursor = max(0, m.detailCursor-pageSize)
			m.autoFollow = false
		case viewOverview:
			m.overviewScroll = max(0, m.overviewScroll-pageSize)
		case viewEvent:
			m.eventScroll = max(0, m.eventScroll-pageSize)
		}

	case "shift+down", "pgdown":
		pageSize := m.pageSize()
		switch m.mode {
		case viewSessions:
			if len(m.sessions) > 0 {
				m.cursor = min(len(m.sessions)-1, m.cursor+pageSize)
			}
		case viewDetail:
			if m.selectedSession != nil && len(m.selectedSession.Events) > 0 {
				m.detailCursor = min(len(m.selectedSession.Events)-1, m.detailCursor+pageSize)
			}
		case viewOverview:
			m.overviewScroll += pageSize
		case viewEvent:
			m.eventScroll += pageSize
		}
	}

	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		switch m.mode {
		case viewSessions:
			if m.cursor > 0 {
				m.cursor--
			}
		case viewDetail:
			if m.detailCursor > 0 {
				m.detailCursor--
				m.autoFollow = false
			}
		case viewOverview:
			if m.overviewScroll > 0 {
				m.overviewScroll--
			}
		case viewEvent:
			if m.eventScroll > 0 {
				m.eventScroll--
			}
		}

	case tea.MouseButtonWheelDown:
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case viewDetail:
			if m.selectedSession != nil && m.detailCursor < len(m.selectedSession.Events)-1 {
				m.detailCursor++
			}
		case viewOverview:
			m.overviewScroll++
		case viewEvent:
			m.eventScroll++
		}
	}
	return m, nil
}

func (m Model) pageSize() int {
	ps := m.height / 2
	if ps < 5 {
		ps = 5
	}
	return ps
}

func (m *Model) refreshSessions() {
	sessions := m.store.GetSessions()

	if m.projectFilter != "" {
		var filtered []session.SessionInfo
		for _, s := range sessions {
			if s.ProjectName == m.projectFilter || s.ProjectDir == m.projectFilter {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	m.sessions = sessions
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}

	if m.selectedSession != nil {
		updated := m.store.GetSession(m.selectedSession.Info.ID)
		if updated != nil {
			m.selectedSession = updated
		}
	}
}

func (m Model) loadSessions() tea.Msg {
	return sessionsUpdatedMsg{}
}

func (m Model) watchForUpdates() tea.Msg {
	if m.updates == nil {
		return nil
	}
	<-m.updates
	return sessionsUpdatedMsg{}
}

type helpKey struct {
	key  string
	desc string
}

func renderHelp(keys []helpKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %s", keyStyle.Render(k.key), mutedStyle.Render(k.desc))
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "  "
		}
		result += p
	}
	return helpStyle.Render(result)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
