package session

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Codex splits a session across TWO SQLite databases under ~/.codex, and
// neither is usable on its own: `state_N.sqlite` holds one `threads` row per
// session — the cwd, the model, the git branch, the token total, the title —
// while `thread_history_N.sqlite` holds the content as `thread_items` and the
// per-turn timing as `thread_turns`. The N is a schema generation, so both are
// found by glob and the highest generation wins; a hard-coded name would stop
// reading anything the day Codex migrates.
//
// A thread row with no items is a real row with nothing in it — a voice chat
// that never ran, an aborted fork — and is skipped like any empty session.

// DefaultCodexDirs returns the standard locations of a Codex home, most likely
// first. Paths are returned whether or not they exist.
func DefaultCodexDirs() []string {
	var dirs []string
	if d := os.Getenv("CODEX_HOME"); d != "" {
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".codex"))
	}
	return dirs
}

// CodexDBs picks the newest generation of each database in a Codex home. ok is
// false unless both were found: the metadata and the content are in different
// files and a session needs them both.
func CodexDBs(dir string) (state, history string, ok bool) {
	state = newestGeneration(filepath.Join(dir, "state_*.sqlite"))
	history = newestGeneration(filepath.Join(dir, "thread_history_*.sqlite"))
	return state, history, state != "" && history != ""
}

// newestGeneration returns the match whose trailing number is highest.
func newestGeneration(pattern string) string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return ""
	}
	best, bestGen := "", -1
	for _, m := range matches {
		if g := generationOf(m); g > bestGen {
			best, bestGen = m, g
		}
	}
	return best
}

// generationOf reads the schema generation off a file name: state_5.sqlite is
// 5. A name without one sorts lowest rather than failing.
func generationOf(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".sqlite")
	i := strings.LastIndexByte(base, '_')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(base[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// cxThread is one row of the `threads` table.
type cxThread struct {
	ID        string
	CWD       string
	Title     string // the opening message, verbatim and often many lines
	Name      string // the generated label, which is the one worth showing
	Model     string
	Provider  string
	Branch    string
	Tokens    int
	CreatedMs int64
	UpdatedMs int64
	Source    string // "vscode", or JSON naming the subagent that owns it
}

// cxItem is one row of `thread_items`: an item's payload plus its place in the
// thread.
type cxItem struct {
	ThreadID  string
	Type      string
	CreatedMs int64
	JSON      json.RawMessage
}

// cxItemJSON is the union of every item payload. Codex tags each one with
// `type`, and the fields of the others stay zero.
type cxItemJSON struct {
	Type string `json:"type"`

	// userMessage
	Content []cxText `json:"content"`

	// agentMessage
	Text string `json:"text"`

	// reasoning — `content` is always empty on disk; `summary` holds the
	// headline and nothing more.
	Summary []string `json:"summary"`

	// commandExecution
	Command          string `json:"command"`
	CWD              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
	DurationMs       int    `json:"durationMs"`
	Status           string `json:"status"`

	// fileChange
	Changes []cxChange `json:"changes"`

	// mcpToolCall
	Server    string                 `json:"server"`
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    *cxMCPResult           `json:"result"`

	// imageGeneration
	RevisedPrompt string `json:"revisedPrompt"`
}

type cxText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type cxMCPResult struct {
	Content []cxText `json:"content"`
}

// cxChange is one file inside a fileChange item. For an "add" the diff field
// holds the file's whole content; for an "update" it holds unified hunks —
// the same split Claude Code makes between a Write's input and a recorded
// structuredPatch, which is why both render without special-casing.
type cxChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

// ParseCodexDir reads a Codex home and returns its sessions. A home missing
// either database yields none, without an error: an installed Codex that has
// never run looks exactly like that.
func ParseCodexDir(dir string) ([]*Session, error) {
	state, history, ok := CodexDBs(dir)
	if !ok {
		return nil, nil
	}
	return ParseCodexDBs(state, history)
}

// ParseCodexDBs reads a named pair of Codex databases.
func ParseCodexDBs(statePath, historyPath string) ([]*Session, error) {
	state, err := openReadOnly(statePath)
	if err != nil {
		return nil, err
	}
	defer state.Close()

	hist, err := openReadOnly(historyPath)
	if err != nil {
		return nil, err
	}
	defer hist.Close()

	threads, err := loadCXThreads(state)
	if err != nil {
		return nil, err
	}
	items, err := loadCXItems(hist)
	if err != nil {
		return nil, err
	}
	turns := loadCXTurns(hist)
	parents := loadCXSpawnEdges(state)

	var sessions []*Session
	for _, t := range threads {
		its := items[t.ID]
		if len(its) == 0 {
			continue
		}
		sessions = append(sessions, buildCXSession(t, its, turns[t.ID], parents[t.ID], statePath))
	}
	return sessions, nil
}

// openReadOnly opens a SQLite file without taking a write lock on a database
// the owning application is still using.
func openReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
}

func loadCXThreads(db *sql.DB) ([]cxThread, error) {
	// created_at_ms and updated_at_ms were added later and are null on older
	// rows, where created_at and updated_at hold seconds instead.
	rows, err := db.Query(`
		SELECT id, cwd, title, name, model, model_provider, git_branch,
		       tokens_used, created_at, updated_at, created_at_ms, updated_at_ms,
		       source
		FROM threads
		ORDER BY updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cxThread
	for rows.Next() {
		var t cxThread
		var name, model, branch sql.NullString
		var createdMs, updatedMs sql.NullInt64
		var created, updated int64
		if err := rows.Scan(&t.ID, &t.CWD, &t.Title, &name, &model, &t.Provider, &branch,
			&t.Tokens, &created, &updated, &createdMs, &updatedMs,
			&t.Source); err != nil {
			continue
		}
		t.Name, t.Model, t.Branch = name.String, model.String, branch.String
		t.CreatedMs = cxMillis(createdMs, created)
		t.UpdatedMs = cxMillis(updatedMs, updated)
		out = append(out, t)
	}
	return out, rows.Err()
}

// cxMillis prefers the millisecond column and falls back to the seconds one.
func cxMillis(ms sql.NullInt64, secs int64) int64 {
	if ms.Valid && ms.Int64 > 0 {
		return ms.Int64
	}
	return secs * 1000
}

// loadCXItems returns every item grouped by thread, in the order it was
// recorded. rollout_ordinal is the thread's own sequence number and is what
// reconstructs the original order; the timestamp alone cannot, because items
// written inside one turn share it.
func loadCXItems(db *sql.DB) (map[string][]cxItem, error) {
	rows, err := db.Query(`
		SELECT thread_id, item_type, created_at_ms, item_json
		FROM thread_items
		ORDER BY thread_id, rollout_ordinal, created_at_ms, item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]cxItem)
	for rows.Next() {
		var it cxItem
		var body string
		if err := rows.Scan(&it.ThreadID, &it.Type, &it.CreatedMs, &body); err != nil {
			continue
		}
		it.JSON = json.RawMessage(body)
		out[it.ThreadID] = append(out[it.ThreadID], it)
	}
	return out, rows.Err()
}

// loadCXTurns returns the recorded duration of each thread's turns. Codex
// times a turn properly, so the active duration is a sum rather than the
// start-to-finish span OpenCode has to approximate.
func loadCXTurns(db *sql.DB) map[string][]int {
	rows, err := db.Query(`SELECT thread_id, duration_ms FROM thread_turns`)
	if err != nil {
		return map[string][]int{}
	}
	defer rows.Close()

	out := make(map[string][]int)
	for rows.Next() {
		var id string
		var ms sql.NullInt64
		if err := rows.Scan(&id, &ms); err != nil {
			continue
		}
		if ms.Valid && ms.Int64 > 0 {
			out[id] = append(out[id], int(ms.Int64))
		}
	}
	return out
}

// loadCXSpawnEdges maps a child thread to the thread that spawned it. The
// table is empty on a machine that has never run a Codex subagent, which is
// why its absence is not an error.
func loadCXSpawnEdges(db *sql.DB) map[string]string {
	rows, err := db.Query(`SELECT child_thread_id, parent_thread_id FROM thread_spawn_edges`)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			continue
		}
		out[child] = parent
	}
	return out
}

// buildCXSession turns one thread into a session.
func buildCXSession(t cxThread, items []cxItem, turns []int, parent, statePath string) *Session {
	sess := &Session{Info: SessionInfo{ID: "cx-" + t.ID, Source: "codex"}}
	sess.Info.ToolCounts = make(map[string]int)
	stats := newOCStats()

	for _, it := range items {
		var payload cxItemJSON
		if json.Unmarshal(it.JSON, &payload) != nil {
			continue
		}
		ts := ocTime(it.CreatedMs)
		sess.Events = append(sess.Events, cxItemEvents(payload, ts, sess, stats)...)
	}

	finishCXSession(sess, t, turns, parent, stats, statePath)
	return sess
}

// cxItemEvents converts a single item into zero or more timeline events,
// updating the session counters as it goes.
func cxItemEvents(it cxItemJSON, ts time.Time, sess *Session, stats *ocStats) []Event {
	switch it.Type {
	case "userMessage":
		text := cxJoinText(it.Content)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		sess.Info.UserPrompts++
		return []Event{{Type: EventUserPrompt, Timestamp: ts, UserText: text}}

	case "agentMessage":
		if strings.TrimSpace(it.Text) == "" {
			return nil
		}
		return []Event{{Type: EventText, Timestamp: ts, Text: it.Text}}

	case "reasoning":
		// Only the headline survives: `content` is empty on every item on
		// disk, and even `summary` is absent from most of them. It is still
		// more than a Claude Code transcript keeps, which is nothing at all.
		summary := strings.TrimSpace(strings.Join(it.Summary, "\n"))
		if summary == "" {
			return nil
		}
		return []Event{{Type: EventThinking, Timestamp: ts, Thinking: summary}}

	case "commandExecution":
		return []Event{cxCommandEvent(it, ts, sess)}

	case "fileChange":
		return cxFileChangeEvents(it, ts, sess, stats)

	case "mcpToolCall":
		return []Event{cxMCPEvent(it, ts, sess)}

	case "imageGeneration":
		sess.Info.ToolCallCount++
		sess.Info.ToolCounts["ImageGeneration"]++
		return []Event{{
			Type: EventToolUse, Timestamp: ts, ToolName: "ImageGeneration",
			ToolInput:  map[string]interface{}{"prompt": it.RevisedPrompt},
			DurationMs: -1,
			IsError:    it.Status == "failed",
		}}
	}
	return nil
}

// cxCommandEvent maps a shell command onto the Bash call the rest of the UI
// already knows how to show.
func cxCommandEvent(it cxItemJSON, ts time.Time, sess *Session) Event {
	in := map[string]interface{}{"command": it.Command}
	if it.CWD != "" {
		in["cwd"] = it.CWD
	}

	e := Event{
		Type: EventToolUse, Timestamp: ts, ToolName: "Bash",
		ToolInput:  in,
		ToolOutput: it.AggregatedOutput,
		DurationMs: -1,
	}
	if it.DurationMs > 0 {
		e.DurationMs = it.DurationMs
	}

	// A failed command records its exit code and writes the reason into the
	// same output field as a successful one, so the status is the signal.
	if it.Status == "failed" || (it.ExitCode != nil && *it.ExitCode != 0) {
		e.IsError = true
		e.Result = &ToolResult{Raw: it.AggregatedOutput}
		sess.Info.Errors++
	} else if it.AggregatedOutput != "" {
		e.Result = &ToolResult{Stdout: it.AggregatedOutput}
	}

	sess.Info.ToolCallCount++
	sess.Info.ToolCounts["Bash"]++
	sess.Info.BashCommands++
	return e
}

// cxFileChangeEvents expands one fileChange into an event per file, each
// carrying that file's own change. One Codex item can rewrite several files,
// and Claude Code records one edit per file, so matching that shape lets the
// existing diff rendering apply unchanged.
func cxFileChangeEvents(it cxItemJSON, ts time.Time, sess *Session, stats *ocStats) []Event {
	if len(it.Changes) == 0 {
		return nil
	}

	sess.Info.ToolCallCount++
	counted := "Edit"
	if len(it.Changes) > 1 {
		counted = "MultiEdit"
	} else if it.Changes[0].Kind.Type == "add" {
		counted = "Write"
	}
	sess.Info.ToolCounts[counted]++

	events := make([]Event, 0, len(it.Changes))
	for _, c := range it.Changes {
		e := Event{Type: EventToolUse, Timestamp: ts, DurationMs: -1}
		in := map[string]interface{}{"file_path": c.Path}

		if c.Kind.Type == "add" {
			// There is no "before", so the diff field carries the content
			// instead. Handing it over as a Write's input is what lets the
			// replay type the new file out.
			e.ToolName = "Write"
			in["content"] = c.Diff
			e.ToolInput = in
			e.Result = &ToolResult{FilePath: c.Path}
			stats.filesCreated[c.Path] = true
			churnFor(stats.churn, c.Path).Edits++
			events = append(events, e)
			continue
		}

		e.ToolName = "Edit"
		e.ToolInput = in
		e.Result = &ToolResult{FilePath: c.Path, StructuredPatch: parseCXHunks(c.Diff)}

		added, removed := e.Result.Churn()
		e.LinesAdded, e.LinesRemoved = added, removed

		ch := churnFor(stats.churn, c.Path)
		ch.Edits++
		ch.LinesAdded += added
		ch.LinesRemoved += removed
		stats.filesWritten[c.Path] = true

		events = append(events, e)
	}
	return events
}

// cxMCPEvent names an MCP call the way Claude Code does, so the tool column
// and the filters read the same for both.
func cxMCPEvent(it cxItemJSON, ts time.Time, sess *Session) Event {
	name := it.Tool
	if it.Server != "" {
		name = "mcp__" + it.Server + "__" + it.Tool
	}

	e := Event{
		Type: EventToolUse, Timestamp: ts, ToolName: name,
		ToolInput:  it.Arguments,
		DurationMs: -1,
	}
	if it.DurationMs > 0 {
		e.DurationMs = it.DurationMs
	}
	if it.Result != nil {
		e.ToolOutput = cxJoinText(it.Result.Content)
	}
	if it.Status == "failed" {
		e.IsError = true
		e.Result = &ToolResult{Raw: e.ToolOutput}
		sess.Info.Errors++
	} else if e.ToolOutput != "" {
		e.Result = &ToolResult{Stdout: e.ToolOutput}
	}

	sess.Info.ToolCallCount++
	sess.Info.ToolCounts[name]++
	return e
}

// parseCXHunks reads the unified hunks of an update. Codex names the file on
// the change itself, so there are no ---/+++ headers to skip.
func parseCXHunks(diff string) []PatchHunk {
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	var (
		hunks []PatchHunk
		cur   *PatchHunk
	)
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			flush()
			h, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			cur = &h
			continue
		}
		if cur == nil {
			continue
		}
		// A trailing empty line is the split artefact, not a context line.
		if line == "" {
			continue
		}
		cur.Lines = append(cur.Lines, line)
	}
	flush()

	return hunks
}

// finishCXSession fills in the metadata and rolls up the counters.
func finishCXSession(sess *Session, t cxThread, turns []int, parent string, stats *ocStats, statePath string) {
	sess.Info.CWD = t.CWD
	sess.Info.ProjectDir = t.CWD
	if t.CWD != "" {
		sess.Info.ProjectName = filepath.Base(t.CWD)
	}
	sess.Info.FilePath = statePath
	sess.Info.GitBranch = t.Branch
	sess.Info.StartTime = ocTime(t.CreatedMs)
	sess.Info.LastUpdate = ocTime(t.UpdatedMs)
	sess.Info.Model = joinOCModel(t.Provider, t.Model)
	sess.Info.EventCount = len(sess.Events)

	// Codex reports one token number per thread and nothing finer, so there is
	// no input/output split to show and faking one into the input column would
	// be a lie the overview would then draw a bar for.
	sess.Info.TokensTotal = t.Tokens

	// `name` is the label Codex generates for the thread; `title` is the
	// opening message verbatim, which on a real thread is several lines of
	// pasted context or an XML delegation wrapper. The generated one is the
	// only one that reads as a title.
	switch {
	case strings.TrimSpace(t.Name) != "":
		sess.Info.Title = strings.TrimSpace(t.Name)
	case strings.TrimSpace(t.Title) != "":
		sess.Info.Title = firstLineOf(t.Title)
	default:
		sess.Info.Title = deriveTitle(sess.Events)
	}

	if parent != "" {
		sess.Info.IsAgent = true
		sess.Info.ParentSessionID = "cx-" + parent
	}
	// AgentType is what a subagent RAN AS, so only a thread Codex owns as one
	// gets it. thread_source also records how a thread was started
	// (voice_chat, chatgpt_handoff) and putting that here would report an
	// agent type for sessions that are not agents.
	if agent := cxSubagentName(t.Source); agent != "" {
		sess.Info.IsAgent = true
		sess.Info.AgentType = agent
	}

	for _, ms := range turns {
		sess.Info.ActiveDuration += time.Duration(ms) * time.Millisecond
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

// cxSubagentName reads the agent out of a thread's source column, which is
// either a plain string ("vscode") or JSON naming the subagent that owns the
// thread: {"subagent":{"other":"guardian"}}.
func cxSubagentName(source string) string {
	if !strings.HasPrefix(strings.TrimSpace(source), "{") {
		return ""
	}
	var s struct {
		Subagent map[string]string `json:"subagent"`
	}
	if json.Unmarshal([]byte(source), &s) != nil {
		return ""
	}
	for _, v := range s.Subagent {
		if v != "" {
			return v
		}
	}
	return ""
}

// cxJoinText flattens a content array to the text a reader sees.
func cxJoinText(parts []cxText) string {
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
