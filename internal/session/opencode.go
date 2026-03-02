package session

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// OpenCode helper types (unexported).

type ocSession struct {
	ID               string
	Title            string
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ocMessage struct {
	ID        string
	SessionID string
	Role      string // "user", "assistant"
	Parts     string // JSON array
	Model     string
	CreatedAt time.Time
}

type ocPart struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type ocTextData struct {
	Text string `json:"text"`
}

type ocToolCallData struct {
	ToolName string                 `json:"toolName"`
	Args     map[string]interface{} `json:"args"`
	ToolID   string                 `json:"id"`
}

type ocToolResultData struct {
	Result  string `json:"result"`
	IsError bool   `json:"isError"`
	ToolID  string `json:"id"`
}

// ParseOpenCodeDB reads an OpenCode SQLite database and returns parsed sessions.
func ParseOpenCodeDB(dbPath string) ([]*Session, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Query all sessions.
	rows, err := db.Query(`SELECT id, title, prompt_tokens, completion_tokens, cost, created_at, updated_at FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ocSessions []ocSession
	for rows.Next() {
		var s ocSession
		var createdAt, updatedAt string
		if err := rows.Scan(&s.ID, &s.Title, &s.PromptTokens, &s.CompletionTokens, &s.Cost, &createdAt, &updatedAt); err != nil {
			continue
		}
		s.CreatedAt = parseOCTime(createdAt)
		s.UpdatedAt = parseOCTime(updatedAt)
		ocSessions = append(ocSessions, s)
	}

	projectDir := filepath.Dir(filepath.Dir(dbPath)) // parent of .opencode/

	var sessions []*Session
	for _, ocs := range ocSessions {
		sess, err := parseOCSession(db, ocs, projectDir)
		if err != nil || len(sess.Events) == 0 {
			continue
		}
		sessions = append(sessions, sess)
	}

	return sessions, nil
}

func parseOCSession(db *sql.DB, ocs ocSession, projectDir string) (*Session, error) {
	rows, err := db.Query(`SELECT id, session_id, role, parts, model, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC`, ocs.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	var model string
	toolCallCount := 0

	for rows.Next() {
		var msg ocMessage
		var createdAt string
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Parts, &msg.Model, &createdAt); err != nil {
			continue
		}
		msg.CreatedAt = parseOCTime(createdAt)
		if model == "" && msg.Model != "" {
			model = msg.Model
		}

		msgEvents, tools := parseOCMessage(msg)
		events = append(events, msgEvents...)
		toolCallCount += tools
	}

	sess := &Session{
		Info: SessionInfo{
			ID:           "oc-" + ocs.ID,
			ProjectDir:   projectDir,
			ProjectName:  filepath.Base(projectDir),
			FilePath:     filepath.Join(projectDir, ".opencode", "opencode.db"),
			StartTime:    ocs.CreatedAt,
			LastUpdate:   ocs.UpdatedAt,
			InputTokens:  ocs.PromptTokens,
			OutputTokens: ocs.CompletionTokens,
			CostUSD:      ocs.Cost,
			EventCount:   len(events),
			ToolCallCount: toolCallCount,
			Model:        model,
			CWD:          projectDir,
			Source:       "opencode",
		},
		Events: events,
	}

	// Count user prompts.
	for _, e := range events {
		if e.Type == EventUserPrompt {
			sess.Info.UserPrompts++
		}
	}

	return sess, nil
}

func parseOCMessage(msg ocMessage) ([]Event, int) {
	var parts []ocPart
	if err := json.Unmarshal([]byte(msg.Parts), &parts); err != nil {
		return nil, 0
	}

	var events []Event
	toolCalls := 0

	for _, part := range parts {
		switch part.Type {
		case "text":
			var d ocTextData
			if json.Unmarshal(part.Data, &d) != nil || d.Text == "" {
				continue
			}
			if msg.Role == "user" {
				events = append(events, Event{
					Type:      EventUserPrompt,
					Timestamp: msg.CreatedAt,
					UserText:  d.Text,
				})
			} else {
				events = append(events, Event{
					Type:      EventText,
					Timestamp: msg.CreatedAt,
					Text:      d.Text,
				})
			}

		case "reasoning":
			var d ocTextData
			if json.Unmarshal(part.Data, &d) != nil || d.Text == "" {
				continue
			}
			events = append(events, Event{
				Type:      EventThinking,
				Timestamp: msg.CreatedAt,
				Thinking:  d.Text,
			})

		case "tool_call":
			var d ocToolCallData
			if json.Unmarshal(part.Data, &d) != nil {
				continue
			}
			events = append(events, Event{
				Type:      EventToolUse,
				Timestamp: msg.CreatedAt,
				ToolName:  d.ToolName,
				ToolInput: d.Args,
				ToolID:    d.ToolID,
			})
			toolCalls++

		case "tool_result":
			var d ocToolResultData
			if json.Unmarshal(part.Data, &d) != nil {
				continue
			}
			events = append(events, Event{
				Type:       EventToolResult,
				Timestamp:  msg.CreatedAt,
				ToolOutput: d.Result,
				IsError:    d.IsError,
				ToolID:     d.ToolID,
			})

		case "finish":
			// End-of-turn marker, skip.
		}
	}

	return events, toolCalls
}

// parseOCTime parses a time string from the OpenCode database.
// Tries RFC3339 first, then falls back to common SQLite formats.
func parseOCTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
