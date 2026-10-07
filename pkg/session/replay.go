package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Replay turns a session into an ordered list of steps meant to be read one at
// a time, slowly, to follow what an agent actually did.
//
// The timeline view answers "what happened in this session". A replay answers
// "what happened at this moment, and what did it do" — so it keeps only the
// events that carry the narrative and drops the bookkeeping. Thinking is the
// notable omission: extended thinking is signed but not retained, so every
// thinking block on disk has an empty body and there is nothing to replay.
//
// A step describes WHAT was done, derived from the transcript. It deliberately
// does not claim to explain WHY: the reasoning is not in the file. Explanation
// holds that, and is filled in by a later pass rather than guessed at here.

// ReplayKind classifies a step by its role in the narrative.
type ReplayKind int

const (
	// StepGoal is something the user asked for.
	StepGoal ReplayKind = iota
	// StepPlan is the agent saying what it is about to do.
	StepPlan
	// StepAction is an operation: a command, an edit, a search, a dispatch.
	StepAction
	// StepProblem is an operation that failed or was refused.
	StepProblem
	// StepNote is session machinery worth seeing — a compaction, a user edit.
	StepNote
)

func (k ReplayKind) String() string {
	switch k {
	case StepGoal:
		return "goal"
	case StepPlan:
		return "plan"
	case StepAction:
		return "action"
	case StepProblem:
		return "problem"
	case StepNote:
		return "note"
	}
	return "step"
}

// ReplayStep is one beat of a replay.
type ReplayStep struct {
	// EventIndex points back into Session.Events, so the renderer can show the
	// full event — diff hunks, command output — without copying any of it.
	EventIndex int

	Kind ReplayKind

	// Title is one plain-text line: the operation and its subject.
	Title string

	// Detail is a plain-English account of what the step did, derived from the
	// transcript. Always safe to show offline.
	Detail string

	// Code is the file content this step wrote, recovered when no diff records
	// it. CodePath is where it landed, when the transcript says.
	Code     string
	CodePath string

	// Explanation is the teaching text for this step. It is empty until
	// something generates it; the reasoning behind a step is not recorded in
	// the transcript, so it cannot be derived here.
	Explanation string

	// IsSidechain marks a step that happened inside a subagent run.
	IsSidechain bool
}

// BuildReplay selects and describes the steps of a session's replay. The
// returned slice is in transcript order and may be empty.
func BuildReplay(sess *Session) []ReplayStep {
	if sess == nil {
		return nil
	}

	var steps []ReplayStep
	for i := range sess.Events {
		e := sess.Events[i]
		kind, ok := replayKind(e)
		if !ok {
			continue
		}
		codePath, code := stepCode(e)
		if code == "" {
			codePath = ""
		}
		title := replayTitle(e, sess.Info.CWD)
		// "Ran mkdir -p … && cat > app.js <<'EOF'" says less than "Wrote
		// app.js", and the command is shown underneath anyway.
		if code != "" && codePath != "" {
			title = "Wrote " + relPath(codePath, sess.Info.CWD)
		}
		steps = append(steps, ReplayStep{
			EventIndex:  i,
			Kind:        kind,
			Title:       title,
			Detail:      DescribeEvent(e, sess.Info.CWD),
			Code:        code,
			CodePath:    codeLabel(codePath, sess.Info.CWD),
			IsSidechain: e.IsSidechain,
		})
	}
	return steps
}

// replayKind decides whether an event belongs in a replay, and as what.
func replayKind(e Event) (ReplayKind, bool) {
	switch e.Type {
	case EventUserPrompt:
		// The filter has to test the text as the step will SHOW it. A prompt
		// that is nothing but command and reminder tags strips down to empty,
		// and would otherwise render as a headline with no body.
		if collapseSpaces(stripPromptTags(e.UserText)) == "" {
			return 0, false
		}
		return StepGoal, true

	case EventText:
		// The agent narrating its next move. Sparse, but it is the only
		// first-hand account of intent the transcript keeps.
		if collapseSpaces(e.Text) == "" {
			return 0, false
		}
		return StepPlan, true

	case EventToolUse:
		if isFailure(e) {
			return StepProblem, true
		}
		return StepAction, true

	case EventToolResult:
		// Only orphan results survive correlation; a result folded into its
		// call is already covered by that call's step.
		if isFailure(e) {
			return StepProblem, true
		}
		return StepAction, true

	case EventToolDenied:
		return StepProblem, true

	case EventCompaction, EventUserFileEdit:
		return StepNote, true

	case EventThinking:
		// Never retained in the transcript — an empty step teaches nothing.
		if strings.TrimSpace(e.Thinking) == "" {
			return 0, false
		}
		return StepPlan, true
	}

	// EventSystem, EventHookProgress, EventBashProgress, EventTurnDuration,
	// EventAgentProgress and EventDiagnostics are bookkeeping: they say how the
	// session ran, not what was built.
	return 0, false
}

// isFailure reports whether an operation went wrong. A failing shell command
// leaves is_error false and records the error as a bare string instead, so both
// signals have to be checked.
func isFailure(e Event) bool {
	if e.IsError {
		return true
	}
	if e.Result != nil && e.Result.Raw != "" {
		return true
	}
	return e.Type == EventToolDenied
}

// replayTitle is the one-line headline for a step.
func replayTitle(e Event, cwd string) string {
	switch e.Type {
	case EventUserPrompt:
		// Inside a subagent run the "user" is the agent that dispatched it, so
		// calling this prompt yours would misattribute it.
		if e.IsSidechain {
			return "The subagent's brief"
		}
		return "You asked"
	case EventText:
		return "Claude's plan"
	case EventThinking:
		return "Claude's reasoning"
	case EventCompaction:
		return "Context compacted"
	case EventUserFileEdit:
		return "You edited " + relPath(e.FilePath, cwd)
	case EventToolDenied:
		return "You declined " + e.ToolName
	}

	if e.ToolName == "" {
		return "Step"
	}

	verb := toolVerb(e.ToolName)
	if subject := toolSubject(e, cwd); subject != "" {
		return verb + " " + subject
	}
	return verb
}

// toolVerb names what a tool does, in the past tense, for a reader who does not
// know Claude Code's tool names.
func toolVerb(tool string) string {
	switch tool {
	case "Read":
		return "Read"
	case "Write":
		return "Created"
	case "Edit", "MultiEdit", "NotebookEdit":
		return "Edited"
	case "Bash", "BashOutput":
		return "Ran"
	case "Grep":
		return "Searched for"
	case "Glob":
		return "Listed files matching"
	case "LS":
		return "Listed"
	case "WebFetch":
		return "Fetched"
	case "WebSearch":
		return "Searched the web for"
	case "Task", "Agent":
		return "Dispatched a subagent"
	case "Skill":
		return "Loaded skill"
	case "TodoWrite":
		return "Updated the todo list"
	case "AskUserQuestion":
		return "Asked you a question"
	case "SubagentHandback":
		return "Subagent reported back"
	}
	return tool
}

// toolSubject is what the operation acted on.
func toolSubject(e Event, cwd string) string {
	switch e.ToolName {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		if p, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
			return relPath(p, cwd)
		}
		if e.Result != nil && e.Result.FilePath != "" {
			return relPath(e.Result.FilePath, cwd)
		}
	case "Bash", "BashOutput":
		if c, ok := stringInput(e.ToolInput, "command"); ok {
			return firstCommandLine(c)
		}
	case "Grep":
		if p, ok := stringInput(e.ToolInput, "pattern"); ok {
			return quote(p)
		}
	case "Glob":
		if p, ok := stringInput(e.ToolInput, "pattern"); ok {
			return quote(p)
		}
	case "LS":
		if p, ok := stringInput(e.ToolInput, "path"); ok {
			return relPath(p, cwd)
		}
	case "WebFetch":
		if u, ok := stringInput(e.ToolInput, "url"); ok {
			return u
		}
	case "WebSearch":
		if q, ok := stringInput(e.ToolInput, "query"); ok {
			return quote(q)
		}
	case "Task", "Agent":
		if t, ok := stringInput(e.ToolInput, "subagent_type"); ok {
			return "(" + t + ")"
		}
	case "Skill":
		if s, ok := stringInput(e.ToolInput, "skill"); ok {
			return s
		}
	}
	return ""
}

// DescribeEvent explains in plain English what one event did, using only what
// the transcript records. This is the offline substitute for reasoning that was
// never saved: it is accurate about the what and silent about the why.
func DescribeEvent(e Event, cwd string) string {
	switch e.Type {
	case EventUserPrompt:
		return collapseSpaces(stripPromptTags(e.UserText))

	case EventText:
		return collapseSpaces(e.Text)

	case EventThinking:
		return collapseSpaces(e.Thinking)

	case EventCompaction:
		return fmt.Sprintf("The conversation was summarised to free up context, "+
			"triggered %s, with %s tokens in play beforehand.",
			orUnknown(e.CompactTrigger), humanCount(e.CompactPreTokens))

	case EventUserFileEdit:
		return fmt.Sprintf("You changed %s outside of Claude, so the agent's "+
			"picture of that file was stale from here on.", relPath(e.FilePath, cwd))

	case EventToolDenied:
		msg := fmt.Sprintf("You rejected this %s call", orUnknown(e.ToolName))
		if e.DenialKind != "" {
			msg += " (" + e.DenialKind + ")"
		}
		if fb := strings.TrimSpace(e.UserFeedback); fb != "" {
			msg += ", saying: " + collapseSpaces(fb)
		}
		return msg + "."
	}

	return describeTool(e, cwd)
}

// describeTool accounts for one tool operation and what it produced.
func describeTool(e Event, cwd string) string {
	r := e.Result

	// A failure is the most important thing to say about a step, so it leads.
	if r != nil && r.Raw != "" {
		return "This failed. " + collapseSpaces(r.Raw)
	}

	switch e.ToolName {
	case "Read":
		if r != nil && r.File != nil {
			f := r.File
			if f.TotalLines > 0 && f.NumLines < f.TotalLines {
				return fmt.Sprintf("Pulled lines %d-%d of %s into context — %d of its %d lines, "+
					"so only part of the file was visible.",
					f.StartLine, f.StartLine+f.NumLines, relPath(f.FilePath, cwd),
					f.NumLines, f.TotalLines)
			}
			return fmt.Sprintf("Pulled all %d lines of %s into context.",
				f.NumLines, relPath(f.FilePath, cwd))
		}
		return "Read a file into context before changing anything."

	case "Write":
		p, _ := stringInput(e.ToolInput, "file_path")
		return fmt.Sprintf("Wrote %s from scratch, replacing anything already there.",
			relPath(p, cwd))

	case "Edit", "MultiEdit", "NotebookEdit":
		return describeEdit(e, cwd)

	case "Bash", "BashOutput":
		return describeBash(e)

	case "Grep":
		pat, _ := stringInput(e.ToolInput, "pattern")
		where, ok := stringInput(e.ToolInput, "path")
		if !ok {
			where = "the project"
		} else {
			where = relPath(where, cwd)
		}
		return fmt.Sprintf("Looked for %s in %s to find the code before touching it.",
			quote(pat), where)

	case "Glob":
		pat, _ := stringInput(e.ToolInput, "pattern")
		return fmt.Sprintf("Listed every file matching %s, to find out what exists.", quote(pat))

	case "Task", "Agent":
		agentType, _ := stringInput(e.ToolInput, "subagent_type")
		desc, _ := stringInput(e.ToolInput, "description")
		out := "Handed this piece of work to a separate agent"
		if agentType != "" {
			out = fmt.Sprintf("Handed this piece of work to a %s agent", agentType)
		}
		if desc != "" {
			out += ": " + collapseSpaces(desc)
		}
		return out + ". Its own steps follow, marked as a subagent."

	case "Skill":
		s, _ := stringInput(e.ToolInput, "skill")
		return fmt.Sprintf("Loaded the %s skill — a set of instructions for this kind of task.", s)

	case "TodoWrite":
		return "Rewrote the task list, which is how the agent tracks multi-step work."

	case "AskUserQuestion":
		return "Stopped to ask you a question rather than guess."
	}

	if out := strings.TrimSpace(e.ToolOutput); out != "" {
		return collapseSpaces(out)
	}
	return ""
}

// describeEdit reports a change by the diff that was actually recorded, not by
// the edit that was requested — they differ when a file moved underfoot.
func describeEdit(e Event, cwd string) string {
	path, _ := stringInput(e.ToolInput, "file_path", "notebook_path")
	r := e.Result
	if r != nil && r.FilePath != "" {
		path = r.FilePath
	}
	name := relPath(path, cwd)

	if r == nil || len(r.StructuredPatch) == 0 {
		return fmt.Sprintf("Changed %s.", name)
	}

	added, removed := r.Churn()
	out := fmt.Sprintf("Changed %s: %d line(s) added, %d removed, across %d hunk(s).",
		name, added, removed, len(r.StructuredPatch))
	if r.ReplaceAll {
		out += " Every occurrence was replaced, not just the first."
	}
	if r.UserModified {
		out += " You had edited this file since the agent last read it, so it re-read before writing."
	}
	return out
}

// describeBash reports a command by what it printed. A non-empty stderr is not
// itself a failure — plenty of tools report progress there.
func describeBash(e Event) string {
	cmd, _ := stringInput(e.ToolInput, "command")
	desc, _ := stringInput(e.ToolInput, "description")

	// A command that carries a heredoc is really a file write. Saying what it
	// wrote is more use than echoing the redirect that carried it.
	if path, body := shellHeredoc(cmd); body != "" {
		lines := strings.Count(body, "\n") + 1
		if path != "" {
			return fmt.Sprintf("Wrote %d line(s) into %s through the shell, so Claude Code "+
				"recorded no diff for it.", lines, path)
		}
		return fmt.Sprintf("Ran a %d-line script inline. It edited files itself, so what "+
			"changed is in the script rather than in any recorded diff.", lines)
	}

	var out string
	switch {
	case desc != "":
		out = collapseSpaces(desc) + "."
	case cmd != "":
		out = "Ran " + quote(firstCommandLine(cmd)) + "."
	default:
		out = "Ran a shell command."
	}

	r := e.Result
	if r == nil {
		return out
	}
	if r.Interrupted {
		return out + " It was interrupted before it finished."
	}
	switch {
	case r.Stdout != "" && r.Stderr != "":
		return out + fmt.Sprintf(" It printed %d bytes of output and %d bytes on stderr.",
			len(r.Stdout), len(r.Stderr))
	case r.Stdout != "":
		return out + fmt.Sprintf(" It printed %d bytes of output.", len(r.Stdout))
	case r.Stderr != "":
		return out + fmt.Sprintf(" It printed nothing, and %d bytes on stderr.", len(r.Stderr))
	}
	return out + " It printed nothing, which for most commands means it worked."
}

// codeLabel names the file some code was written to. Unlike relPath it returns
// empty for an unknown path rather than "a file": a heredoc piped to an
// interpreter writes no file at all, and claiming otherwise would be a lie.
func codeLabel(path, cwd string) string {
	if path == "" {
		return ""
	}
	return relPath(path, cwd)
}

// relPath shortens a path against the session's working directory, so a step
// reads as "storage.tf" rather than a full home-directory path.
func relPath(path, cwd string) string {
	if path == "" {
		return "a file"
	}
	if cwd != "" && strings.HasPrefix(path, cwd) {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != "." {
			return rel
		}
	}
	// Outside the project — a scratchpad or a temp directory. The full path is
	// mostly machine-specific noise, so keep the end of it, which is the part
	// that identifies the file.
	if len(path) > 48 {
		dir, base := filepath.Split(path)
		return "…/" + filepath.Join(filepath.Base(filepath.Clean(dir)), base)
	}
	return path
}

// firstCommandLine reduces a multi-line or chained command to something that
// fits on one line, keeping the part that says what it does.
func firstCommandLine(cmd string) string {
	// Split before collapsing: collapseSpaces turns newlines into spaces, so
	// doing it the other way round flattens an entire heredoc — body and all —
	// onto one line and calls it the command.
	if i := strings.IndexByte(cmd, '\n'); i >= 0 {
		cmd = cmd[:i]
	}
	return collapseSpaces(cmd)
}

func quote(s string) string {
	if s == "" {
		return ""
	}
	return `"` + s + `"`
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// humanCount formats a token count for prose.
func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// Extracting code that no diff records.
//
// Claude Code only writes a structuredPatch for Edit/Write/MultiEdit, and for a
// brand-new file that patch is EMPTY — there is no "before" to diff against. An
// agent working through the shell produces no patch at all. Measured across one
// machine's transcripts, 6.4% of replay steps carry a usable diff while 882
// shell commands wrote files with none, so a replay built on diffs alone shows
// almost none of the code that was actually produced.
//
// The code is still there, in two places the diff renderer never looks: a Write
// call keeps it in its `content` input, and a shell heredoc keeps it in the
// command text. Both are recovered here.

// heredocStart matches the `<<EOF`, `<<'EOF'` or `<<-"EOF"` that opens a shell
// heredoc, capturing the marker that will close it.
var heredocStart = regexp.MustCompile(`<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// redirectTarget matches the `> path` or `>> path` of a shell redirect, but not
// a file descriptor dup such as `2>&1`.
var redirectTarget = regexp.MustCompile(`>>?\s*([^\s<>|&;]+)`)

// shellHeredoc pulls the body out of a command that writes through the shell,
// along with the file it lands in when the command says.
//
// A heredoc fed to an interpreter (`python3 - <<'PY'`) has no target file; the
// body is still the code that ran, so it is returned with an empty path.
func shellHeredoc(cmd string) (path, body string) {
	m := heredocStart.FindStringSubmatch(cmd)
	if m == nil {
		return "", ""
	}
	marker := m[2]

	lines := strings.Split(cmd, "\n")
	if len(lines) < 2 {
		return "", ""
	}
	if t := redirectTarget.FindStringSubmatch(lines[0]); t != nil {
		path = strings.Trim(t[1], `"'`)
	}

	var out []string
	for _, l := range lines[1:] {
		// `<<-` allows the terminator to be indented.
		if strings.TrimSpace(l) == marker {
			break
		}
		out = append(out, l)
	}
	return path, strings.Join(out, "\n")
}

// stepCode is the code a step wrote when no diff records it. It returns empty
// when a usable diff exists, because a diff says more than a wall of content:
// it shows what changed rather than what the file now contains.
func stepCode(e Event) (path, code string) {
	if e.Result != nil && len(e.Result.StructuredPatch) > 0 {
		return "", ""
	}

	switch e.ToolName {
	case "Write":
		// A new file: the patch is empty, but the whole body is in the input.
		content, _ := stringInput(e.ToolInput, "content")
		p, _ := stringInput(e.ToolInput, "file_path")
		return p, content

	case "Bash", "BashOutput":
		cmd, ok := stringInput(e.ToolInput, "command")
		if !ok {
			return "", ""
		}
		return shellHeredoc(cmd)
	}
	return "", ""
}

// StructuredPatchOrNil is the diff an event recorded, if any.
func (e Event) StructuredPatchOrNil() []PatchHunk {
	if e.Result == nil {
		return nil
	}
	return e.Result.StructuredPatch
}
