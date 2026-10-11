package session

// Milo (github.com/fooxytv/ProjectMilo) is a self-hosted agent server. It
// writes two things worth reading here, both append-only JSONL:
//
//	sessions/<id>.jsonl            one chat session
//	sessions/runs/<run>/events.jsonl   one delivery run
//
// The shape is closer to Claude Code's transcript than to OpenCode's database
// — a line per event, each with a `type` and a float unix `ts` — so this
// parser is small. What it does not share is Claude Code's message/content
// block structure: Milo logs one line per *event*, already flattened, so none
// of the per-block accumulation in parser.go applies.
//
// Facts worth not re-deriving, from agent/core.py and agent/runs.py:
//
//   - Token counts are NOT in the JSONL. They live in a sibling
//     `<id>.usage.json`, shaped {model: {prompt, completion}}, rewritten in
//     place as the session goes. A session with no such file is not an error;
//     it is one that never reached a model.
//   - `tool_result.result` is always a string: the server stringifies whatever
//     the tool returned before logging it.
//   - A confirm-gated tool logs `confirm` and then `confirm_response`, and the
//     tool_call only follows if it was approved. A denial is therefore the
//     absence of a call, not a failed one.
//   - Events from a sub-agent are tagged `nested: true` and are written into
//     the parent's log rather than a file of their own, so there is nothing to
//     splice the way linkSubagents does for Claude Code.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SourceMilo identifies sessions that came from a Milo server.
const SourceMilo = "milo"

// DefaultMiloDirs returns the directories to look for a Milo `sessions/` in.
//
// Milo usually runs in Docker with the repository bind-mounted, so the
// sessions directory is on the host beside the checkout rather than under a
// dot-directory in $HOME the way Claude Code and OpenCode are.
func DefaultMiloDirs() []string {
	var paths []string
	if dir := os.Getenv("MILO_SESSIONS_DIR"); dir != "" {
		paths = append(paths, dir)
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, "sessions"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, "ProjectMilo", "sessions"),
			filepath.Join(home, "milo", "sessions"),
		)
	}
	return paths
}

// miloEvent is one line of a Milo log. Every event type shares the file, so
// this is the union of their fields; absent ones decode to their zero value.
type miloEvent struct {
	Type string  `json:"type"`
	TS   float64 `json:"ts"`

	// chat
	Content  string         `json:"content"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args"`
	Result   string         `json:"result"`
	Model    string         `json:"model"`
	Approved *bool          `json:"approved"`
	Kind     string         `json:"kind"`
	Nested   bool           `json:"nested"`

	// runs
	Goal       string   `json:"goal"`
	Stages     []string `json:"stages"`
	Stage      string   `json:"stage"`
	Status     string   `json:"status"`
	ExitCode   *int     `json:"exit_code"`
	Decision   string   `json:"decision"`
	Comment    string   `json:"comment"`
	ApprovalID string   `json:"approval_id"`
	Error      string   `json:"error"`
}

func miloTime(ts float64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	sec := int64(ts)
	return time.Unix(sec, int64((ts-float64(sec))*1e9))
}

// ParseMiloDir reads every chat session and delivery run under one Milo
// `sessions/` directory.
func ParseMiloDir(dir string) ([]*Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var sessions []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		s, err := ParseMiloSession(filepath.Join(dir, e.Name()))
		if err != nil || s == nil {
			continue // a half-written or unreadable log is skipped, not fatal
		}
		sessions = append(sessions, s)
	}

	runsDir := filepath.Join(dir, "runs")
	if runEntries, err := os.ReadDir(runsDir); err == nil {
		for _, e := range runEntries {
			if !e.IsDir() {
				continue
			}
			s, err := ParseMiloRun(filepath.Join(runsDir, e.Name()))
			if err != nil || s == nil {
				continue
			}
			sessions = append(sessions, s)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Info.LastUpdate.After(sessions[j].Info.LastUpdate)
	})
	return sessions, nil
}

func readMiloEvents(path string) ([]miloEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // match parser.go

	var out []miloEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev miloEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // the tail of a log being appended to can be a partial line
		}
		out = append(out, ev)
	}
	return out, scanner.Err()
}

// ParseMiloSession reads one sessions/<id>.jsonl.
func ParseMiloSession(path string) (*Session, error) {
	raw, err := readMiloEvents(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	sess := &Session{
		Info: SessionInfo{
			ID:          id,
			FilePath:    path,
			Source:      SourceMilo,
			ProjectDir:  filepath.Dir(path),
			ProjectName: miloProjectName(path),
			ToolCounts:  map[string]int{},
		},
		SubagentLaunches: map[string]string{},
	}

	for i, ev := range raw {
		sess.Events = append(sess.Events, miloChatEvent(ev, id, i)...)
		if ev.Model != "" {
			sess.Info.Model = ev.Model
		}
	}

	applyMiloUsage(sess, filepath.Join(filepath.Dir(path),
		strings.TrimSuffix(filepath.Base(path), ".jsonl")+".usage.json"))
	finishMiloSession(sess)
	return sess, nil
}

// miloChatEvent maps one logged event onto verbose's event model.
//
// The mapping is deliberately lossy in one direction only: anything without a
// natural counterpart becomes EventSystem with a readable line, rather than
// being dropped, so the timeline still accounts for every line on disk.
func miloChatEvent(ev miloEvent, sessionID string, idx int) []Event {
	ts := miloTime(ev.TS)
	base := Event{Timestamp: ts, UUID: fmt.Sprintf("%s-%d", sessionID, idx)}

	switch ev.Type {
	case "user":
		base.Type = EventUserPrompt
		base.UserText = ev.Content
	case "message":
		base.Type = EventText
		base.Text = ev.Content
	case "thinking":
		base.Type = EventThinking
		base.Thinking = ev.Content
	case "tool_call":
		base.Type = EventToolUse
		base.ToolName = ev.Name
		base.ToolInput = ev.Args
		base.ToolID = fmt.Sprintf("%s-%d", sessionID, idx)
		base.DurationMs = -1 // set by pairing below, if a result follows
	case "tool_result":
		base.Type = EventToolResult
		base.ToolName = ev.Name
		base.ToolOutput = ev.Result
		base.IsError = strings.HasPrefix(ev.Result, "error:") ||
			strings.HasPrefix(ev.Result, "⚠")
	case "confirm":
		base.Type = EventSystem
		base.Text = fmt.Sprintf("awaiting confirmation: %s", ev.Name)
	case "confirm_response":
		// A denial is the interesting half: the tool_call never follows, so
		// without this the transcript would simply go quiet.
		if ev.Approved != nil && !*ev.Approved {
			base.Type = EventToolDenied
			base.ToolName = ev.Name
			base.Text = fmt.Sprintf("denied: %s", ev.Name)
		} else {
			base.Type = EventSystem
			base.Text = fmt.Sprintf("approved: %s", ev.Name)
		}
	case "session_start":
		base.Type = EventSystem
		base.Text = fmt.Sprintf("session started · model %s", ev.Model)
	case "recovery":
		base.Type = EventSystem
		base.Text = "recovered: " + ev.Kind
		if ev.Name != "" {
			base.Text += " (" + ev.Name + ")"
		}
	case "cancelled":
		base.Type = EventSystem
		base.Text = "turn cancelled"
	case "clear":
		base.Type = EventCompaction
		base.Text = ev.Content
		if base.Text == "" {
			base.Text = "context cleared"
		}
	default:
		base.Type = EventSystem
		base.Text = ev.Type
	}
	return []Event{base}
}

// ParseMiloRun reads one sessions/runs/<run_id>/ as a session.
//
// A run is a job rather than a conversation, but it has the same shape for
// reading purposes — an ordered event log with a goal at the top — so it is
// presented as a session whose prompt is the goal. That puts runs in the same
// list as everything else, which is the point of reading them here at all.
func ParseMiloRun(dir string) (*Session, error) {
	raw, err := readMiloEvents(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	id := filepath.Base(dir)
	sess := &Session{
		Info: SessionInfo{
			ID:          id,
			FilePath:    filepath.Join(dir, "events.jsonl"),
			Source:      SourceMilo,
			ProjectDir:  dir,
			ProjectName: miloProjectName(dir),
			ToolCounts:  map[string]int{},
		},
		SubagentLaunches: map[string]string{},
	}

	// run.json carries the goal and the stage table authoritatively; the event
	// log is the history of how it got there. Read the first for the title,
	// and fall back to the log when it is missing.
	if b, err := os.ReadFile(filepath.Join(dir, "run.json")); err == nil {
		var meta struct {
			Goal   string `json:"goal"`
			Status string `json:"status"`
			Stages []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"stages"`
		}
		if json.Unmarshal(b, &meta) == nil {
			sess.Info.Title = meta.Goal
			for _, st := range meta.Stages {
				if st.Model != "" {
					sess.Info.Model = st.Model
				}
			}
		}
	}

	for i, ev := range raw {
		ts := miloTime(ev.TS)
		e := Event{Timestamp: ts, UUID: fmt.Sprintf("%s-%d", id, i)}
		switch ev.Type {
		case "run_created":
			e.Type = EventUserPrompt
			e.UserText = ev.Goal
			if sess.Info.Title == "" {
				sess.Info.Title = ev.Goal
			}
		case "stage":
			e.Type = EventSystem
			e.Text = fmt.Sprintf("stage %s: %s", ev.Stage, ev.Status)
			if ev.Model != "" {
				e.Text += " · " + ev.Model
				sess.Info.Model = ev.Model
			}
			if ev.ExitCode != nil {
				e.Text += fmt.Sprintf(" · exit %d", *ev.ExitCode)
			}
		case "status":
			e.Type = EventSystem
			e.Text = "run " + ev.Status
			if ev.Error != "" {
				e.Text += ": " + ev.Error
				e.IsError = true
			}
		case "gate_opened":
			e.Type = EventSystem
			e.Text = "gate opened · awaiting approval"
		case "gate_decided":
			e.Type = EventSystem
			e.Text = "gate " + ev.Decision
			if ev.Comment != "" {
				e.Text += ": " + ev.Comment
			}
		default:
			e.Type = EventSystem
			e.Text = ev.Type
		}
		sess.Events = append(sess.Events, e)
	}

	finishMiloSession(sess)
	return sess, nil
}

// miloProjectName groups Milo's sessions in the list.
//
// Milo's sessions are not per-project the way Claude Code's are — one server
// serves everything — so the checkout directory above `sessions/` stands in,
// which keeps two servers on one machine apart.
func miloProjectName(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "runs" || strings.HasPrefix(filepath.Base(dir), "run_") {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) == "runs" {
		dir = filepath.Dir(dir)
	}
	parent := filepath.Base(filepath.Dir(dir))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return "milo"
	}
	return parent
}

// applyMiloUsage folds in <id>.usage.json, which is where the tokens are.
func applyMiloUsage(sess *Session, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return // a session that never reached a model has no usage file
	}
	var perModel map[string]struct {
		Prompt     int `json:"prompt"`
		Completion int `json:"completion"`
	}
	if json.Unmarshal(b, &perModel) != nil {
		return
	}
	for model, u := range perModel {
		sess.Info.InputTokens += u.Prompt
		sess.Info.OutputTokens += u.Completion
		if sess.Info.Model == "" {
			sess.Info.Model = model
		}
	}
}

// finishMiloSession fills in everything derived from the events: timings,
// counts, tool pairing and cost.
func finishMiloSession(sess *Session) {
	info := &sess.Info
	info.EventCount = len(sess.Events)

	// Pair each tool call with the next result for the same tool, so the
	// timeline can show how long an operation took. Milo logs them adjacently
	// and without ids, so position is the only link available.
	for i := range sess.Events {
		if sess.Events[i].Type != EventToolUse {
			continue
		}
		for j := i + 1; j < len(sess.Events); j++ {
			r := sess.Events[j]
			if r.Type != EventToolResult || r.ToolName != sess.Events[i].ToolName {
				continue
			}
			if !r.Timestamp.IsZero() && !sess.Events[i].Timestamp.IsZero() {
				sess.Events[i].DurationMs = int(r.Timestamp.Sub(sess.Events[i].Timestamp).Milliseconds())
			}
			break
		}
	}

	for _, e := range sess.Events {
		switch e.Type {
		case EventUserPrompt:
			info.UserPrompts++
		case EventToolUse:
			info.ToolCallCount++
			info.ToolCounts[e.ToolName]++
			if e.ToolName == "run_shell" || e.ToolName == "run_curl" {
				info.BashCommands++
			}
			if e.ToolName == "web_fetch" || e.ToolName == "web_search" {
				info.WebRequests++
			}
			if p, ok := e.ToolInput["path"].(string); ok && p != "" {
				switch e.ToolName {
				case "read_file":
					info.FilesRead = appendUnique(info.FilesRead, p)
				case "write_file":
					info.FilesWritten = appendUnique(info.FilesWritten, p)
				}
			}
		case EventToolResult:
			if e.IsError {
				info.Errors++
			}
		case EventToolDenied:
			info.Denials++
		}
		if !e.Timestamp.IsZero() {
			if info.StartTime.IsZero() || e.Timestamp.Before(info.StartTime) {
				info.StartTime = e.Timestamp
			}
			if e.Timestamp.After(info.LastUpdate) {
				info.LastUpdate = e.Timestamp
			}
		}
	}

	if info.Title == "" {
		info.Title = deriveTitle(sess.Events)
	}
	info.CostUSD = estimateCost(*info)
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
