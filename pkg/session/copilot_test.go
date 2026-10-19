package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// copilotHome writes a Copilot home: the database, and an events.jsonl for
// each session given one.
func copilotHome(t *testing.T, sessions []map[string]any, turns []map[string]any, events map[string][]map[string]any) string {
	t.Helper()
	dir := t.TempDir()

	db := openCodexFixture(t, filepath.Join(dir, "session-store.db"), `
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			cwd TEXT,
			repository TEXT,
			host_type TEXT,
			branch TEXT,
			summary TEXT,
			created_at TEXT,
			updated_at TEXT
		);
		CREATE TABLE turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			turn_index INTEGER NOT NULL,
			user_message TEXT,
			assistant_response TEXT,
			timestamp TEXT
		);`)
	defer db.Close()

	for _, s := range sessions {
		insert(t, db, "sessions", s)
	}
	for _, tn := range turns {
		insert(t, db, "turns", tn)
	}

	for id, lines := range events {
		d := filepath.Join(dir, "session-state", id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		var body []byte
		for _, l := range lines {
			b, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			body = append(append(body, b...), '\n')
		}
		if err := os.WriteFile(filepath.Join(d, "events.jsonl"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func cpEventLine(kind, ts string, data map[string]any) map[string]any {
	return map[string]any{"type": kind, "timestamp": ts, "data": data}
}

// The database's `turns` table lags behind: a session can hold a whole
// conversation in its event stream while `turns` still says nothing happened —
// the live one on this machine had 25 events and 0 turns. The events win.
func TestCopilotPrefersTheEventStream(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo", "summary": "Check the MCP server",
			"created_at": "2026-10-19T19:03:27.078Z", "updated_at": "2026-10-19T19:04:45.507Z"}},
		// The database says one thing...
		[]map[string]any{{"session_id": "s1", "turn_index": 0,
			"user_message": "stale", "assistant_response": "stale reply"}},
		// ...the event stream says what actually happened.
		map[string][]map[string]any{"s1": {
			cpEventLine("session.start", "2026-10-19T19:03:27.132Z",
				map[string]any{"context": map[string]any{"cwd": "/repo"}}),
			cpEventLine("user.message", "2026-10-19T19:04:44.883Z",
				map[string]any{"content": "which mcp server is connected?"}),
			cpEventLine("assistant.message", "2026-10-19T19:04:45.100Z",
				map[string]any{"content": "The GitHub MCP server.", "model": "mai-code-1.1-flash"}),
		}})

	sessions, err := ParseCopilotDir(dir)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(sessions))
	}
	s := sessions[0]

	if s.Info.ID != "cp-s1" || s.Info.Source != "copilot" {
		t.Errorf("ID = %q, Source = %q", s.Info.ID, s.Info.Source)
	}
	if len(s.Events) != 2 {
		t.Fatalf("emitted %d events, want the prompt and the reply", len(s.Events))
	}
	if s.Events[0].UserText != "which mcp server is connected?" {
		t.Errorf("prompt = %q — the stale database row was used", s.Events[0].UserText)
	}
	if s.Events[1].Text != "The GitHub MCP server." {
		t.Errorf("reply = %q", s.Events[1].Text)
	}
	if s.Info.Model != "mai-code-1.1-flash" {
		t.Errorf("Model = %q", s.Info.Model)
	}
	if s.Info.Title != "Check the MCP server" {
		t.Errorf("Title = %q", s.Info.Title)
	}
}

// A run hosted by GitHub rather than by this machine never writes a state
// directory, so the database's own record of each turn is all there is.
func TestCopilotFallsBackToTheTurnsTable(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo", "host_type": "github",
			"summary": "Implement this ticket"}},
		[]map[string]any{
			{"session_id": "s1", "turn_index": 0, "user_message": "do the ticket",
				"assistant_response": "done", "timestamp": "2026-09-10T09:00:58.737Z"},
			// Half the turns on disk recorded no reply at all.
			{"session_id": "s1", "turn_index": 1, "user_message": "and again",
				"assistant_response": "", "timestamp": "2026-09-10T09:01:58.737Z"},
		}, nil)

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	s := sessions[0]
	if len(s.Events) != 3 {
		t.Fatalf("emitted %d events, want two prompts and one reply", len(s.Events))
	}
	if s.Info.UserPrompts != 2 {
		t.Errorf("UserPrompts = %d, want 2", s.Info.UserPrompts)
	}
}

// model.message and model.response look like the reply and are not: they are
// the side call that writes the session's own title, and their content is a
// <session-title> tag. Reading them puts markup where the answer belongs.
func TestCopilotIgnoresTheTitleGeneratorCall(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo"}},
		nil,
		map[string][]map[string]any{"s1": {
			cpEventLine("user.message", "2026-10-19T19:04:44Z",
				map[string]any{"content": "hello"}),
			cpEventLine("model.response", "2026-10-19T19:04:45Z",
				map[string]any{"response": map[string]any{
					"content": "<session-title>Check Loaded MCP Server</session-title>",
					"role":    "assistant"}}),
			cpEventLine("model.message", "2026-10-19T19:04:45Z",
				map[string]any{"message": map[string]any{
					"content": "<session-title>Check Loaded MCP Server</session-title>"}}),
		}})

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	for _, e := range sessions[0].Events {
		if e.Type == EventText {
			t.Errorf("the title generator became an assistant message: %q", e.Text)
		}
	}
	if n := len(sessions[0].Events); n != 1 {
		t.Errorf("emitted %d events, want only the prompt", n)
	}
}

// session.usage_record is the only place a turn's cost is recorded, and it is
// a full breakdown with a real price — the one source here that has both.
func TestCopilotAccumulatesUsage(t *testing.T) {
	usage := func(in, out, cache int, cost float64, ms int) map[string]any {
		return map[string]any{"usage": map[string]any{
			"model": "mai-code-1.1-flash", "inputTokens": in, "outputTokens": out,
			"cacheReadTokens": cache, "cost": cost, "duration": ms}}
	}
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo"}},
		nil,
		map[string][]map[string]any{"s1": {
			cpEventLine("user.message", "2026-10-19T19:04:44Z", map[string]any{"content": "hi"}),
			cpEventLine("session.usage_record", "2026-10-19T19:04:50Z", usage(15881, 536, 10, 0.25, 10726)),
			cpEventLine("session.usage_record", "2026-10-19T19:05:50Z", usage(100, 20, 0, 0.75, 2000)),
		}})

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	i := sessions[0].Info
	if i.InputTokens != 15981 || i.OutputTokens != 556 || i.CacheReadTokens != 10 {
		t.Errorf("tokens = %d/%d/%d, want 15981/556/10",
			i.InputTokens, i.OutputTokens, i.CacheReadTokens)
	}
	if i.CostUSD != 1.0 {
		t.Errorf("CostUSD = %v, want 1.0", i.CostUSD)
	}
	if got := i.ActiveDuration.Milliseconds(); got != 12726 {
		t.Errorf("ActiveDuration = %dms, want 12726", got)
	}
	// The breakdown is real, so TotalTokens must use it rather than any total.
	if got := i.TotalTokens(); got != 16547 {
		t.Errorf("TotalTokens = %d, want 16547", got)
	}
}

// A rate-limited turn raises a session.error AND a model.turn_failed, so
// counting both reported five failures for three failed prompts.
func TestCopilotCountsAFailedTurnOnce(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo"}},
		nil,
		map[string][]map[string]any{"s1": {
			cpEventLine("user.message", "2026-10-19T16:39:00Z", map[string]any{"content": "go"}),
			cpEventLine("session.error", "2026-10-19T16:39:01Z",
				map[string]any{"errorType": "rate_limit", "statusCode": 429,
					"message": "You've hit your rate limit."}),
			cpEventLine("model.turn_failed", "2026-10-19T16:39:01Z",
				map[string]any{"kind": "turn_failed"}),
		}})

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if n := sessions[0].Info.Errors; n != 1 {
		t.Errorf("Errors = %d, want 1 — the pair is one failure", n)
	}
}

// A GitHub-hosted run gets the whole issue body as its summary. A title with a
// newline in it costs the session list a row it did not count.
func TestCopilotTitleIsOneLine(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo",
			"summary": "Implement this ticket in this repository.\n\norders-api: namespace\nand more"}},
		[]map[string]any{{"session_id": "s1", "turn_index": 0, "user_message": "go"}},
		nil)

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if got := sessions[0].Info.Title; got != "Implement this ticket in this repository." {
		t.Errorf("Title = %q, want the first line only", got)
	}
}

// Copilot staples a <current_datetime> preamble onto every prompt before
// sending it. That is machinery, and showing it puts it in front of every
// thing the user actually typed.
func TestCopilotShowsWhatWasTypedNotWhatWasSent(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{{"id": "s1", "cwd": "/repo"}},
		nil,
		map[string][]map[string]any{"s1": {
			cpEventLine("user.message", "2026-10-19T19:04:44Z", map[string]any{
				"content":            "what's the current mcp server?",
				"transformedContent": "<current_datetime>2026-10-19T20:04:44.883+01:00</current_datetime>\n\nwhat's the current mcp server?",
			}),
		}})

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 || len(sessions[0].Events) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if got := sessions[0].Events[0].UserText; got != "what's the current mcp server?" {
		t.Errorf("prompt = %q, want what was typed", got)
	}
}

// A home that has never run yields nothing and no error.
func TestCopilotEmptyHomeIsNotAnError(t *testing.T) {
	sessions, err := ParseCopilotDir(t.TempDir())
	if err != nil {
		t.Errorf("an empty home errored: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("parsed %d sessions from an empty home", len(sessions))
	}
}

// A session row with neither events nor turns is a row and not a session.
func TestCopilotSkipsASessionWithNothingInIt(t *testing.T) {
	dir := copilotHome(t,
		[]map[string]any{
			{"id": "empty", "cwd": "/repo", "summary": "nothing happened"},
			{"id": "real", "cwd": "/repo", "summary": "something did"},
		},
		[]map[string]any{{"session_id": "real", "turn_index": 0, "user_message": "go"}},
		nil)

	sessions, _ := ParseCopilotDir(dir)
	if len(sessions) != 1 || sessions[0].Info.ID != "cp-real" {
		t.Fatalf("parsed %d sessions, want only cp-real", len(sessions))
	}
}
