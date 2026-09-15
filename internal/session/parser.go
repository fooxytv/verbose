package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ParseSessionFile reads a JSONL transcript and returns a fully parsed Session.
func ParseSessionFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	basename := filepath.Base(path)
	sessionID := strings.TrimSuffix(basename, ".jsonl")
	isAgent := strings.HasPrefix(basename, "agent-")

	// Decode the project path from the directory name
	dirName := filepath.Base(filepath.Dir(path))
	projectDir := strings.ReplaceAll(dirName, "-", "/")
	projectName := filepath.Base(projectDir)

	sess := &Session{
		Info: SessionInfo{
			ID:          sessionID,
			ProjectDir:  projectDir,
			ProjectName: projectName,
			FilePath:    path,
			IsAgent:     isAgent,
		},
	}

	// Claude Code writes one JSONL line per content block, with every block of a
	// single assistant message sharing that message's ID. Usage is repeated
	// verbatim on each of those lines, so tokens are accumulated once per message
	// ID while events are emitted from every line.
	usageByMessage := make(map[string]rawUsage)

	// Track unique files
	filesRead := make(map[string]bool)
	filesWritten := make(map[string]bool)
	filesCreated := make(map[string]bool)

	churn := make(map[string]*FileChurn)
	skills := make(map[string]bool)
	sess.Info.ToolCounts = make(map[string]int)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry rawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		ts := parseTimestamp(entry.Timestamp)
		if sess.Info.StartTime.IsZero() || (!ts.IsZero() && ts.Before(sess.Info.StartTime)) {
			sess.Info.StartTime = ts
		}
		if ts.After(sess.Info.LastUpdate) {
			sess.Info.LastUpdate = ts
		}

		if sess.Info.CWD == "" && entry.CWD != "" {
			sess.Info.CWD = entry.CWD
		}
		if sess.Info.GitBranch == "" && entry.GitBranch != "" {
			sess.Info.GitBranch = entry.GitBranch
		}

		// Index of the first event this entry contributes, so per-entry flags can
		// be stamped onto everything it produced.
		firstNew := len(sess.Events)

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
				sess.Events = append(sess.Events, Event{
					Type:             EventCompaction,
					Timestamp:        ts,
					UUID:             entry.UUID,
					CompactPreTokens: preTokens,
					CompactTrigger:   trigger,
				})
			case "turn_duration":
				sess.Info.ActiveDuration += time.Duration(entry.DurationMs) * time.Millisecond
				sess.Events = append(sess.Events, Event{
					Type:           EventTurnDuration,
					Timestamp:      ts,
					UUID:           entry.UUID,
					TurnDurationMs: entry.DurationMs,
				})
			}

		case "progress":
			sess.Events = append(sess.Events, parseProgressEntry(entry, ts)...)

		case "attachment":
			sess.Events = append(sess.Events, parseAttachment(entry, ts)...)

		case "queue-operation", "file-history-snapshot", "file-history-delta":
			// Low-value metadata — skip

		case "user":
			if entry.Message == nil || entry.IsCompactSummary {
				continue
			}
			result := decodeToolUseResult(entry.ToolUseResult)
			events := parseUserMessage(entry, ts, result)
			sess.Events = append(sess.Events, events...)

			for _, e := range events {
				switch e.Type {
				case EventUserPrompt:
					sess.Info.UserPrompts++
				case EventToolResult:
					if e.IsError {
						sess.Info.Errors++
					}
					if e.Result != nil && e.Result.Interrupted {
						sess.Info.Interruptions++
					}
				}
			}

			if entry.ToolDenialKind != "" {
				sess.Info.Denials++
				sess.Events = append(sess.Events, Event{
					Type:         EventToolDenied,
					Timestamp:    ts,
					UUID:         entry.UUID,
					DenialKind:   entry.ToolDenialKind,
					UserFeedback: decodeUserFeedback(entry.UserFeedback),
				})
			}

		case "assistant":
			if entry.Message == nil {
				continue
			}
			if entry.Message.ID != "" && entry.Message.Usage != nil {
				usageByMessage[entry.Message.ID] = *entry.Message.Usage
			}
			// "<synthetic>" marks locally generated messages, not a real model.
			if entry.Message.Model != "" && entry.Message.Model != "<synthetic>" && sess.Info.Model == "" {
				sess.Info.Model = entry.Message.Model
			}

			events := parseAssistantMessage(entry, ts)
			sess.Events = append(sess.Events, events...)

			for _, e := range events {
				if e.Type != EventToolUse {
					continue
				}
				sess.Info.ToolCallCount++
				sess.Info.ToolCounts[e.ToolName]++

				switch e.ToolName {
				case "Read", "NotebookRead":
					if fp, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
						filesRead[fp] = true
					}
				case "Write":
					if fp, ok := stringInput(e.ToolInput, "file_path"); ok {
						filesCreated[fp] = true
						churnFor(churn, fp).Edits++
					}
				case "Edit", "MultiEdit", "NotebookEdit":
					if fp, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
						filesWritten[fp] = true
						churnFor(churn, fp).Edits++
					}
				case "Bash", "BashOutput":
					if e.ToolName == "Bash" {
						sess.Info.BashCommands++
					}
				case "Task":
					sess.Info.SubagentCalls++
				case "WebFetch", "WebSearch":
					sess.Info.WebRequests++
				case "Skill":
					if name, ok := stringInput(e.ToolInput, "skill"); ok {
						skills[name] = true
					}
				}
			}
		}

		// Stamp sidechain provenance onto everything this entry produced.
		if entry.IsSidechain {
			for i := firstNew; i < len(sess.Events); i++ {
				sess.Events[i].IsSidechain = true
				sess.Info.SubagentEvents++
			}
		}
	}

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

	return sess, nil
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

func parseTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// estimateCost gives a rough USD cost estimate based on Claude pricing.
// Uses Opus pricing: $15/M input, $75/M output, cache read $1.5/M, cache write $18.75/M
func estimateCost(info SessionInfo) float64 {
	inputPrice := 15.0 / 1_000_000.0
	outputPrice := 75.0 / 1_000_000.0
	cacheReadPrice := 1.5 / 1_000_000.0
	cacheWritePrice := 18.75 / 1_000_000.0

	return float64(info.InputTokens)*inputPrice +
		float64(info.OutputTokens)*outputPrice +
		float64(info.CacheReadTokens)*cacheReadPrice +
		float64(info.CacheWriteTokens)*cacheWritePrice
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
