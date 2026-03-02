package session

import (
	"encoding/json"
	"time"
)

// SessionInfo is lightweight metadata for the sessions list view.
type SessionInfo struct {
	ID          string
	ProjectDir  string // decoded project path
	ProjectName string // last path component
	FilePath    string // full path to .jsonl file
	StartTime   time.Time
	LastUpdate  time.Time

	// Token breakdown
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	CostUSD          float64

	// Activity summary
	EventCount    int
	ToolCallCount int
	UserPrompts   int
	FilesRead     []string // unique file paths read
	FilesWritten  []string // unique file paths written/edited
	FilesCreated  []string // unique file paths created via Write
	BashCommands  int
	Errors        int

	IsAgent bool // agent-* files are subagent sessions
	Model   string
	CWD     string
}

// Session is a fully parsed session with all events.
type Session struct {
	Info   SessionInfo
	Events []Event
}

// EventType classifies what kind of event occurred.
type EventType int

const (
	EventUserPrompt EventType = iota
	EventThinking
	EventText
	EventToolUse
	EventToolResult
	EventSystem
	EventCompaction
	EventAgentProgress // Subagent activity (Explore, Plan, etc.)
	EventHookProgress  // Pre/post tool hooks
	EventBashProgress  // Real-time bash output
	EventTurnDuration  // System turn timing metadata
)

// Event is a single thing that happened in a session.
type Event struct {
	Type      EventType
	Timestamp time.Time
	UUID      string

	// EventUserPrompt
	UserText string

	// EventText
	Text string

	// EventThinking
	Thinking string

	// EventToolUse
	ToolName  string
	ToolInput map[string]interface{}
	ToolID    string

	// EventToolResult
	ToolOutput string
	IsError    bool

	// EventCompaction
	CompactPreTokens int
	CompactTrigger   string

	// EventAgentProgress
	AgentID          string
	AgentDescription string // from "prompt" or task description

	// EventHookProgress
	HookEvent string // "PostToolUse", etc.
	HookName  string // "PostToolUse:Read", etc.

	// EventBashProgress
	BashElapsedSec int

	// EventTurnDuration
	TurnDurationMs int

	// Token usage from the message that contains this event
	InputTokens  int
	OutputTokens int
}

// rawEntry represents a single line in the JSONL transcript.
type rawEntry struct {
	Type       string      `json:"type"`
	Subtype    string      `json:"subtype"`
	UUID       string      `json:"uuid"`
	ParentUUID *string     `json:"parentUuid"`
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	Timestamp  string      `json:"timestamp"`
	Version    string      `json:"version"`
	GitBranch  string      `json:"gitBranch"`
	Message    *rawMessage `json:"message"`
	Content    string      `json:"content"`

	// Compaction metadata
	CompactMetadata  *rawCompactMetadata `json:"compactMetadata"`
	IsCompactSummary bool                `json:"isCompactSummary"`

	// Progress events and system metadata
	Data       json.RawMessage `json:"data"`
	DurationMs int             `json:"durationMs"`
}

type rawCompactMetadata struct {
	Trigger   string `json:"trigger"`
	PreTokens int    `json:"preTokens"`
}

type rawMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string for user prompts, []interface{} for assistant
	Model   string      `json:"model"`
	Usage   *rawUsage   `json:"usage"`
	ID      string      `json:"id"` // message ID, same across incremental updates
}

type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// rawProgressData is a helper for parsing progress event data.
type rawProgressData struct {
	Type            string `json:"type"`
	AgentID         string `json:"agentId"`
	Prompt          string `json:"prompt"`
	TaskDescription string `json:"taskDescription"`
	HookEvent       string `json:"hookEvent"`
	HookName        string `json:"hookName"`
	ElapsedTimeSec  int    `json:"elapsedTimeSeconds"`
	Output          string `json:"output"`
}

// ProjectInfo holds project-level aggregated information.
type ProjectInfo struct {
	ProjectName string
	ProjectDir  string
	EncodedDir  string // raw dir name on disk

	Memory string // MEMORY.md contents

	// Aggregate stats
	TotalSessions, TotalToolCalls, TotalUserPrompts, TotalErrors int
	TotalInputTokens, TotalOutputTokens                          int
	TotalCacheReadTokens, TotalCacheWriteTokens                  int
	TotalCostUSD                                                  float64
	FirstSession, LastSession                                     time.Time

	MostEditedFiles []FileEditCount // sorted desc by count
	Sessions        []SessionInfo   // sorted desc by LastUpdate
}

// FileEditCount tracks how many times a file was edited across sessions.
type FileEditCount struct {
	Path  string
	Count int
}

// TodoItem represents a task/todo from ~/.claude/todos/.
type TodoItem struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ActiveForm  string `json:"activeForm"`
}
