package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/pkg/session"
)

// runFixture is a parent session with two background agents, written the way
// Claude Code writes them: each agent in its own file under the parent's
// subagents directory, linked only by the agentId in the otherwise-empty tool
// result.
func runFixture(t *testing.T) Model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	project := filepath.Join(home, ".claude", "projects", "-repo")
	subagents := filepath.Join(project, "parent-1", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(path string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	agentResult := func(id, hex string) string {
		return `{"type":"user","uuid":"u-` + id + `","sessionId":"parent-1","cwd":"/repo",` +
			`"timestamp":"2026-10-01T12:00:1` + id + `Z","message":{"role":"user","content":[` +
			`{"type":"tool_result","tool_use_id":"toolu_` + id + `","content":[{"type":"text",` +
			`"text":"Async agent launched successfully.\nagentId: ` + hex + ` (internal)"}]}]}}`
	}
	agentCall := func(id string) string {
		return `{"type":"assistant","uuid":"a-` + id + `","sessionId":"parent-1","cwd":"/repo",` +
			`"timestamp":"2026-10-01T12:00:0` + id + `Z","message":{"role":"assistant",` +
			`"model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu_` + id + `",` +
			`"name":"Task","input":{"subagent_type":"scout"}}]}}`
	}

	write(filepath.Join(project, "parent-1.jsonl"),
		`{"type":"user","uuid":"u1","sessionId":"parent-1","cwd":"/repo",`+
			`"timestamp":"2026-10-01T12:00:00Z","message":{"role":"user","content":"do the work"}}`,
		agentCall("1"), agentResult("1", "aaaa111122223333"),
		agentCall("2"), agentResult("2", "bbbb444455556666"),
		`{"type":"assistant","uuid":"a9","sessionId":"parent-1","cwd":"/repo",`+
			`"timestamp":"2026-10-01T12:30:00Z","message":{"role":"assistant",`+
			`"model":"claude-opus-5","content":[{"type":"text","text":"finished"}]}}`,
	)

	agentTurns := func(tag, minute string) []string {
		return []string{
			`{"type":"user","uuid":"s1-` + tag + `","sessionId":"parent-1","isSidechain":true,` +
				`"cwd":"/repo","timestamp":"2026-10-01T12:` + minute + `:00Z",` +
				`"message":{"role":"user","content":"brief for ` + tag + `"}}`,
			`{"type":"assistant","uuid":"s2-` + tag + `","sessionId":"parent-1","isSidechain":true,` +
				`"cwd":"/repo","timestamp":"2026-10-01T12:` + minute + `:30Z",` +
				`"message":{"role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text",` +
				`"text":"` + tag + ` reporting"}]}}`,
		}
	}
	write(filepath.Join(subagents, "agent-aaaa111122223333.jsonl"), agentTurns("first", "05")...)
	write(filepath.Join(subagents, "agent-bbbb444455556666.jsonl"), agentTurns("second", "10")...)

	store, err := session.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Scan(); err != nil {
		t.Fatal(err)
	}

	m := Model{width: 140, height: 40, version: "test"}
	m.store = store
	sess := store.GetSession("parent-1")
	if sess == nil {
		t.Fatal("parent session not in the store")
	}
	m.startReplay(sess)
	return m
}

// a cycles parent -> agent -> agent -> parent. Without it the only way to see
// an agent's own run is to leave the replay, find it in the list and come back.
func TestSwitchRunCyclesThroughTheAgentsAndBack(t *testing.T) {
	m := runFixture(t)
	start := m.selectedSession.Info.ID
	if start != "parent-1" {
		t.Fatalf("fixture opened %q, want parent-1", start)
	}

	seen := []string{start}
	for i := 0; i < 3; i++ {
		m.switchRun(1)
		seen = append(seen, m.selectedSession.Info.ID)
	}

	if seen[3] != "parent-1" {
		t.Errorf("cycle = %v, want it to return to parent-1", seen)
	}
	if seen[1] == seen[2] {
		t.Errorf("cycle = %v, want two distinct agents", seen)
	}
	for _, id := range seen[1:3] {
		if !strings.HasPrefix(id, "agent-") {
			t.Errorf("cycle = %v, want agent sessions in the middle", seen)
		}
	}
}

// A is the other way round the ring, which matters once a session has more
// than a couple of agents.
func TestSwitchRunGoesBackwards(t *testing.T) {
	m := runFixture(t)
	m.switchRun(-1)
	last := m.selectedSession.Info.ID
	m.switchRun(1)
	if m.selectedSession.Info.ID != "parent-1" {
		t.Errorf("back then forward landed on %q, want parent-1", m.selectedSession.Info.ID)
	}
	if !strings.HasPrefix(last, "agent-") {
		t.Errorf("stepping back from the parent gave %q, want an agent", last)
	}
}

// Leaving a session and returning must land where you were. Starting again at
// step 1 each time is what makes switching not worth doing.
func TestSwitchRunRemembersWhereYouWere(t *testing.T) {
	m := runFixture(t)
	if len(m.replaySteps) < 3 {
		t.Fatalf("parent has %d steps, need at least 3", len(m.replaySteps))
	}
	m.replayIndex = 2

	m.switchRun(1) // into an agent
	if m.replayIndex != 0 {
		t.Errorf("a freshly opened agent started at step %d, want 0", m.replayIndex)
	}
	m.switchRun(-1) // back to the parent

	if m.selectedSession.Info.ID != "parent-1" {
		t.Fatalf("landed on %q, want parent-1", m.selectedSession.Info.ID)
	}
	if m.replayIndex != 2 {
		t.Errorf("returned to step %d, want the step 2 we left", m.replayIndex)
	}
}

// The header has to say whose work is on screen, because the status message
// that says it on the way in clears after a few seconds.
func TestRunLabelNamesThePositionInTheGroup(t *testing.T) {
	m := runFixture(t)
	if got := m.runLabel(); !strings.Contains(got, "2 agents") {
		t.Errorf("parent label = %q, want it to count the agents", got)
	}
	m.switchRun(1)
	if got := m.runLabel(); !strings.Contains(got, "of 2") {
		t.Errorf("agent label = %q, want it to say which of the two", got)
	}
}

// A session with no agents must say so rather than appear to ignore the key.
func TestSwitchRunSaysSoWithNoAgents(t *testing.T) {
	// deleteFixture builds a store holding one session with no agents.
	// treeModel has no store at all, and switchRun returns before it can say
	// anything in that case — which is a programming error, not a reader's.
	m, _ := deleteFixture(t)
	sess := m.store.GetSession(m.sessions[0].ID)
	if sess == nil {
		t.Fatal("fixture session missing from the store")
	}
	m.startReplay(sess)

	res, _ := m.handleReplayKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, "a")
	after := res.(Model)
	if !strings.Contains(after.statusMsg, "No subagents") {
		t.Errorf("statusMsg = %q, want it to explain there are none", after.statusMsg)
	}
}
