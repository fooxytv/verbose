package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// The project tree, as a session changed it.
//
// The structure comes from disk, so it is the project as it is now; the colours
// come from the transcript, which is exact however old the session is. The two
// are different kinds of truth and the header says so, because a tree from a
// session three weeks old is today's shape with three-week-old marks on it.
//
// Which marks are shown depends on where the replay has reached, so scrubbing a
// replay walks the project forward: files appear as they are written, and the
// one being written right now is lit.

// How long a change stays lit after the replay reaches it, and how many steps
// that ramp has. Short enough not to blur one step into the next at the default
// two seconds a step.
const (
	treeFadeDuration = 900 * time.Millisecond
	treeFadeStages   = 3
	// treeTickInterval redraws while something is still fading.
	treeTickInterval = 110 * time.Millisecond
)

// treeTickMsg redraws the tree while a change is still fading.
type treeTickMsg struct{ gen int }

func treeTickCmd(gen int) tea.Cmd {
	return tea.Tick(treeTickInterval, func(time.Time) tea.Msg {
		return treeTickMsg{gen: gen}
	})
}

// treeRow is one rendered line of the tree.
type treeRow struct {
	node  *session.TreeNode
	depth int
	// stem is the box-drawing prefix, built from whether each ancestor was the
	// last of its siblings.
	stem string
	// collapsed is true for a directory whose children are hidden.
	collapsed bool
}

// treeView is everything the tree renderer needs.
type treeView struct {
	sess     *session.Session
	root     *session.TreeNode
	activity map[string]*session.FileActivity

	// upto is the event index the tree is drawn as of: everything the session
	// had done by then is shown, and nothing after.
	upto int
	// justNow is the event index of the step being shown, whose files are lit.
	justNow int
	// fade is how far through the highlight ramp that step is, 0 newest.
	fade int

	rows      []treeRow
	cursor    int
	scroll    int
	changedOn bool // showing only files the session changed

	width, height int
}

// flattenTree turns the tree into the rows to draw, skipping the contents of
// collapsed directories and, when asked, everything the session did not change.
func flattenTree(root *session.TreeNode, collapsed map[string]bool,
	activity map[string]*session.FileActivity, upto int, changedOnly bool) []treeRow {

	if root == nil {
		return nil
	}
	var rows []treeRow

	var walk func(n *session.TreeNode, depth int, stem string, last bool)
	walk = func(n *session.TreeNode, depth int, stem string, last bool) {
		if depth > 0 {
			branch := "├─ "
			if last {
				branch = "└─ "
			}
			rows = append(rows, treeRow{
				node:      n,
				depth:     depth,
				stem:      stem + branch,
				collapsed: n.IsDir && collapsed[n.Path],
			})
		} else {
			rows = append(rows, treeRow{node: n, depth: 0, collapsed: collapsed[n.Path]})
		}

		if n.IsDir && collapsed[n.Path] {
			return
		}

		// Keep only children worth showing, so the last-child branch is drawn
		// against what is actually rendered.
		var shown []*session.TreeNode
		for _, c := range n.Children {
			if keepInTree(c, activity, upto, changedOnly) {
				shown = append(shown, c)
			}
		}
		childStem := stem
		if depth > 0 {
			if last {
				childStem += "   "
			} else {
				childStem += "│  "
			}
		}
		for i, c := range shown {
			walk(c, depth+1, childStem, i == len(shown)-1)
		}
	}

	walk(root, 0, "", true)
	return rows
}

// keepInTree decides whether a node earns a row.
//
// With changedOnly, a directory survives if anything beneath it changed —
// otherwise collapsing a project to its changes would lose the paths that lead
// to them.
func keepInTree(n *session.TreeNode, activity map[string]*session.FileActivity,
	upto int, changedOnly bool) bool {

	if !changedOnly {
		return true
	}
	if !n.IsDir {
		a := activity[n.Path]
		if a == nil {
			return false
		}
		kind, _, _, touched := a.StateAt(upto)
		return touched && kind.Changed()
	}
	for _, c := range n.Children {
		if keepInTree(c, activity, upto, changedOnly) {
			return true
		}
	}
	return false
}

// renderTree draws the tree.
func renderTree(v treeView) string {
	var b strings.Builder

	title := titleStyle.Render("Tree")
	if v.changedOn {
		title += "  " + agentStyle.Render("changed only")
	}
	b.WriteString(title + "  " +
		dimStyle.Render(truncate(session.ShortPath(v.sess.Info.CWD, ""), max(10, v.width-40))) + "\n")
	b.WriteString(treeLegend(v) + "\n\n")

	if len(v.rows) == 0 {
		msg := "  Nothing to show."
		if v.changedOn {
			msg = "  This session changed no files yet."
		}
		b.WriteString(dimStyle.Render(msg) + "\n\n" +
			mutedStyle.Render("  tab shows every file in the project."))
		return b.String()
	}

	// Chrome above and below the rows: the title, the legend, a blank line and
	// the "… N more" note, plus the footer the frame reserves.
	visible := max(1, v.height-5)
	scroll := clampInt(v.scroll, 0, max(0, len(v.rows)-visible))
	end := min(len(v.rows), scroll+visible)

	for i := scroll; i < end; i++ {
		b.WriteString(renderTreeRow(v, i, v.rows[i]) + "\n")
	}
	if end < len(v.rows) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … %d more", len(v.rows)-end)))
	}
	return b.String()
}

// treeLegend says what the tree is showing and, importantly, that its structure
// is from disk now while its marks are from the session.
func treeLegend(v treeView) string {
	changed, created, deleted := 0, 0, 0
	for _, a := range v.activity {
		kind, _, _, touched := a.StateAt(v.upto)
		if !touched {
			continue
		}
		switch kind {
		case session.TouchCreate:
			created++
		case session.TouchEdit, session.TouchWrite:
			changed++
		case session.TouchDelete:
			deleted++
		}
	}

	parts := []string{
		createdStyle.Render(fmt.Sprintf("%d new", created)),
		changedStyle.Render(fmt.Sprintf("%d changed", changed)),
	}
	if deleted > 0 {
		parts = append(parts, deletedStyle.Render(fmt.Sprintf("%d removed", deleted)))
	}
	return "  " + strings.Join(parts, mutedStyle.Render(" · ")) +
		mutedStyle.Render("   structure from disk now · marks from the session")
}

// renderTreeRow draws one row: the box-drawing stem, the name coloured by what
// the session did to it, and its churn.
func renderTreeRow(v treeView, i int, row treeRow) string {
	selected := i == v.cursor
	bg := func(s lipgloss.Style) lipgloss.Style {
		if selected {
			return s.Copy().Background(colorBgSelected)
		}
		return s
	}

	name := row.node.Name
	if row.node.IsDir {
		if row.collapsed {
			name = "▸ " + name + "/"
		} else {
			name = "▾ " + name + "/"
		}
	}

	style, suffix := treeRowStyle(v, row)

	line := bg(mutedStyle).Render("  "+row.stem) + bg(style).Render(name)
	if suffix != "" {
		line += " " + bg(mutedStyle).Render(suffix)
	}

	// Pad the selection across the row so it reads as one band.
	if selected {
		if pad := usableWidth(v.width) - visibleLen(line); pad > 0 {
			line += lipgloss.NewStyle().Background(colorBgSelected).
				Render(strings.Repeat(" ", pad))
		}
	}
	return line
}

// treeRowStyle picks the colour for a row and the churn text beside it.
func treeRowStyle(v treeView, row treeRow) (lipgloss.Style, string) {
	if row.node.IsDir {
		if row.node.Missing {
			return mutedStyle, "(gone)"
		}
		return dirStyle, ""
	}

	a := v.activity[row.node.Path]
	if a == nil {
		// Present in the project, untouched by this session.
		if row.node.Missing {
			return mutedStyle, "(gone)"
		}
		return mutedStyle, ""
	}

	kind, added, removed, touched := a.StateAt(v.upto)
	if !touched {
		return mutedStyle, ""
	}

	churn := ""
	if added > 0 || removed > 0 {
		churn = fmt.Sprintf("+%d -%d", added, removed)
	}
	if row.node.Missing && kind != session.TouchDelete {
		churn = strings.TrimSpace(churn + " (gone)")
	}

	// A file touched by the step on screen is lit, then settles over a few
	// frames — the fade that makes a change announce itself while a replay
	// plays without staying shouty afterwards.
	if _, now := a.TouchedAt(v.justNow); now && v.fade < treeFadeStages {
		return fadeStyle(kind, v.fade), churn
	}

	switch kind {
	case session.TouchCreate:
		return createdStyle, strings.TrimSpace(churn + " new")
	case session.TouchEdit, session.TouchWrite:
		return changedStyle, churn
	case session.TouchDelete:
		return deletedStyle, strings.TrimSpace(churn + " removed")
	}
	return readStyle, churn
}

// fadeStyle is the highlight ramp a just-changed file passes through: a filled
// band, then bold, then its resting colour.
func fadeStyle(kind session.TouchKind, stage int) lipgloss.Style {
	base := changedStyle
	switch kind {
	case session.TouchCreate:
		base = createdStyle
	case session.TouchDelete:
		base = deletedStyle
	}
	switch stage {
	case 0:
		return base.Copy().Reverse(true).Bold(true)
	case 1:
		return base.Copy().Bold(true).Underline(true)
	}
	return base.Copy().Bold(true)
}

// treeFadeStage maps elapsed time since the replay reached a step onto the
// highlight ramp. Past the end of the ramp it returns treeFadeStages, which
// means "settled".
func treeFadeStage(since time.Duration) int {
	if since < 0 {
		return 0
	}
	per := treeFadeDuration / treeFadeStages
	stage := int(since / per)
	if stage > treeFadeStages {
		return treeFadeStages
	}
	return stage
}

// openTree reads the project directory and opens the tree for a session.
//
// The scan happens once, here, rather than on every frame: a project can have
// thousands of entries and the view redraws many times a second while a replay
// plays.
func (m *Model) openTree(sess *session.Session) {
	m.selectedSession = sess
	m.treeActivity = session.BuildFileActivity(sess)

	root := sess.Info.CWD
	if root == "" {
		root = sess.Info.ProjectDir
	}
	m.treeRoot = session.ScanTree(root, m.treeActivity)
	if m.treeCollapsed == nil {
		m.treeCollapsed = make(map[string]bool)
	}
	m.treeCursor = 0
	m.treeScroll = 0
	m.treeGen++
	m.mode = viewTree

	if m.replaySteps == nil {
		// Opened without a replay: show everything the session ended up doing.
		m.replaySteps = session.BuildReplay(sess)
		m.replayIndex = max(0, len(m.replaySteps)-1)
		m.replayStepAt = time.Now()
	}
	if m.treeRoot == nil {
		m.statusMsg = "Could not read " + root
	}
}

// treeViewState assembles what the renderer needs, including the rows, which
// depend on the replay position and the filter.
func (m Model) treeViewState() treeView {
	upto := m.treeUpto()
	rows := flattenTree(m.treeRoot, m.treeCollapsed, m.treeActivity, upto, m.treeChangedOnly)

	return treeView{
		sess:      m.selectedSession,
		root:      m.treeRoot,
		activity:  m.treeActivity,
		upto:      upto,
		justNow:   upto,
		fade:      treeFadeStage(time.Since(m.replayStepAt)),
		rows:      rows,
		cursor:    clampInt(m.treeCursor, 0, max(0, len(rows)-1)),
		scroll:    m.treeScroll,
		changedOn: m.treeChangedOnly,
		width:     m.width,
		height:    m.height,
	}
}

// treeUpto is the event the tree is drawn as of: where the replay has reached,
// or the end of the session when there is no replay.
func (m Model) treeUpto() int {
	if m.selectedSession == nil {
		return 0
	}
	if m.replayIndex < len(m.replaySteps) {
		return m.replaySteps[m.replayIndex].EventIndex
	}
	return len(m.selectedSession.Events)
}

// replayVisible reports whether playback has somewhere to show itself. The tree
// counts, because watching the project change is the point of driving it from a
// replay.
func (m Model) replayVisible() bool {
	return m.mode == viewReplay || m.mode == viewTree
}

// treeRows is the current row list, for navigation.
func (m Model) treeRows() []treeRow {
	return flattenTree(m.treeRoot, m.treeCollapsed, m.treeActivity, m.treeUpto(), m.treeChangedOnly)
}

// handleTreeKey consumes keys while the tree is open.
func (m Model) handleTreeKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	rows := m.treeRows()
	m.treeCursor = clampInt(m.treeCursor, 0, max(0, len(rows)-1))

	// Space plays and pauses the replay that drives the tree, so the project
	// can be watched changing without leaving this view.
	if msg.Type == tea.KeySpace {
		m.replayPlaying = !m.replayPlaying
		m.replayGen++
		m.treeGen++
		if !m.replayPlaying {
			m.replayTyped = -1
			return m, nil
		}
		if m.replayIndex >= len(m.replaySteps)-1 {
			m.replayIndex = 0
		}
		m.replayTyped = 0
		m.replayStepAt = time.Now()
		return m, tea.Batch(m.replayAdvanceCmd(), treeTickCmd(m.treeGen))
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc", "backspace":
		m.replayPlaying = false
		m.replayGen++
		m.mode = viewSessions
		if m.selectedSession != nil {
			m.mode = viewDetail
		}
		return m, nil

	case "down", "j":
		if m.treeCursor < len(rows)-1 {
			m.treeCursor++
		}
		m.treeFollowCursor(len(rows))
		return m, nil

	case "up", "k":
		if m.treeCursor > 0 {
			m.treeCursor--
		}
		m.treeFollowCursor(len(rows))
		return m, nil

	case "right", "l":
		// Open a directory, or step into the file's last change.
		if m.treeCursor < len(rows) {
			n := rows[m.treeCursor].node
			if n.IsDir {
				delete(m.treeCollapsed, n.Path)
			}
		}
		return m, nil

	case "left", "h":
		if m.treeCursor < len(rows) {
			n := rows[m.treeCursor].node
			if n.IsDir && !m.treeCollapsed[n.Path] {
				m.treeCollapsed[n.Path] = true
				return m, nil
			}
			// Already closed, or a file: go to the parent, which is where a
			// reader expects left to take them.
			for i := m.treeCursor - 1; i >= 0; i-- {
				if rows[i].depth < rows[m.treeCursor].depth {
					m.treeCursor = i
					m.treeFollowCursor(len(rows))
					break
				}
			}
		}
		return m, nil

	case "enter":
		if m.treeCursor < len(rows) {
			n := rows[m.treeCursor].node
			if n.IsDir {
				m.treeCollapsed[n.Path] = !m.treeCollapsed[n.Path]
				return m, nil
			}
			return m.openFileInReplay(n.Path)
		}
		return m, nil

	case "tab":
		m.treeChangedOnly = !m.treeChangedOnly
		m.treeCursor = 0
		m.treeScroll = 0
		return m, nil

	case "R":
		// Back to the replay, at the same point.
		m.mode = viewReplay
		m.replayGen++
		return m, m.replayAdvanceCmd()

	case "g", "home":
		m.treeCursor, m.treeScroll = 0, 0
		return m, nil

	case "G", "end":
		m.treeCursor = max(0, len(rows)-1)
		m.treeFollowCursor(len(rows))
		return m, nil

	case "shift+down", "pgdown":
		m.treeCursor = clampInt(m.treeCursor+m.pageSize(), 0, max(0, len(rows)-1))
		m.treeFollowCursor(len(rows))
		return m, nil

	case "shift+up", "pgup":
		m.treeCursor = clampInt(m.treeCursor-m.pageSize(), 0, max(0, len(rows)-1))
		m.treeFollowCursor(len(rows))
		return m, nil
	}

	return m, nil
}

// treeFollowCursor keeps the selected row on screen.
func (m *Model) treeFollowCursor(total int) {
	visible := max(1, m.height-5)
	if m.treeCursor < m.treeScroll {
		m.treeScroll = m.treeCursor
	}
	if m.treeCursor >= m.treeScroll+visible {
		m.treeScroll = m.treeCursor - visible + 1
	}
	m.treeScroll = clampInt(m.treeScroll, 0, max(0, total-visible))
}

// openFileInReplay moves the replay to the last thing this session did to a
// file and shows it, which is how a tree row becomes a diff.
func (m Model) openFileInReplay(path string) (tea.Model, tea.Cmd) {
	a := m.treeActivity[path]
	if a == nil || len(a.Touches) == 0 {
		m.statusMsg = "This session did not touch " + session.ShortPath(path, m.selectedSession.Info.CWD)
		return m, clearStatusAfter()
	}

	// The last touch at or before where the replay has reached, so opening a
	// file never jumps ahead of what is being watched.
	upto := m.treeUpto()
	target := a.Touches[0].EventIndex
	for _, t := range a.Touches {
		if t.EventIndex <= upto {
			target = t.EventIndex
		}
	}

	for i, st := range m.replaySteps {
		if st.EventIndex == target {
			m.replayIndex = i
			m.replayScroll = 0
			m.replayPlaying = false
			m.replayTyped = -1
			m.replayGen++
			m.mode = viewReplay
			return m, nil
		}
	}
	m.statusMsg = "No replay step for that change"
	return m, clearStatusAfter()
}
