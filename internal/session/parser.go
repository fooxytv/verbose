package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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

	// Track which assistant message IDs we've seen so we only keep the last (most complete) version.
	type assistantGroup struct {
		entry     rawEntry
		timestamp time.Time
	}
	assistantMessages := make(map[string]*assistantGroup)

	var allEntries []rawEntry

	// Track unique files
	filesRead := make(map[string]bool)
	filesWritten := make(map[string]bool)
	filesCreated := make(map[string]bool)

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

		allEntries = append(allEntries, entry)

		// Capture metadata from first entry with data
		if sess.Info.CWD == "" && entry.CWD != "" {
			sess.Info.CWD = entry.CWD
		}

		switch entry.Type {
		case "assistant":
			if entry.Message != nil && entry.Message.ID != "" {
				ts := parseTimestamp(entry.Timestamp)
				existing, ok := assistantMessages[entry.Message.ID]
				if !ok || ts.After(existing.timestamp) {
					assistantMessages[entry.Message.ID] = &assistantGroup{
						entry:     entry,
						timestamp: ts,
					}
				}
				if sess.Info.Model == "" && entry.Message.Model != "" {
					sess.Info.Model = entry.Message.Model
				}
			}
		case "user":
			// Skip compact summary user messages from the first pass
		}
	}

	// Build the event timeline.
	seenMessageIDs := make(map[string]bool)

	for _, entry := range allEntries {
		ts := parseTimestamp(entry.Timestamp)

		if sess.Info.StartTime.IsZero() || (!ts.IsZero() && ts.Before(sess.Info.StartTime)) {
			sess.Info.StartTime = ts
		}
		if ts.After(sess.Info.LastUpdate) {
			sess.Info.LastUpdate = ts
		}

		switch entry.Type {
		case "system":
			if entry.Subtype == "compact_boundary" {
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
			}

		case "user":
			if entry.Message == nil {
				continue
			}
			// Skip compact summary messages - they are injected context, not real user messages
			if entry.IsCompactSummary {
				continue
			}
			events := parseUserMessage(entry, ts)
			sess.Events = append(sess.Events, events...)

			// Count user prompts (only text ones, not tool results)
			for _, e := range events {
				if e.Type == EventUserPrompt {
					sess.Info.UserPrompts++
				}
				if e.Type == EventToolResult && e.IsError {
					sess.Info.Errors++
				}
			}

		case "assistant":
			if entry.Message == nil || entry.Message.ID == "" {
				continue
			}
			if seenMessageIDs[entry.Message.ID] {
				continue
			}
			final, ok := assistantMessages[entry.Message.ID]
			if !ok {
				continue
			}
			if entry.UUID != final.entry.UUID {
				continue
			}
			seenMessageIDs[entry.Message.ID] = true
			events := parseAssistantMessage(final.entry, ts)
			sess.Events = append(sess.Events, events...)

			// Track file operations and tool stats
			for _, e := range events {
				if e.Type == EventToolUse {
					sess.Info.ToolCallCount++

					switch e.ToolName {
					case "Read":
						if fp, ok := e.ToolInput["file_path"].(string); ok {
							filesRead[fp] = true
						}
					case "Write":
						if fp, ok := e.ToolInput["file_path"].(string); ok {
							filesCreated[fp] = true
						}
					case "Edit":
						if fp, ok := e.ToolInput["file_path"].(string); ok {
							filesWritten[fp] = true
						}
					case "Bash":
						sess.Info.BashCommands++
					}
				}
			}

			// Accumulate token usage
			if final.entry.Message.Usage != nil {
				u := final.entry.Message.Usage
				sess.Info.InputTokens += u.InputTokens
				sess.Info.OutputTokens += u.OutputTokens
				sess.Info.CacheReadTokens += u.CacheReadInputTokens
				sess.Info.CacheWriteTokens += u.CacheCreationInputTokens
			}
		}
	}

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

func parseUserMessage(entry rawEntry, ts time.Time) []Event {
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
					IsError:    isError,
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
			thinking, _ := bMap["thinking"].(string)
			if strings.TrimSpace(thinking) != "" {
				events = append(events, Event{
					Type:         EventThinking,
					Timestamp:    ts,
					UUID:         entry.UUID,
					Thinking:     thinking,
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				})
			}

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
