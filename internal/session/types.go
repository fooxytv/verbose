package session

import (
	"encoding/json"
	"strings"
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

	// Operation auditing
	ToolCounts     map[string]int // tool name -> call count
	LinesAdded     int
	LinesRemoved   int
	FileChurns     []FileChurn   // per-file edit/churn detail, sorted desc
	SubagentCalls  int           // Task tool invocations
	SubagentEvents int           // events recorded on a sidechain (subagent turns)
	Interruptions  int           // operations the user or system aborted
	Denials        int           // tool calls the user rejected
	WebRequests    int           // WebFetch + WebSearch calls
	SkillsUsed     []string      // distinct skills invoked
	ActiveDuration time.Duration // sum of reported turn durations

	IsAgent   bool // agent-* files are subagent sessions
	Model     string
	CWD       string
	GitBranch string
	Source    string // "claude" or "opencode"
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
	EventToolDenied    // User rejected a tool call
	EventUserFileEdit  // User edited a file outside of Claude
	EventDiagnostics   // IDE diagnostics attached to the conversation
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
	ToolName   string
	ToolInput  map[string]interface{}
	ToolID     string
	DurationMs int // wall time until the matching tool result, -1 if unmatched

	// Line churn attributed from the matching result's structured patch
	LinesAdded   int
	LinesRemoved int

	// EventToolResult
	ToolOutput string
	IsError    bool
	Result     *ToolResult // structured record of what the operation actually did
	ResultUUID string      // transcript entry the result came from, when folded in

	// EventToolDenied
	DenialKind   string
	UserFeedback string

	// EventUserFileEdit / EventDiagnostics
	FilePath    string
	Diagnostics []Diagnostic

	// Set on every event that occurred inside a subagent turn
	IsSidechain bool

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

	// Structured record of a completed tool call. Usually an object, but a bare
	// string when the operation failed (e.g. "Error: Exit code 127").
	ToolUseResult json.RawMessage `json:"toolUseResult"`

	IsSidechain    bool            `json:"isSidechain"`
	ToolDenialKind string          `json:"toolDenialKind"`
	UserFeedback   json.RawMessage `json:"userFeedback"`
	Attachment     json.RawMessage `json:"attachment"`
}

// ToolResult is the decoded toolUseResult payload. Fields are populated
// selectively depending on which tool produced it.
type ToolResult struct {
	// Bash
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`

	// Edit / Write
	FilePath     string `json:"filePath"`
	OldString    string `json:"oldString"`
	NewString    string `json:"newString"`
	ReplaceAll   bool   `json:"replaceAll"`
	UserModified bool   `json:"userModified"`

	// Edit / Write — the real diff Claude Code recorded for the change
	StructuredPatch []PatchHunk `json:"structuredPatch"`

	// Read
	File *ReadFile `json:"file"`

	// Set when toolUseResult was a bare string rather than an object
	Raw string `json:"-"`
}

// PatchHunk is one unified-diff hunk from a Edit or Write operation.
type PatchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// Churn counts the added and removed lines across all hunks.
func (r *ToolResult) Churn() (added, removed int) {
	for _, h := range r.StructuredPatch {
		for _, l := range h.Lines {
			switch {
			case strings.HasPrefix(l, "+"):
				added++
			case strings.HasPrefix(l, "-"):
				removed++
			}
		}
	}
	return added, removed
}

// ReadFile describes the file returned by a Read operation.
type ReadFile struct {
	FilePath   string `json:"filePath"`
	NumLines   int    `json:"numLines"`
	StartLine  int    `json:"startLine"`
	TotalLines int    `json:"totalLines"`
}

// Diagnostic is a single IDE diagnostic surfaced in the transcript.
type Diagnostic struct {
	File     string
	Severity string
	Message  string
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
	CWD         string // real working directory, from the sessions themselves

	Memory string // MEMORY.md contents

	// Aggregate stats
	TotalSessions, TotalToolCalls, TotalUserPrompts, TotalErrors int
	TotalLinesAdded, TotalLinesRemoved                           int
	TotalSubagentCalls, TotalDenials, TotalInterruptions         int
	ToolCounts                                                   map[string]int
	TotalInputTokens, TotalOutputTokens                          int
	TotalCacheReadTokens, TotalCacheWriteTokens                  int
	TotalCostUSD                                                 float64
	FirstSession, LastSession                                    time.Time

	MostEditedFiles []FileEditCount // sorted desc by count
	Sessions        []SessionInfo   // sorted desc by LastUpdate
}

// FileEditCount tracks how many times a file was edited across sessions.
type FileEditCount struct {
	Path         string
	Count        int
	LinesAdded   int
	LinesRemoved int
}

// FileChurn records how much a single file changed within one session.
type FileChurn struct {
	Path         string
	Edits        int
	LinesAdded   int
	LinesRemoved int
}

// TodoItem represents a task/todo from ~/.claude/todos/.
type TodoItem struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ActiveForm  string `json:"activeForm"`
}
