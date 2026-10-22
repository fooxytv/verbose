package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strconv"
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

// run executes the command against an isolated HOME.
//
// The isolation is not optional. Without it these tests read the ignore list
// on the machine running them, and a fingerprint acknowledged there silently
// hides the finding a test is asserting on — which is how this helper got
// written: the fixture's token is a shape common enough to have been
// acknowledged for real.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	isolate(t)
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut, testStore(), now)
	return out.String(), errOut.String(), code
}

// isolate points HOME at a temp directory, so nothing here reads or writes
// the reader's own ignore list.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows resolves home from %USERPROFILE%, and os.UserHomeDir is what
	// every production path uses.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
}

func TestSecretsReportsWhatToRevokeAndWhere(t *testing.T) {
	out, _, code := run(t)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"KIND", "github token", "dotfiles", "3 days ago",
		"revoke and re-issue",        // the table's short form
		"MITIGATIONS",                // and the long form below it
		"github.com/settings/tokens", // where to revoke
		"credential helper",          // how to stop it happening again
		"HOW THEY GOT THERE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The table summarises; the sessions are a flag away, not in the way.
	if strings.Contains(out, "capture.sh help") {
		t.Errorf("session titles should need -details:\n%s", out)
	}

	out, _, _ = run(t, "-details")
	for _, want := range []string{"WHERE THEY APPEAR", "aaa", "capture.sh help"} {
		if !strings.Contains(out, want) {
			t.Errorf("-details output missing %q:\n%s", want, out)
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
	if strings.Contains(out, "check whether it is real") {
		t.Errorf("a possible finding showed without -all:\n%s", out)
	}
	if !strings.Contains(out, "possible hidden (-all)") {
		t.Errorf("nothing says the rest is hidden:\n%s", out)
	}
	if !strings.Contains(out, "1 to deal with") {
		t.Errorf("the count should be what is shown:\n%s", out)
	}

	out, _, _ = run(t, "-all")
	if !strings.Contains(out, "check whether it is real") {
		t.Errorf("-all did not show the possible finding:\n%s", out)
	}
	// With everything shown there is a status column to tell them apart.
	if !strings.Contains(out, "STATUS") || !strings.Contains(out, "possible") {
		t.Errorf("-all should distinguish the statuses:\n%s", out)
	}
}

func TestSecretsNarrowsToAProject(t *testing.T) {
	out, _, _ := run(t, "-project", "other", "-all")
	if strings.Contains(out, "github token") {
		t.Errorf("the project filter did not hold:\n%s", out)
	}
	if !strings.Contains(out, "1 session scanned") {
		t.Errorf("want one session scanned:\n%s", out)
	}
}

// Nothing to revoke is the best answer the command has, not an error.
func TestSecretsWithNothingToRevokeSaysSo(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	empty := &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "ccc", CWD: "/w/clean", LastUpdate: now},
			Events: []session.Event{cmd("go build ./...")}},
	}}
	code := Run(nil, &out, &errOut, empty, now)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Nothing to deal with") {
		t.Errorf("output = %q", out.String())
	}
	if !strings.Contains(out.String(), "1 session scanned") {
		t.Errorf("a clean report still says how much was looked at: %q", out.String())
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
	isolate(t)
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

// The report has to SHRINK as it is dealt with, or it is one nobody reads
// twice. Acknowledging is by fingerprint, so nothing secret is written down.
func TestIgnoringAFindingStopsItBeingReported(t *testing.T) {
	isolate(t)

	// Find the github token's fingerprint the way a reader would: off the
	// report itself.
	var out, errOut bytes.Buffer
	Run([]string{"-json"}, &out, &errOut, testStore(), now)
	var findings []session.SecretFinding
	if err := json.Unmarshal(out.Bytes(), &findings); err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("%d findings to start with, want 1", len(findings))
	}
	fp := findings[0].Fingerprint

	out.Reset()
	errOut.Reset()
	if code := Run([]string{"-ignore", fp, "-note", "a test fixture"},
		&out, &errOut, testStore(), now); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ignoring "+fp) {
		t.Errorf("output = %q", out.String())
	}

	out.Reset()
	Run(nil, &out, &errOut, testStore(), now)
	if strings.Contains(out.String(), fp) {
		t.Errorf("the finding is still reported:\n%s", out.String())
	}
	// Hidden, but SAID to be hidden.
	if !strings.Contains(out.String(), "1 acknowledged") {
		t.Errorf("nothing says a finding was hidden:\n%s", out.String())
	}

	// And -ignored lists it with the note.
	out.Reset()
	Run([]string{"-ignored"}, &out, &errOut, testStore(), now)
	for _, want := range []string{fp, "a test fixture", "2026-10-20"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("-ignored output missing %q:\n%s", want, out.String())
		}
	}

	// Un-ignoring brings it back.
	out.Reset()
	if code := Run([]string{"-unignore", fp}, &out, &errOut, testStore(), now); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out.Reset()
	Run(nil, &out, &errOut, testStore(), now)
	if !strings.Contains(out.String(), fp) {
		t.Errorf("un-ignoring did not bring it back:\n%s", out.String())
	}
}

func TestUnignoringSomethingThatWasNotIgnoredIsAnError(t *testing.T) {
	_, errOut, code := run(t, "-unignore", "deadbeef")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "was not being ignored") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestIgnoredWithAnEmptyListSaysSo(t *testing.T) {
	out, _, code := run(t, "-ignored")
	if code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(out, "Nothing is being ignored") {
		t.Errorf("output = %q", out)
	}
}

// The table is a comparison, so its columns have to line up whatever is in
// them — a ragged table is a list with extra steps.
func TestTheTableColumnsLineUp(t *testing.T) {
	out, _, _ := run(t, "-all")

	var header string
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "  KIND"):
			header = line
		case header != "" && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "":
			if strings.HasPrefix(strings.TrimSpace(line), "MITIGATIONS") {
				header = "" // past the table
				continue
			}
			rows = append(rows, line)
		}
	}
	if header == "" || len(rows) < 2 {
		t.Fatalf("no table found in:\n%s", out)
	}

	// Every row must start its ID column at the same character as the header.
	want := strings.Index(header, "ID")
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) < 2 {
			continue
		}
		// The fingerprint is eight hex characters; find where it starts.
		for _, f := range fields {
			if len(f) == 8 && strings.Trim(f, "0123456789abcdef") == "" {
				if got := strings.Index(row, f); got != want {
					t.Errorf("row starts its id at %d, header at %d:\n%s\n%s",
						got, want, header, row)
				}
				break
			}
		}
	}
}

// The advice is per KIND, so four GitHub tokens get it once. Repeating it
// four times buries it.
func TestMitigationsAreNotRepeatedPerFinding(t *testing.T) {
	store := &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "aaa", CWD: "/w/one", LastUpdate: now},
			Events: []session.Event{
				cmd("git push https://" + ghToken + "@github.com/x/y"),
				cmd("curl -H 'token ghp_Z9y8X7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2'"),
			}},
	}}
	var out, errOut bytes.Buffer
	isolate(t)
	Run(nil, &out, &errOut, store, now)

	if n := strings.Count(out.String(), "github.com/settings/tokens"); n != 1 {
		t.Errorf("the revoke address appears %d times, want 1:\n%s", n, out.String())
	}
	// But both findings are still listed.
	if n := strings.Count(out.String(), "github token"); n < 2 {
		t.Errorf("only %d rows for two tokens:\n%s", n, out.String())
	}
}

// Nothing to revoke still prints the mitigations' premise? No — a clean
// report should be short. Advice nobody needs is noise, which is the one
// thing this output must not become.
func TestACleanReportStaysShort(t *testing.T) {
	clean := &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "ccc", CWD: "/w/clean", LastUpdate: now},
			Events: []session.Event{cmd("go build ./...")}},
	}}
	var out, errOut bytes.Buffer
	isolate(t)
	Run(nil, &out, &errOut, clean, now)

	if strings.Contains(out.String(), "MITIGATIONS") {
		t.Errorf("advice with nothing to apply it to:\n%s", out.String())
	}
	if n := len(strings.Split(strings.TrimSpace(out.String()), "\n")); n > 4 {
		t.Errorf("a clean report is %d lines:\n%s", n, out.String())
	}
}

func TestWrappedAdviceFitsEightyColumns(t *testing.T) {
	out, _, _ := run(t, "-all")
	for i, line := range strings.Split(out, "\n") {
		// The table sizes itself to its contents and may be wider; the prose
		// is what has to wrap.
		if strings.HasPrefix(line, "      revoke ") || strings.HasPrefix(line, "      prevent ") ||
			strings.HasPrefix(line, "               ") {
			if len([]rune(line)) > 80 {
				t.Errorf("line %d is %d columns:\n%s", i, len([]rune(line)), line)
			}
		}
	}
}

// A report of things that need nothing done says so in the table, and offers
// no advice — there is nothing to advise.
func TestFindingsThatNeedNothingSayNothingToDo(t *testing.T) {
	dead := jwtLike(now.Add(-72 * time.Hour))
	store := &fakeStore{sessions: []*session.Session{
		{Info: session.SessionInfo{ID: "aaa", CWD: "/w/one", LastUpdate: now},
			Events: []session.Event{cmd("curl -H 'Authorization: Bearer " + dead + "'")}},
	}}
	var out, errOut bytes.Buffer
	isolate(t)
	Run([]string{"-all"}, &out, &errOut, store, now)

	got := out.String()
	if !strings.Contains(got, "nothing — already dead") {
		t.Errorf("the table should say there is nothing to do:\n%s", got)
	}
	if strings.Contains(got, "revoke and re-issue") {
		t.Errorf("a dead token is not something to re-issue:\n%s", got)
	}
	if strings.Contains(got, "MITIGATIONS") {
		t.Errorf("advice with nothing to apply it to:\n%s", got)
	}
	if strings.Contains(got, "HOW THEY GOT THERE") {
		t.Errorf("the premise is only worth stating when something needs doing:\n%s", got)
	}
}

// jwtLike builds a signed-looking token that expires when told. The payload
// is base64url of plain JSON, which is how verbose reads the expiry.
func jwtLike(exp time.Time) string {
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(
		`{"exp":` + strconv.FormatInt(exp.Unix(), 10) + `,"iss":"https://sts.example/x/"}`))
	return head + "." + body + ".c2lnbmF0dXJl"
}
