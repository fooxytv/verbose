package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

type viewMode int

const (
	viewSessions viewMode = iota
	viewDetail            // event timeline (default when opening a session)
	viewOverview          // session summary (opt-in via "s")
	viewEvent             // single event drill-down
	viewProject           // project-level view
	viewReplay            // step-by-step playback of a session
)

// sessionsUpdatedMsg signals that the session store has new data.
type sessionsUpdatedMsg struct{}

// statusClearMsg clears the transient status message.
type statusClearMsg struct{}

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

	// Project view
	selectedProject *session.ProjectInfo
	projectScroll   int
	projectCursor   int // selected session within project view (tab/shift-tab)

	// Session todos
	sessionTodos []session.TodoItem

	// Replay: one step of the session at a time, at reading speed.
	replaySteps   []session.ReplayStep
	replayIndex   int
	replayScroll  int
	replayPlaying bool
	replayDelay   time.Duration
	// replayTyped is how many characters of the current step have been typed
	// out. -1 means "all of it", which is what a paused step shows: a reader
	// who has stopped to look wants the whole diff, not a half-written one.
	replayTyped int
	// replayLive keeps playback alive at the end of a session that is still
	// being written, so a running agent can be followed at reading speed.
	// Falling behind is expected and reported, not corrected.
	replayLive bool
	// replayCodeOnly narrows the step list to the steps that wrote code.
	replayCodeOnly bool
	// A step number being typed at the "/" prompt, as in "/24".
	replayGotoTyping bool
	replayGotoDraft  string
	// replayGen invalidates ticks scheduled before a pause, a manual step or a
	// speed change. Without it a stale tick would advance a second step.
	replayGen int

	width  int
	height int

	// Optional project filter
	projectFilter string

	// Transient status message (e.g. "Copied to clipboard")
	statusMsg string

	// Session awaiting delete confirmation; every key is captured while set
	confirmDelete *session.SessionInfo

	// A delete is in flight. Removing an OpenCode session shells out to its
	// CLI and takes about a second, which otherwise looks like a freeze.
	deleting bool

	// Timeline filter and search
	eventFilter  eventFilter
	searchQuery  string
	searchTyping bool   // capturing keystrokes into searchQuery
	searchDraft  string // query being typed, committed on enter

	version string
}

// NewModel creates a new TUI model.
func NewModel(store *session.Store, projectFilter, version string) Model {
	return Model{
		store:         store,
		projectFilter: projectFilter,
		version:       version,
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
		// Identify the step being read before the session is swapped for a
		// fresh parse: afterwards the old index may point somewhere else.
		anchor := m.replayStepUUID()
		wasWaiting := m.replayWaiting()

		m.refreshSessions()

		if m.mode == viewReplay {
			m.rebuildReplay(anchor)
			// A replay that had caught up now has more to play.
			if wasWaiting && !m.replayAtEnd() {
				m.replayTyped = 0
				m.replayGen++
				return m, tea.Batch(m.watchForUpdates, m.replayAdvanceCmd())
			}
		}
		// Auto-scroll to bottom when in detail view (follow live output)
		if m.mode == viewDetail && m.selectedSession != nil && m.autoFollow {
			m.detailCursor = max(0, len(m.visibleEvents())-1)
		}
		return m, m.watchForUpdates

	case resumeStartedMsg:
		// Session was opened in a new tab — nothing to do, TUI stays active
		return m, nil

	case resumeDoneMsg:
		// In-place resume finished — TUI resumes automatically via tea.ExecProcess
		return m, nil

	case yankDoneMsg:
		if msg.err != nil {
			m.statusMsg = "Failed to copy: " + msg.err.Error()
		} else {
			m.statusMsg = "Copied last prompt to clipboard"
		}
		return m, clearStatusAfter()

	case sessionDeletedMsg:
		m.deleting = false
		m.statusMsg = deleteResultMessage(msg)
		// The open session may be the one that just went away.
		if msg.err == nil && m.selectedSession != nil && m.selectedSession.Info.ID == msg.id {
			m.selectedSession = nil
			m.selectedEvent = nil
			m.detailCursor = 0
			m.autoFollow = false
			m.resetTimelineFilter()
			m.mode = viewSessions
		}
		if msg.err == nil && m.selectedProject != nil {
			// Rebuild the project view so its session list drops the deleted row.
			if proj := m.store.GetProjectInfo(m.selectedProject.ProjectDir); proj != nil {
				m.selectedProject = proj
				m.projectCursor = min(m.projectCursor, max(0, len(proj.Sessions)-1))
			}
		}
		m.refreshSessions()
		return m, clearStatusIn(deleteStatusDuration)

	case replayTypeMsg:
		if msg.gen != m.replayGen || !m.replayPlaying || m.mode != viewReplay {
			return m, nil
		}
		m.replayTyped += m.replayCharsPerTick()
		return m, m.replayAdvanceCmd()

	case replayTickMsg:
		// Ignore ticks from before the last pause/step/speed change.
		if msg.gen != m.replayGen || !m.replayPlaying || m.mode != viewReplay {
			return m, nil
		}
		if m.replayAtEnd() {
			// Caught up. If the session is still being written, hold and poll
			// rather than declaring an end that has not happened yet.
			if m.replayLive && m.replaySessionActive() {
				m.replayTyped = -1
				return m, replayTickCmd(m.replayDelay, m.replayGen)
			}
			m.replayPlaying = false
			m.replayTyped = -1
			m.statusMsg = "Replay finished — 0 to restart"
			return m, clearStatusAfter()
		}
		m.replayIndex++
		m.replayScroll = 0
		m.replayTyped = 0
		return m, m.replayAdvanceCmd()

	case statusClearMsg:
		m.statusMsg = ""
		return m, nil
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
			{"p", "project"},
			{"c", "continue"},
			{"R", "replay"},
			{"n", "new"},
			{"y", "yank"},
			{"F", "fork"},
			{"d", "delete"},
			{"r", "refresh"},
			{"q", "quit"},
		})

	case viewDetail:
		if m.selectedSession != nil {
			visible := m.visibleEvents()
			content = renderSessionDetail(m.selectedSession, visible, m.detailCursor,
				m.width, m.height,
				filterStatus(m.eventFilter, m.searchQuery, len(visible), len(m.selectedSession.Events)))
		}
		if m.searchTyping {
			help = searchPromptStyle.Render(" search ") + " " + m.searchDraft +
				mutedStyle.Render("█   enter to apply · esc to cancel")
			break
		}
		followLabel := "follow"
		if m.autoFollow {
			followLabel = "follow ●"
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "navigate"},
			{"→/enter", "expand"},
			{"←", "back"},
			{"/", "search"},
			{"tab", "filter: " + m.eventFilter.label()},
			{"N", "next fail"},
			{"s", "summary"},
			{"R", "replay"},
			{"c", "continue"},
			{"d", "delete"},
			{"f", followLabel},
			{"q", "quit"},
		})

	case viewOverview:
		if m.selectedSession != nil {
			hasMemory := false
			if m.selectedSession != nil {
				proj := m.store.GetProjectInfo(m.selectedSession.Info.ProjectDir)
				hasMemory = proj != nil && proj.Memory != ""
			}
			content = renderSessionOverview(m.selectedSession, m.sessionTodos, hasMemory, m.overviewScroll, m.width, m.height)
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "scroll"},
			{"←/esc", "back"},
			{"p", "project"},
			{"d", "delete"},
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

	case viewReplay:
		if m.selectedSession != nil {
			content = renderReplay(replayView{
				sess:     m.selectedSession,
				steps:    m.replaySteps,
				idx:      m.replayIndex,
				scroll:   m.replayScroll,
				typed:    m.replayTyped,
				playing:  m.replayPlaying,
				waiting:  m.replayWaiting(),
				codeOnly: m.replayCodeOnly,
				delay:    m.replayDelay,
				width:    m.width,
				height:   m.height,
			})
		}
		playLabel := "play"
		if m.replayPlaying {
			playLabel = "pause"
		}
		if m.replayGotoTyping {
			help = m.replayGotoPrompt()
			break
		}
		liveLabel := "live"
		if m.replayLive {
			liveLabel = "live ●"
		}
		help = renderHelp([]helpKey{
			{"space", playLabel},
			{"→/←", "step"},
			{"↑/↓", "scroll"},
			{"+/-", "speed"},
			{"/", "go to step"},
			{"0", "restart"},
			{"tab", "code only"},
			{"f", liveLabel},
			{"t", "timeline"},
			{"esc", "back"},
			{"q", "quit"},
		})

	case viewProject:
		if m.selectedProject != nil {
			content = renderProjectView(m.selectedProject, m.projectScroll, m.projectCursor, m.width, m.height)
		}
		help = renderHelp([]helpKey{
			{"↑/↓", "scroll"},
			{"tab", "select session"},
			{"enter", "open"},
			{"c", "continue"},
			{"n", "new"},
			{"d", "delete"},
			{"←/esc", "back"},
			{"q", "quit"},
		})
	}

	// The footer holds one thing at a time. A pending delete outranks a status
	// message, which outranks the help: appending them instead pushed the text
	// past the right edge, so a delete looked like it had done nothing.
	switch {
	case m.confirmDelete != nil:
		help = deletePrompt(*m.confirmDelete)
	case m.statusMsg != "":
		help = statusStyle.Render(m.statusMsg)
	}

	versionTag := mutedStyle.Render("  v" + m.version)

	// Pin the footer to the last row of the terminal. Views whose content is
	// shorter than the window would otherwise leave it floating mid-screen.
	return padToHeight(content, m.height-1) + "\n" + help + versionTag
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// A pending delete swallows every key until it is confirmed or cancelled,
	// so no binding can fire by accident with the prompt on screen.
	if m.confirmDelete != nil {
		return m.handleDeleteConfirmKey(key)
	}

	// While typing a search the timeline keybindings are suspended, otherwise
	// every letter of the query would trigger a command.
	if m.searchTyping {
		return m.handleSearchKey(msg, key)
	}

	// Replay owns its keys: stepping, scrolling and speed all reuse letters
	// that mean something else elsewhere.
	if m.mode == viewReplay {
		return m.handleReplayKey(msg, key)
	}

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
			m.resetTimelineFilter()
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
		case viewProject:
			m.mode = viewSessions
			m.selectedProject = nil
			m.projectScroll = 0
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
			if m.detailCursor < len(m.visibleEvents())-1 {
				m.detailCursor++
			}
		case viewEvent:
			m.eventScroll++
		case viewProject:
			m.projectScroll++
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
		case viewProject:
			if m.projectScroll > 0 {
				m.projectScroll--
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
		case viewProject:
			m.projectScroll = 0
		}

	case "G", "end":
		switch m.mode {
		case viewSessions:
			if len(m.sessions) > 0 {
				m.cursor = len(m.sessions) - 1
			}
		case viewDetail:
			if n := len(m.visibleEvents()); n > 0 {
				m.detailCursor = n - 1
			}
		case viewProject:
			m.projectScroll = 99999 // will be clamped by renderer
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
					m.resetTimelineFilter()
					m.detailCursor = max(0, len(sess.Events)-1)
					m.autoFollow = true
					m.mode = viewDetail
				}
			}
		case viewDetail:
			if evt := m.currentEvent(); evt != nil {
				m.selectedEvent = evt
				m.eventScroll = 0
				m.mode = viewEvent
			}
		case viewProject:
			if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
				info := m.selectedProject.Sessions[m.projectCursor]
				sess := m.store.GetSession(info.ID)
				if sess != nil {
					m.selectedSession = sess
					m.resetTimelineFilter()
					m.detailCursor = max(0, len(sess.Events)-1)
					m.autoFollow = true
					m.mode = viewDetail
				}
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
					m.sessionTodos = m.store.GetSessionTodos(info.ID)
					m.overviewScroll = 0
					m.mode = viewOverview
				}
			}
		case viewDetail:
			if m.selectedSession != nil {
				m.sessionTodos = m.store.GetSessionTodos(m.selectedSession.Info.ID)
				m.overviewScroll = 0
				m.mode = viewOverview
			}
		}

	case "p":
		// Open project view
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				info := m.sessions[m.cursor]
				proj := m.store.GetProjectInfo(info.ProjectDir)
				if proj != nil {
					m.selectedProject = proj
					m.projectScroll = 0
					m.projectCursor = 0
					m.mode = viewProject
				}
			}
		case viewDetail, viewOverview:
			if m.selectedSession != nil {
				proj := m.store.GetProjectInfo(m.selectedSession.Info.ProjectDir)
				if proj != nil {
					m.selectedProject = proj
					m.projectScroll = 0
					m.projectCursor = 0
					m.mode = viewProject
				}
			}
		}

	case "d":
		// Ask before removing anything: deletion touches real files.
		if target := m.deleteTarget(); target != nil {
			m.confirmDelete = target
		}

	case "c":
		// Continue/resume session in a new terminal tab
		var info *session.SessionInfo
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				s := m.sessions[m.cursor]
				info = &s
			}
		case viewDetail, viewOverview:
			if m.selectedSession != nil {
				s := m.selectedSession.Info
				info = &s
			}
		case viewProject:
			if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
				s := m.selectedProject.Sessions[m.projectCursor]
				info = &s
			}
		}
		if info != nil {
			return m, resumeSessionCmd(info.ID, info.CWD)
		}

	case "n":
		// New session in same project CWD
		var info *session.SessionInfo
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				s := m.sessions[m.cursor]
				info = &s
			}
		case viewDetail, viewOverview:
			if m.selectedSession != nil {
				s := m.selectedSession.Info
				info = &s
			}
		case viewProject:
			if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
				s := m.selectedProject.Sessions[m.projectCursor]
				info = &s
			}
		}
		if info != nil {
			return m, newSessionCmd(info.Source, info.CWD)
		}

	case "y":
		// Yank last user prompt to clipboard
		var sess *session.Session
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				sess = m.store.GetSession(m.sessions[m.cursor].ID)
			}
		case viewDetail, viewOverview:
			sess = m.selectedSession
		case viewProject:
			if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
				sess = m.store.GetSession(m.selectedProject.Sessions[m.projectCursor].ID)
			}
		}
		if sess != nil {
			if prompt := lastUserPrompt(sess); prompt != "" {
				return m, yankPromptCmd(prompt)
			}
		}

	case "F":
		// Fork: new session with last prompt as first message
		var info *session.SessionInfo
		var sess *session.Session
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions) {
				s := m.sessions[m.cursor]
				info = &s
				sess = m.store.GetSession(s.ID)
			}
		case viewDetail, viewOverview:
			if m.selectedSession != nil {
				s := m.selectedSession.Info
				info = &s
				sess = m.selectedSession
			}
		case viewProject:
			if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
				s := m.selectedProject.Sessions[m.projectCursor]
				info = &s
				sess = m.store.GetSession(s.ID)
			}
		}
		if info != nil && sess != nil {
			if prompt := lastUserPrompt(sess); prompt != "" {
				return m, forkSessionCmd(info.Source, info.CWD, prompt)
			}
		}

	case "R":
		// Replay the session one step at a time.
		if sess := m.sessionInContext(); sess != nil {
			m.startReplay(sess)
			return m, m.replayAdvanceCmd()
		}

	case "/":
		if m.mode == viewDetail {
			m.searchTyping = true
			m.searchDraft = m.searchQuery
		}

	case "N":
		// Jump to the next failure at or after the cursor.
		if m.mode == viewDetail {
			m.jumpToNextFailure()
		}

	case "f":
		if m.mode == viewDetail {
			m.autoFollow = !m.autoFollow
			if m.autoFollow {
				m.detailCursor = max(0, len(m.visibleEvents())-1)
			}
		}

	case "r":
		m.refreshSessions()

	case "tab":
		switch m.mode {
		case viewProject:
			if m.selectedProject != nil && len(m.selectedProject.Sessions) > 0 {
				m.projectCursor = (m.projectCursor + 1) % len(m.selectedProject.Sessions)
			}
		case viewDetail:
			m.eventFilter = m.eventFilter.next()
			m.detailCursor = 0
			m.clampDetailCursor()
			m.autoFollow = false
		}

	case "shift+tab":
		if m.mode == viewProject && m.selectedProject != nil && len(m.selectedProject.Sessions) > 0 {
			m.projectCursor = (m.projectCursor - 1 + len(m.selectedProject.Sessions)) % len(m.selectedProject.Sessions)
		}

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
		case viewProject:
			m.projectScroll = max(0, m.projectScroll-pageSize)
		}

	case "shift+down", "pgdown":
		pageSize := m.pageSize()
		switch m.mode {
		case viewSessions:
			if len(m.sessions) > 0 {
				m.cursor = min(len(m.sessions)-1, m.cursor+pageSize)
			}
		case viewDetail:
			if n := len(m.visibleEvents()); n > 0 {
				m.detailCursor = min(n-1, m.detailCursor+pageSize)
			}
		case viewOverview:
			m.overviewScroll += pageSize
		case viewEvent:
			m.eventScroll += pageSize
		case viewProject:
			m.projectScroll += pageSize
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
		case viewProject:
			if m.projectScroll > 0 {
				m.projectScroll--
			}
		}

	case tea.MouseButtonWheelDown:
		switch m.mode {
		case viewSessions:
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case viewDetail:
			if m.detailCursor < len(m.visibleEvents())-1 {
				m.detailCursor++
			}
		case viewOverview:
			m.overviewScroll++
		case viewEvent:
			m.eventScroll++
		case viewProject:
			m.projectScroll++
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

// How long a transient status message stays on screen. Delete results get
// longer, because they report something irreversible.
const (
	statusDuration       = 2 * time.Second
	deleteStatusDuration = 5 * time.Second
)

// clearStatusAfter wipes the transient status message after a short delay.
func clearStatusAfter() tea.Cmd {
	return clearStatusIn(statusDuration)
}

func clearStatusIn(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return statusClearMsg{}
	})
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

// padToHeight makes a block occupy exactly n lines, padding short content and
// trimming anything that would push the footer off the bottom of the screen.
func padToHeight(content string, n int) string {
	if n <= 0 {
		return content
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
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

// lastUserPrompt returns the text of the last EventUserPrompt in the session.
func lastUserPrompt(sess *session.Session) string {
	for i := len(sess.Events) - 1; i >= 0; i-- {
		if sess.Events[i].Type == session.EventUserPrompt {
			return sess.Events[i].UserText
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// visibleEvents returns the indices of the timeline events passing the active
// filter and search. The detail cursor indexes into this list.
func (m Model) visibleEvents() []int {
	return visibleEvents(m.selectedSession, m.eventFilter, m.searchQuery)
}

// currentEvent returns the event under the detail cursor, or nil.
func (m Model) currentEvent() *session.Event {
	visible := m.visibleEvents()
	if m.detailCursor < 0 || m.detailCursor >= len(visible) {
		return nil
	}
	evt := m.selectedSession.Events[visible[m.detailCursor]]
	return &evt
}

func (m *Model) resetTimelineFilter() {
	m.eventFilter = filterAll
	m.searchQuery = ""
	m.searchDraft = ""
	m.searchTyping = false
}

// clampDetailCursor keeps the cursor inside the filtered list after the filter
// or query changes.
func (m *Model) clampDetailCursor() {
	n := len(m.visibleEvents())
	if n == 0 {
		m.detailCursor = 0
		return
	}
	if m.detailCursor >= n {
		m.detailCursor = n - 1
	}
	if m.detailCursor < 0 {
		m.detailCursor = 0
	}
}

// jumpToNextFailure moves the cursor to the next failed or denied operation,
// wrapping to the top once the end is reached.
func (m *Model) jumpToNextFailure() {
	visible := m.visibleEvents()
	if len(visible) == 0 {
		return
	}
	isFailure := func(i int) bool {
		return filterFailures.matches(m.selectedSession.Events[visible[i]])
	}
	for off := 1; off <= len(visible); off++ {
		i := (m.detailCursor + off) % len(visible)
		if isFailure(i) {
			m.detailCursor = i
			m.autoFollow = false
			m.statusMsg = ""
			return
		}
	}
	m.statusMsg = "No failed operations in this view"
}

// handleSearchKey consumes keystrokes while the search prompt is open.
func (m Model) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.searchTyping = false
		m.searchDraft = ""
		return m, nil

	case "enter":
		m.searchQuery = strings.TrimSpace(m.searchDraft)
		m.searchTyping = false
		m.autoFollow = false
		m.detailCursor = 0
		m.clampDetailCursor()
		return m, nil

	case "backspace":
		if r := []rune(m.searchDraft); len(r) > 0 {
			m.searchDraft = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.searchDraft = ""
		return m, nil
	}

	if msg.Type == tea.KeySpace {
		m.searchDraft += " "
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.searchDraft += string(msg.Runes)
	}
	return m, nil
}
