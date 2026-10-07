package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// OpenCode keeps everything in one global SQLite database rather than a file
// per session: `session` rows carry the metadata, `message` rows the turn
// envelope, and `part` rows the actual content. A session's transcript is the
// parts of its messages, ordered by message time then part ID — part IDs are
// monotonic, so that reconstructs the original order exactly.

// DefaultOpenCodeDBs returns the standard locations of the OpenCode database,
// most likely first. Paths are returned whether or not they exist.
func DefaultOpenCodeDBs() []string {
	var paths []string
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		paths = append(paths, filepath.Join(dir, "opencode", "opencode.db"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
			filepath.Join(home, "Library", "Application Support", "opencode", "opencode.db"),
		)
	}
	return paths
}

// ocSessionRow is one row of the `session` table.
type ocSessionRow struct {
	ID        string
	ProjectID string
	ParentID  string
	Title     string
	Directory string
	Model     string
	Agent     string
	Cost      float64
	TokIn     int
	TokOut    int
	TokCacheR int
	TokCacheW int
	Created   int64
	Updated   int64
}

// ocMessageRow is one row of the `message` table, with `data` already decoded.
type ocMessageRow struct {
	ID      string
	Created int64
	Role    string
	Model   string
}

// ocMessageData is the JSON blob on a message row.
type ocMessageData struct {
	Role       string `json:"role"`
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Model      *struct {
		ModelID    string `json:"modelID"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
}

// ocPartRow is one row of the `part` table.
type ocPartRow struct {
	ID        string
	MessageID string
	Created   int64
	Data      json.RawMessage
}

// ocPart is the common envelope of every part blob; only Type is always set.
type ocPart struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// type == "tool"
	Tool   string      `json:"tool"`
	CallID string      `json:"callID"`
	State  ocToolState `json:"state"`

	// type == "reasoning" | "tool"
	Time *ocTimeSpan `json:"time"`
}

type ocTimeSpan struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type ocToolState struct {
	Status   string                 `json:"status"`
	Input    map[string]interface{} `json:"input"`
	Output   string                 `json:"output"`
	Error    string                 `json:"error"`
	Title    string                 `json:"title"`
	Metadata ocToolMetadata         `json:"metadata"`
	Time     *ocTimeSpan            `json:"time"`
}

type ocToolMetadata struct {
	Diff      string `json:"diff"`
	SessionID string `json:"sessionId"`
	Output    string `json:"output"`
}

// ParseOpenCodeDB reads an OpenCode database and returns its top-level
// sessions. Subagent sessions are folded into the parent that spawned them,
// the same way Claude Code subagent transcripts are spliced in by
// Store.linkSubagents.
func ParseOpenCodeDB(dbPath string) ([]*Session, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	worktrees, err := loadOCProjects(db)
	if err != nil {
		return nil, err
	}
	rows, err := loadOCSessions(db)
	if err != nil {
		return nil, err
	}
	messages, err := loadOCMessages(db)
	if err != nil {
		return nil, err
	}
	parts, err := loadOCParts(db)
	if err != nil {
		return nil, err
	}
	todos, err := loadOCTodos(db)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]ocSessionRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	var sessions []*Session
	for _, row := range rows {
		// Children are emitted inside their parent, not on their own.
		if row.ParentID != "" {
			if _, ok := byID[row.ParentID]; ok {
				continue
			}
		}
		sess := buildOCSession(row, byID, messages, parts, worktrees, todos, dbPath)
		if len(sess.Events) == 0 {
			continue
		}
		sessions = append(sessions, sess)
	}

	return sessions, nil
}

func loadOCProjects(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT id, worktree FROM project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var id string
		var worktree sql.NullString
		if err := rows.Scan(&id, &worktree); err != nil {
			continue
		}
		out[id] = worktree.String
	}
	return out, rows.Err()
}

func loadOCSessions(db *sql.DB) ([]ocSessionRow, error) {
	rows, err := db.Query(`
		SELECT id, project_id, parent_id, title, directory, model, agent,
		       cost, tokens_input, tokens_output, tokens_cache_read, tokens_cache_write,
		       time_created, time_updated
		FROM session
		ORDER BY time_created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ocSessionRow
	for rows.Next() {
		var r ocSessionRow
		var parent, title, dir, model, agent sql.NullString
		if err := rows.Scan(&r.ID, &r.ProjectID, &parent, &title, &dir, &model, &agent,
			&r.Cost, &r.TokIn, &r.TokOut, &r.TokCacheR, &r.TokCacheW,
			&r.Created, &r.Updated); err != nil {
			continue
		}
		r.ParentID, r.Title, r.Directory = parent.String, title.String, dir.String
		r.Model, r.Agent = model.String, agent.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadOCMessages returns messages grouped by session, in chronological order.
func loadOCMessages(db *sql.DB) (map[string][]ocMessageRow, error) {
	rows, err := db.Query(`SELECT id, session_id, time_created, data FROM message ORDER BY session_id, time_created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]ocMessageRow)
	for rows.Next() {
		var id, sessionID, data string
		var created int64
		if err := rows.Scan(&id, &sessionID, &created, &data); err != nil {
			continue
		}
		var d ocMessageData
		_ = json.Unmarshal([]byte(data), &d)

		m := ocMessageRow{ID: id, Created: created, Role: d.Role}
		switch {
		case d.ModelID != "":
			m.Model = joinOCModel(d.ProviderID, d.ModelID)
		case d.Model != nil && d.Model.ModelID != "":
			m.Model = joinOCModel(d.Model.ProviderID, d.Model.ModelID)
		}
		out[sessionID] = append(out[sessionID], m)
	}
	return out, rows.Err()
}

// loadOCParts returns parts grouped by message, in the order they were written.
func loadOCParts(db *sql.DB) (map[string][]ocPartRow, error) {
	rows, err := db.Query(`SELECT id, message_id, time_created, data FROM part ORDER BY message_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]ocPartRow)
	for rows.Next() {
		var p ocPartRow
		var data string
		if err := rows.Scan(&p.ID, &p.MessageID, &p.Created, &data); err != nil {
			continue
		}
		p.Data = json.RawMessage(data)
		out[p.MessageID] = append(out[p.MessageID], p)
	}
	return out, rows.Err()
}

func loadOCTodos(db *sql.DB) (map[string][]TodoItem, error) {
	rows, err := db.Query(`SELECT session_id, content, status FROM todo ORDER BY session_id, position`)
	if err != nil {
		// The todo table is recent; its absence is not fatal.
		return map[string][]TodoItem{}, nil
	}
	defer rows.Close()

	out := make(map[string][]TodoItem)
	for rows.Next() {
		var sessionID, content, status string
		if err := rows.Scan(&sessionID, &content, &status); err != nil {
			continue
		}
		out[sessionID] = append(out[sessionID], TodoItem{Subject: content, Status: status})
	}
	return out, rows.Err()
}

// ocStats accumulates the per-session counters while events are emitted.
type ocStats struct {
	filesRead    map[string]bool
	filesWritten map[string]bool
	filesCreated map[string]bool
	churn        map[string]*FileChurn
}

func newOCStats() *ocStats {
	return &ocStats{
		filesRead:    make(map[string]bool),
		filesWritten: make(map[string]bool),
		filesCreated: make(map[string]bool),
		churn:        make(map[string]*FileChurn),
	}
}

func buildOCSession(
	row ocSessionRow,
	byID map[string]ocSessionRow,
	messages map[string][]ocMessageRow,
	parts map[string][]ocPartRow,
	worktrees map[string]string,
	todos map[string][]TodoItem,
	dbPath string,
) *Session {
	// Index children so a task call can splice its subagent's turns inline.
	children := make(map[string]ocSessionRow)
	for _, c := range byID {
		if c.ParentID == row.ID {
			children[c.ID] = c
		}
	}

	sess := &Session{Info: SessionInfo{ID: "oc-" + row.ID, Source: "opencode"}}
	sess.Info.ToolCounts = make(map[string]int)
	stats := newOCStats()

	sess.Events = emitOCSession(row, messages, parts, children, sess, stats, false)

	// A subagent's own todo list belongs to the parent's view of the work.
	sess.Todos = append(sess.Todos, todos[row.ID]...)
	for id := range children {
		sess.Todos = append(sess.Todos, todos[id]...)
	}

	finishOCSession(sess, row, worktrees, stats, dbPath)
	return sess
}

// emitOCSession turns one session's messages into events. Child sessions are
// recursed into at the task call that launched them, flagged as sidechain.
func emitOCSession(
	row ocSessionRow,
	messages map[string][]ocMessageRow,
	parts map[string][]ocPartRow,
	children map[string]ocSessionRow,
	sess *Session,
	stats *ocStats,
	sidechain bool,
) []Event {
	var events []Event

	for _, msg := range messages[row.ID] {
		if sess.Info.Model == "" && msg.Model != "" {
			sess.Info.Model = msg.Model
		}

		for _, p := range parts[msg.ID] {
			var part ocPart
			if json.Unmarshal(p.Data, &part) != nil {
				continue
			}

			ts := ocTime(firstNonZero(p.Created, msg.Created))
			produced := ocPartEvents(part, msg.Role, ts, sess, stats)
			for i := range produced {
				produced[i].IsSidechain = sidechain
				if sidechain {
					sess.Info.SubagentEvents++
				}
			}
			events = append(events, produced...)

			// Splice the subagent's transcript in right after its task call.
			if part.Type == "tool" && part.State.Metadata.SessionID != "" {
				child, ok := children[part.State.Metadata.SessionID]
				if !ok {
					continue
				}
				events = append(events,
					emitOCSession(child, messages, parts, children, sess, stats, true)...)
			}
		}
	}

	return events
}

// ocPartEvents converts a single part into zero or more timeline events,
// updating the session counters as it goes.
func ocPartEvents(part ocPart, role string, ts time.Time, sess *Session, stats *ocStats) []Event {
	switch part.Type {
	case "text":
		if strings.TrimSpace(part.Text) == "" {
			return nil
		}
		if role == "user" {
			sess.Info.UserPrompts++
			return []Event{{Type: EventUserPrompt, Timestamp: ts, UserText: part.Text}}
		}
		return []Event{{Type: EventText, Timestamp: ts, Text: part.Text}}

	case "reasoning":
		if strings.TrimSpace(part.Text) == "" {
			return nil
		}
		e := Event{Type: EventThinking, Timestamp: ts, Thinking: part.Text}
		if part.Time != nil {
			e.Timestamp = ocTime(firstNonZero(part.Time.Start, ts.UnixMilli()))
			e.DurationMs = int(part.Time.End - part.Time.Start)
		}
		return []Event{e}

	case "tool":
		return ocToolEvents(part, ts, sess, stats)
	}

	// step-start, step-finish, patch and agent parts carry no timeline content
	// of their own: the surrounding tool and text parts already show the work.
	return nil
}

func ocToolEvents(part ocPart, ts time.Time, sess *Session, stats *ocStats) []Event {
	st := part.State
	name := normalizeOCToolName(part.Tool)
	input := normalizeOCToolInput(st.Input)

	base := Event{
		Type:       EventToolUse,
		Timestamp:  ts,
		ToolName:   name,
		ToolInput:  input,
		ToolID:     part.CallID,
		DurationMs: -1,
	}
	if st.Time != nil {
		if st.Time.Start > 0 {
			base.Timestamp = ocTime(st.Time.Start)
		}
		if st.Time.End > st.Time.Start {
			base.DurationMs = int(st.Time.End - st.Time.Start)
		}
	}

	if st.Status == "error" {
		base.IsError = true
		base.ToolOutput = st.Error
		base.Result = &ToolResult{Raw: st.Error}
		sess.Info.Errors++
	} else {
		base.ToolOutput = st.Output
	}

	// A file-editing call is reported as one event per file it touched, but is
	// still a single call as far as the counters are concerned.
	if name == "Write" || name == "Edit" || name == "MultiEdit" {
		events, counted := ocFileEditEvents(base, st, name, stats)
		sess.Info.ToolCallCount++
		sess.Info.ToolCounts[counted]++
		return events
	}

	sess.Info.ToolCallCount++
	sess.Info.ToolCounts[name]++

	switch name {
	case "Read":
		if fp, ok := stringInput(input, "file_path"); ok {
			stats.filesRead[fp] = true
			if base.Result == nil {
				base.Result = &ToolResult{File: &ReadFile{FilePath: fp}}
			}
		}
	case "Bash":
		sess.Info.BashCommands++
		if base.Result == nil && st.Output != "" {
			base.Result = &ToolResult{Stdout: st.Output}
		}
	case "Task":
		sess.Info.SubagentCalls++
	case "WebFetch", "WebSearch":
		sess.Info.WebRequests++
	}

	return []Event{base}
}

// ocFileEditEvents expands a file-mutating call into one event per file it
// touched, each carrying that file's own diff. OpenCode's apply_patch can
// rewrite several files in a single call; Claude Code records one Edit per
// file, and matching that shape lets the existing diff rendering apply. The
// second return value is the name the call itself should be counted under.
func ocFileEditEvents(base Event, st ocToolState, name string, stats *ocStats) ([]Event, string) {
	diffs := parseOCDiff(st.Metadata.Diff)

	// No usable diff: fall back to a single event against the stated path.
	if len(diffs) == 0 {
		fp, _ := stringInput(base.ToolInput, "file_path")
		if fp != "" {
			if name == "Write" {
				stats.filesCreated[fp] = true
			} else {
				stats.filesWritten[fp] = true
			}
			churnFor(stats.churn, fp).Edits++
			if base.Result == nil {
				base.Result = &ToolResult{FilePath: fp}
			} else {
				base.Result.FilePath = fp
			}
		}
		return []Event{base}, name
	}

	counted := "Edit"
	if len(diffs) > 1 {
		counted = "MultiEdit"
	}

	events := make([]Event, 0, len(diffs))
	for _, d := range diffs {
		e := base

		// A diff with nothing on the old side created the file.
		created := d.createsFile()
		e.ToolName = "Edit"
		if created {
			e.ToolName = "Write"
		}

		// Each event addresses exactly one file.
		in := make(map[string]interface{}, len(base.ToolInput)+1)
		for k, v := range base.ToolInput {
			in[k] = v
		}
		delete(in, "patchText")
		in["file_path"] = d.Path
		e.ToolInput = in

		e.Result = &ToolResult{FilePath: d.Path, StructuredPatch: d.Hunks}
		if base.Result != nil {
			e.Result.Raw = base.Result.Raw
		}

		added, removed := e.Result.Churn()
		e.LinesAdded, e.LinesRemoved = added, removed

		c := churnFor(stats.churn, d.Path)
		c.Edits++
		c.LinesAdded += added
		c.LinesRemoved += removed

		if created {
			stats.filesCreated[d.Path] = true
		} else {
			stats.filesWritten[d.Path] = true
		}

		events = append(events, e)
	}

	if len(diffs) == 1 && events[0].ToolName == "Write" {
		counted = "Write"
	}
	return events, counted
}

// finishOCSession fills in the session metadata and rolls up the counters.
func finishOCSession(sess *Session, row ocSessionRow, worktrees map[string]string, stats *ocStats, dbPath string) {
	dir := row.Directory
	worktree := worktrees[row.ProjectID]
	// The "global" project is rooted at "/", which is not a useful project name.
	if worktree == "" || worktree == "/" {
		worktree = dir
	}

	sess.Info.ProjectDir = worktree
	sess.Info.ProjectName = filepath.Base(worktree)
	sess.Info.CWD = dir
	sess.Info.FilePath = dbPath
	sess.Info.StartTime = ocTime(row.Created)
	sess.Info.LastUpdate = ocTime(row.Updated)
	sess.Info.InputTokens = row.TokIn
	sess.Info.OutputTokens = row.TokOut
	sess.Info.CacheReadTokens = row.TokCacheR
	sess.Info.CacheWriteTokens = row.TokCacheW
	sess.Info.CostUSD = row.Cost
	sess.Info.EventCount = len(sess.Events)

	if sess.Info.Model == "" {
		sess.Info.Model = parseOCModelJSON(row.Model)
	}

	// OpenCode names a session before it has any content; that placeholder says
	// less than the opening prompt does.
	sess.Info.Title = strings.TrimSpace(row.Title)
	if sess.Info.Title == "" || strings.HasPrefix(sess.Info.Title, "New session - ") {
		sess.Info.Title = deriveTitle(sess.Events)
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

	// Turn timing is not recorded per turn, so approximate it from the span the
	// session was actually producing output.
	if !sess.Info.StartTime.IsZero() && sess.Info.LastUpdate.After(sess.Info.StartTime) {
		sess.Info.ActiveDuration = sess.Info.LastUpdate.Sub(sess.Info.StartTime)
	}
}

// normalizeOCToolName maps OpenCode's tool names onto the Claude Code names the
// rest of the UI keys off, so filters, icons and diff rendering all apply.
func normalizeOCToolName(tool string) string {
	switch tool {
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	case "patch", "apply_patch":
		return "MultiEdit"
	case "bash":
		return "Bash"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	case "list", "ls":
		return "LS"
	case "webfetch":
		return "WebFetch"
	case "websearch":
		return "WebSearch"
	case "task":
		return "Task"
	case "todowrite":
		return "TodoWrite"
	case "todoread":
		return "TodoRead"
	}
	return tool
}

// normalizeOCToolInput renames OpenCode's argument keys to their Claude Code
// equivalents. The map is copied rather than mutated in place.
func normalizeOCToolInput(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		switch k {
		case "filePath":
			out["file_path"] = v
		case "oldString":
			out["old_string"] = v
		case "newString":
			out["new_string"] = v
		case "replaceAll":
			out["replace_all"] = v
		case "notebookPath":
			out["notebook_path"] = v
		default:
			out[k] = v
		}
	}
	return out
}

// ocFileDiff is one file's worth of hunks out of a multi-file diff.
type ocFileDiff struct {
	Path  string
	Hunks []PatchHunk
}

// createsFile reports whether the diff adds a file rather than changing one:
// every hunk starts from an empty old side.
func (d ocFileDiff) createsFile() bool {
	if len(d.Hunks) == 0 {
		return false
	}
	for _, h := range d.Hunks {
		if h.OldLines != 0 {
			return false
		}
	}
	return true
}

// parseOCDiff reads the unified diff OpenCode attaches to a file-editing tool
// call. Multi-file patches separate files with "Index: <path>" headers.
func parseOCDiff(diff string) []ocFileDiff {
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	var (
		files []ocFileDiff
		cur   *ocFileDiff
		hunk  *PatchHunk
	)

	flushHunk := func() {
		if hunk != nil && cur != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil && len(cur.Hunks) > 0 {
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "Index: "):
			flushFile()
			cur = &ocFileDiff{Path: strings.TrimSpace(strings.TrimPrefix(line, "Index: "))}

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			if cur == nil {
				cur = &ocFileDiff{}
			}
			h, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			hunk = &h

		case strings.HasPrefix(line, "==="), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			// Diff headers carry no content.

		default:
			if hunk == nil {
				continue
			}
			// A trailing empty line is the split artefact, not a context line.
			if line == "" {
				continue
			}
			hunk.Lines = append(hunk.Lines, line)
		}
	}
	flushFile()

	// A single-file diff produced by `edit` has no Index header; recover the
	// path from the "--- " header instead.
	if len(files) == 1 && files[0].Path == "" {
		files[0].Path = firstDiffPath(diff)
	}

	return files
}

// parseHunkHeader reads "@@ -oldStart,oldLines +newStart,newLines @@".
func parseHunkHeader(line string) (PatchHunk, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return PatchHunk{}, false
	}
	oldStart, oldLines, ok := parseHunkRange(strings.TrimPrefix(fields[1], "-"))
	if !ok {
		return PatchHunk{}, false
	}
	newStart, newLines, ok := parseHunkRange(strings.TrimPrefix(fields[2], "+"))
	if !ok {
		return PatchHunk{}, false
	}
	return PatchHunk{
		OldStart: oldStart, OldLines: oldLines,
		NewStart: newStart, NewLines: newLines,
	}, true
}

// parseHunkRange reads "start,count", where a missing count means one line.
func parseHunkRange(s string) (start, count int, ok bool) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return 0, 0, false
		}
		count = n
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, false
	}
	return n, count, true
}

// firstDiffPath returns the path named by the first "--- " header.
func firstDiffPath(diff string) string {
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "--- ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "--- "))
		}
	}
	return ""
}

// parseOCModelJSON decodes the session row's model column, which holds
// {"id":"...","providerID":"..."}.
func parseOCModelJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var m struct {
		ID         string `json:"id"`
		ModelID    string `json:"modelID"`
		ProviderID string `json:"providerID"`
	}
	if json.Unmarshal([]byte(s), &m) != nil {
		return s
	}
	id := m.ID
	if id == "" {
		id = m.ModelID
	}
	if id == "" {
		return ""
	}
	return joinOCModel(m.ProviderID, id)
}

func joinOCModel(provider, model string) string {
	if provider == "" {
		return model
	}
	return fmt.Sprintf("%s/%s", provider, model)
}

// ocTime converts OpenCode's Unix milliseconds to a time.Time.
func ocTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func firstNonZero(vals ...int64) int64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
