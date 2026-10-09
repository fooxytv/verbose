package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fooxytv/verbose/pkg/session"

	tea "github.com/charmbracelet/bubbletea"
)

// Picking a session back up without leaving the replay.
//
// Two halves of one thing: what the session last said, so you can see where it
// left off, and somewhere to write the reply. The reply does not run inside
// verbose — it is handed to the CLI in a tmux pane beside it, which is what
// keeps the replay, the tree and your place in them on screen. Verbose reads
// transcripts; it is not a terminal multiplexer, and pretending otherwise
// would mean carrying a PTY and an ANSI parser to end up with a worse tmux.

// continueMaxReply is as much of a prompt as the box will take. Long enough for
// a real instruction, short enough that it is clearly a reply and not a place
// to draft a document.
const continueMaxReply = 2000

// continueView is what the panel needs to draw.
type continueView struct {
	sess *session.Session

	// lastOutput is the final thing the session said, which is the context for
	// whatever is about to be typed.
	lastOutput string
	// stopped is how long ago the session was last written to.
	stopped string

	draft  string
	scroll int

	// unsupported explains why a reply cannot be sent, when it cannot.
	unsupported string

	width, height int
}

// lastAssistantText is the last thing the session said: its closing message,
// rather than the last tool call or system note.
func lastAssistantText(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	for i := len(sess.Events) - 1; i >= 0; i-- {
		e := sess.Events[i]
		if e.Type == session.EventText && strings.TrimSpace(e.Text) != "" {
			return e.Text
		}
	}
	return ""
}

// renderContinue draws the panel: what was said last, then the reply box.
func renderContinue(v continueView) string {
	var b strings.Builder

	b.WriteString(clampWidth(titleStyle.Render("Continue")+"  "+
		dimStyle.Render(truncate(v.sess.Info.Title, max(10, v.width-28))), v.width) + "\n")
	b.WriteString(clampWidth("  "+mutedStyle.Render("last active "+v.stopped+
		" · the reply opens the CLI beside verbose"), v.width) + "\n\n")

	// The reply box first: it is what the panel is for, and it must not be
	// pushed off the bottom by a long closing message.
	b.WriteString("  " + headerLabelStyle.Render("Your reply") + "\n")
	for _, line := range continueDraftLines(v) {
		b.WriteString(clampWidth(line, v.width) + "\n")
	}
	b.WriteString("\n")

	b.WriteString("  " + headerLabelStyle.Render("It last said") + "\n")
	if strings.TrimSpace(v.lastOutput) == "" {
		b.WriteString("  " + dimStyle.Render("(this session ended without a closing message)"))
		return b.String()
	}

	body := wrapProse(v.lastOutput, max(10, v.width-4), "  ")
	visible := max(1, v.height-12)
	scroll := clampInt(v.scroll, 0, max(0, len(body)-visible))
	end := min(len(body), scroll+visible)
	for _, line := range body[scroll:end] {
		b.WriteString(clampWidth(textStyle.Render(line), v.width) + "\n")
	}
	if end < len(body) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … %d more lines (↑/↓)", len(body)-end)))
	}
	return b.String()
}

// continueDraftLines renders the reply box, wrapped, with a cursor.
func continueDraftLines(v continueView) []string {
	if v.unsupported != "" {
		return []string{"  " + toolErrorStyle.Render(v.unsupported)}
	}

	draft := v.draft
	if strings.TrimSpace(draft) == "" {
		return []string{"  " + searchPromptStyle.Render(" ▌ ") + " " +
			mutedStyle.Render("type a reply, enter to send, esc to cancel")}
	}

	lines := wrapProse(draft+"▌", max(10, v.width-6), "  ")
	for i := range lines {
		lines[i] = textStyle.Render(lines[i])
	}
	return lines
}

// continueCmd hands the reply to the CLI, resuming this session.
//
// `claude --resume <id> "<prompt>"` is a verified combination: the CLI takes a
// positional prompt alongside --resume, and rejects only the session id when it
// does not exist. An empty reply is just a resume, which is what "c" has always
// done.
func continueCmd(sess *session.Session, prompt string) tea.Cmd {
	if sess == nil {
		return nil
	}
	cli, args := continueArgs(sess.Info.ID, prompt)
	return launchInTerminal(cli, args, sess.Info.CWD)
}

// continueArgs builds the command line, kept apart from launching it so that
// what gets run can be asserted rather than taken on trust.
func continueArgs(id, prompt string) (cli string, args []string) {
	prompt = strings.TrimSpace(prompt)
	if len([]rune(prompt)) > continueMaxReply {
		prompt = string([]rune(prompt)[:continueMaxReply])
	}

	// OpenCode resumes by --session and takes its prompt through --prompt.
	if ocID, ok := strings.CutPrefix(id, "oc-"); ok {
		args = []string{"--session", ocID}
		if prompt != "" {
			args = append(args, "--prompt", prompt)
		}
		return "opencode", args
	}

	args = []string{"--resume", id}
	if prompt != "" {
		args = append(args, prompt)
	}
	return "claude", args
}

// continueUnsupported explains why a session cannot be replied to, or returns
// empty when it can.
func continueUnsupported(sess *session.Session) string {
	if sess == nil {
		return "No session open."
	}
	if sess.Info.IsAgent {
		return "This is a subagent's own transcript. Open its parent session to reply."
	}
	if sess.Info.CWD == "" {
		return "This session records no working directory, so the CLI cannot be " +
			"started in the right place."
	}
	return ""
}

// openContinue opens the panel for the session on screen.
func (m *Model) openContinue() {
	if m.selectedSession == nil {
		return
	}
	m.continueOpen = true
	m.continueDraft = ""
	m.continueScroll = 0

	// Stop playback: this is a place to stop and think, and the pane the panel
	// needs is the one the replay was using.
	m.replayPlaying = false
	m.replayTyped = -1
	m.replayGen++
}

func (m *Model) closeContinue() {
	m.continueOpen = false
	m.continueDraft = ""
	m.continueScroll = 0
}

// continueViewState assembles the panel's inputs at a given size.
func (m Model) continueViewState(width, height int) continueView {
	return continueView{
		sess:        m.selectedSession,
		lastOutput:  lastAssistantText(m.selectedSession),
		stopped:     agoShort(time.Since(m.selectedSession.Info.LastUpdate)),
		draft:       m.continueDraft,
		scroll:      m.continueScroll,
		unsupported: continueUnsupported(m.selectedSession),
		width:       width,
		height:      height,
	}
}

// renderContinueSplit draws the tree beside the panel, matching the diff.
func renderContinueSplit(m Model) string {
	rows := max(1, m.height-1)
	side := sidebarWidth(m.width)
	if side == 0 || m.treeRoot == nil {
		return renderContinue(m.continueViewState(usableWidth(m.width), rows))
	}
	main := usableWidth(m.width) - side - 3
	return joinPanes(renderTree(m.treeViewState(side, rows)),
		renderContinue(m.continueViewState(main, rows)), side, main, rows)
}

// handleContinueKey consumes keys while the panel is open. It is a text box, so
// almost everything is typing.
func (m Model) handleContinueKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.closeContinue()
		return m, nil

	case "enter":
		if continueUnsupported(m.selectedSession) != "" {
			m.closeContinue()
			return m, nil
		}
		sess, draft := m.selectedSession, m.continueDraft
		m.closeContinue()
		m.statusMsg = "Opening the CLI beside verbose…"
		return m, tea.Batch(continueCmd(sess, draft), clearStatusAfter())

	case "backspace":
		if r := []rune(m.continueDraft); len(r) > 0 {
			m.continueDraft = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.continueDraft = ""
		return m, nil

	// Scrolling the message above, so a long one can be read while replying.
	case "up":
		m.continueScroll = max(0, m.continueScroll-1)
		return m, nil
	case "down":
		m.continueScroll++
		return m, nil
	}

	if msg.Type == tea.KeySpace {
		m.continueDraft += " "
		return m, nil
	}
	if msg.Type == tea.KeyRunes && len([]rune(m.continueDraft)) < continueMaxReply {
		m.continueDraft += string(msg.Runes)
	}
	return m, nil
}
