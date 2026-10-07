package session

import (
	"os"
	"path/filepath"
	"testing"
)

// Claude Code keeps a project's own sessions flat and each subagent run one
// level down. Reading the project from the immediate parent directory is only
// correct for the flat case — a subagent file would otherwise be filed under a
// project literally called "subagents".
func TestLocate(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		wantProjectDir string
		wantParent     string
	}{
		{
			name:           "a project's own session",
			path:           "/root/.claude/projects/-Users-me-work-infra/abc-123.jsonl",
			wantProjectDir: "/Users/me/work/infra",
			wantParent:     "",
		},
		{
			name:           "a subagent run under that session",
			path:           "/root/.claude/projects/-Users-me-work-infra/abc-123/subagents/agent-deadbeef.jsonl",
			wantProjectDir: "/Users/me/work/infra",
			wantParent:     "abc-123",
		},
	}

	for _, tc := range tests {
		gotDir, gotParent := locate(tc.path)
		if gotDir != tc.wantProjectDir {
			t.Errorf("%s: projectDir = %q, want %q", tc.name, gotDir, tc.wantProjectDir)
		}
		if gotParent != tc.wantParent {
			t.Errorf("%s: parentID = %q, want %q", tc.name, gotParent, tc.wantParent)
		}
	}
}

// A subagent transcript records the parent's sessionId, so identity has to come
// from the filename or the two would collide in the store.
func TestSubagentTranscriptIsItsOwnSession(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-Users-me-work-infra")
	subagents := filepath.Join(project, "parent-session-id", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}

	// Both files claim the same sessionId, as Claude Code writes them.
	const parentLine = `{"type":"user","uuid":"u1","sessionId":"parent-session-id",` +
		`"timestamp":"2026-10-01T12:00:00Z","cwd":"/Users/me/work/infra",` +
		`"message":{"role":"user","content":"do the thing"}}`
	const agentLine = `{"type":"user","uuid":"u2","sessionId":"parent-session-id",` +
		`"isSidechain":true,"agentId":"deadbeef","attributionAgent":"scout",` +
		`"timestamp":"2026-10-01T12:01:00Z","cwd":"/Users/me/work/infra",` +
		`"message":{"role":"user","content":"map the code"}}`

	parentPath := filepath.Join(project, "parent-session-id.jsonl")
	agentPath := filepath.Join(subagents, "agent-deadbeef.jsonl")
	if err := os.WriteFile(parentPath, []byte(parentLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPath, []byte(agentLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parent, err := ParseSessionFile(parentPath)
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	agent, err := ParseSessionFile(agentPath)
	if err != nil {
		t.Fatalf("agent: %v", err)
	}

	if parent.Info.ID == agent.Info.ID {
		t.Fatalf("parent and subagent share id %q — one would overwrite the other",
			parent.Info.ID)
	}
	if parent.Info.IsAgent {
		t.Error("parent IsAgent = true, want false")
	}

	if !agent.Info.IsAgent {
		t.Error("subagent IsAgent = false, want true")
	}
	if agent.Info.ID != "agent-deadbeef" {
		t.Errorf("subagent ID = %q, want \"agent-deadbeef\"", agent.Info.ID)
	}
	if agent.Info.ParentSessionID != "parent-session-id" {
		t.Errorf("subagent ParentSessionID = %q, want \"parent-session-id\"",
			agent.Info.ParentSessionID)
	}
	if agent.Info.AgentType != "scout" {
		t.Errorf("subagent AgentType = %q, want \"scout\"", agent.Info.AgentType)
	}
	// The run belongs to the project, not to a project called "subagents".
	if agent.Info.ProjectName != "infra" {
		t.Errorf("subagent ProjectName = %q, want \"infra\"", agent.Info.ProjectName)
	}
	if agent.Info.ProjectDir != parent.Info.ProjectDir {
		t.Errorf("subagent ProjectDir = %q, want the parent's %q",
			agent.Info.ProjectDir, parent.Info.ProjectDir)
	}
}

// Scan must descend one level to find subagent runs; before this it read only
// the flat .jsonl files and missed every one of them.
func TestScanFindsSubagentRuns(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "-Users-me-work-infra")
	subagents := filepath.Join(project, "sess-1", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(project, "sess-1.jsonl"),
		`{"type":"user","uuid":"u1","sessionId":"sess-1","timestamp":"2026-10-01T12:00:00Z",`+
			`"message":{"role":"user","content":"do the thing"}}`)
	write(filepath.Join(subagents, "agent-aaa.jsonl"),
		`{"type":"user","uuid":"u2","sessionId":"sess-1","isSidechain":true,`+
			`"attributionAgent":"scout","timestamp":"2026-10-01T12:01:00Z",`+
			`"message":{"role":"user","content":"map the code"}}`)

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.baseDir = base // point at the fixture instead of ~/.claude/projects

	if err := store.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	byID := map[string]SessionInfo{}
	for _, info := range store.GetSessions() {
		byID[info.ID] = info
	}
	if _, ok := byID["sess-1"]; !ok {
		t.Error("the project's own session was not scanned")
	}
	agent, ok := byID["agent-aaa"]
	if !ok {
		t.Fatalf("the subagent run was not scanned; found %v", keys(byID))
	}
	if agent.AgentType != "scout" || agent.ParentSessionID != "sess-1" {
		t.Errorf("subagent = %+v, want AgentType scout and parent sess-1", agent)
	}

	// Both belong to the same project, so the project view covers them together.
	proj := store.GetProjectInfo("/Users/me/work/infra")
	if proj == nil {
		t.Fatal("GetProjectInfo returned nil for the fixture project")
	}
	if proj.TotalSessions != 2 {
		t.Errorf("project TotalSessions = %d, want 2", proj.TotalSessions)
	}
}

func keys(m map[string]SessionInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
