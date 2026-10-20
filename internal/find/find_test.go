package find

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// fakeStore is two sessions in two projects, one of which ran the command
// being looked for and printed something worth finding.
type fakeStore struct{ sessions []*session.Session }

func (f *fakeStore) GetSessions() []session.SessionInfo {
	var out []session.SessionInfo
	for _, s := range f.sessions {
		out = append(out, s.Info)
	}
	return out
}

func (f *fakeStore) GetSession(id string) *session.Session {
	for _, s := range f.sessions {
		if s.Info.ID == id {
			return s
		}
	}
	return nil
}

func bash(cmd, out string) session.Event {
	return session.Event{Type: session.EventToolUse, ToolName: "Bash",
		ToolInput: map[string]interface{}{"command": cmd}, ToolOutput: out}
}

func testStore() *fakeStore {
	day := time.Date(2026, 10, 20, 13, 40, 0, 0, time.UTC)
	return &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "aaa", CWD: "/w/lifecycle", LastUpdate: day,
			Title: "the storage work"},
			Events: []session.Event{
				bash("ls -la", "total 0\n"),
				// A MULTI-LINE command, which is the case worth having in a
				// fixture: one step in five runs one, and the headline can
				// only show the first line — so this hit can be found at all
				// only because the command itself is searched.
				bash("NET=$(docker network ls --format '{{.Name}}' | head -1)\n"+
					"docker run --rm azure-cli az storage entity query --table-name requests",
					"Pk  Rk  Outcome\n"),
				bash("./deploy.sh", "Error: connection refused on port 5432\n"),
				bash("docker compose down", "ok\n"),
			}},
		{Info: session.SessionInfo{ID: "bbb", CWD: "/w/other", Source: "opencode",
			LastUpdate: day.Add(-48 * time.Hour), Title: "something else"},
			Events: []session.Event{bash("go build ./...", "ok\n")}},
	}}
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut, testStore())
	return out.String(), errOut.String(), code
}

func TestFindReportsTheStepAndWhyItMatched(t *testing.T) {
	out, _, code := run(t, "docker run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"1 hit for \"docker run\"",
		"aaa",             // the session to go back to
		"lifecycle",       // the project, not the session id alone
		"step 2/4",        // where in the replay
		"command:",        // which part of the step matched
		"docker run --rm", // the line itself
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// The outcome lives in the output and nowhere else, which is the reason this
// searches more than the headline.
func TestFindReachesWhatAStepPrinted(t *testing.T) {
	out, _, _ := run(t, "connection refused")
	if !strings.Contains(out, "step 3/4") {
		t.Errorf("did not reach the failing step:\n%s", out)
	}
	if !strings.Contains(out, "output:") {
		t.Errorf("did not say the match was in the output:\n%s", out)
	}
}

func TestFindNarrowsToAProject(t *testing.T) {
	out, _, _ := run(t, "-project", "other", "go build")
	if !strings.Contains(out, "bbb") {
		t.Errorf("missed the session in that project:\n%s", out)
	}

	out, _, _ = run(t, "-project", "lifecycle", "go build")
	if !strings.Contains(out, "Nothing in 1 session matches") {
		t.Errorf("the project filter did not hold:\n%s", out)
	}
}

func TestFindSaysWhenItStopsEarly(t *testing.T) {
	out, _, _ := run(t, "-limit", "1", "docker")
	if !strings.Contains(out, "stopped at 1") {
		t.Errorf("a truncated answer must say so:\n%s", out)
	}

	// And it really stops: saying "stopped at 1" while printing two is the
	// failure this message exists to prevent.
	if n := strings.Count(out, "step "); n != 1 {
		t.Errorf("printed %d steps under -limit 1:\n%s", n, out)
	}
	var hits []Hit
	jsonOut, _, _ := run(t, "-json", "-limit", "1", "docker")
	if err := json.Unmarshal([]byte(jsonOut), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("-limit 1 returned %d hits", len(hits))
	}
}

// Nothing matching is an answer, not a failure — and it says how much was
// looked through, because "no results" over an empty store and over a whole
// history are different answers.
func TestFindWithNoMatchIsNotAnError(t *testing.T) {
	out, _, code := run(t, "nothing-like-this")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "Nothing in 2 sessions matches") {
		t.Errorf("output = %q", out)
	}
}

func TestFindWithoutAQueryIsAUsageError(t *testing.T) {
	out, errOut, code := run(t)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "usage: verbose find") {
		t.Errorf("stderr = %q", errOut)
	}
	if out != "" {
		t.Errorf("usage should not print hits: %q", out)
	}
}

func TestFindJSONIsMachineReadable(t *testing.T) {
	out, _, code := run(t, "-json", "docker run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var hits []Hit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(hits) != 1 {
		t.Fatalf("%d hits, want 1", len(hits))
	}
	h := hits[0]
	if h.SessionID != "aaa" || h.Step != 2 || h.Steps != 4 {
		t.Errorf("hit = %+v", h)
	}
	if h.Kind != "command" || !strings.Contains(h.Line, "docker run") {
		t.Errorf("hit does not say why it matched: %+v", h)
	}
	if h.Source != "claude" {
		t.Errorf("source = %q — an empty source is Claude Code, not a hole", h.Source)
	}

	// An empty result is an empty array, not null: a consumer should be able
	// to range over it without checking.
	out, _, _ = run(t, "-json", "nothing-like-this")
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty JSON = %q, want []", strings.TrimSpace(out))
	}
}

// Several hits in one session are one place to go back to, so they are
// printed under one heading rather than repeating it.
func TestFindGroupsHitsBySession(t *testing.T) {
	out, _, _ := run(t, "-limit", "0", "docker")
	if n := strings.Count(out, "aaa"); n != 1 {
		t.Errorf("the session heading appears %d times, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "step "); n < 2 {
		t.Errorf("want both hits listed:\n%s", out)
	}
}
