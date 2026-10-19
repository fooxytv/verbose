package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// codexHome writes a pair of Codex databases with the columns the real ones
// have, including the NOT NULL ones: a nullable column where Codex has a
// required one hides a scan error, and loadCXThreads skips a row it cannot
// read rather than failing loudly.
func codexHome(t *testing.T, stateGen int, threads []map[string]any, items []map[string]any, turns []map[string]any, edges [][2]string) string {
	t.Helper()
	dir := t.TempDir()

	state := openCodexFixture(t, filepath.Join(dir, fmt.Sprintf("state_%d.sqlite", stateGen)), `
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			cwd TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			name TEXT,
			model TEXT,
			model_provider TEXT NOT NULL DEFAULT '',
			git_branch TEXT,
			tokens_used INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			created_at_ms INTEGER,
			updated_at_ms INTEGER,
			source TEXT NOT NULL DEFAULT 'vscode',
			thread_source TEXT
		);
		CREATE TABLE thread_spawn_edges (
			parent_thread_id TEXT NOT NULL,
			child_thread_id TEXT NOT NULL PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'completed'
		);`)
	defer state.Close()

	for _, th := range threads {
		insert(t, state, "threads", th)
	}
	for _, e := range edges {
		insert(t, state, "thread_spawn_edges", map[string]any{
			"parent_thread_id": e[0], "child_thread_id": e[1],
		})
	}

	hist := openCodexFixture(t, filepath.Join(dir, "thread_history_1.sqlite"), `
		CREATE TABLE thread_items (
			thread_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			rollout_ordinal INTEGER NOT NULL,
			created_at_ms INTEGER NOT NULL,
			item_json TEXT NOT NULL,
			item_type TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (thread_id, turn_id, item_id)
		);
		CREATE TABLE thread_turns (
			thread_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			rollout_ordinal INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'completed',
			duration_ms INTEGER,
			PRIMARY KEY (thread_id, turn_id)
		);`)
	defer hist.Close()

	for _, it := range items {
		insert(t, hist, "thread_items", it)
	}
	for _, tn := range turns {
		insert(t, hist, "thread_turns", tn)
	}

	return dir
}

func openCodexFixture(t *testing.T, path, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func insert(t *testing.T, db *sql.DB, table string, row map[string]any) {
	t.Helper()
	cols, marks, args := "", "", []any{}
	for k, v := range row {
		if cols != "" {
			cols, marks = cols+", ", marks+", "
		}
		cols, marks = cols+k, marks+"?"
		args = append(args, v)
	}
	if _, err := db.Exec("INSERT INTO "+table+" ("+cols+") VALUES ("+marks+")", args...); err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

// cxItemRow is one thread_items row, with the payload encoded.
func cxItemRow(thread string, ordinal int, payload map[string]any) map[string]any {
	body, _ := json.Marshal(payload)
	return map[string]any{
		"thread_id": thread, "turn_id": "turn-1",
		"item_id":         fmt.Sprintf("item-%d", ordinal),
		"rollout_ordinal": ordinal,
		"created_at_ms":   1_700_000_000_000 + int64(ordinal)*1000,
		"item_json":       string(body),
		"item_type":       payload["type"],
	}
}

// Every item type Codex records, end to end: the prompt, the narration, the
// reasoning headline, a shell command, a new file, a patched file and an MCP
// call.
func TestCodexReadsEveryItemType(t *testing.T) {
	dir := codexHome(t, 5,
		[]map[string]any{{
			"id": "t1", "cwd": "/repo", "title": "raw opening message",
			"name": "Fix the handler", "model": "gpt-6-astra",
			"model_provider": "openai", "git_branch": "main",
			"tokens_used": 4321, "created_at_ms": 1_700_000_000_000,
			"updated_at_ms": 1_700_000_100_000,
		}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "userMessage",
				"content": []map[string]any{{"type": "text", "text": "fix the 404"}}}),
			cxItemRow("t1", 2, map[string]any{"type": "reasoning",
				"summary": []string{"**Reading the handler**"}}),
			cxItemRow("t1", 3, map[string]any{"type": "agentMessage",
				"text": "## The fix\n\nThe route was never registered."}),
			cxItemRow("t1", 4, map[string]any{"type": "commandExecution",
				"command": "go test ./...", "cwd": "/repo",
				"aggregatedOutput": "ok\n", "exitCode": 0,
				"status": "completed", "durationMs": 1200}),
			cxItemRow("t1", 5, map[string]any{"type": "fileChange",
				"status": "completed", "changes": []map[string]any{{
					"path": "/repo/new.go", "kind": map[string]any{"type": "add"},
					"diff": "package main\n\nfunc main() {}\n",
				}}}),
			cxItemRow("t1", 6, map[string]any{"type": "fileChange",
				"status": "completed", "changes": []map[string]any{{
					"path": "/repo/handler.go", "kind": map[string]any{"type": "update"},
					"diff": "@@ -1,2 +1,3 @@\n ctx\n-old line\n+new line\n+another\n",
				}}}),
			cxItemRow("t1", 7, map[string]any{"type": "mcpToolCall",
				"server": "node_repl", "tool": "js", "status": "completed",
				"arguments": map[string]any{"code": "1+1"},
				"result":    map[string]any{"content": []map[string]any{{"type": "text", "text": "2"}}}}),
		},
		[]map[string]any{
			{"thread_id": "t1", "turn_id": "turn-1", "duration_ms": 90_000},
			{"thread_id": "t1", "turn_id": "turn-2", "duration_ms": 10_000},
		}, nil)

	sessions, err := ParseCodexDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(sessions))
	}
	s := sessions[0]

	if s.Info.ID != "cx-t1" {
		t.Errorf("ID = %q, want cx-t1", s.Info.ID)
	}
	if s.Info.Source != "codex" {
		t.Errorf("Source = %q, want codex", s.Info.Source)
	}
	// The generated name, not the verbatim opening message.
	if s.Info.Title != "Fix the handler" {
		t.Errorf("Title = %q, want the generated name", s.Info.Title)
	}
	if s.Info.Model != "openai/gpt-6-astra" {
		t.Errorf("Model = %q, want provider/model", s.Info.Model)
	}
	if s.Info.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want main", s.Info.GitBranch)
	}
	if s.Info.TokensTotal != 4321 {
		t.Errorf("TokensTotal = %d, want 4321", s.Info.TokensTotal)
	}
	if got := s.Info.ActiveDuration.Seconds(); got != 100 {
		t.Errorf("ActiveDuration = %vs, want the sum of the turns (100s)", got)
	}

	kinds := map[EventType]int{}
	for _, e := range s.Events {
		kinds[e.Type]++
	}
	if kinds[EventUserPrompt] != 1 || kinds[EventText] != 1 || kinds[EventThinking] != 1 {
		t.Errorf("prompt/text/thinking counts = %d/%d/%d, want 1/1/1",
			kinds[EventUserPrompt], kinds[EventText], kinds[EventThinking])
	}
	if kinds[EventToolUse] != 4 {
		t.Errorf("tool events = %d, want 4 (bash, write, edit, mcp)", kinds[EventToolUse])
	}

	byTool := map[string]Event{}
	for _, e := range s.Events {
		if e.Type == EventToolUse {
			byTool[e.ToolName] = e
		}
	}

	bash, ok := byTool["Bash"]
	if !ok {
		t.Fatal("a commandExecution did not become a Bash call")
	}
	if cmd, _ := stringInput(bash.ToolInput, "command"); cmd != "go test ./..." {
		t.Errorf("Bash command = %q", cmd)
	}
	if bash.Result == nil || bash.Result.Stdout != "ok\n" {
		t.Error("the command's output did not survive")
	}
	if bash.DurationMs != 1200 {
		t.Errorf("Bash DurationMs = %d, want 1200", bash.DurationMs)
	}

	mcp, ok := byTool["mcp__node_repl__js"]
	if !ok {
		t.Fatalf("an MCP call was not named like Claude Code's: %v", byTool)
	}
	if mcp.ToolOutput != "2" {
		t.Errorf("MCP output = %q, want 2", mcp.ToolOutput)
	}

	if s.Info.UserPrompts != 1 {
		t.Errorf("UserPrompts = %d, want 1", s.Info.UserPrompts)
	}
	if s.Info.BashCommands != 1 {
		t.Errorf("BashCommands = %d, want 1", s.Info.BashCommands)
	}
	if s.Info.Errors != 0 {
		t.Errorf("Errors = %d, want 0", s.Info.Errors)
	}
}

// An "add" has no before-state, so Codex puts the file's whole content in the
// same field an "update" uses for hunks. The content has to arrive as a
// Write's input or the replay has nothing to type; the hunks have to arrive as
// a patch or the diff view has nothing to pair.
func TestCodexAddCarriesContentAndUpdateCarriesHunks(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": "/repo", "name": "changes"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "fileChange",
				"changes": []map[string]any{{
					"path": "/repo/new.go", "kind": map[string]any{"type": "add"},
					"diff": "package main\n",
				}}}),
			cxItemRow("t1", 2, map[string]any{"type": "fileChange",
				"changes": []map[string]any{{
					"path": "/repo/old.go", "kind": map[string]any{"type": "update"},
					"diff": "@@ -1,2 +1,2 @@\n ctx\n-gone\n+added\n",
				}}}),
		}, nil, nil)

	sessions, err := ParseCodexDir(dir)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(sessions))
	}
	s := sessions[0]

	var write, edit Event
	for _, e := range s.Events {
		switch e.ToolName {
		case "Write":
			write = e
		case "Edit":
			edit = e
		}
	}

	if write.ToolName == "" {
		t.Fatal("an add did not become a Write")
	}
	if content, _ := stringInput(write.ToolInput, "content"); content != "package main\n" {
		t.Errorf("the new file's content = %q, want it on the Write's input", content)
	}
	if write.Result == nil || len(write.Result.StructuredPatch) != 0 {
		t.Error("a new file must carry no patch: there is no before-state to diff")
	}
	// stepCode is what the replay types out, and it only looks at the input.
	if _, code := stepCode(write); code != "package main\n" {
		t.Errorf("stepCode = %q, want the new file's content", code)
	}

	if edit.ToolName == "" {
		t.Fatal("an update did not become an Edit")
	}
	hunks := edit.Result.StructuredPatch
	if len(hunks) != 1 {
		t.Fatalf("parsed %d hunks, want 1", len(hunks))
	}
	if hunks[0].OldStart != 1 || hunks[0].NewLines != 2 {
		t.Errorf("hunk header lost: %+v", hunks[0])
	}
	if len(hunks[0].Lines) != 3 {
		t.Errorf("hunk has %d lines, want 3", len(hunks[0].Lines))
	}
	if edit.LinesAdded != 1 || edit.LinesRemoved != 1 {
		t.Errorf("churn = +%d -%d, want +1 -1", edit.LinesAdded, edit.LinesRemoved)
	}
	if len(s.Info.FilesCreated) != 1 || len(s.Info.FilesWritten) != 1 {
		t.Errorf("created %v, written %v — one each expected",
			s.Info.FilesCreated, s.Info.FilesWritten)
	}
}

// A failed command records its reason in the same field a successful one uses
// for output, so the status is the only failure signal.
func TestCodexFailedCommandIsAnError(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": "/repo", "name": "fails"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "commandExecution",
				"command": "python3 -c 'import qrcode'", "status": "failed",
				"aggregatedOutput": "ModuleNotFoundError: No module named 'qrcode'\n",
				"exitCode":         1}),
		}, nil, nil)

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Info.Errors != 1 {
		t.Errorf("Errors = %d, want 1", s.Info.Errors)
	}
	e := s.Events[0]
	if !e.IsError {
		t.Error("a failed command must be marked as an error")
	}
	if e.Result == nil || e.Result.Raw == "" {
		t.Error("the failure text must survive as the result")
	}
}

// Codex writes a threads row before anything happens in it, so an abandoned
// voice chat is a row with no items. It is not a session.
func TestCodexSkipsAThreadWithNoItems(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{
			{"id": "empty", "cwd": "/repo", "name": "New voice chat"},
			{"id": "real", "cwd": "/repo", "name": "Did something"},
		},
		[]map[string]any{
			cxItemRow("real", 1, map[string]any{"type": "agentMessage", "text": "done"}),
		}, nil, nil)

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions, want only the one with items", len(sessions))
	}
	if sessions[0].Info.ID != "cx-real" {
		t.Errorf("kept %q, want cx-real", sessions[0].Info.ID)
	}
}

// Without a generated name the opening message stands in — but only its first
// line, because a real one is pasted context or an XML delegation wrapper
// running to thousands of characters.
func TestCodexFallsBackToTheFirstLineOfTheOpeningMessage(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": "/repo",
			"title": "## Referenced conversation\n\nthis is an enormous paste\nand more"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "agentMessage", "text": "ok"}),
		}, nil, nil)

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if got := sessions[0].Info.Title; got != "## Referenced conversation" {
		t.Errorf("Title = %q, want the first line only", got)
	}
}

// The databases carry a schema generation in the file name, so the newest pair
// is the one to read. A home with only one of the two is not readable at all.
func TestCodexPicksTheNewestGeneration(t *testing.T) {
	dir := codexHome(t, 5,
		[]map[string]any{{"id": "t1", "cwd": "/repo", "name": "new"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "agentMessage", "text": "ok"}),
		}, nil, nil)

	// An older generation beside it, holding a thread that must not be read.
	old := openCodexFixture(t, filepath.Join(dir, "state_4.sqlite"),
		`CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '', name TEXT, model TEXT,
			model_provider TEXT NOT NULL DEFAULT '', git_branch TEXT,
			tokens_used INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0,
			created_at_ms INTEGER, updated_at_ms INTEGER,
			source TEXT NOT NULL DEFAULT 'vscode', thread_source TEXT);`)
	insert(t, old, "threads", map[string]any{"id": "stale", "cwd": "/repo", "name": "stale"})
	old.Close()

	state, history, ok := CodexDBs(dir)
	if !ok {
		t.Fatal("both databases should have been found")
	}
	if filepath.Base(state) != "state_5.sqlite" {
		t.Errorf("state = %q, want generation 5", filepath.Base(state))
	}
	if filepath.Base(history) != "thread_history_1.sqlite" {
		t.Errorf("history = %q", filepath.Base(history))
	}

	sessions, err := ParseCodexDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.Info.ID == "cx-stale" {
			t.Error("a thread from the older generation was read")
		}
	}
}

// A Codex home that has never run, or one holding only half the pair, yields
// nothing and no error: an installed CLI looks exactly like that.
func TestCodexEmptyHomeIsNotAnError(t *testing.T) {
	sessions, err := ParseCodexDir(t.TempDir())
	if err != nil {
		t.Errorf("an empty home errored: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("parsed %d sessions from an empty home", len(sessions))
	}
}

// Codex owns a review thread as a subagent and says so two ways: a spawn edge
// to the parent, and a JSON source column naming the agent. The parent id has
// to be the PREFIXED id or grouping cannot match it against the list.
func TestCodexSubagentIsLinkedToItsParent(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{
			{"id": "parent", "cwd": "/repo", "name": "The work"},
			{"id": "child", "cwd": "/repo", "name": "Guardian review",
				"source": `{"subagent":{"other":"guardian"}}`},
		},
		[]map[string]any{
			cxItemRow("parent", 1, map[string]any{"type": "agentMessage", "text": "a"}),
			cxItemRow("child", 1, map[string]any{"type": "agentMessage", "text": "b"}),
		}, nil, [][2]string{{"parent", "child"}})

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 2 {
		t.Fatalf("parsed %d sessions, want 2", len(sessions))
	}

	var infos []SessionInfo
	var child SessionInfo
	for _, s := range sessions {
		infos = append(infos, s.Info)
		if s.Info.ID == "cx-child" {
			child = s.Info
		}
	}
	if !child.IsAgent {
		t.Error("the spawned thread is not marked as an agent")
	}
	if child.AgentType != "guardian" {
		t.Errorf("AgentType = %q, want guardian", child.AgentType)
	}
	if child.ParentSessionID != "cx-parent" {
		t.Errorf("ParentSessionID = %q, want the prefixed id", child.ParentSessionID)
	}

	grouped := GroupSubagents(infos)
	if len(grouped) != 2 || grouped[0].ID != "cx-parent" || grouped[1].ID != "cx-child" {
		t.Errorf("grouping did not nest the agent under its parent: %v", grouped)
	}
}

// A thread that is not an agent must not claim an agent type. thread_source
// records how a thread was started (voice_chat, chatgpt_handoff) and reporting
// that as an agent type would make every voice chat look like a subagent run.
func TestCodexPlainThreadIsNotAnAgent(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": "/repo", "name": "Chat",
			"source": "vscode", "thread_source": "voice_chat"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "agentMessage", "text": "hi"}),
		}, nil, nil)

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if sessions[0].Info.IsAgent || sessions[0].Info.AgentType != "" {
		t.Errorf("IsAgent=%v AgentType=%q — a voice chat is not a subagent",
			sessions[0].Info.IsAgent, sessions[0].Info.AgentType)
	}
}

// Reasoning is nearly all absent: the content array is empty on every item on
// disk and most have no summary either. An item with nothing in it must not
// become a blank thinking row.
func TestCodexEmptyReasoningIsDropped(t *testing.T) {
	dir := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": "/repo", "name": "quiet"}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "reasoning",
				"summary": []string{}, "content": []string{}}),
			cxItemRow("t1", 2, map[string]any{"type": "reasoning",
				"summary": []string{"**Planning the change**"}}),
		}, nil, nil)

	sessions, _ := ParseCodexDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if n := len(sessions[0].Events); n != 1 {
		t.Fatalf("emitted %d events, want only the reasoning that had a summary", n)
	}
	if got := sessions[0].Events[0].Thinking; got != "**Planning the change**" {
		t.Errorf("Thinking = %q", got)
	}
}

// A source that reports one number and no split must still show a total, and
// one that reports a split must not be overridden by it.
func TestTotalTokensPrefersTheBreakdown(t *testing.T) {
	split := SessionInfo{InputTokens: 10, OutputTokens: 5, TokensTotal: 999}
	if got := split.TotalTokens(); got != 15 {
		t.Errorf("TotalTokens = %d, want the breakdown's 15", got)
	}
	total := SessionInfo{TokensTotal: 4321}
	if got := total.TotalTokens(); got != 4321 {
		t.Errorf("TotalTokens = %d, want 4321", got)
	}
}

// A project is a DIRECTORY, and more than one agent can work in the same one —
// on the machine this was written on, one directory holds both OpenCode and
// Copilot sessions. The project has to report every tool that worked there,
// and the token total has to include a source that reports no breakdown.
func TestProjectInfoCountsEverySourceInADirectory(t *testing.T) {
	const repo = "/fixture-repo-sources"

	cx := codexHome(t, 1,
		[]map[string]any{{"id": "t1", "cwd": repo, "name": "codex thread",
			"tokens_used": 4321}},
		[]map[string]any{
			cxItemRow("t1", 1, map[string]any{"type": "agentMessage", "text": "a"}),
		}, nil, nil)

	cp := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": repo, "summary": "copilot session"}},
		[]map[string]any{{"session_id": "s1", "turn_index": 0, "user_message": "go",
			"assistant_response": "done"}},
		nil)

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// No Claude transcripts: this project is only the two synthetic homes.
	store.baseDir = t.TempDir()
	store.AddCodexDir(cx)
	store.AddCopilotDir(cp)
	if err := store.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	proj := store.GetProjectInfo(repo)
	if proj.TotalSessions != 2 {
		t.Fatalf("TotalSessions = %d, want 2", proj.TotalSessions)
	}
	if got := strings.Join(proj.Sources, "+"); got != "codex+copilot" {
		t.Errorf("Sources = %q, want codex+copilot", got)
	}
	if proj.SourceSessions["codex"] != 1 || proj.SourceSessions["copilot"] != 1 {
		t.Errorf("SourceSessions = %v, want one each", proj.SourceSessions)
	}
	// Codex reports a total and no breakdown; it must still reach the project.
	if got := proj.TotalTokens(); got != 4321 {
		t.Errorf("TotalTokens = %d, want the Codex thread's 4321", got)
	}
}

// The Claude Code parser never sets Source — it is empty on every Claude
// session on disk — so the project's source set has to read an empty one as
// Claude. Dropping it instead leaves every Claude-only project reporting no
// sources at all.
func TestProjectSourcesCountAnEmptySourceAsClaude(t *testing.T) {
	const repo = "/fixture-repo-claude"

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.baseDir = t.TempDir()

	// Inserted directly: what the Claude parser produces is a session with no
	// Source, and that is the case under test.
	store.sessions["plain"] = &Session{Info: SessionInfo{
		ID: "plain", ProjectDir: repo, ProjectName: "claude-only",
		InputTokens: 10, OutputTokens: 5,
	}}

	proj := store.GetProjectInfo(repo)
	if got := strings.Join(proj.Sources, "+"); got != "claude" {
		t.Errorf("Sources = %q, want claude", got)
	}
	if proj.SourceSessions["claude"] != 1 {
		t.Errorf("SourceSessions = %v, want claude 1", proj.SourceSessions)
	}
	if got := proj.TotalTokens(); got != 15 {
		t.Errorf("TotalTokens = %d, want 15", got)
	}
}

// A breakdown and a reported total add up rather than one winning: only a
// source with no breakdown sets the reported number, so nothing is counted
// twice.
func TestProjectTotalTokensAddsBothKinds(t *testing.T) {
	empty := ProjectInfo{}
	if n := empty.TotalTokens(); n != 0 {
		t.Errorf("an empty project has %d tokens", n)
	}
	mixed := ProjectInfo{TotalInputTokens: 10, TotalOutputTokens: 5, TotalTokensReported: 7}
	if got := mixed.TotalTokens(); got != 22 {
		t.Errorf("TotalTokens = %d, want 22", got)
	}
}

// A step's headline names the agent whose work it is. With four of them
// reading into one list, "Claude's plan" on a Codex thread names the wrong
// one — and that headline is what the replay puts on screen.
func TestReplayTitlesNameTheRightAgent(t *testing.T) {
	for source, want := range map[string]string{
		"":         "Claude's plan",
		"codex":    "Codex's plan",
		"opencode": "OpenCode's plan",
		"copilot":  "Copilot's plan",
	} {
		sess := &Session{
			Info: SessionInfo{ID: "s", Source: source, CWD: "/repo"},
			Events: []Event{
				{Type: EventUserPrompt, UserText: "do it"},
				{Type: EventText, Text: "Here is the plan."},
			},
		}
		steps := BuildReplay(sess)
		if len(steps) != 2 {
			t.Fatalf("source %q: built %d steps, want 2", source, len(steps))
		}
		if steps[1].Title != want {
			t.Errorf("source %q: title = %q, want %q", source, steps[1].Title, want)
		}
	}

	if got := AgentName("codex"); got != "Codex" {
		t.Errorf("AgentName(codex) = %q", got)
	}
	// Claude Code's parser never sets Source.
	if got := AgentName(""); got != "Claude" {
		t.Errorf("AgentName(\"\") = %q, want Claude", got)
	}
}
