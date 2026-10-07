package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A parent transcript and one background agent, written the way Claude Code
// writes them: the agent's turns in their own file, and the only link back a
// line in the Task tool result naming the agent.
func writeSpliceFixture(t *testing.T, base string) {
	t.Helper()
	project := filepath.Join(base, "-Users-me-work-infra")
	subagents := filepath.Join(project, "sess-1", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}

	parent := []string{
		`{"type":"user","uuid":"u1","sessionId":"sess-1","timestamp":"2026-10-01T12:00:00Z",` +
			`"message":{"role":"user","content":"fix the findings"}}`,
		// the Task call that launches the agent
		`{"type":"assistant","uuid":"a1","sessionId":"sess-1","timestamp":"2026-10-01T12:00:10Z",` +
			`"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use",` +
			`"id":"toolu_X","name":"Task","input":{"subagent_type":"scout"}}]}}`,
		// its result, which names the agent but carries no output
		`{"type":"user","uuid":"u2","sessionId":"sess-1","timestamp":"2026-10-01T12:00:11Z",` +
			`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_X",` +
			`"content":[{"type":"text","text":"Async agent launched successfully.\nagentId: abc123def4567890 (internal)"}]}]}}`,
		// work the parent does afterwards, which must stay after the subagent
		`{"type":"assistant","uuid":"a2","sessionId":"sess-1","timestamp":"2026-10-01T12:09:00Z",` +
			`"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"done"}]}}`,
	}
	agent := []string{
		`{"type":"user","uuid":"s1","sessionId":"sess-1","isSidechain":true,` +
			`"attributionAgent":"scout","timestamp":"2026-10-01T12:01:00Z",` +
			`"message":{"role":"user","content":"map the terraform"}}`,
		`{"type":"assistant","uuid":"s2","sessionId":"sess-1","isSidechain":true,` +
			`"attributionAgent":"scout","timestamp":"2026-10-01T12:02:00Z",` +
			`"message":{"role":"assistant","model":"claude-haiku-4-5","content":[{"type":"tool_use",` +
			`"id":"toolu_Y","name":"Read","input":{"file_path":"main.tf"}}]}}`,
	}

	write := func(path string, lines []string) {
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(project, "sess-1.jsonl"), parent)
	write(filepath.Join(subagents, "agent-abc123def4567890.jsonl"), agent)
}

func scanned(t *testing.T, base string) *Store {
	t.Helper()
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.baseDir = base
	if err := store.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return store
}

// The tool result is the only thing tying a background agent to the call that
// started it, so the parser has to pick the id out of it.
func TestParserRecordsSubagentLaunch(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)

	parent, err := ParseSessionFile(
		filepath.Join(base, "-Users-me-work-infra", "sess-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parent.SubagentLaunches["toolu_X"]; got != "abc123def4567890" {
		t.Errorf("SubagentLaunches[toolu_X] = %q, want the agent id", got)
	}
}

func TestSubagentTurnsAreSplicedAtTheTaskCall(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	parent := store.GetSession("sess-1")
	if parent == nil {
		t.Fatal("parent session missing")
	}

	// Find the Task call and check the agent's turns follow it immediately,
	// before the parent's own later work.
	taskAt := -1
	for i, e := range parent.Events {
		if e.Type == EventToolUse && e.ToolID == "toolu_X" {
			taskAt = i
		}
	}
	if taskAt < 0 {
		t.Fatal("the Task call is not in the parent timeline")
	}

	next := parent.Events[taskAt+1]
	if !next.IsSidechain {
		t.Fatalf("event after the Task call is not a subagent turn: %+v", next)
	}
	if next.AgentID != "abc123def4567890" {
		t.Errorf("spliced event AgentID = %q, want the agent id", next.AgentID)
	}

	var sidechain int
	for _, e := range parent.Events {
		if e.IsSidechain {
			sidechain++
		}
	}
	if sidechain != 2 {
		t.Errorf("sidechain events in the parent = %d, want 2", sidechain)
	}
	if parent.Info.EventCount != len(parent.Events) {
		t.Errorf("EventCount = %d, want %d", parent.Info.EventCount, len(parent.Events))
	}

	// The parent's own closing turn must still come last.
	last := parent.Events[len(parent.Events)-1]
	if last.IsSidechain || last.Text != "done" {
		t.Errorf("last event = %+v, want the parent's own final turn", last)
	}
}

// The subagent keeps its own row: seeing what one run cost is the other half of
// the point.
func TestSubagentIsStillItsOwnSession(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	kid := store.GetSession("agent-abc123def4567890")
	if kid == nil {
		t.Fatal("the subagent run is not listed in its own right")
	}
	if kid.Info.AgentType != "scout" || kid.Info.ParentSessionID != "sess-1" {
		t.Errorf("subagent info = %+v, want scout under sess-1", kid.Info)
	}
}

// Copying a child's cost into its parent would double-count it against the
// project total, since the child is also listed separately.
func TestSplicingDoesNotDuplicateCost(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	parent := store.GetSession("sess-1")
	kid := store.GetSession("agent-abc123def4567890")
	proj := store.GetProjectInfo("/Users/me/work/infra")
	if proj == nil {
		t.Fatal("no project info")
	}
	if want := parent.Info.CostUSD + kid.Info.CostUSD; proj.TotalCostUSD != want {
		t.Errorf("project cost = %v, want %v (each session counted once)",
			proj.TotalCostUSD, want)
	}
}

// Watching re-scans, so splicing has to be safe to run more than once.
func TestRescanDoesNotDuplicateSplicedTurns(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	first := len(store.GetSession("sess-1").Events)
	for i := 0; i < 3; i++ {
		if err := store.Scan(); err != nil {
			t.Fatalf("rescan %d: %v", i, err)
		}
	}
	if got := len(store.GetSession("sess-1").Events); got != first {
		t.Errorf("after rescans the parent has %d events, want %d", got, first)
	}
}

// Without the anchoring tool result, the turns still have to appear somewhere
// sensible rather than being dropped.
func TestSplicingFallsBackToChronology(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "-Users-me-work-infra")
	subagents := filepath.Join(project, "sess-2", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	// A parent with no Task result naming the agent.
	parentLines := fmt.Sprintf(
		`{"type":"user","uuid":"u1","sessionId":"sess-2","timestamp":"2026-10-01T12:00:00Z",`+
			`"message":{"role":"user","content":"go"}}`+"\n"+
			`{"type":"assistant","uuid":"a9","sessionId":"sess-2","timestamp":"2026-10-01T12:30:00Z",`+
			`"message":{"role":"assistant","model":%q,"content":[{"type":"text","text":"after"}]}}`+"\n",
		"claude-opus-5")
	if err := os.WriteFile(filepath.Join(project, "sess-2.jsonl"), []byte(parentLines), 0o644); err != nil {
		t.Fatal(err)
	}
	agentLine := `{"type":"user","uuid":"s1","sessionId":"sess-2","isSidechain":true,` +
		`"attributionAgent":"scout","timestamp":"2026-10-01T12:10:00Z",` +
		`"message":{"role":"user","content":"middle"}}` + "\n"
	if err := os.WriteFile(filepath.Join(subagents, "agent-deadbeefdeadbeef.jsonl"),
		[]byte(agentLine), 0o644); err != nil {
		t.Fatal(err)
	}

	store := scanned(t, base)
	parent := store.GetSession("sess-2")
	if parent == nil {
		t.Fatal("parent missing")
	}
	var at = -1
	for i, e := range parent.Events {
		if e.IsSidechain {
			at = i
		}
	}
	if at != 1 {
		t.Fatalf("subagent turn at index %d, want 1 (between the two parent turns)", at)
	}
}

// A live session reloads when its file grows. A fresh parse holds only its own
// events, so without re-linking, the subagent turns that had been spliced in
// would vanish from the parent the moment anything was appended to it.
func TestReloadKeepsSubagentTurnsSpliced(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	parentPath := filepath.Join(base, "-Users-me-work-infra", "sess-1.jsonl")
	before := store.GetSession("sess-1")
	if before == nil {
		t.Fatal("parent not in the store")
	}
	sideBefore := countSidechain(before)
	if sideBefore == 0 {
		t.Fatal("fixture should have spliced subagent turns")
	}

	// The agent keeps working: another line is appended to the parent.
	f, err := os.OpenFile(parentPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"a3","sessionId":"sess-1",` +
		`"timestamp":"2026-10-01T12:10:00Z","message":{"role":"assistant",` +
		`"model":"claude-opus-5","content":[{"type":"text","text":"and more"}]}}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	store.reload(parentPath)

	after := store.GetSession("sess-1")
	if after == nil {
		t.Fatal("parent gone after reload")
	}
	if got := countSidechain(after); got != sideBefore {
		t.Errorf("sidechain events after reload = %d, want %d — the subagent's "+
			"turns were dropped", got, sideBefore)
	}
	if len(after.Events) <= len(before.Events) {
		t.Errorf("events after reload = %d, want more than %d: the appended line "+
			"should be there too", len(after.Events), len(before.Events))
	}
}

// Reloading the same file twice must not splice the subagent in twice.
func TestReloadIsIdempotent(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)
	parentPath := filepath.Join(base, "-Users-me-work-infra", "sess-1.jsonl")

	store.reload(parentPath)
	first := len(store.GetSession("sess-1").Events)
	store.reload(parentPath)
	store.reload(parentPath)
	if got := len(store.GetSession("sess-1").Events); got != first {
		t.Errorf("events = %d after repeated reloads, want %d", got, first)
	}
}

// A subagent's own file growing has to show up in its parent's timeline too.
func TestReloadOfSubagentUpdatesParent(t *testing.T) {
	base := t.TempDir()
	writeSpliceFixture(t, base)
	store := scanned(t, base)

	agentPath := filepath.Join(base, "-Users-me-work-infra", "sess-1",
		"subagents", "agent-abc123def4567890.jsonl")
	before := countSidechain(store.GetSession("sess-1"))

	f, err := os.OpenFile(agentPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"s3","sessionId":"sess-1",` +
		`"isSidechain":true,"attributionAgent":"scout","timestamp":"2026-10-01T12:03:00Z",` +
		`"message":{"role":"assistant","model":"claude-haiku-4-5","content":[{"type":"tool_use",` +
		`"id":"toolu_Z","name":"Grep","input":{"pattern":"aws_"}}]}}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	store.reload(agentPath)

	if got := countSidechain(store.GetSession("sess-1")); got != before+1 {
		t.Errorf("sidechain events in parent = %d, want %d: the subagent's new "+
			"turn never reached its parent", got, before+1)
	}
}

func countSidechain(s *Session) int {
	n := 0
	for i := range s.Events {
		if s.Events[i].IsSidechain {
			n++
		}
	}
	return n
}
