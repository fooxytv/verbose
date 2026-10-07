// Package jsonout renders the session store as JSON on stdout, so tools other
// than the TUI can consume what verbose already parses.
//
// The DTOs here are the wire contract and are deliberately separate from
// session.SessionInfo and friends: the internal types are free to change shape
// without breaking a consumer, and every field name is chosen once, here.
package jsonout

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// Kinds accepted by Run.
const (
	KindProjects = "projects"
	KindSessions = "sessions"
	KindSession  = "session"
	KindAll      = "all"
)

// Kinds lists the valid -json arguments, for the usage message.
var Kinds = []string{KindProjects, KindSessions, KindSession, KindAll}

// Query is one headless request against the store.
type Query struct {
	Kind    string // one of the Kind* constants
	Project string // optional filter: project name, decoded dir, or real cwd
	Session string // required for KindSession
	Events  bool   // include the event timeline in a KindSession response
	Limit   int    // cap on sessions returned; 0 means no cap
}

// Run answers q from store and writes indented JSON to w.
func Run(w io.Writer, store *session.Store, q Query) error {
	var payload any

	switch q.Kind {
	case KindProjects:
		payload = projectsPayload(store, q)
	case KindSessions:
		payload = sessionsPayload(store, q)
	case KindAll:
		// One process, one scan. A caller building a dashboard needs both
		// lists, and each separate query re-parses every transcript on disk.
		payload = allResponse{
			GeneratedAt: time.Now(),
			Projects:    projectsPayload(store, q).Projects,
			Sessions:    sessionsPayload(store, q).Sessions,
		}
	case KindSession:
		if q.Session == "" {
			return fmt.Errorf("-json session needs a session id: verbose -json session <id>")
		}
		p, err := sessionPayload(store, q)
		if err != nil {
			return err
		}
		payload = p
	default:
		return fmt.Errorf("unknown -json kind %q (want one of %v)", q.Kind, Kinds)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// ── payloads ────────────────────────────────────────────────────────────────

type projectsResponse struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Projects    []projectD `json:"projects"`
}

type sessionsResponse struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Total       int        `json:"total"` // before Limit was applied
	Sessions    []sessionD `json:"sessions"`
}

type allResponse struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Projects    []projectD `json:"projects"`
	Sessions    []sessionD `json:"sessions"`
}

type sessionResponse struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Info        sessionD  `json:"info"`
	Todos       []todoD   `json:"todos,omitempty"`
	Events      []eventD  `json:"events,omitempty"`
	EventCount  int       `json:"eventCount"`
}

func projectsPayload(store *session.Store, q Query) projectsResponse {
	out := projectsResponse{GeneratedAt: time.Now(), Projects: []projectD{}}

	// Distinct project dirs, in first-seen order of the (already sorted) list.
	seen := map[string]bool{}
	var dirs []string
	for _, info := range store.GetSessions() {
		if seen[info.ProjectDir] {
			continue
		}
		seen[info.ProjectDir] = true
		dirs = append(dirs, info.ProjectDir)
	}

	for _, dir := range dirs {
		proj := store.GetProjectInfo(dir)
		if proj == nil {
			continue
		}
		if q.Project != "" && !matchesProject(q.Project, proj) {
			continue
		}
		out.Projects = append(out.Projects, newProjectD(proj))
	}

	sort.Slice(out.Projects, func(i, j int) bool {
		return out.Projects[i].LastSession.After(out.Projects[j].LastSession)
	})
	return out
}

func sessionsPayload(store *session.Store, q Query) sessionsResponse {
	out := sessionsResponse{GeneratedAt: time.Now(), Sessions: []sessionD{}}

	infos := store.GetSessions() // already sorted by LastUpdate desc
	for _, info := range infos {
		if q.Project != "" && !matchesSession(q.Project, info) {
			continue
		}
		out.Total++
		if q.Limit > 0 && len(out.Sessions) >= q.Limit {
			continue
		}
		out.Sessions = append(out.Sessions, newSessionD(info))
	}
	return out
}

func sessionPayload(store *session.Store, q Query) (sessionResponse, error) {
	sess := store.GetSession(q.Session)
	if sess == nil {
		return sessionResponse{}, fmt.Errorf("no session with id %q", q.Session)
	}

	out := sessionResponse{
		GeneratedAt: time.Now(),
		Info:        newSessionD(sess.Info),
		EventCount:  len(sess.Events),
	}
	for _, t := range store.GetSessionTodos(q.Session) {
		out.Todos = append(out.Todos, todoD{
			Subject: t.Subject, Description: t.Description,
			Status: t.Status, ActiveForm: t.ActiveForm,
		})
	}
	if q.Events {
		out.Events = make([]eventD, 0, len(sess.Events))
		for _, e := range sess.Events {
			out.Events = append(out.Events, newEventD(e))
		}
	}
	return out, nil
}

// ── matching ────────────────────────────────────────────────────────────────

// matchesSession reports whether a filter names this session's project. The
// TUI matches on name or decoded dir; the real cwd is accepted too, because a
// caller that knows where it is standing has a real path, not a decoded one.
func matchesSession(filter string, info session.SessionInfo) bool {
	return filter == info.ProjectName || filter == info.ProjectDir || filter == info.CWD
}

func matchesProject(filter string, proj *session.ProjectInfo) bool {
	return filter == proj.ProjectName || filter == proj.ProjectDir ||
		filter == proj.CWD || filter == proj.EncodedDir
}

// ── DTOs ────────────────────────────────────────────────────────────────────

type projectD struct {
	Name       string `json:"name"`
	ProjectDir string `json:"projectDir"`
	CWD        string `json:"cwd,omitempty"`
	EncodedDir string `json:"encodedDir,omitempty"`

	Sessions      int `json:"sessions"`
	ToolCalls     int `json:"toolCalls"`
	UserPrompts   int `json:"userPrompts"`
	Errors        int `json:"errors"`
	SubagentCalls int `json:"subagentCalls"`
	Denials       int `json:"denials"`
	Interruptions int `json:"interruptions"`
	LinesAdded    int `json:"linesAdded"`
	LinesRemoved  int `json:"linesRemoved"`

	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUSD"`

	FirstSession time.Time `json:"firstSession"`
	LastSession  time.Time `json:"lastSession"`

	Models          []modelD       `json:"models,omitempty"`
	Sources         []string       `json:"sources,omitempty"`
	ToolCounts      map[string]int `json:"toolCounts,omitempty"`
	MostEditedFiles []fileEditD    `json:"mostEditedFiles,omitempty"`
	HasMemory       bool           `json:"hasMemory"`
}

// modelD is per-model spend inside one project, so a caller can aggregate
// cost by model across the machine without re-reading every session.
type modelD struct {
	Model        string  `json:"model"`
	Sessions     int     `json:"sessions"`
	CostUSD      float64 `json:"costUSD"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
}

type fileEditD struct {
	Path         string `json:"path"`
	Count        int    `json:"count"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

type sessionD struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ProjectName string `json:"projectName"`
	ProjectDir  string `json:"projectDir"`
	CWD         string `json:"cwd,omitempty"`
	FilePath    string `json:"filePath,omitempty"`
	Source      string `json:"source"`    // always named: "claude" or "opencode"
	CostBasis   string `json:"costBasis"` // recorded | estimated | estimated-fallback
	Model       string `json:"model,omitempty"`
	GitBranch   string `json:"gitBranch,omitempty"`

	// A subagent run: which session spawned it, and the agent type it ran as.
	IsAgent         bool   `json:"isAgent"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	AgentType       string `json:"agentType,omitempty"`

	StartTime  time.Time `json:"startTime"`
	LastUpdate time.Time `json:"lastUpdate"`
	ActiveSec  float64   `json:"activeSeconds"`

	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUSD"`

	Events         int `json:"events"`
	ToolCalls      int `json:"toolCalls"`
	UserPrompts    int `json:"userPrompts"`
	BashCommands   int `json:"bashCommands"`
	Errors         int `json:"errors"`
	LinesAdded     int `json:"linesAdded"`
	LinesRemoved   int `json:"linesRemoved"`
	SubagentCalls  int `json:"subagentCalls"`
	SubagentEvents int `json:"subagentEvents"`
	Interruptions  int `json:"interruptions"`
	Denials        int `json:"denials"`
	WebRequests    int `json:"webRequests"`

	ToolCounts   map[string]int `json:"toolCounts,omitempty"`
	SkillsUsed   []string       `json:"skillsUsed,omitempty"`
	FilesRead    []string       `json:"filesRead,omitempty"`
	FilesWritten []string       `json:"filesWritten,omitempty"`
	FilesCreated []string       `json:"filesCreated,omitempty"`
	FileChurns   []fileChurnD   `json:"fileChurns,omitempty"`
}

type fileChurnD struct {
	Path         string `json:"path"`
	Edits        int    `json:"edits"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

type todoD struct {
	Subject     string `json:"subject"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	ActiveForm  string `json:"activeForm,omitempty"`
}

type eventD struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	UUID      string    `json:"uuid,omitempty"`

	UserText string `json:"userText,omitempty"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	ToolName   string `json:"toolName,omitempty"`
	ToolID     string `json:"toolId,omitempty"`
	DurationMs int    `json:"durationMs,omitempty"`
	IsError    bool   `json:"isError,omitempty"`

	LinesAdded   int `json:"linesAdded,omitempty"`
	LinesRemoved int `json:"linesRemoved,omitempty"`

	FilePath    string `json:"filePath,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`

	AgentID          string `json:"agentId,omitempty"`
	AgentDescription string `json:"agentDescription,omitempty"`
	HookEvent        string `json:"hookEvent,omitempty"`
	HookName         string `json:"hookName,omitempty"`

	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
}

// ── conversions ─────────────────────────────────────────────────────────────

// sourceName always names the agent that produced a session. The parser sets
// Source only for OpenCode; a Claude Code transcript leaves it empty, which the
// TUI reads as "not OpenCode". A consumer should not have to know that.
func sourceName(src string) string {
	if src == "" {
		return "claude"
	}
	return src
}

// costBasis says where a session's cost figure came from, so a caller can show
// a recorded cost with more confidence than a reconstructed one. OpenCode
// records real spend; Claude Code records only tokens, so cost is derived from
// the model's published rates — and from a fallback rate when the model id is
// newer than verbose's price table.
func costBasis(s session.SessionInfo) string {
	if sourceName(s.Source) == "opencode" {
		return "recorded"
	}
	if session.PriceKnown(s.Model) {
		return "estimated"
	}
	return "estimated-fallback"
}

func newProjectD(p *session.ProjectInfo) projectD {
	d := projectD{
		Name: p.ProjectName, ProjectDir: p.ProjectDir, CWD: p.CWD,
		EncodedDir: p.EncodedDir,

		Sessions: p.TotalSessions, ToolCalls: p.TotalToolCalls,
		UserPrompts: p.TotalUserPrompts, Errors: p.TotalErrors,
		SubagentCalls: p.TotalSubagentCalls, Denials: p.TotalDenials,
		Interruptions: p.TotalInterruptions,
		LinesAdded:    p.TotalLinesAdded, LinesRemoved: p.TotalLinesRemoved,

		InputTokens: p.TotalInputTokens, OutputTokens: p.TotalOutputTokens,
		CacheReadTokens:  p.TotalCacheReadTokens,
		CacheWriteTokens: p.TotalCacheWriteTokens,
		CostUSD:          p.TotalCostUSD,

		FirstSession: p.FirstSession, LastSession: p.LastSession,
		ToolCounts: p.ToolCounts,
		HasMemory:  p.Memory != "",
	}

	for _, f := range p.MostEditedFiles {
		d.MostEditedFiles = append(d.MostEditedFiles, fileEditD{
			Path: f.Path, Count: f.Count,
			LinesAdded: f.LinesAdded, LinesRemoved: f.LinesRemoved,
		})
	}

	// Per-model spend and the mix of sources, folded out of the sessions.
	models := map[string]*modelD{}
	sources := map[string]bool{}
	for _, s := range p.Sessions {
		if s.Source != "" {
			sources[s.Source] = true
		}
		name := s.Model
		if name == "" {
			name = "unknown"
		}
		m, ok := models[name]
		if !ok {
			m = &modelD{Model: name}
			models[name] = m
		}
		m.Sessions++
		m.CostUSD += s.CostUSD
		m.InputTokens += s.InputTokens
		m.OutputTokens += s.OutputTokens
	}
	for _, m := range models {
		d.Models = append(d.Models, *m)
	}
	sort.Slice(d.Models, func(i, j int) bool { return d.Models[i].CostUSD > d.Models[j].CostUSD })

	for s := range sources {
		d.Sources = append(d.Sources, s)
	}
	sort.Strings(d.Sources)

	return d
}

func newSessionD(s session.SessionInfo) sessionD {
	d := sessionD{
		ID: s.ID, Title: s.Title,
		ProjectName: s.ProjectName, ProjectDir: s.ProjectDir,
		CWD: s.CWD, FilePath: s.FilePath,
		Source: sourceName(s.Source), CostBasis: costBasis(s),
		Model: s.Model, GitBranch: s.GitBranch,
		IsAgent: s.IsAgent, ParentSessionID: s.ParentSessionID,
		AgentType: s.AgentType,

		StartTime: s.StartTime, LastUpdate: s.LastUpdate,
		ActiveSec: s.ActiveDuration.Seconds(),

		InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
		CacheReadTokens: s.CacheReadTokens, CacheWriteTokens: s.CacheWriteTokens,
		CostUSD: s.CostUSD,

		Events: s.EventCount, ToolCalls: s.ToolCallCount,
		UserPrompts: s.UserPrompts, BashCommands: s.BashCommands,
		Errors:     s.Errors,
		LinesAdded: s.LinesAdded, LinesRemoved: s.LinesRemoved,
		SubagentCalls: s.SubagentCalls, SubagentEvents: s.SubagentEvents,
		Interruptions: s.Interruptions, Denials: s.Denials,
		WebRequests: s.WebRequests,

		ToolCounts: s.ToolCounts, SkillsUsed: s.SkillsUsed,
		FilesRead: s.FilesRead, FilesWritten: s.FilesWritten,
		FilesCreated: s.FilesCreated,
	}
	for _, c := range s.FileChurns {
		d.FileChurns = append(d.FileChurns, fileChurnD{
			Path: c.Path, Edits: c.Edits,
			LinesAdded: c.LinesAdded, LinesRemoved: c.LinesRemoved,
		})
	}
	return d
}

// eventTypeNames maps the internal enum to stable wire names. A type missing
// from this table serialises as "unknown" rather than a bare integer, so an
// added EventType cannot silently become a different event to a consumer.
var eventTypeNames = map[session.EventType]string{
	session.EventUserPrompt:    "user_prompt",
	session.EventThinking:      "thinking",
	session.EventText:          "text",
	session.EventToolUse:       "tool_use",
	session.EventToolResult:    "tool_result",
	session.EventSystem:        "system",
	session.EventCompaction:    "compaction",
	session.EventAgentProgress: "agent_progress",
	session.EventHookProgress:  "hook_progress",
	session.EventBashProgress:  "bash_progress",
	session.EventTurnDuration:  "turn_duration",
	session.EventToolDenied:    "tool_denied",
	session.EventUserFileEdit:  "user_file_edit",
	session.EventDiagnostics:   "diagnostics",
}

func eventTypeName(t session.EventType) string {
	if name, ok := eventTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

func newEventD(e session.Event) eventD {
	return eventD{
		Type: eventTypeName(e.Type), Timestamp: e.Timestamp, UUID: e.UUID,
		UserText: e.UserText, Text: e.Text, Thinking: e.Thinking,
		ToolName: e.ToolName, ToolID: e.ToolID,
		DurationMs: e.DurationMs, IsError: e.IsError,
		LinesAdded: e.LinesAdded, LinesRemoved: e.LinesRemoved,
		FilePath: e.FilePath, IsSidechain: e.IsSidechain,
		AgentID: e.AgentID, AgentDescription: e.AgentDescription,
		HookEvent: e.HookEvent, HookName: e.HookName,
		InputTokens: e.InputTokens, OutputTokens: e.OutputTokens,
	}
}
