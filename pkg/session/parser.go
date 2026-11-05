package session

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// maxLineBytes is the longest transcript line read. A longer one stops the
// read there, as bufio.Scanner does.
const maxLineBytes = 10 * 1024 * 1024

// ParseSessionFile reads a JSONL transcript and returns a fully parsed Session.
func ParseSessionFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p := newTranscriptParser(path)
	p.read(f)
	// Nothing else holds p, so the session can take its events as they are.
	return p.session(false), nil
}

// transcriptParser is a transcript read so far: everything gathered line by
// line, before the whole-session passes (pairing calls with results, totals,
// the title) are made.
//
// It exists so that a transcript being appended to can be read from where it
// was left, rather than from the start. A long session's file runs to hundreds
// of megabytes, and re-parsing all of it on every write kept a core busy for as
// long as the session was live. ParseSessionFile is the same parser fed the
// whole file at once, so the two cannot disagree.
type transcriptParser struct {
	// The fields gathered line by line. Totals that need the whole session
	// (tokens, churn lines, cost, title, file lists) are left zero and filled
	// in by session.
	info SessionInfo
	// Events as parsed, before tool results are folded into their calls.
	events []Event

	// Claude Code writes one JSONL line per content block, with every block of
	// a single assistant message sharing that message's ID. Usage is repeated
	// verbatim on each of those lines, so tokens are accumulated once per
	// message ID while events are emitted from every line.
	usageByMessage map[string]rawUsage

	filesRead    map[string]bool
	filesWritten map[string]bool
	filesCreated map[string]bool
	launches     map[string]string
	churn        map[string]*FileChurn // edit counts only; lines come from results
	skills       map[string]bool

	// offset is how many bytes of the file have been consumed: always the end
	// of a complete line, so the next read starts at the start of one.
	offset int64
}

func newTranscriptParser(path string) *transcriptParser {
	basename := filepath.Base(path)
	projectDir, parentID := locate(path)

	return &transcriptParser{
		info: SessionInfo{
			ID:              strings.TrimSuffix(basename, ".jsonl"),
			ProjectDir:      projectDir,
			ProjectName:     filepath.Base(projectDir),
			FilePath:        path,
			IsAgent:         strings.HasPrefix(basename, "agent-"),
			ParentSessionID: parentID,
			ToolCounts:      make(map[string]int),
		},
		usageByMessage: make(map[string]rawUsage),
		filesRead:      make(map[string]bool),
		filesWritten:   make(map[string]bool),
		filesCreated:   make(map[string]bool),
		launches:       make(map[string]string),
		churn:          make(map[string]*FileChurn),
		skills:         make(map[string]bool),
	}
}

// read consumes the lines in r, which must be positioned at p.offset.
//
// A last line with no newline yet may still be being written. It is taken only
// if it already parses; otherwise it is left for the next read, which will see
// it whole. Reading the whole file at once gives the same result either way,
// because a fragment does not parse and was always skipped.
func (p *transcriptParser) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), maxLineBytes)

	// How many bytes the current line took, newline included, and whether it
	// had one.
	var advance int
	var terminated bool
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		n, token, err := bufio.ScanLines(data, atEOF)
		if n > 0 {
			advance = n
			terminated = data[n-1] == '\n'
		}
		return n, token, err
	})

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			p.offset += int64(advance)
			continue
		}

		var entry rawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			if !terminated {
				return
			}
			p.offset += int64(advance)
			continue
		}
		p.offset += int64(advance)
		p.feed(entry)
	}
	// A line over maxLineBytes ends the scan with an error, and the offset
	// stays before it — the same place a full parse has always stopped.
}

// feed takes one transcript entry into the parse.
func (p *transcriptParser) feed(entry rawEntry) {
	info := &p.info

	ts := parseTimestamp(entry.Timestamp)
	if info.StartTime.IsZero() || (!ts.IsZero() && ts.Before(info.StartTime)) {
		info.StartTime = ts
	}
	if ts.After(info.LastUpdate) {
		info.LastUpdate = ts
	}

	if info.CWD == "" && entry.CWD != "" {
		info.CWD = entry.CWD
	}
	if info.GitBranch == "" && entry.GitBranch != "" {
		info.GitBranch = entry.GitBranch
	}
	// Which agent type a subagent run ran as. Claude Code stamps this on
	// the run's entries, not only its assistant turns.
	if info.AgentType == "" && entry.AttributionAgent != "" {
		info.AgentType = entry.AttributionAgent
	}

	// Index of the first event this entry contributes, so per-entry flags can
	// be stamped onto everything it produced.
	firstNew := len(p.events)

	switch entry.Type {
	case "system":
		switch entry.Subtype {
		case "compact_boundary":
			preTokens := 0
			trigger := ""
			if entry.CompactMetadata != nil {
				preTokens = entry.CompactMetadata.PreTokens
				trigger = entry.CompactMetadata.Trigger
			}
			p.events = append(p.events, Event{
				Type:             EventCompaction,
				Timestamp:        ts,
				UUID:             entry.UUID,
				CompactPreTokens: preTokens,
				CompactTrigger:   trigger,
			})
		case "turn_duration":
			info.ActiveDuration += time.Duration(entry.DurationMs) * time.Millisecond
			p.events = append(p.events, Event{
				Type:           EventTurnDuration,
				Timestamp:      ts,
				UUID:           entry.UUID,
				TurnDurationMs: entry.DurationMs,
			})
		}

	case "progress":
		p.events = append(p.events, parseProgressEntry(entry, ts)...)

	case "attachment":
		p.events = append(p.events, parseAttachment(entry, ts)...)

	case "queue-operation", "file-history-snapshot", "file-history-delta":
		// Low-value metadata — skip

	case "user":
		if entry.Message == nil || entry.IsCompactSummary {
			return
		}
		result := decodeToolUseResult(entry.ToolUseResult)
		events := parseUserMessage(entry, ts, result)
		p.events = append(p.events, events...)

		for _, e := range events {
			switch e.Type {
			case EventUserPrompt:
				info.UserPrompts++
			case EventToolResult:
				if e.IsError {
					info.Errors++
				}
				if e.Result != nil && e.Result.Interrupted {
					info.Interruptions++
				}
			}
		}

		if entry.ToolDenialKind != "" {
			info.Denials++
			p.events = append(p.events, Event{
				Type:         EventToolDenied,
				Timestamp:    ts,
				UUID:         entry.UUID,
				DenialKind:   entry.ToolDenialKind,
				UserFeedback: decodeUserFeedback(entry.UserFeedback),
			})
		}

	case "assistant":
		if entry.Message == nil {
			return
		}
		if entry.Message.ID != "" && entry.Message.Usage != nil {
			p.usageByMessage[entry.Message.ID] = *entry.Message.Usage
		}
		// "<synthetic>" marks locally generated messages, not a real model.
		if entry.Message.Model != "" && entry.Message.Model != "<synthetic>" && info.Model == "" {
			info.Model = entry.Message.Model
		}

		events := parseAssistantMessage(entry, ts)
		p.events = append(p.events, events...)

		for _, e := range events {
			if e.Type != EventToolUse {
				continue
			}
			info.ToolCallCount++
			info.ToolCounts[e.ToolName]++

			switch e.ToolName {
			case "Read", "NotebookRead":
				if fp, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
					p.filesRead[fp] = true
				}
			case "Write":
				if fp, ok := stringInput(e.ToolInput, "file_path"); ok {
					p.filesCreated[fp] = true
					churnFor(p.churn, fp).Edits++
				}
			case "Edit", "MultiEdit", "NotebookEdit":
				if fp, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
					p.filesWritten[fp] = true
					churnFor(p.churn, fp).Edits++
				}
			case "Bash", "BashOutput":
				if e.ToolName == "Bash" {
					info.BashCommands++
				}
			// Claude Code renamed this tool Task -> Agent; OpenCode transcripts
			// normalise to "Task". Old transcripts keep the old name forever.
			case "Task", "Agent":
				info.SubagentCalls++
			case "WebFetch", "WebSearch":
				info.WebRequests++
			case "Skill":
				if name, ok := stringInput(e.ToolInput, "skill"); ok {
					p.skills[name] = true
				}
			}
		}
	}

	recordSubagentLaunch(entry, p.launches)

	// Stamp sidechain provenance onto everything this entry produced.
	if entry.IsSidechain {
		for i := firstNew; i < len(p.events); i++ {
			p.events[i].IsSidechain = true
			info.SubagentEvents++
		}
	}
}

// session finishes the parse into a Session.
//
// With shared false the session takes p's events and maps as they are, and p
// must not be used again. With shared true p is left untouched for the next
// read: the session gets its own copy of everything a later pass writes to —
// pairing calls with results here, and relinking and redaction in the store —
// so the next session built from p is built from the transcript as parsed,
// exactly as a fresh parse would be.
func (p *transcriptParser) session(shared bool) *Session {
	sess := &Session{Info: p.info, Events: p.events, SubagentLaunches: p.launches}
	churn := p.churn
	if shared {
		sess.Info.ToolCounts = make(map[string]int, len(p.info.ToolCounts))
		for k, v := range p.info.ToolCounts {
			sess.Info.ToolCounts[k] = v
		}
		sess.SubagentLaunches = make(map[string]string, len(p.launches))
		for k, v := range p.launches {
			sess.SubagentLaunches[k] = v
		}
		churn = make(map[string]*FileChurn, len(p.churn))
		for k, c := range p.churn {
			copied := *c
			churn[k] = &copied
		}
		sess.Events = cloneEvents(p.events)
	}
	filesRead, filesWritten, filesCreated := p.filesRead, p.filesWritten, p.filesCreated
	skills := p.skills
	usageByMessage := p.usageByMessage

	// Accumulate token usage once per assistant message.
	for _, u := range usageByMessage {
		sess.Info.InputTokens += u.InputTokens
		sess.Info.OutputTokens += u.OutputTokens
		sess.Info.CacheReadTokens += u.CacheReadInputTokens
		sess.Info.CacheWriteTokens += u.CacheCreationInputTokens
	}

	// Pair each tool call with its result: gives per-operation duration, and lets
	// file churn be attributed from the structured patch the result carries.
	correlateToolCalls(sess, churn)

	for _, c := range churn {
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

	for name := range skills {
		sess.Info.SkillsUsed = append(sess.Info.SkillsUsed, name)
	}
	sort.Strings(sess.Info.SkillsUsed)

	// Convert file maps to slices
	for fp := range filesRead {
		sess.Info.FilesRead = append(sess.Info.FilesRead, fp)
	}
	for fp := range filesWritten {
		sess.Info.FilesWritten = append(sess.Info.FilesWritten, fp)
	}
	for fp := range filesCreated {
		sess.Info.FilesCreated = append(sess.Info.FilesCreated, fp)
	}

	sess.Info.EventCount = len(sess.Events)
	sess.Info.CostUSD = estimateCost(sess.Info)
	sess.Info.Title = deriveTitle(sess.Events)

	return sess
}

// cloneEvents copies events deeply enough that nothing done to the copy reaches
// the original: the tool input, the structured result and the diagnostics are
// all written to in place by relinking and redaction.
//
// One result is shared by every tool_result block of the entry it came from,
// and that sharing is kept, so the copy is what a fresh parse would have made.
func cloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	results := make(map[*ToolResult]*ToolResult)
	for i := range out {
		e := &out[i]
		if e.ToolInput != nil {
			e.ToolInput = cloneInput(e.ToolInput)
		}
		if e.Result != nil {
			c, ok := results[e.Result]
			if !ok {
				c = cloneResult(e.Result)
				results[e.Result] = c
			}
			e.Result = c
		}
		if e.Diagnostics != nil {
			e.Diagnostics = append([]Diagnostic(nil), e.Diagnostics...)
		}
	}
	return out
}

func cloneInput(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return cloneInput(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = cloneValue(item)
		}
		return out
	}
	return v
}

func cloneResult(r *ToolResult) *ToolResult {
	c := *r
	c.StructuredPatch = cloneHunks(r.StructuredPatch)
	if r.BashEdit != nil {
		edit := *r.BashEdit
		if r.BashEdit.Files != nil {
			edit.Files = make([]ChangedFile, len(r.BashEdit.Files))
			for i, f := range r.BashEdit.Files {
				f.Hunks = cloneHunks(f.Hunks)
				edit.Files[i] = f
			}
		}
		c.BashEdit = &edit
	}
	if r.File != nil {
		file := *r.File
		c.File = &file
	}
	return &c
}

func cloneHunks(hunks []PatchHunk) []PatchHunk {
	if hunks == nil {
		return nil
	}
	out := make([]PatchHunk, len(hunks))
	for i, h := range hunks {
		if h.Lines != nil {
			h.Lines = append([]string(nil), h.Lines...)
		}
		out[i] = h
	}
	return out
}

func parseUserMessage(entry rawEntry, ts time.Time, result *ToolResult) []Event {
	if entry.Message == nil {
		return nil
	}

	var events []Event

	switch content := entry.Message.Content.(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			events = append(events, Event{
				Type:      EventUserPrompt,
				Timestamp: ts,
				UUID:      entry.UUID,
				UserText:  content,
			})
		}
	case []interface{}:
		for _, block := range content {
			bMap, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := bMap["type"].(string)

			switch blockType {
			case "tool_result":
				output := ""
				switch c := bMap["content"].(type) {
				case string:
					output = c
				case []interface{}:
					for _, item := range c {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if text, ok := itemMap["text"].(string); ok {
								output += text
							}
						}
					}
				default:
					b, _ := json.Marshal(bMap["content"])
					output = string(b)
				}

				isError, _ := bMap["is_error"].(bool)
				toolUseID, _ := bMap["tool_use_id"].(string)

				events = append(events, Event{
					Type:       EventToolResult,
					Timestamp:  ts,
					UUID:       entry.UUID,
					ToolID:     toolUseID,
					ToolOutput: output,
					IsError:    isError || resultIsError(result),
					Result:     result,
				})

			case "text":
				text, _ := bMap["text"].(string)
				if strings.TrimSpace(text) != "" {
					events = append(events, Event{
						Type:      EventUserPrompt,
						Timestamp: ts,
						UUID:      entry.UUID,
						UserText:  text,
					})
				}
			}
		}
	}

	return events
}

func parseAssistantMessage(entry rawEntry, ts time.Time) []Event {
	if entry.Message == nil {
		return nil
	}

	var events []Event
	contentArr, ok := entry.Message.Content.([]interface{})
	if !ok {
		return nil
	}

	inputTokens := 0
	outputTokens := 0
	if entry.Message.Usage != nil {
		inputTokens = entry.Message.Usage.InputTokens
		outputTokens = entry.Message.Usage.OutputTokens
	}

	for _, block := range contentArr {
		bMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := bMap["type"].(string)

		switch blockType {
		case "thinking":
			// Transcripts carry a signature but an empty body for extended
			// thinking — the text is not retained. Emit the event regardless so
			// the timeline still shows that the agent reasoned at this point.
			thinking, _ := bMap["thinking"].(string)
			events = append(events, Event{
				Type:         EventThinking,
				Timestamp:    ts,
				UUID:         entry.UUID,
				Thinking:     thinking,
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			})

		case "text":
			text, _ := bMap["text"].(string)
			if strings.TrimSpace(text) != "" {
				events = append(events, Event{
					Type:         EventText,
					Timestamp:    ts,
					UUID:         entry.UUID,
					Text:         text,
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				})
			}

		case "tool_use":
			name, _ := bMap["name"].(string)
			id, _ := bMap["id"].(string)
			input, _ := bMap["input"].(map[string]interface{})

			events = append(events, Event{
				Type:         EventToolUse,
				Timestamp:    ts,
				UUID:         entry.UUID,
				ToolName:     name,
				ToolInput:    input,
				ToolID:       id,
				DurationMs:   -1,
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			})
		}
	}

	return events
}

// locate works out which project a transcript belongs to, and its parent.
//
// Claude Code writes a project's own sessions flat, as
// <project>/<session-id>.jsonl, and each subagent run one level down, as
// <project>/<parent-session-id>/subagents/agent-<agentId>.jsonl. Reading the
// project from the immediate parent directory is therefore only correct for
// the flat case; a subagent file has to climb past its parent session.
func locate(path string) (projectDir, parentID string) {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		parentDir := filepath.Dir(dir) // <project>/<parent-session-id>
		parentID = filepath.Base(parentDir)
		dir = filepath.Dir(parentDir) // <project>
	}
	// The encoded directory name cannot distinguish a path separator from a
	// hyphen; the CWD recorded in the transcript is the authoritative path.
	return strings.ReplaceAll(filepath.Base(dir), "-", "/"), parentID
}

// agentIDPattern matches the id Claude Code reports when a Task launches a
// background agent: "agentId: a6f11ac2459e5e31b".
var agentIDPattern = regexp.MustCompile(`agentId:\s*([0-9a-f]{8,})`)

// recordSubagentLaunch notes which agent a Task call started.
//
// A background agent's result carries no output — the work lands in its own
// transcript under <session>/subagents/. The only link back to the call that
// started it is the agent id in that result, so it is captured here and used to
// splice the subagent's turns into this session's timeline.
func recordSubagentLaunch(entry rawEntry, launches map[string]string) {
	if entry.Message == nil {
		return
	}
	blocks, ok := entry.Message.Content.([]interface{})
	if !ok {
		return
	}
	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok || block["type"] != "tool_result" {
			continue
		}
		toolUseID, _ := block["tool_use_id"].(string)
		if toolUseID == "" {
			continue
		}
		text, err := json.Marshal(block["content"])
		if err != nil {
			continue
		}
		if m := agentIDPattern.FindSubmatch(text); m != nil {
			launches[toolUseID] = string(m[1])
		}
	}
}

func parseTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// decodeToolUseResult decodes the toolUseResult payload. Claude Code writes an
// object for successful operations and a bare string when the operation failed.
func decodeToolUseResult(raw json.RawMessage) *ToolResult {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return &ToolResult{Raw: asString}
	}

	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil
	}
	return &res
}

// resultIsError reports whether a structured result represents a failure. The
// is_error flag on the content block is not set for failing shell commands, so
// the string form ("Error: Exit code 127") is the reliable signal.
func resultIsError(r *ToolResult) bool {
	if r == nil {
		return false
	}
	return strings.HasPrefix(r.Raw, "Error:") || strings.HasPrefix(r.Raw, "Error ")
}

// decodeUserFeedback pulls readable text out of the userFeedback payload, which
// may be a plain string or a structured object.
func decodeUserFeedback(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"message", "text", "reason"} {
			if v, ok := obj[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return string(raw)
}

// stringInput returns the first of the given keys that holds a non-empty string.
func stringInput(input map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := input[k].(string); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func churnFor(m map[string]*FileChurn, path string) *FileChurn {
	c, ok := m[path]
	if !ok {
		c = &FileChurn{Path: path}
		m[path] = c
	}
	return c
}

// correlateToolCalls matches each tool_use to the tool_result carrying the same
// ID and folds the result into the call: an operation and its outcome are one
// thing, and keeping them as two events made every timeline twice as long as it
// needed to be. Results whose call is missing stay as standalone events.
func correlateToolCalls(sess *Session, churn map[string]*FileChurn) {
	results := make(map[string]*Event)
	for i := range sess.Events {
		e := &sess.Events[i]
		if e.Type == EventToolResult && e.ToolID != "" {
			results[e.ToolID] = e
		}
	}

	folded := make(map[string]bool, len(results))

	for i := range sess.Events {
		e := &sess.Events[i]
		if e.Type != EventToolUse || e.ToolID == "" {
			continue
		}
		res, ok := results[e.ToolID]
		if !ok {
			continue
		}
		folded[e.ToolID] = true

		e.Result = res.Result
		e.ToolOutput = res.ToolOutput
		e.IsError = res.IsError
		e.ResultUUID = res.UUID

		if !e.Timestamp.IsZero() && !res.Timestamp.IsZero() {
			if d := res.Timestamp.Sub(e.Timestamp); d >= 0 {
				e.DurationMs = int(d.Milliseconds())
			}
		}

		if res.Result == nil {
			continue
		}
		added, removed := res.Result.Churn()
		if added == 0 && removed == 0 {
			continue
		}
		// Prefer the path the result reports; fall back to the call's input.
		fp := res.Result.FilePath
		if fp == "" {
			fp, _ = stringInput(e.ToolInput, "file_path", "notebook_path")
		}
		if fp == "" {
			continue
		}
		c := churnFor(churn, fp)
		c.LinesAdded += added
		c.LinesRemoved += removed
		e.LinesAdded = added
		e.LinesRemoved = removed
	}

	kept := sess.Events[:0]
	for _, e := range sess.Events {
		if e.Type == EventToolResult && folded[e.ToolID] {
			continue
		}
		kept = append(kept, e)
	}
	sess.Events = kept
}

// parseProgressEntry handles subagent, hook and bash progress records. These are
// absent from current transcript formats but still emitted by older versions.
func parseProgressEntry(entry rawEntry, ts time.Time) []Event {
	if len(entry.Data) == 0 {
		return nil
	}
	var pd rawProgressData
	if err := json.Unmarshal(entry.Data, &pd); err != nil {
		return nil
	}

	switch pd.Type {
	case "agent_progress", "waiting_for_task":
		desc := pd.TaskDescription
		if desc == "" {
			desc = pd.Prompt
		}
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		return []Event{{
			Type:             EventAgentProgress,
			Timestamp:        ts,
			UUID:             entry.UUID,
			AgentID:          pd.AgentID,
			AgentDescription: desc,
		}}

	case "hook_progress":
		return []Event{{
			Type:      EventHookProgress,
			Timestamp: ts,
			UUID:      entry.UUID,
			HookEvent: pd.HookEvent,
			HookName:  pd.HookName,
		}}

	case "bash_progress":
		if strings.TrimSpace(pd.Output) == "" && pd.ElapsedTimeSec == 0 {
			return nil
		}
		return []Event{{
			Type:           EventBashProgress,
			Timestamp:      ts,
			UUID:           entry.UUID,
			BashElapsedSec: pd.ElapsedTimeSec,
		}}
	}
	return nil
}

// parseAttachment surfaces the attachment kinds that record real changes to the
// workspace: edits the user made outside Claude, and IDE diagnostics.
func parseAttachment(entry rawEntry, ts time.Time) []Event {
	if len(entry.Attachment) == 0 {
		return nil
	}

	var att struct {
		Type     string `json:"type"`
		Filename string `json:"filename"`
		Snippet  string `json:"snippet"`
		Files    []struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
			} `json:"diagnostics"`
		} `json:"files"`
	}
	if err := json.Unmarshal(entry.Attachment, &att); err != nil {
		return nil
	}

	switch att.Type {
	case "edited_text_file":
		if att.Filename == "" {
			return nil
		}
		return []Event{{
			Type:      EventUserFileEdit,
			Timestamp: ts,
			UUID:      entry.UUID,
			FilePath:  att.Filename,
			Text:      att.Snippet,
		}}

	case "diagnostics":
		var diags []Diagnostic
		for _, f := range att.Files {
			for _, d := range f.Diagnostics {
				diags = append(diags, Diagnostic{
					File:     f.URI,
					Severity: d.Severity,
					Message:  d.Message,
				})
			}
		}
		if len(diags) == 0 {
			return nil
		}
		return []Event{{
			Type:        EventDiagnostics,
			Timestamp:   ts,
			UUID:        entry.UUID,
			Diagnostics: diags,
		}}
	}
	return nil
}
