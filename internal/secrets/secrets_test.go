package secrets

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

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

var (
	ghToken = "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	now     = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
)

func cmd(text string) session.Event {
	return session.Event{Type: session.EventToolUse, ToolName: "Bash",
		ToolInput: map[string]interface{}{"command": text}}
}

func testStore() *fakeStore {
	return &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "aaa", CWD: "/w/dotfiles",
			LastUpdate: now.Add(-72 * time.Hour), Title: "capture.sh help"},
			Events: []session.Event{cmd("git push https://" + ghToken + "@github.com/x/y")}},
		{Info: session.SessionInfo{ID: "bbb", CWD: "/w/other",
			LastUpdate: now.Add(-24 * time.Hour), Title: "unrelated"},
			Events: []session.Event{cmd(`password = "hunter2hunter2"`)}},
	}}
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut, testStore(), now)
	return out.String(), errOut.String(), code
}

func TestSecretsReportsWhatToRevokeAndWhere(t *testing.T) {
	out, _, code := run(t)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"REVOKE THESE", "github token", "dotfiles", "aaa",
		"3 days ago", "github.com/settings/tokens", "capture.sh help",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "A1b2C3d4") {
		t.Error("the report printed the token")
	}
}

// A judgement is not an alarm: possible findings are hidden until asked for,
// or the one row that matters is buried.
func TestSecretsHidesTheJudgementCallsByDefault(t *testing.T) {
	out, _, _ := run(t)
	if strings.Contains(out, "WORTH A LOOK") {
		t.Errorf("a possible finding showed without -all:\n%s", out)
	}
	if !strings.Contains(out, "pass -all") {
		t.Errorf("nothing says the rest is hidden:\n%s", out)
	}

	out, _, _ = run(t, "-all")
	if !strings.Contains(out, "WORTH A LOOK") {
		t.Errorf("-all did not show the possible finding:\n%s", out)
	}
}

func TestSecretsNarrowsToAProject(t *testing.T) {
	out, _, _ := run(t, "-project", "other", "-all")
	if strings.Contains(out, "github token") {
		t.Errorf("the project filter did not hold:\n%s", out)
	}
	if !strings.Contains(out, "across 1 session") {
		t.Errorf("want one session scanned:\n%s", out)
	}
}

// Nothing to revoke is the best answer the command has, not an error.
func TestSecretsWithNothingToRevokeSaysSo(t *testing.T) {
	var out, errOut bytes.Buffer
	empty := &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "ccc", CWD: "/w/clean", LastUpdate: now},
			Events: []session.Event{cmd("go build ./...")}},
	}}
	code := Run(nil, &out, &errOut, empty, now)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Nothing to revoke across 1 session") {
		t.Errorf("output = %q", out.String())
	}
}

func TestSecretsJSONIsMachineReadable(t *testing.T) {
	out, _, _ := run(t, "-json", "-all")
	var findings []session.SecretFinding
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(findings) != 2 {
		t.Fatalf("%d findings, want 2", len(findings))
	}
	if findings[0].Kind != "github token" || findings[0].Status != session.SecretRotate {
		t.Errorf("worst first: %+v", findings[0])
	}
	if strings.Contains(out, "A1b2C3d4") {
		t.Error("the JSON carries the token")
	}

	// Empty is an array, not null.
	var buf, errBuf bytes.Buffer
	Run([]string{"-json"}, &buf, &errBuf, &fakeStore{}, now)
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty JSON = %q, want []", strings.TrimSpace(buf.String()))
	}
}

func TestSecretsRejectsABadFlag(t *testing.T) {
	_, errOut, code := run(t, "-nope")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "usage: verbose secrets") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestAgoReadsAsAReaderWouldSayIt(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{-2 * time.Hour, "today"},
		{-26 * time.Hour, "yesterday"},
		{-5 * 24 * time.Hour, "5 days ago"},
		{-90 * 24 * time.Hour, "2026-07-22"},
	} {
		if got := ago(now.Add(c.d), now); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := ago(time.Time{}, now); got != "unknown" {
		t.Errorf("ago(zero) = %q", got)
	}
}
