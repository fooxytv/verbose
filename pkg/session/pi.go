package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pi keeps one JSONL file per session under
// `~/.pi/agent/sessions/<encoded cwd>/<timestamp>_<uuid>.jsonl` — the closest
// layout to Claude Code's of any source here, and read the same way: a file
// per session, an entry per line.
//
// Every entry carries `{type, id, parentId, timestamp}` and the entries form a
// chain through parentId. Verbose reads them in file order, which is the same
// order: the chain exists for pi's own branching, not for reconstructing time.
//
// It is the only source on disk that keeps its reasoning. Claude Code's
// thinking blocks are signed but empty and Codex keeps a one-line headline at
// best; every pi thinking block has its full text.

// DefaultPiDirs returns the standard locations of a pi home, most likely
// first. Paths are returned whether or not they exist.
func DefaultPiDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".pi"))
	}
	return dirs
}

// piEntry is one line of a session file.
type piEntry struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	Timestamp string     `json:"timestamp"`
	CWD       string     `json:"cwd"`      // type == "session"
	Provider  string     `json:"provider"` // type == "model_change"
	ModelID   string     `json:"modelId"`
	Message   *piMessage `json:"message"`
}

// piMessage is the payload of a "message" entry. Which fields are set depends
// on the role: user, assistant, toolResult, or bashExecution.
type piMessage struct {
	Role      string   `json:"role"`
	Content   []piPart `json:"content"`
	Timestamp int64    `json:"timestamp"` // Unix milliseconds
	Model     string   `json:"model"`
	Provider  string   `json:"provider"`
	Usage     *piUsage `json:"usage"`

	// role == "toolResult"
	ToolCallID string     `json:"toolCallId"`
	ToolName   string     `json:"toolName"`
	IsError    bool       `json:"isError"`
	Details    *piDetails `json:"details"`

	// role == "bashExecution" — a command run from pi's own shell rather than
	// by the model.
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exitCode"`
}

// piPart is one piece of a message's content.
type piPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	// type == "toolCall"
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type piUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// piDetails rides along with an edit's result and carries the diff pi rendered
// for it.
type piDetails struct {
	Diff string `json:"diff"`
}

// ParsePiDir reads a pi home and returns its sessions.
func ParsePiDir(dir string) ([]*Session, error) {
	root := filepath.Join(dir, "agent", "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		// A pi that has never run looks exactly like this.
		return nil, nil
	}

	var sessions []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			sess, err := ParsePiSessionFile(filepath.Join(root, e.Name(), f.Name()))
			if err != nil || sess == nil || len(sess.Events) == 0 {
				continue
			}
			sessions = append(sessions, sess)
		}
	}
	return sessions, nil
}

// ParsePiSessionFile reads one pi session file.
func ParsePiSessionFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sess := &Session{Info: SessionInfo{Source: "pi", FilePath: path}}
	sess.Info.ToolCounts = make(map[string]int)
	stats := newOCStats()

	// Where each tool call landed in the event list, so its result can be
	// folded back into it: one operation is one event, which is the shape
	// every other view already expects.
	pending := make(map[string]int)

	sc := bufio.NewScanner(f)
	// A single entry can carry a whole file's contents.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e piEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}

		switch e.Type {
		case "session":
			sess.Info.ID = "pi-" + e.ID
			sess.Info.CWD = e.CWD
			sess.Info.StartTime = cpTime(e.Timestamp)

		case "model_change":
			// The first model the session used is the one that labels it; a
			// session can change model part way through and the headline
			// should not keep moving.
			if sess.Info.Model == "" {
				sess.Info.Model = joinOCModel(e.Provider, e.ModelID)
			}

		case "message":
			if e.Message != nil {
				piMessageEvents(*e.Message, sess, stats, pending)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	finishPiSession(sess, stats, path)
	return sess, nil
}

// piMessageEvents turns one message into events, appending them to the
// session.
func piMessageEvents(m piMessage, sess *Session, stats *ocStats, pending map[string]int) {
	ts := ocTime(m.Timestamp)

	switch m.Role {
	case "user":
		text := piText(m.Content)
		if strings.TrimSpace(text) == "" {
			return
		}
		sess.Info.UserPrompts++
		sess.Events = append(sess.Events,
			Event{Type: EventUserPrompt, Timestamp: ts, UserText: text})

	case "assistant":
		if m.Usage != nil {
			sess.Info.InputTokens += m.Usage.Input
			sess.Info.OutputTokens += m.Usage.Output
			sess.Info.CacheReadTokens += m.Usage.CacheRead
			sess.Info.CacheWriteTokens += m.Usage.CacheWrite
			sess.Info.CostUSD += m.Usage.Cost.Total
		}
		for _, p := range m.Content {
			switch p.Type {
			case "text":
				if strings.TrimSpace(p.Text) == "" {
					continue
				}
				sess.Events = append(sess.Events,
					Event{Type: EventText, Timestamp: ts, Text: p.Text})

			case "thinking":
				// Kept, and kept whole. This is the one source that records
				// it, so dropping it here would throw away the only answer to
				// "why" any transcript on this machine has.
				if strings.TrimSpace(p.Thinking) == "" {
					continue
				}
				sess.Events = append(sess.Events,
					Event{Type: EventThinking, Timestamp: ts, Thinking: p.Thinking})

			case "toolCall":
				pending[p.ID] = len(sess.Events)
				sess.Events = append(sess.Events, piToolEvent(p, ts, sess, stats))
			}
		}

	case "toolResult":
		i, ok := pending[m.ToolCallID]
		if !ok || i >= len(sess.Events) {
			return
		}
		delete(pending, m.ToolCallID)
		piApplyResult(&sess.Events[i], m, sess, stats)

	case "bashExecution":
		// A command run from pi's own shell rather than by the model. It is
		// still a command that ran in this session's directory, and reading
		// the session without it leaves an unexplained change on disk.
		if strings.TrimSpace(m.Command) == "" {
			return
		}
		e := Event{
			Type: EventToolUse, Timestamp: ts, ToolName: "Bash",
			ToolInput:  map[string]interface{}{"command": m.Command},
			ToolOutput: m.Output,
			DurationMs: -1,
			Result:     &ToolResult{Stdout: m.Output},
		}
		if m.ExitCode != nil && *m.ExitCode != 0 {
			e.IsError = true
			e.Result = &ToolResult{Raw: m.Output}
			sess.Info.Errors++
		}
		sess.Info.ToolCallCount++
		sess.Info.ToolCounts["Bash"]++
		sess.Info.BashCommands++
		sess.Events = append(sess.Events, e)
	}
}

// piToolEvent is the call itself, before its result has arrived.
func piToolEvent(p piPart, ts time.Time, sess *Session, stats *ocStats) Event {
	name := normalizeOCToolName(p.Name)
	in := normalizePiInput(p.Arguments)

	sess.Info.ToolCallCount++
	sess.Info.ToolCounts[name]++

	switch name {
	case "Bash":
		sess.Info.BashCommands++
	case "Read":
		if fp, ok := stringInput(in, "file_path"); ok {
			stats.filesRead[fp] = true
		}
	case "WebFetch", "WebSearch":
		sess.Info.WebRequests++
	}

	return Event{
		Type: EventToolUse, Timestamp: ts, ToolName: name,
		ToolInput: in, ToolID: p.ID, DurationMs: -1,
	}
}

// piApplyResult folds a result back into the call it belongs to.
func piApplyResult(e *Event, m piMessage, sess *Session, stats *ocStats) {
	out := piText(m.Content)
	e.ToolOutput = out

	if ms := m.Timestamp - e.Timestamp.UnixMilli(); ms > 0 && !e.Timestamp.IsZero() {
		e.DurationMs = int(ms)
	}

	if m.IsError {
		e.IsError = true
		e.Result = &ToolResult{Raw: out}
		sess.Info.Errors++
		return
	}

	fp, _ := stringInput(e.ToolInput, "file_path")
	switch e.ToolName {
	case "Write":
		// No diff: there is no before-state. The content is on the call, which
		// is what the replay types out.
		e.Result = &ToolResult{FilePath: fp}
		if fp != "" {
			stats.filesCreated[fp] = true
			churnFor(stats.churn, fp).Edits++
		}

	case "Edit":
		e.Result = &ToolResult{FilePath: fp}
		if m.Details != nil {
			e.Result.StructuredPatch = parsePiDiff(m.Details.Diff)
		}
		added, removed := e.Result.Churn()
		e.LinesAdded, e.LinesRemoved = added, removed
		if fp != "" {
			stats.filesWritten[fp] = true
			c := churnFor(stats.churn, fp)
			c.Edits++
			c.LinesAdded += added
			c.LinesRemoved += removed
		}

	case "Read":
		if fp != "" {
			e.Result = &ToolResult{File: &ReadFile{FilePath: fp}}
		}

	default:
		if out != "" {
			e.Result = &ToolResult{Stdout: out}
		}
	}
}

// piDiffLine matches one line of the diff pi renders for an edit:
// a marker, the line number right-aligned in a field whose width follows the
// file, a space, and the content.
//
// The spaces between the marker and the number are optional, not fixed: a
// two-digit line reads "- 86 x" and a three-digit one "-100 x", so a pattern
// expecting a separator silently skips every hundredth line onward.
var piDiffLine = regexp.MustCompile(`^([-+ ]) *(\d+) (.*)$`)

// parsePiDiff turns that rendering back into hunks.
//
// pi records the diff it displayed rather than a unified one, so there are no
// @@ headers to read a range from: a run of consecutive lines is a hunk, and
// the elision between runs ("...") ends it. The first line number in a run is
// its start on both sides, which is exact for the first change in a file and
// is what the gutter needs.
func parsePiDiff(diff string) []PatchHunk {
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	var (
		hunks []PatchHunk
		cur   *PatchHunk
	)
	flush := func() {
		if cur != nil && len(cur.Lines) > 0 {
			hunks = append(hunks, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		g := piDiffLine.FindStringSubmatch(line)
		if g == nil {
			// The elision, or a blank line between runs.
			flush()
			continue
		}
		marker, text := g[1], g[3]
		if cur == nil {
			n, err := strconv.Atoi(g[2])
			if err != nil {
				continue
			}
			cur = &PatchHunk{OldStart: n, NewStart: n}
		}
		cur.Lines = append(cur.Lines, marker+text)
		switch marker {
		case "-":
			cur.OldLines++
		case "+":
			cur.NewLines++
		default:
			cur.OldLines++
			cur.NewLines++
		}
	}
	flush()

	return hunks
}

// normalizePiInput renames pi's argument keys to the Claude Code ones the rest
// of the UI keys off. The map is copied rather than mutated in place.
func normalizePiInput(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		switch k {
		case "path":
			out["file_path"] = v
		case "oldText":
			out["old_string"] = v
		case "newText":
			out["new_string"] = v
		default:
			out[k] = v
		}
	}
	return out
}

// piText flattens a content array to the text a reader sees.
func piText(parts []piPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// finishPiSession fills in the metadata and rolls up the counters.
func finishPiSession(sess *Session, stats *ocStats, path string) {
	if sess.Info.ID == "" {
		// No session header: name it after the file, which carries the id.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if i := strings.LastIndexByte(base, '_'); i >= 0 {
			base = base[i+1:]
		}
		sess.Info.ID = "pi-" + base
	}

	sess.Info.ProjectDir = sess.Info.CWD
	if sess.Info.CWD != "" {
		sess.Info.ProjectName = filepath.Base(sess.Info.CWD)
	}
	sess.Info.EventCount = len(sess.Events)
	sess.Info.Title = deriveTitle(sess.Events)

	if len(sess.Events) > 0 {
		last := sess.Events[len(sess.Events)-1].Timestamp
		if !last.IsZero() {
			sess.Info.LastUpdate = last
		}
		if sess.Info.StartTime.IsZero() {
			sess.Info.StartTime = sess.Events[0].Timestamp
		}
	}
	if sess.Info.LastUpdate.IsZero() {
		sess.Info.LastUpdate = sess.Info.StartTime
	}

	for fp := range stats.filesRead {
		sess.Info.FilesRead = append(sess.Info.FilesRead, fp)
	}
	for fp := range stats.filesWritten {
		sess.Info.FilesWritten = append(sess.Info.FilesWritten, fp)
	}
	for fp := range stats.filesCreated {
		sess.Info.FilesCreated = append(sess.Info.FilesCreated, fp)
	}
	sort.Strings(sess.Info.FilesRead)
	sort.Strings(sess.Info.FilesWritten)
	sort.Strings(sess.Info.FilesCreated)

	for _, c := range stats.churn {
		sess.Info.LinesAdded += c.LinesAdded
		sess.Info.LinesRemoved += c.LinesRemoved
		sess.Info.FileChurns = append(sess.Info.FileChurns, *c)
	}
	sort.Slice(sess.Info.FileChurns, func(i, j int) bool {
		a, b := sess.Info.FileChurns[i], sess.Info.FileChurns[j]
		if a.LinesAdded+a.LinesRemoved != b.LinesAdded+b.LinesRemoved {
			return a.LinesAdded+a.LinesRemoved > b.LinesAdded+b.LinesRemoved
		}
		return a.Path < b.Path
	})
}
