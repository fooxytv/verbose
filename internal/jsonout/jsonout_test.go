package jsonout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

func TestRunRejectsUnknownKind(t *testing.T) {
	var b strings.Builder
	err := Run(&b, nil, Query{Kind: "nonsense"})
	if err == nil || !strings.Contains(err.Error(), "unknown -json kind") {
		t.Fatalf("err = %v, want an unknown-kind error", err)
	}
}

func TestRunRequiresSessionID(t *testing.T) {
	var b strings.Builder
	err := Run(&b, nil, Query{Kind: KindSession})
	if err == nil || !strings.Contains(err.Error(), "session id") {
		t.Fatalf("err = %v, want a missing-id error", err)
	}
}

// A Claude Code transcript leaves Source empty; the wire format must still
// name it, because a consumer cannot be expected to know that empty means
// "claude".
func TestSourceIsAlwaysNamed(t *testing.T) {
	if got := sourceName(""); got != "claude" {
		t.Errorf("sourceName(\"\") = %q, want \"claude\"", got)
	}
	if got := sourceName("opencode"); got != "opencode" {
		t.Errorf("sourceName(\"opencode\") = %q, want \"opencode\"", got)
	}

	d := newSessionD(session.SessionInfo{ID: "x"})
	if d.Source != "claude" {
		t.Errorf("newSessionD Source = %q, want \"claude\"", d.Source)
	}
}

func TestCostBasis(t *testing.T) {
	tests := []struct {
		name string
		info session.SessionInfo
		want string
	}{
		{"opencode records real spend",
			session.SessionInfo{Source: "opencode", Model: "milo/qwen3:8b"}, "recorded"},
		{"known claude model is estimated",
			session.SessionInfo{Model: "claude-opus-5"}, "estimated"},
		{"unrecognised model falls back",
			session.SessionInfo{Model: "claude-unreleased-9"}, "estimated-fallback"},
	}
	for _, tc := range tests {
		if got := costBasis(tc.info); got != tc.want {
			t.Errorf("%s: costBasis = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMatchesSessionAcceptsNameDirOrCWD(t *testing.T) {
	info := session.SessionInfo{
		ProjectName: "harness",
		ProjectDir:  "/Users/me/work/harness",
		CWD:         "/Users/me/work/harness",
	}
	for _, f := range []string{"harness", "/Users/me/work/harness"} {
		if !matchesSession(f, info) {
			t.Errorf("matchesSession(%q) = false, want true", f)
		}
	}
	if matchesSession("other", info) {
		t.Error("matchesSession(\"other\") = true, want false")
	}
}

// A decoded project dir cannot distinguish a path separator from a hyphen, so
// the real cwd and the encoded dir name are both accepted as filters.
func TestMatchesProjectAcceptsEveryIdentifier(t *testing.T) {
	proj := &session.ProjectInfo{
		ProjectName: "harness",
		ProjectDir:  "/Users/me/work/harness",
		CWD:         "/Users/me/real/harness",
		EncodedDir:  "-Users-me-real-harness",
	}
	for _, f := range []string{"harness", "/Users/me/work/harness",
		"/Users/me/real/harness", "-Users-me-real-harness"} {
		if !matchesProject(f, proj) {
			t.Errorf("matchesProject(%q) = false, want true", f)
		}
	}
}

func TestEventTypeNamesAreComplete(t *testing.T) {
	// Every type the parser can emit must have a stable wire name, so that a
	// newly added EventType cannot quietly serialise as an existing one.
	for t2 := session.EventUserPrompt; t2 <= session.EventDiagnostics; t2++ {
		if name, ok := eventTypeNames[t2]; !ok || name == "" {
			t.Errorf("EventType %d has no wire name", t2)
		}
	}
	if got := eventTypeName(session.EventType(999)); got != "unknown" {
		t.Errorf("eventTypeName(999) = %q, want \"unknown\"", got)
	}
}

func TestSessionDRoundTripsAsJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	d := newSessionD(session.SessionInfo{
		ID: "abc", Title: "tighten the NSG", ProjectName: "infra",
		Model: "claude-opus-5", StartTime: now, LastUpdate: now,
		OutputTokens: 1000, ActiveDuration: 90 * time.Second,
		ToolCounts: map[string]int{"Read": 3},
	})

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "title", "source", "costBasis", "activeSeconds"} {
		if _, ok := back[key]; !ok {
			t.Errorf("missing key %q in %s", key, raw)
		}
	}
	if back["activeSeconds"] != 90.0 {
		t.Errorf("activeSeconds = %v, want 90", back["activeSeconds"])
	}
}
