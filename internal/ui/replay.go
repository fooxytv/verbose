package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// Replay shows one step of a session at a time, at reading speed.
//
// The timeline is built for scanning a whole session; this is built for
// following one. It gives a step the whole screen — what was done, the real
// diff or command output, and room for an explanation — so that work which
// originally scrolled past in seconds can be read.

// Playback speed. Two seconds is long enough to read a headline and take in a
// small diff; the bounds stop + and - from reaching uselessly fast or slow.
const (
	replayDefaultDelay = 2 * time.Second
	replayMinDelay     = 500 * time.Millisecond
	replayMaxDelay     = 15 * time.Second
	replayDelayStep    = 500 * time.Millisecond

	// Typing cadence. One tick every replayTypeInterval reveals
	// replayTypeChars characters, so the default is about 100 a second —
	// roughly a fast typist, and slow enough to read a diff as it lands.
	replayTypeInterval = 30 * time.Millisecond
	replayTypeChars    = 3

	// A session written to this recently is treated as still running, so a
	// caught-up replay waits for more instead of declaring the end.
	replayLiveWindow = 2 * time.Minute
)

// replayTickMsg advances playback. gen guards against ticks scheduled before a
// pause or a manual step: a stale tick would double-advance the step.
type replayTickMsg struct{ gen int }

// replayTypeMsg reveals the next few characters of the current step.
type replayTypeMsg struct{ gen int }

// replayTickCmd schedules the next automatic advance.
func replayTickCmd(d time.Duration, gen int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return replayTickMsg{gen: gen}
	})
}

// replayTypeCmd schedules the next few characters.
func replayTypeCmd(gen int) tea.Cmd {
	return tea.Tick(replayTypeInterval, func(time.Time) tea.Msg {
		return replayTypeMsg{gen: gen}
	})
}

// renderReplay draws the current step full-screen.
// replayView is everything the replay renderer needs. It is a struct because
// the alternative is a ten-argument function whose call sites are unreadable.
type replayView struct {
	sess  *session.Session
	steps []session.ReplayStep
	idx   int

	scroll int
	// typed is how many characters of the step's code are visible. -1 shows
	// all of it, which is what a paused replay wants.
	typed int

	playing bool
	// waiting means playback has caught up with a session that is still being
	// written, and is holding for more rather than finishing.
	waiting bool

	// codeOnly reports that the step list has been narrowed to the steps that
	// wrote code, which is what the header and the empty state have to say.
	codeOnly bool

	delay         time.Duration
	width, height int
}

func renderReplay(v replayView) string {
	sess, steps, idx := v.sess, v.steps, v.idx
	scroll, typed := v.scroll, v.typed
	width, height := v.width, v.height

	if len(steps) == 0 {
		if v.codeOnly {
			return "\n" + titleStyle.Render("Replay") + "  " +
				agentStyle.Render("code only") + "\n\n" +
				dimStyle.Render("  No step in this session wrote code.") + "\n\n" +
				mutedStyle.Render("  Nothing here produced a diff, a new file or a heredoc.\n"+
					"  Press tab to see every step.")
		}
		return "\n" + titleStyle.Render("Replay") + "\n\n" +
			dimStyle.Render("  This session has no steps to replay.") + "\n\n" +
			mutedStyle.Render("  Replay keeps prompts, plans and operations. A session of only\n"+
				"  system bookkeeping has nothing to show.")
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(steps) {
		idx = len(steps) - 1
	}

	step := steps[idx]
	var b strings.Builder

	// Header: which session, where we are in it, and whether it is running.
	header := titleStyle.Render("Replay")
	if v.codeOnly {
		header += "  " + agentStyle.Render("code only")
	}
	b.WriteString(clampWidth(header+"  "+
		dimStyle.Render(truncate(sess.Info.Title, max(10, width-46))), width) + "\n")

	b.WriteString(clampWidth(replayProgress(v, idx, step, width), width) + "\n\n")

	// The step itself.
	b.WriteString(clampWidth(replayHeadline(step, sess, width), width) + "\n\n")

	body := replayBody(step, sess, width, typed)

	// Rows this frame spends on everything that is not the body: the header,
	// the progress bar, the headline, their blank lines, and the "more lines"
	// note. The waiting notice costs three more when it is shown.
	reserved := 9
	if v.waiting {
		reserved += 3
	}
	visible := max(1, height-reserved)

	// While typing, the viewport follows the cursor. Without this the window
	// stays at the top of the diff and the code arrives off-screen, which is
	// the one thing this view exists to prevent.
	if typed >= 0 {
		scroll = max(0, len(body)-visible)
	}
	if scroll > max(0, len(body)-visible) {
		scroll = max(0, len(body)-visible)
	}
	if scroll < 0 {
		scroll = 0
	}
	end := min(len(body), scroll+visible)
	for _, line := range body[scroll:end] {
		b.WriteString(clampWidth(line, width) + "\n")
	}
	if end < len(body) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … %d more lines (↑/↓ to scroll)",
			len(body)-end)) + "\n")
	}

	// Caught up on a session that is still going: say so, rather than looking
	// like playback has frozen.
	if v.waiting {
		b.WriteString("\n  " + agentStyle.Render("◴ caught up — waiting for the session") +
			"\n  " + mutedStyle.Render("new steps play as they are written"))
	}

	return clampHeight(b.String(), height-1)
}

// clampHeight drops any rows beyond what the view was given.
//
// padToHeight would trim them anyway when the frame is placed on screen; doing
// it here keeps the renderer honest about its own budget, so a frame can never
// claim more rows than it is allowed — including at terminal sizes too small
// for everything to fit.
func clampHeight(content string, rows int) string {
	if rows <= 0 {
		return content
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= rows {
		return content
	}
	return strings.Join(lines[:rows], "\n")
}

// replayProgress is the position bar: a filled track, the step count, and the
// playback state.
func replayProgress(v replayView, idx int, step session.ReplayStep, width int) string {
	total := len(v.steps)
	typing := v.playing && v.typed >= 0 && v.typed < TypedLength(step, v.sess)

	state := mutedStyle.Render("paused")
	switch {
	case v.waiting:
		state = agentStyle.Render("◴ live")
	case typing:
		state = statusStyle.Render("▶ typing")
	case v.playing:
		state = statusStyle.Render("▶ playing")
	}

	counter := fmt.Sprintf("step %d/%d", idx+1, total)
	speed := fmt.Sprintf("%.1fs/step", v.delay.Seconds())

	// How far behind the live session this replay is. Being behind is the point
	// — the replay reads at human speed while the agent writes at its own — so
	// the gap is reported rather than hidden.
	lag := ""
	if n := total - 1 - idx; n > 0 && v.playing {
		lag = fmt.Sprintf("  %s", agentStyle.Render(fmt.Sprintf("%d behind", n)))
	}

	// Reserve room for the text either side of the bar.
	barWidth := width - len(counter) - len(speed) - visibleLen(lag) - 24
	if barWidth < 8 {
		return "  " + dimStyle.Render(counter) + lag + "  " + state
	}

	filled := 0
	if total > 1 {
		filled = idx * barWidth / (total - 1)
	}
	bar := searchStyle.Render(strings.Repeat("━", filled)) +
		mutedStyle.Render(strings.Repeat("─", barWidth-filled))

	return "  " + dimStyle.Render(counter) + "  " + bar + "  " +
		mutedStyle.Render(speed) + lag + "  " + state
}

// replayHeadline is the step's kind badge and title.
func replayHeadline(step session.ReplayStep, sess *session.Session, width int) string {
	badge, body := replayKindStyles(step.Kind)

	line := "  " + badge.Render(" "+strings.ToUpper(step.Kind.String())+" ") + " " +
		body.Render(truncate(step.Title, max(10, width-24)))

	if step.IsSidechain {
		line += "  " + agentStyle.Render("│ subagent")
	}

	// When this step happened, relative to the session start, so the pacing of
	// the original work is still visible even though playback is uniform.
	if step.EventIndex < len(sess.Events) {
		e := sess.Events[step.EventIndex]
		if !e.Timestamp.IsZero() && !sess.Info.StartTime.IsZero() {
			offset := e.Timestamp.Sub(sess.Info.StartTime)
			line += "  " + mutedStyle.Render("+"+formatOffset(offset))
		}
	}
	return line
}

// replayBody is everything below the headline: the plain-English account, the
// explanation if one exists, and the raw evidence from the transcript.
func replayBody(step session.ReplayStep, sess *session.Session, width, typed int) []string {
	var lines []string

	if step.Detail != "" {
		lines = append(lines, wrapProse(step.Detail, width-6, "  ")...)
		lines = append(lines, "")
	}

	// Layer 2 fills this in. Until then the absence is stated rather than
	// papered over, because the reasoning genuinely is not in the transcript.
	if step.Explanation != "" {
		lines = append(lines, "  "+headerLabelStyle.Render("Why")+"\n")
		lines = append(lines, wrapProse(step.Explanation, width-6, "  ")...)
		lines = append(lines, "")
	}

	if step.EventIndex >= len(sess.Events) {
		return lines
	}
	e := sess.Events[step.EventIndex]

	// The evidence: the actual diff, the actual output. renderToolResult
	// already knows how to present every shape of result.
	if e.Type == session.EventToolUse || e.Type == session.EventToolResult {
		// The command and the diff are "typed": revealed a character at a time
		// so the code can be followed as it lands, rather than appearing whole.
		rv := newReveal(typed)

		// Code that no diff recorded: a new file's contents, or a heredoc body.
		// This is the case for most of what an agent actually writes, so it
		// leads, and the command that carried it is reduced to a subtitle.
		if step.Code != "" {
			label := "Code written"
			if step.CodePath != "" {
				label = "Writing " + step.CodePath
			}
			lines = append(lines, "  "+headerLabelStyle.Render(label))
			if cmd := commandOf(e); cmd != "" {
				lines = append(lines, "  "+mutedStyle.Render(
					truncateRunes(firstLine(cmd), width-6)))
			}
			lines = append(lines, "")
			// Highlighted lines have the same visible length as the plain ones,
			// so the typing budget is unaffected by colouring.
			for _, l := range highlightCode(step.Code, step.CodePath) {
				shown, ok := rv.ansiLine(l)
				if !ok {
					break
				}
				// Expand before truncating, or the cut is made against a width
				// the terminal does not agree with.
				lines = append(lines, "  "+truncateVisible(expandTabs(shown), width-6))
			}
			lines = append(lines, "")
			if !rv.exhausted() {
				lines = append(lines, renderStepOutput(e, width)...)
			}
			return lines
		}

		if cmd := commandOf(e); cmd != "" {
			lines = append(lines, "  "+headerLabelStyle.Render("Command"))
			lines = append(lines, "")
			for _, l := range strings.Split(strings.TrimRight(cmd, "\n"), "\n") {
				shown, ok := rv.line(l)
				if !ok {
					break
				}
				lines = append(lines, "  "+toolUseStyle.Render(truncateRunes(shown, width-6)))
			}
			lines = append(lines, "")
		}

		if hunks := patchOf(e); len(hunks) > 0 {
			lines = append(lines, "  "+headerLabelStyle.Render("The change")+" "+
				mutedStyle.Render(changeSummary(e, sess.Info.CWD)))
			lines = append(lines, "")
			lines = append(lines, renderPatch(revealHunks(hunks, rv), width)...)
			// Output is never typed out: a diff is worth following keystroke by
			// keystroke, nine kilobytes of log output is not.
			if !rv.exhausted() {
				lines = append(lines, renderStepOutput(e, width)...)
			}
			return lines
		}
		if !rv.exhausted() {
			lines = append(lines, renderStepOutput(e, width)...)
		}
	}

	return lines
}

// renderStepOutput shows what an operation produced, when there is anything
// worth showing.
//
// A dispatch returns only launch plumbing — an agent id, a transcript path,
// instructions aimed at the model. The work itself arrives as the subagent's
// own steps, so showing that payload here is pure noise.
func renderStepOutput(e session.Event, width int) []string {
	if isDispatch(e) || (e.Result == nil && e.ToolOutput == "") {
		return nil
	}
	out := []string{"  " + headerLabelStyle.Render("What it produced"), ""}
	return append(out, renderToolResult(e, width)...)
}

// commandOf is the shell command an event ran, if any.
func commandOf(e session.Event) string {
	cmd, _ := e.ToolInput["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	return cmd
}

// patchOf is the diff an event actually recorded. The recorded patch is used
// rather than the requested edit: they differ when the file moved underfoot.
func patchOf(e session.Event) []session.PatchHunk {
	if e.Result == nil {
		return nil
	}
	return e.Result.StructuredPatch
}

// changeSummary is the churn headline that sits beside a diff. The path is
// shortened against the session's directory: a full home-directory path is
// wider than most terminals on its own.
func changeSummary(e session.Event, cwd string) string {
	if e.Result == nil {
		return ""
	}
	added, removed := e.Result.Churn()
	return fmt.Sprintf("+%d -%d in %s", added, removed,
		session.ShortPath(e.Result.FilePath, cwd))
}

// TypedLength is how many characters of a step get typed out. It is the budget
// the reveal counts down, and the renderer counts the same lines in the same
// order, so the two always agree.
func TypedLength(step session.ReplayStep, sess *session.Session) int {
	if step.EventIndex >= len(sess.Events) {
		return 0
	}
	e := sess.Events[step.EventIndex]
	if e.Type != session.EventToolUse && e.Type != session.EventToolResult {
		return 0
	}

	n := 0
	// Recovered code is what gets typed when there is any: it is the thing
	// worth watching, and the command that carried it is just a wrapper.
	if step.Code != "" {
		for _, l := range strings.Split(step.Code, "\n") {
			n += len([]rune(l))
		}
		return n
	}
	if cmd := commandOf(e); cmd != "" {
		for _, l := range strings.Split(strings.TrimRight(cmd, "\n"), "\n") {
			n += len([]rune(l))
		}
	}
	for _, h := range patchOf(e) {
		for _, l := range h.Lines {
			n += len([]rune(l))
		}
	}
	return n
}

// reveal is the typing budget for one rendered step: how many characters may
// still be drawn before the cursor is reached.
type reveal struct {
	left      int
	unlimited bool
	stopped   bool
}

// newReveal makes a budget of n characters. A negative n means no limit, which
// is how a paused step shows its whole diff.
func newReveal(n int) *reveal {
	if n < 0 {
		return &reveal{unlimited: true}
	}
	if n < 0 {
		n = 0
	}
	return &reveal{left: n}
}

// exhausted reports whether the cursor has been drawn and nothing further
// should appear.
func (r *reveal) exhausted() bool { return !r.unlimited && r.stopped }

// line returns how much of one line may be drawn. ok is false once the budget
// has run out, which is the caller's signal to stop emitting lines.
func (r *reveal) line(s string) (string, bool) {
	if r.unlimited {
		return s, true
	}
	if r.stopped {
		return "", false
	}
	runes := []rune(s)
	if r.left >= len(runes) {
		r.left -= len(runes)
		return s, true
	}
	// The cursor marks where typing has reached, mid-line.
	out := string(runes[:r.left]) + "▌"
	r.left = 0
	r.stopped = true
	return out, true
}

// ansiLine is line() for text that already carries colour escapes. The budget
// counts visible characters, so an escape sequence must not consume any of it
// and must never be cut through the middle.
func (r *reveal) ansiLine(s string) (string, bool) {
	if r.unlimited {
		return s, true
	}
	if r.stopped {
		return "", false
	}
	n := visibleLen(s)
	if r.left >= n {
		r.left -= n
		return s, true
	}
	out := truncateVisible(s, r.left) + "▌"
	r.left = 0
	r.stopped = true
	return out, true
}

// revealHunks trims a diff to the part that has been typed so far, dropping
// whole hunks that have not been reached.
func revealHunks(hunks []session.PatchHunk, rv *reveal) []session.PatchHunk {
	var out []session.PatchHunk
	for _, h := range hunks {
		if rv.exhausted() {
			break
		}
		shown := h
		shown.Lines = nil
		for _, l := range h.Lines {
			text, ok := rv.line(l)
			if !ok {
				break
			}
			shown.Lines = append(shown.Lines, text)
		}
		out = append(out, shown)
	}
	return out
}

// isDispatch reports whether an event launched a subagent.
func isDispatch(e session.Event) bool {
	return e.ToolName == "Task" || e.ToolName == "Agent"
}

// replayKindStyles colours a step by its role: goals green, plans purple,
// actions blue, problems red.
func replayKindStyles(k session.ReplayKind) (badge, body lipgloss.Style) {
	switch k {
	case session.StepGoal:
		return userStyle.Copy().Reverse(true), userStyle
	case session.StepPlan:
		return thinkingStyle.Copy().Reverse(true).Bold(true), thinkingStyle
	case session.StepProblem:
		return deletePromptStyle, toolErrorStyle
	case session.StepNote:
		return systemStyle.Copy().Reverse(true).Bold(true), systemStyle
	}
	return toolUseStyle.Copy().Reverse(true), toolUseStyle
}

// clampWidth is the last line of defence against a row wider than the terminal.
//
// A line that overflows is wrapped by the terminal into two rows, so the view
// occupies more rows than padToHeight counted — the footer is pushed off the
// bottom and the whole frame appears duplicated as the terminal scrolls. Every
// producer here is supposed to fit already; this makes a mistake in one of them
// a clipped line rather than a broken screen.
func clampWidth(line string, width int) string {
	if width <= 0 || visibleLen(line) <= width {
		return line
	}
	return truncateVisible(line, width)
}

// wrapProse word-wraps a paragraph to the given width.
//
// wrapLines truncates each line instead, which is right for command output —
// one transcript line per screen line — but wrong here: a replay exists to be
// read, and a prompt cut off mid-word is the one thing it must not do.
func wrapProse(s string, maxWidth int, prefix string) []string {
	if maxWidth <= 0 {
		maxWidth = 76
	}

	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}

		line := ""
		flush := func() {
			if line != "" {
				out = append(out, prefix+line)
				line = ""
			}
		}
		for _, w := range words {
			// A single word wider than the column (a URL, a long path) is
			// broken rather than allowed to run off the edge.
			for len([]rune(w)) > maxWidth {
				flush()
				r := []rune(w)
				out = append(out, prefix+string(r[:maxWidth]))
				w = string(r[maxWidth:])
			}
			switch {
			case line == "":
				line = w
			case len([]rune(line))+1+len([]rune(w)) <= maxWidth:
				line += " " + w
			default:
				flush()
				line = w
			}
		}
		flush()
	}
	return out
}

// formatOffset renders a duration since the session started, compactly.
func formatOffset(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// startReplay builds the step list for a session and opens the replay view.
// Playback starts paused: dropping straight into a moving view gives you no
// chance to read the first step.
func (m *Model) startReplay(sess *session.Session) {
	m.selectedSession = sess
	m.replaySteps = session.BuildReplay(sess)
	if m.replayCodeOnly {
		m.replaySteps = codeSteps(m.replaySteps, sess)
	}
	m.replayIndex = 0
	m.replayScroll = 0
	m.replayPlaying = false
	m.replayTyped = -1 // paused: show the whole step
	m.replayLive = true
	if m.replayDelay == 0 {
		m.replayDelay = replayDefaultDelay
	}
	m.replayGen++
	m.mode = viewReplay

	if len(m.replaySteps) == 0 {
		m.statusMsg = "Nothing to replay in this session"
	}
}

// replayAdvanceCmd drives playback: type out whatever is left of this step,
// then hold it for the step delay before moving on.
//
// Typing has to finish before the hold starts, or a long diff would be cut off
// mid-write by the next step.
func (m Model) replayAdvanceCmd() tea.Cmd {
	if !m.replayPlaying {
		return nil
	}
	if m.replayTyped >= 0 && m.replayTyped < m.replayTypedLength() {
		return replayTypeCmd(m.replayGen)
	}
	return replayTickCmd(m.replayDelay, m.replayGen)
}

// codeSteps keeps only the steps that wrote code: a recorded diff, a new
// file's contents, or a heredoc body.
//
// It exists because an agent's operations are overwhelmingly not code. Across
// one machine's transcripts there were 2592 shell commands against 549 edits,
// so the handful of steps that actually produced something are otherwise
// buried in hundreds of greps, builds and git invocations.
func codeSteps(steps []session.ReplayStep, sess *session.Session) []session.ReplayStep {
	var out []session.ReplayStep
	for _, st := range steps {
		if st.Code != "" {
			out = append(out, st)
			continue
		}
		if st.EventIndex < len(sess.Events) &&
			len(sess.Events[st.EventIndex].StructuredPatchOrNil()) > 0 {
			out = append(out, st)
		}
	}
	return out
}

// replaySessionActive reports whether the transcript is still being written.
// A caught-up replay of a live session waits; a caught-up replay of a finished
// one ends.
func (m Model) replaySessionActive() bool {
	if m.selectedSession == nil {
		return false
	}
	return time.Since(m.selectedSession.Info.LastUpdate) < replayLiveWindow
}

// replayAtEnd reports whether playback has reached the last step known so far.
func (m Model) replayAtEnd() bool {
	return m.replayIndex >= len(m.replaySteps)-1
}

// replayWaiting is "caught up, but the session is still going".
func (m Model) replayWaiting() bool {
	return m.replayPlaying && m.replayLive && m.replayAtEnd() && m.replaySessionActive()
}

// replayStepUUID identifies the step being read, for re-anchoring after the
// transcript grows. It must be called BEFORE the session pointer is swapped
// for a fresh parse, while the index and the events still agree.
func (m Model) replayStepUUID() string {
	if m.selectedSession == nil || m.replayIndex >= len(m.replaySteps) {
		return ""
	}
	at := m.replaySteps[m.replayIndex].EventIndex
	if at >= len(m.selectedSession.Events) {
		return ""
	}
	return m.selectedSession.Events[at].UUID
}

// rebuildReplay re-derives the steps after new events arrived, keeping the
// reader on the step they were on.
//
// The step index is not a stable identity. linkSubagents splices a subagent's
// turns into the MIDDLE of its parent's timeline, so one new background agent
// shifts every index after it — anchoring on the number would silently jump
// the reader. The event UUID is stable, so that is the anchor. OpenCode events
// carry no UUID, but their order is fixed when the database is parsed, so the
// index is a safe fallback there.
func (m *Model) rebuildReplay(anchor string) {
	if m.selectedSession == nil {
		return
	}
	steps := session.BuildReplay(m.selectedSession)
	if m.replayCodeOnly {
		steps = codeSteps(steps, m.selectedSession)
	}
	m.replaySteps = steps
	if len(steps) == 0 {
		m.replayIndex = 0
		return
	}

	if anchor != "" {
		for i, st := range steps {
			if st.EventIndex < len(m.selectedSession.Events) &&
				m.selectedSession.Events[st.EventIndex].UUID == anchor {
				m.replayIndex = i
				return
			}
		}
	}
	m.replayIndex = clampInt(m.replayIndex, 0, len(steps)-1)
}

// replayTypedLength is how many characters the current step types out.
func (m Model) replayTypedLength() int {
	if m.selectedSession == nil || m.replayIndex >= len(m.replaySteps) {
		return 0
	}
	return TypedLength(m.replaySteps[m.replayIndex], m.selectedSession)
}

// replayCharsPerTick scales the typing rate with the playback speed, so + and
// - change the whole feel of a replay rather than only the pause between
// steps.
func (m Model) replayCharsPerTick() int {
	if m.replayDelay <= 0 {
		return replayTypeChars
	}
	n := int(float64(replayTypeChars) * float64(replayDefaultDelay) / float64(m.replayDelay))
	return max(1, n)
}

// replayReveal shows the rest of the current step at once.
func (m *Model) replayReveal() {
	m.replayTyped = m.replayTypedLength()
}

// sessionInContext is the session the current view is about, whichever view
// that is. Actions that work from several views share it.
func (m Model) sessionInContext() *session.Session {
	switch m.mode {
	case viewSessions:
		if m.cursor < len(m.sessions) {
			return m.store.GetSession(m.sessions[m.cursor].ID)
		}
	case viewDetail, viewOverview, viewEvent, viewReplay:
		return m.selectedSession
	case viewProject:
		if m.selectedProject != nil && m.projectCursor < len(m.selectedProject.Sessions) {
			return m.store.GetSession(m.selectedProject.Sessions[m.projectCursor].ID)
		}
	}
	return nil
}

// handleReplayGotoKey consumes keys while the "go to step" prompt is open.
// Nothing else may fire: a digit is part of the number being typed, not a
// speed change or a restart.
func (m Model) handleReplayGotoKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.replayGotoTyping = false
		m.replayGotoDraft = ""
		return m, nil

	case "enter":
		n, err := strconv.Atoi(m.replayGotoDraft)
		m.replayGotoTyping = false
		m.replayGotoDraft = ""
		if err != nil || len(m.replaySteps) == 0 {
			return m, nil
		}
		// Steps are numbered from 1 on screen.
		m.replayIndex = clampInt(n-1, 0, len(m.replaySteps)-1)
		m.replayScroll = 0
		m.replayPlaying = false
		m.replayTyped = -1
		m.replayGen++
		if n < 1 || n > len(m.replaySteps) {
			m.statusMsg = fmt.Sprintf("Only %d steps — showing step %d",
				len(m.replaySteps), m.replayIndex+1)
			return m, clearStatusAfter()
		}
		return m, nil

	case "backspace":
		if r := []rune(m.replayGotoDraft); len(r) > 0 {
			m.replayGotoDraft = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.replayGotoDraft = ""
		return m, nil
	}

	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r >= '0' && r <= '9' {
				m.replayGotoDraft += string(r)
			}
		}
	}
	return m, nil
}

// replayGotoPrompt is the footer shown while a step number is being typed.
func (m Model) replayGotoPrompt() string {
	return searchPromptStyle.Render(" go to step ") + " " + m.replayGotoDraft +
		mutedStyle.Render(fmt.Sprintf("█   of %d · enter to jump · esc to cancel",
			len(m.replaySteps)))
}

// handleReplayKey consumes keys while the replay view is open.
func (m Model) handleReplayKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	if m.replayGotoTyping {
		return m.handleReplayGotoKey(msg, key)
	}

	// Space is normalised to "enter" for selection elsewhere; here it is the
	// play/pause control, so it has to be caught before that mapping applies.
	if msg.Type == tea.KeySpace || key == "enter" {
		m.replayPlaying = !m.replayPlaying
		m.replayGen++
		if !m.replayPlaying {
			// Stopping to look means you want the whole step, not a half-typed
			// one.
			m.replayTyped = -1
			return m, nil
		}
		if m.replayIndex >= len(m.replaySteps)-1 {
			m.replayIndex = 0 // play from the top once it has run out
			m.replayScroll = 0
		}
		m.replayTyped = 0
		return m, m.replayAdvanceCmd()
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc", "backspace":
		m.replayPlaying = false
		m.replayTyped = -1
		m.replayGen++
		m.mode = viewDetail
		if m.selectedSession == nil {
			m.mode = viewSessions
		}
		return m, nil

	case "right", "l", "n":
		// Mid-write, the first press finishes the line rather than skipping
		// what you were reading.
		if m.replayPlaying && m.replayTyped >= 0 && m.replayTyped < m.replayTypedLength() {
			m.replayReveal()
			m.replayGen++
			return m, m.replayAdvanceCmd()
		}
		m.replayStep(1)
		return m, nil

	case "left", "h":
		m.replayStep(-1)
		return m, nil

	case "down", "j":
		m.replayTakeOver()
		m.replayScroll++
		return m, nil

	case "up", "k":
		m.replayTakeOver()
		if m.replayScroll > 0 {
			m.replayScroll--
		}
		return m, nil

	case "shift+down", "pgdown":
		m.replayTakeOver()
		m.replayScroll += m.pageSize()
		return m, nil

	case "shift+up", "pgup":
		m.replayTakeOver()
		m.replayScroll = max(0, m.replayScroll-m.pageSize())
		return m, nil

	case "+", "=":
		// Faster means a shorter hold per step.
		m.replayDelay = clampDuration(m.replayDelay-replayDelayStep, replayMinDelay, replayMaxDelay)
		m.replayGen++
		return m, m.replayAdvanceCmd()

	case "-", "_":
		m.replayDelay = clampDuration(m.replayDelay+replayDelayStep, replayMinDelay, replayMaxDelay)
		m.replayGen++
		return m, m.replayAdvanceCmd()

	case "0", "g", "home":
		m.replayIndex = 0
		m.replayScroll = 0
		m.replayGen++
		if m.replayPlaying {
			m.replayTyped = 0
		}
		return m, m.replayAdvanceCmd()

	case "G", "end":
		m.replayIndex = max(0, len(m.replaySteps)-1)
		m.replayScroll = 0
		m.replayPlaying = false
		m.replayTyped = -1
		m.replayGen++
		return m, nil

	case "/":
		// Jump straight to a step by number, the way "/24" reads.
		m.replayGotoTyping = true
		m.replayGotoDraft = ""
		m.replayPlaying = false
		m.replayTyped = -1
		m.replayGen++
		return m, nil

	case "tab":
		// Keep the reader on the step they were on, if it survives the filter.
		anchor := m.replayStepUUID()
		m.replayCodeOnly = !m.replayCodeOnly
		m.replayPlaying = false
		m.replayTyped = -1
		m.replayScroll = 0
		m.replayGen++
		m.rebuildReplay(anchor)
		if m.replayCodeOnly {
			m.statusMsg = fmt.Sprintf("Code only — %d of the session's steps wrote something",
				len(m.replaySteps))
		} else {
			m.statusMsg = "Showing every step"
		}
		return m, clearStatusAfter()

	case "f":
		m.replayLive = !m.replayLive
		m.replayGen++
		if m.replayLive {
			m.statusMsg = "Following the session — new steps play as they are written"
		} else {
			m.statusMsg = "Stopping at the last step recorded so far"
		}
		return m, tea.Batch(m.replayAdvanceCmd(), clearStatusAfter())

	case "t":
		// Hand off to the timeline at the same point in the session.
		m.replayPlaying = false
		m.replayGen++
		m.jumpTimelineToReplayStep()
		m.mode = viewDetail
		return m, nil
	}

	return m, nil
}

// replayTakeOver pauses playback and reveals the whole step. Scrolling by hand
// is a statement that you want to read this one, so the view stops moving
// underneath you and stops hiding the rest of the diff.
func (m *Model) replayTakeOver() {
	if !m.replayPlaying && m.replayTyped < 0 {
		return
	}
	m.replayPlaying = false
	m.replayTyped = -1
	m.replayGen++
}

// replayStep moves by n steps, pausing playback: a manual step means you want
// to look at something.
func (m *Model) replayStep(n int) {
	if len(m.replaySteps) == 0 {
		return
	}
	m.replayPlaying = false
	m.replayGen++
	m.replayIndex = clampInt(m.replayIndex+n, 0, len(m.replaySteps)-1)
	m.replayScroll = 0
	m.replayTyped = -1
}

// jumpTimelineToReplayStep points the timeline cursor at the event the replay
// is currently showing.
func (m *Model) jumpTimelineToReplayStep() {
	if m.selectedSession == nil || m.replayIndex >= len(m.replaySteps) {
		return
	}
	target := m.replaySteps[m.replayIndex].EventIndex

	// The timeline cursor indexes the filtered list, so a filter hiding the
	// target would land the cursor somewhere unrelated.
	m.resetTimelineFilter()
	m.autoFollow = false
	for i, idx := range m.visibleEvents() {
		if idx == target {
			m.detailCursor = i
			return
		}
	}
	m.detailCursor = 0
}

// clampDuration keeps the playback delay inside its bounds. The package-level
// min/max take ints, so durations need their own.
func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
