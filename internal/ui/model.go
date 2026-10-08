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
	viewTree              // project tree, marked with what the session changed
)

// sessionsUpdatedMsg signals that the session store has new data.
type sessionsUpdatedMsg struct{}

// statusClearMsg clears the transient status message.
type statusClearMsg struct{}

// clockTickMsg redraws once a second so the footer clock and the "updated N ago"
// age stay truthful while nothing else is happening.
type clockTickMsg struct{}

func clockTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clockTickMsg{} })
}

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

	// Project tree, marked with what the open session did to each file. The
	// structure is read from disk once when the view opens; the marks come from
	// the transcript and follow the replay position.
	treeRoot        *session.TreeNode
	treeActivity    map[string]*session.FileActivity
	treeCollapsed   map[string]bool
	treeCursor      int
	treeScroll      int
	treeChangedOnly bool
	// treeSplit shows the tree beside the replay rather than instead of it.
	treeSplit bool
	// treeFollow keeps the tree scrolled to the change the replay has reached.
	// Turned off when the reader moves the cursor themselves.
	treeFollow bool
	// treeGen invalidates fade ticks left over from an earlier step.
	treeGen int

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
	// replayStepAt is when playback reached the current step, which is what the
	// tree's highlight fade is measured from.
	replayStepAt time.Time
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
		clockTickCmd(),
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
		if msg.gen != m.replayGen || !m.replayPlaying || !m.replayVisible() {
			return m, nil
		}
		m.replayTyped += m.replayCharsPerTick()
		return m, m.replayAdvanceCmd()

	case replayTickMsg:
		// Ignore ticks from before the last pause/step/speed change.
		if msg.gen != m.replayGen || !m.replayPlaying || !m.replayVisible() {
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
		m.replayStepAt = time.Now()
		return m, m.replayAdvanceCmd()

	case treeTickMsg:
		// Only keeps ticking while a highlight is still fading.
		if msg.gen != m.treeGen || m.mode != viewTree {
			return m, nil
		}
		if treeFadeStage(time.Since(m.replayStepAt)) < treeFadeStages {
			return m, treeTickCmd(m.treeGen)
		}
		return m, nil

	case clockTickMsg:
		// The view reads the wall clock directly; the tick only forces a redraw.
		return m, clockTickCmd()

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
	// help is an explicit footer override (a prompt or a status line);
	// helpKeys is the normal keybinding list, which the footer fits to the
	// width it has.
	var help string
	var helpKeys []helpKey

	switch m.mode {
	case viewSessions:
		content = renderSessionsList(m.sessions, m.cursor, m.width, m.height)
		// Ordered by how much a reader needs it: the footer drops from the end
		// on a narrow terminal, so anything that must stay visible comes first.
		helpKeys = []helpKey{
			{"↑/↓", "move"},
			{"enter", "open"},
			{"R", "replay"},
			{"T", "tree"},
			{"s", "summary"},
			{"p", "project"},
			{"c", "continue"},
			{"n", "new"},
			{"d", "delete"},
			{"y", "yank"},
			{"F", "fork"},
			{"r", "refresh"},
			{"q", "quit"},
		}

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
		helpKeys = []helpKey{
			{"↑/↓", "move"},
			{"enter", "expand"},
			{"R", "replay"},
			{"T", "tree"},
			{"/", "search"},
			{"tab", m.eventFilter.label()},
			{"N", "next fail"},
			{"s", "summary"},
			{"c", "continue"},
			{"d", "delete"},
			{"f", followLabel},
			{"←", "back"},
			{"q", "quit"},
		}

	case viewOverview:
		if m.selectedSession != nil {
			hasMemory := false
			if m.selectedSession != nil {
				proj := m.store.GetProjectInfo(m.selectedSession.Info.ProjectDir)
				hasMemory = proj != nil && proj.Memory != ""
			}
			content = renderSessionOverview(m.selectedSession, m.sessionTodos, hasMemory, m.overviewScroll, m.width, m.height)
		}
		helpKeys = []helpKey{
			{"↑/↓", "scroll"},
			{"←/esc", "back"},
			{"p", "project"},
			{"d", "delete"},
			{"q", "quit"},
		}

	case viewEvent:
		if m.selectedEvent != nil {
			content = renderEventDetail(*m.selectedEvent, m.eventScroll, m.width, m.height)
		}
		helpKeys = []helpKey{
			{"↑/↓", "scroll"},
			{"←", "back"},
			{"q", "quit"},
		}

	case viewReplay:
		if m.selectedSession != nil {
			if m.treeSplit {
				content = renderSplit(m)
			} else {
				content = renderReplay(m.replayViewState(m.width, m.height))
			}
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
		codeLabel := "code only"
		if m.replayCodeOnly {
			codeLabel = "all steps"
		}
		treeLabel := "tree"
		if m.treeSplit {
			treeLabel = "tree ●"
		}
		helpKeys = []helpKey{
			{"space", playLabel},
			{"→/←", "step"},
			{"T", treeLabel},
		}
		if m.treeSplit {
			followLabel := "follow tree"
			if m.treeFollow {
				followLabel = "following ●"
			}
			helpKeys = append(helpKeys,
				helpKey{"ctrl+↑/↓", "tree"},
				helpKey{"ctrl+f", followLabel},
			)
		}
		helpKeys = append(helpKeys,
			helpKey{"tab", codeLabel},
			helpKey{"+/-", "speed"},
			helpKey{"/", "go to step"},
			helpKey{"↑/↓", "scroll"},
			helpKey{"0", "restart"},
			helpKey{"f", liveLabel},
			helpKey{"t", "timeline"},
			helpKey{"esc", "back"},
			helpKey{"q", "quit"},
		)

	case viewTree:
		if m.selectedSession != nil {
			content = renderTree(m.treeViewState(usableWidth(m.width), m.height))
		}
		changedLabel := "changed only"
		if m.treeChangedOnly {
			changedLabel = "all files"
		}
		helpKeys = []helpKey{
			{"↑/↓", "move"},
			{"→/←", "open/close"},
			{"space", "play"},
			{"tab", changedLabel},
			{"enter", "jump to change"},
			{"R", "replay"},
			{"esc", "back"},
			{"q", "quit"},
		}

	case viewProject:
		if m.selectedProject != nil {
			content = renderProjectView(m.selectedProject, m.projectScroll, m.projectCursor, m.width, m.height)
		}
		helpKeys = []helpKey{
			{"↑/↓", "scroll"},
			{"tab", "select session"},
			{"enter", "open"},
			{"c", "continue"},
			{"n", "new"},
			{"d", "delete"},
			{"←/esc", "back"},
			{"q", "quit"},
		}
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

	// Pin the footer to the last row of the terminal. Views whose content is
	// shorter than the window would otherwise leave it floating mid-screen.
	frame := padToHeight(content, m.height-1) + "\n" + m.footer(help, helpKeys)

	// Last chokepoint before anything reaches the terminal. A row wider than
	// the window is wrapped into two, so the frame occupies more rows than it
	// claims; the terminal scrolls, the renderer's cursor arithmetic no longer
	// matches the screen, and rows are stranded — the header and footer appear
	// twice, and a line being typed appears repeated down the screen. Every
	// view goes through here, so this is the one place that can guarantee it
	// cannot happen.
	return clampFrame(frame, m.width, m.height)
}

// clampFrame trims a frame to the terminal it is being drawn into: each row to
// the width, and the whole frame to the number of rows.
func clampFrame(frame string, width, height int) string {
	rows := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	if height > 0 && len(rows) > height {
		rows = rows[:height]
	}
	w := usableWidth(width)
	for i, r := range rows {
		r = expandTabs(r)
		if w > 0 && visibleLen(r) > w {
			r = truncateVisible(r, w)
		}
		rows[i] = r
	}
	return strings.Join(rows, "\n")
}

// tabStop is how many columns a tab is expanded to. Terminals default to 8,
// which wastes a lot of a narrow pane; what matters is that verbose and the
// terminal agree, and they do because verbose expands tabs itself.
const tabStop = 4

// expandTabs replaces tabs with spaces to the next tab stop, counting only
// visible characters so colour escapes do not shift the stops.
//
// This is not cosmetic. visibleLen counts a tab as one column but a terminal
// draws it as up to eight, so every width measurement in this package is wrong
// for code that is indented with tabs — which is most Go, Python and Makefile
// code. Measured over real transcripts, a line measured as 94 columns drew as
// 179: it wrapped, and every row below it was left stranded on screen. Once
// tabs are gone, visibleLen is exact.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col, inEsc := 0, false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		if r == '\t' {
			n := tabStop - (col % tabStop)
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// usableWidth is the last column a row may occupy: one short of the terminal.
//
// Filling the final column leaves the cursor in the terminal's pending-wrap
// state at the right margin. The newline that follows then costs an extra row,
// so the frame occupies more rows than the renderer believes and its cursor
// arithmetic no longer matches the screen — rows are left behind, which reads
// as code sticking on screen while the view scrolls underneath it. Never
// writing to the last column avoids the state entirely.
func usableWidth(width int) int {
	if width <= 1 {
		return width
	}
	return width - 1
}

// footer puts the keybindings on the left and the clock, the age of the open
// session and the version on the right.
//
// The age is the point: watching a session that is still being written, it is
// the difference between "nothing is happening" and "nothing has happened for
// four minutes".
func (m Model) footer(help string, keys []helpKey) string {
	// The status gets at most a third of the row. On an 80-column terminal the
	// full version of it is 36 columns, which was enough to push real
	// keybindings off the footer — the keys are what the reader acts on.
	right := mutedStyle.Render(m.footerStatus(usableWidth(m.width) / 3))

	// Build the keybindings to fit what is left after the status. The sessions
	// list has a dozen of them, which is wider than an 80- or 100-column
	// terminal on its own; dropping the last few is better than wrapping onto
	// a second row, which pushes the footer off the screen.
	if help == "" {
		help = renderHelpFit(keys, usableWidth(m.width)-visibleLen(right))
	}

	// Right-align by padding between the two. visibleLen is required because
	// both sides carry colour escapes, and a line wider than the terminal
	// wraps and pushes the footer off the screen.
	// One short of the terminal, so the footer never fills the last column.
	w := usableWidth(m.width)
	gap := w - visibleLen(help) - visibleLen(right)
	if gap < 1 {
		// No room for both: the keys matter more than the clock.
		if visibleLen(help) <= w {
			return help
		}
		return truncateVisible(help, w)
	}
	return help + strings.Repeat(" ", gap) + right
}

// footerStatus is the right-hand text: how long ago the open session was
// written, the time now, and the version — as much of it as fits in budget.
//
// Dropped least-useful first. The age of the open session is the one that earns
// its place, because it is what tells a live session apart from one that stopped
// four minutes ago; the version is the one nobody reads twice.
func (m Model) footerStatus(budget int) string {
	var parts []string
	if sess := m.selectedSession; sess != nil && !sess.Info.LastUpdate.IsZero() {
		parts = append(parts, "updated "+agoShort(time.Since(sess.Info.LastUpdate)))
	}
	parts = append(parts, time.Now().Format("15:04:05"), "v"+m.version)

	for n := len(parts); n > 0; n-- {
		out := strings.Join(parts[:n], " · ") + " "
		if len(out) <= budget {
			return out
		}
	}
	return ""
}

// agoShort renders an elapsed duration in as few characters as possible, for a
// footer that has to share a row with the keybindings.
func agoShort(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
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

	// So does the tree: arrows move a cursor and open directories rather than
	// scrolling a page.
	if m.mode == viewTree {
		return m.handleTreeKey(msg, key)
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

	case "T":
		// The project tree, marked with what this session did to it.
		if sess := m.sessionInContext(); sess != nil {
			m.openTree(sess)
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

// renderHelpFit renders as many keybindings as fit in width, dropping from the
// end and marking the omission. Keys are listed most useful first, so the ones
// that go are the ones least missed.
func renderHelpFit(keys []helpKey, width int) string {
	if len(keys) == 0 {
		return ""
	}
	full := renderHelp(keys)
	if width <= 0 || visibleLen(full) <= width {
		return full
	}
	// The last binding is always kept. It is "quit" in every view, and a footer
	// that has dropped the way out is worse than one that has dropped anything
	// else.
	last := keys[len(keys)-1]
	for n := len(keys) - 1; n > 0; n-- {
		candidate := renderHelp(keys[:n]) + mutedStyle.Render(" … ") + renderHelp([]helpKey{last})
		if visibleLen(candidate) <= width {
			return candidate
		}
	}
	if only := renderHelp([]helpKey{last}); visibleLen(only) <= width {
		return only
	}
	return truncateVisible(full, width)
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
