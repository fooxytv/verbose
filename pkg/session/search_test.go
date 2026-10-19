package session

import (
	"strings"
	"testing"
	"time"
)

func searchFixture() *Session {
	return &Session{
		Info: SessionInfo{ID: "s1", Title: "Cap the retry backoff", CWD: "/repo"},
		Events: []Event{
			{Type: EventUserPrompt, UserText: "Deliveries keep retrying for hours.\nCap the backoff."},
			{Type: EventText, Text: "The backoff doubles without a ceiling."},
			{Type: EventToolUse, ToolName: "Read",
				ToolInput: map[string]interface{}{"file_path": "/repo/delivery.go"}},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "go test ./internal/webhooks"}},
		},
	}
}

// A search has to reach what was IN a session, not just its label: the label
// is the one thing the list already shows.
func TestMatchSessionReachesPromptsFilesAndCommands(t *testing.T) {
	cases := []struct {
		needle string
		kind   string
		index  int
	}{
		{"backoff", "title", 0}, // the title matched, and so did event 0
		{"ceiling", "reply", 1}, // only the agent's own prose
		{"delivery.go", "file", 2},
		{"go test", "command", 3},
	}
	for _, c := range cases {
		m, ok := MatchSession(searchFixture(), c.needle)
		if !ok {
			t.Errorf("%q did not match at all", c.needle)
			continue
		}
		if m.Kind != c.kind {
			t.Errorf("%q matched as %q, want %q", c.needle, m.Kind, c.kind)
		}
		if m.EventIndex != c.index {
			t.Errorf("%q first matched at event %d, want %d", c.needle, m.EventIndex, c.index)
		}
		if strings.TrimSpace(m.Snippet) == "" {
			t.Errorf("%q matched with no snippet to show for it", c.needle)
		}
	}
}

// The snippet explains WHY a session matched, so it has to be the line the
// needle was on rather than the start of a thousand-character prompt.
func TestMatchSnippetIsTheLineTheNeedleWasOn(t *testing.T) {
	sess := &Session{
		Info: SessionInfo{ID: "s"},
		Events: []Event{{Type: EventUserPrompt,
			UserText: "first line\nsecond line mentions BACKOFF here\nthird line"}},
	}
	m, ok := MatchSession(sess, "backoff")
	if !ok {
		t.Fatal("no match")
	}
	if m.Snippet != "second line mentions BACKOFF here" {
		t.Errorf("snippet = %q", m.Snippet)
	}
}

// A minified file or a pasted log is one enormous line. The snippet is bounded
// and keeps the match in view rather than the first 160 characters of a line
// the needle is nowhere near.
func TestMatchSnippetIsBoundedAroundTheMatch(t *testing.T) {
	long := strings.Repeat("x", 400) + " NEEDLE " + strings.Repeat("y", 400)
	sess := &Session{Info: SessionInfo{ID: "s"},
		Events: []Event{{Type: EventUserPrompt, UserText: long}}}

	m, ok := MatchSession(sess, "needle")
	if !ok {
		t.Fatal("no match")
	}
	if n := len([]rune(m.Snippet)); n > 170 {
		t.Errorf("snippet is %d runes, want it bounded", n)
	}
	if !strings.Contains(m.Snippet, "NEEDLE") {
		t.Errorf("the match itself is not in the snippet: %q", m.Snippet)
	}
}

// Every hit is counted, because "17 hits" and "1 hit" are different answers to
// "did I work on this here".
func TestMatchCountsEveryHit(t *testing.T) {
	m, ok := MatchSession(searchFixture(), "backoff")
	if !ok {
		t.Fatal("no match")
	}
	// Title, the prompt's second line, and the reply.
	if m.Hits != 3 {
		t.Errorf("Hits = %d, want 3", m.Hits)
	}
}

// Case is not something anyone remembers about a prompt they wrote in March.
func TestMatchIgnoresCase(t *testing.T) {
	if _, ok := MatchSession(searchFixture(), "cap the retry"); !ok {
		t.Error("a lower-case needle did not match a capitalised title")
	}
}

// An empty query matches nothing rather than everything: the list it would
// filter is already showing everything.
func TestMatchEmptyNeedleMatchesNothing(t *testing.T) {
	if _, ok := MatchSession(searchFixture(), ""); ok {
		t.Error("an empty needle matched")
	}
}

// The store searches every session it holds, newest first, which is the order
// the list is already in: a search narrows it rather than reshuffling it.
func TestStoreSearchIsNewestFirst(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.baseDir = t.TempDir()

	now := time.Now()
	for i, id := range []string{"old", "new", "middle"} {
		when := now.Add(-time.Duration(i) * time.Hour)
		if id == "new" {
			when = now
		}
		if id == "old" {
			when = now.Add(-10 * time.Hour)
		}
		if id == "middle" {
			when = now.Add(-5 * time.Hour)
		}
		store.sessions[id] = &Session{
			Info:   SessionInfo{ID: id, Title: "about backoff", LastUpdate: when},
			Events: []Event{{Type: EventUserPrompt, UserText: "the backoff again"}},
		}
	}

	got := store.Search("backoff")
	if len(got) != 3 {
		t.Fatalf("matched %d sessions, want 3", len(got))
	}
	want := []string{"new", "middle", "old"}
	for i, id := range want {
		if got[i].SessionID != id {
			t.Errorf("position %d is %q, want %q", i, got[i].SessionID, id)
		}
	}

	if n := len(store.Search("   ")); n != 0 {
		t.Errorf("a blank query matched %d sessions", n)
	}

	// The store lower-cases the needle once, for every session. Nobody
	// remembers the case of a prompt they wrote in March.
	if n := len(store.Search("BACKOFF")); n != 3 {
		t.Errorf("an upper-case query matched %d sessions, want 3", n)
	}
}
