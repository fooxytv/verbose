package session

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// ocFixture builds a miniature OpenCode database: one root session that reads a
// file, runs a failing command, patches two files in one call, and delegates to
// a subagent session.
func ocFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := []string{
		`CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT)`,
		`CREATE TABLE session (
			id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, title TEXT,
			directory TEXT, model TEXT, agent TEXT, cost REAL,
			tokens_input INT, tokens_output INT, tokens_cache_read INT, tokens_cache_write INT,
			time_created INT, time_updated INT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INT, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INT, data TEXT)`,
		`CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, priority TEXT, position INT)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`INSERT INTO project VALUES ('p1', '/repo')`)
	exec(`INSERT INTO session VALUES ('ses_root','p1',NULL,'Root work','/repo/sub',
		'{"id":"gpt-5.6","providerID":"openai"}','build', 0, 100, 20, 5, 7, 1000, 9000)`)
	exec(`INSERT INTO session VALUES ('ses_kid','p1','ses_root','Scout (@explore subagent)','/repo/sub',
		'{"id":"gpt-5.6","providerID":"openai"}','explore', 0, 50, 10, 0, 0, 2000, 3000)`)

	// Root turn 1: the user's prompt.
	exec(`INSERT INTO message VALUES ('msg_1','ses_root',1000,'{"role":"user"}')`)
	exec(`INSERT INTO part VALUES ('prt_001','msg_1','ses_root',1000,'{"type":"text","text":"fix the config"}')`)

	// Root turn 2: assistant reasons, reads, fails a command, patches, delegates.
	exec(`INSERT INTO message VALUES ('msg_2','ses_root',2000,
		'{"role":"assistant","modelID":"gpt-5.6","providerID":"openai"}')`)
	exec(`INSERT INTO part VALUES ('prt_002','msg_2','ses_root',2000,'{"type":"step-start"}')`)
	exec(`INSERT INTO part VALUES ('prt_003','msg_2','ses_root',2000,
		'{"type":"reasoning","text":"weighing options","time":{"start":2000,"end":2500}}')`)
	exec(`INSERT INTO part VALUES ('prt_004','msg_2','ses_root',2000,'{"type":"text","text":"On it."}')`)
	exec(`INSERT INTO part VALUES ('prt_005','msg_2','ses_root',2000,
		'{"type":"tool","tool":"read","callID":"c1","state":{"status":"completed",
		  "input":{"filePath":"/repo/a.go"},"output":"package main",
		  "time":{"start":2100,"end":2150}}}')`)
	exec(`INSERT INTO part VALUES ('prt_006','msg_2','ses_root',2000,
		'{"type":"tool","tool":"bash","callID":"c2","state":{"status":"error",
		  "input":{"command":"go test ./..."},"error":"Error: Exit code 127",
		  "time":{"start":2200,"end":2400}}}')`)
	exec(`INSERT INTO part VALUES ('prt_007','msg_2','ses_root',2000,
		'{"type":"tool","tool":"apply_patch","callID":"c3","state":{"status":"completed",
		  "input":{"patchText":"..."},"output":"ok","metadata":{"diff":
		  "Index: /repo/a.go\n===================================================================\n--- /repo/a.go\n+++ /repo/a.go\n@@ -1,2 +1,3 @@\n package main\n-old\n+new\n+extra\nIndex: /repo/new.go\n===================================================================\n--- /repo/new.go\n+++ /repo/new.go\n@@ -0,0 +1,2 @@\n+package main\n+// fresh\n"}}}')`)
	exec(`INSERT INTO part VALUES ('prt_008','msg_2','ses_root',2000,
		'{"type":"tool","tool":"task","callID":"c4","state":{"status":"completed",
		  "input":{"description":"Scout","subagent_type":"explore"},"output":"done",
		  "metadata":{"sessionId":"ses_kid"}}}')`)
	exec(`INSERT INTO part VALUES ('prt_009','msg_2','ses_root',2000,'{"type":"step-finish","reason":"stop"}')`)

	// The subagent's own turn.
	exec(`INSERT INTO message VALUES ('msg_3','ses_kid',2500,
		'{"role":"assistant","modelID":"gpt-5.6","providerID":"openai"}')`)
	exec(`INSERT INTO part VALUES ('prt_010','msg_3','ses_kid',2500,
		'{"type":"tool","tool":"grep","callID":"c5","state":{"status":"completed",
		  "input":{"pattern":"func"},"output":"a.go:1"}}')`)

	exec(`INSERT INTO todo VALUES ('ses_root','Fix the config','completed','high',0)`)
	exec(`INSERT INTO todo VALUES ('ses_kid','Scout the repo','completed','low',0)`)

	return path
}

func TestParseOpenCodeDB(t *testing.T) {
	sessions, err := ParseOpenCodeDB(ocFixture(t))
	if err != nil {
		t.Fatal(err)
	}

	// The subagent session is folded into its parent, not listed separately.
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1 (subagent should be inlined)", len(sessions))
	}
	sess := sessions[0]
	info := sess.Info

	if info.ID != "oc-ses_root" {
		t.Errorf("ID = %q, want oc-ses_root", info.ID)
	}
	if info.Source != "opencode" {
		t.Errorf("Source = %q, want opencode", info.Source)
	}
	if info.ProjectDir != "/repo" || info.ProjectName != "repo" {
		t.Errorf("project = %q/%q, want /repo/repo", info.ProjectDir, info.ProjectName)
	}
	if info.CWD != "/repo/sub" {
		t.Errorf("CWD = %q, want /repo/sub", info.CWD)
	}
	if info.Model != "openai/gpt-5.6" {
		t.Errorf("Model = %q, want openai/gpt-5.6", info.Model)
	}
	if info.InputTokens != 100 || info.OutputTokens != 20 {
		t.Errorf("tokens = %d/%d, want 100/20", info.InputTokens, info.OutputTokens)
	}
	// OpenCode reports cost itself; it must never be estimated from Claude pricing.
	if info.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 (as recorded)", info.CostUSD)
	}

	if info.UserPrompts != 1 {
		t.Errorf("UserPrompts = %d, want 1", info.UserPrompts)
	}
	if info.Errors != 1 {
		t.Errorf("Errors = %d, want 1", info.Errors)
	}
	if info.BashCommands != 1 {
		t.Errorf("BashCommands = %d, want 1", info.BashCommands)
	}
	if info.SubagentCalls != 1 {
		t.Errorf("SubagentCalls = %d, want 1", info.SubagentCalls)
	}

	// read, bash, apply_patch, task, and the subagent's grep — five calls, even
	// though apply_patch reports as two per-file events.
	if info.ToolCallCount != 5 {
		t.Errorf("ToolCallCount = %d, want 5", info.ToolCallCount)
	}
	wantCounts := map[string]int{"Read": 1, "Bash": 1, "MultiEdit": 1, "Task": 1, "Grep": 1}
	for name, want := range wantCounts {
		if info.ToolCounts[name] != want {
			t.Errorf("ToolCounts[%s] = %d, want %d", name, info.ToolCounts[name], want)
		}
	}

	if len(sess.Todos) != 2 {
		t.Errorf("Todos = %d, want 2 (parent + subagent)", len(sess.Todos))
	}
}

func TestOpenCodeEventOrderAndShape(t *testing.T) {
	sessions, err := ParseOpenCodeDB(ocFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	events := sessions[0].Events

	// step-start/step-finish carry no content and must not reach the timeline.
	want := []EventType{
		EventUserPrompt, // "fix the config"
		EventThinking,   // reasoning
		EventText,       // "On it."
		EventToolUse,    // read
		EventToolUse,    // bash (failed)
		EventToolUse,    // apply_patch → /repo/a.go
		EventToolUse,    // apply_patch → /repo/new.go
		EventToolUse,    // task
		EventToolUse,    // subagent's grep, spliced in after the task call
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, wt := range want {
		if events[i].Type != wt {
			t.Errorf("event %d type = %v, want %v", i, events[i].Type, wt)
		}
	}

	// Tool names and argument keys are normalized to the Claude Code spelling
	// the rest of the UI keys off.
	read := events[3]
	if read.ToolName != "Read" {
		t.Errorf("read tool name = %q, want Read", read.ToolName)
	}
	if fp, _ := read.ToolInput["file_path"].(string); fp != "/repo/a.go" {
		t.Errorf("read file_path = %q, want /repo/a.go", fp)
	}
	if read.DurationMs != 50 {
		t.Errorf("read DurationMs = %d, want 50", read.DurationMs)
	}

	// A failed call folds its error into the same event.
	bash := events[4]
	if !bash.IsError {
		t.Error("failed bash call not marked as an error")
	}
	if bash.ToolOutput != "Error: Exit code 127" {
		t.Errorf("bash output = %q", bash.ToolOutput)
	}
	if bash.Result == nil || bash.Result.Raw != "Error: Exit code 127" {
		t.Errorf("bash Result.Raw not set: %+v", bash.Result)
	}

	// One apply_patch call becomes one event per file, each with its own diff.
	edit, create := events[5], events[6]
	if edit.ToolName != "Edit" {
		t.Errorf("edited file tool = %q, want Edit", edit.ToolName)
	}
	if edit.Result == nil || edit.Result.FilePath != "/repo/a.go" {
		t.Fatalf("edit result = %+v", edit.Result)
	}
	if edit.LinesAdded != 2 || edit.LinesRemoved != 1 {
		t.Errorf("edit churn = +%d/-%d, want +2/-1", edit.LinesAdded, edit.LinesRemoved)
	}
	// A diff with an empty old side created the file, so it reads as a Write.
	if create.ToolName != "Write" {
		t.Errorf("added file tool = %q, want Write", create.ToolName)
	}
	if create.Result == nil || create.Result.FilePath != "/repo/new.go" {
		t.Fatalf("create result = %+v", create.Result)
	}

	// Only the subagent's turns are flagged as sidechain.
	if events[7].IsSidechain {
		t.Error("task call should not be marked sidechain")
	}
	if !events[8].IsSidechain {
		t.Error("subagent grep should be marked sidechain")
	}
	if sessions[0].Info.SubagentEvents != 1 {
		t.Errorf("SubagentEvents = %d, want 1", sessions[0].Info.SubagentEvents)
	}
}

func TestOpenCodeFileStats(t *testing.T) {
	sessions, err := ParseOpenCodeDB(ocFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	info := sessions[0].Info

	if len(info.FilesRead) != 1 || info.FilesRead[0] != "/repo/a.go" {
		t.Errorf("FilesRead = %v, want [/repo/a.go]", info.FilesRead)
	}
	if len(info.FilesWritten) != 1 || info.FilesWritten[0] != "/repo/a.go" {
		t.Errorf("FilesWritten = %v, want [/repo/a.go]", info.FilesWritten)
	}
	if len(info.FilesCreated) != 1 || info.FilesCreated[0] != "/repo/new.go" {
		t.Errorf("FilesCreated = %v, want [/repo/new.go]", info.FilesCreated)
	}
	if info.LinesAdded != 4 || info.LinesRemoved != 1 {
		t.Errorf("churn = +%d/-%d, want +4/-1", info.LinesAdded, info.LinesRemoved)
	}
	if len(info.FileChurns) != 2 {
		t.Fatalf("FileChurns = %d, want 2", len(info.FileChurns))
	}
}

func TestParseOCDiff(t *testing.T) {
	diff := "Index: /a.go\n" +
		"===================================================================\n" +
		"--- /a.go\n+++ /a.go\n" +
		"@@ -1,3 +1,4 @@\n one\n-two\n+TWO\n+three\n" +
		"@@ -10 +11,2 @@\n+tail\n" +
		"Index: /b.go\n--- /b.go\n+++ /b.go\n@@ -0,0 +1 @@\n+only\n"

	files := parseOCDiff(diff)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}

	if files[0].Path != "/a.go" || len(files[0].Hunks) != 2 {
		t.Fatalf("first file = %q with %d hunks", files[0].Path, len(files[0].Hunks))
	}
	h := files[0].Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 {
		t.Errorf("hunk header = %+v", h)
	}
	if len(h.Lines) != 4 {
		t.Errorf("hunk lines = %v", h.Lines)
	}
	// A range without a comma covers a single line.
	if h2 := files[0].Hunks[1]; h2.OldStart != 10 || h2.OldLines != 1 {
		t.Errorf("second hunk = %+v, want start 10 count 1", h2)
	}
	if files[0].createsFile() {
		t.Error("/a.go modifies an existing file, not a creation")
	}

	if files[1].Path != "/b.go" || !files[1].createsFile() {
		t.Errorf("second file = %q, createsFile = %v", files[1].Path, files[1].createsFile())
	}

	if got := parseOCDiff(""); got != nil {
		t.Errorf("empty diff returned %+v", got)
	}
}

func TestNormalizeOpenCodeTools(t *testing.T) {
	names := map[string]string{
		"read": "Read", "write": "Write", "edit": "Edit",
		"apply_patch": "MultiEdit", "bash": "Bash", "glob": "Glob",
		"grep": "Grep", "webfetch": "WebFetch", "task": "Task",
		"todowrite": "TodoWrite",
		// Unknown tools (MCP servers, plugins) pass through untouched.
		"milo_remember": "milo_remember",
	}
	for in, want := range names {
		if got := normalizeOCToolName(in); got != want {
			t.Errorf("normalizeOCToolName(%q) = %q, want %q", in, got, want)
		}
	}

	in := map[string]interface{}{"filePath": "/a", "oldString": "x", "newString": "y", "pattern": "p"}
	out := normalizeOCToolInput(in)
	for _, key := range []string{"file_path", "old_string", "new_string", "pattern"} {
		if _, ok := out[key]; !ok {
			t.Errorf("normalized input missing %q: %v", key, out)
		}
	}
	// The original map must not be mutated.
	if _, ok := in["file_path"]; ok {
		t.Error("normalizeOCToolInput mutated its argument")
	}
}

func TestParseOCModelJSON(t *testing.T) {
	cases := map[string]string{
		`{"id":"qwen3:8b","providerID":"milo"}`:     "milo/qwen3:8b",
		`{"modelID":"gpt-5.6","providerID":"open"}`: "open/gpt-5.6",
		`{"id":"solo"}`: "solo",
		``:              "",
		`not json`:      "not json",
	}
	for in, want := range cases {
		if got := parseOCModelJSON(in); got != want {
			t.Errorf("parseOCModelJSON(%q) = %q, want %q", in, got, want)
		}
	}
}
