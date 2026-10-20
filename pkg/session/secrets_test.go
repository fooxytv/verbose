package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The values here are the right SHAPE and invented. The Slack-like one is
// assembled rather than written out because GitHub's push protection rejects
// a literal of that shape — which is the system working, and the reason this
// command exists at all.
var (
	ghToken   = "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	slackLike = "xox" + "b-" + "9876543210-" + "ponmlkjihgfedcba"
)

// jwtWith builds a JWT whose payload says when it dies. No signing: the
// payload is base64url of plain JSON by design, which is exactly why the
// report can say "expired" without asking anyone.
func jwtWith(exp time.Time, iss string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"exp": exp.Unix(), "iss": iss, "sub": "someone",
	})
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return head + "." + body + ".c2lnbmF0dXJlLWdvZXMtaGVyZQ"
}

func secretSession(id, cwd string, when time.Time, texts ...string) *Session {
	sess := &Session{Info: SessionInfo{ID: id, CWD: cwd, LastUpdate: when,
		Title: "a session"}}
	for _, t := range texts {
		sess.Events = append(sess.Events, Event{Type: EventToolUse, ToolName: "Bash",
			ToolInput: map[string]interface{}{"command": t}})
	}
	return sess
}

func scan(sessions ...*Session) []SecretFinding {
	return ScanSecrets(sessions, nil)
}

func findingFor(t *testing.T, findings []SecretFinding, kind string) SecretFinding {
	t.Helper()
	for _, f := range findings {
		if f.Kind == kind {
			return f
		}
	}
	t.Fatalf("no %q finding in %d findings", kind, len(findings))
	return SecretFinding{}
}

// The report's job: one row per credential, however many times it appears,
// with where it is and what to do.
func TestScanSecretsGroupsOneCredentialAcrossSessions(t *testing.T) {
	day := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	findings := scan(
		secretSession("aaa", "/w/dotfiles", day,
			"git push https://"+ghToken+"@github.com/x/y",
			"echo "+ghToken),
		secretSession("bbb", "/w/dotfiles", day.Add(time.Hour), "curl -H 'token "+ghToken+"'"),
	)

	f := findingFor(t, findings, "github token")
	if f.Count != 3 {
		t.Errorf("Count = %d, want 3 — every occurrence", f.Count)
	}
	if len(f.Seen) != 2 {
		t.Fatalf("seen in %d sessions, want 2", len(f.Seen))
	}
	if f.Seen[0].SessionID != "bbb" {
		t.Errorf("sightings should be newest first, got %q", f.Seen[0].SessionID)
	}
	if f.Seen[0].Project != "dotfiles" {
		t.Errorf("project = %q, want the cwd's last segment", f.Seen[0].Project)
	}
	if f.Status != SecretRotate {
		t.Errorf("status = %q, want rotate", f.Status)
	}
	if !strings.Contains(f.Rotate, "github.com/settings/tokens") {
		t.Errorf("no revoke address: %q", f.Rotate)
	}
}

// Never the value. This output is going to be pasted into a ticket.
func TestScanSecretsNeverCarriesTheValue(t *testing.T) {
	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		"export TOKEN="+ghToken+" AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"slack="+slackLike))

	blob, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{ghToken, slackLike, "AKIAIOSFODNN7EXAMPLE",
		"A1b2C3d4", "ponmlkjihgfedcba"} {
		if strings.Contains(string(blob), secret) {
			t.Errorf("the report carries %q", secret)
		}
	}
	if len(findings) < 3 {
		t.Errorf("found %d credentials, want at least 3", len(findings))
	}
	for _, f := range findings {
		if len(f.Fingerprint) != 8 {
			t.Errorf("fingerprint %q is not 8 characters", f.Fingerprint)
		}
	}
}

// The point the report exists to make: most tokens an agent handles are dead
// within the hour, and nothing on disk says so.
func TestScanSecretsReadsAJWTsOwnExpiry(t *testing.T) {
	dead := jwtWith(time.Now().Add(-72*time.Hour), "https://sts.windows.net/tenant-id/")
	live := jwtWith(time.Now().Add(48*time.Hour), "https://token.actions.githubusercontent.com")

	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		"curl -H 'Authorization: Bearer "+dead+"'",
		"curl -H 'Authorization: Bearer "+live+"'"))

	var expired, rotate int
	for _, f := range findings {
		switch f.Status {
		case SecretExpired:
			expired++
			if !strings.Contains(f.Note, "expired") {
				t.Errorf("note does not say when it died: %q", f.Note)
			}
			if !strings.Contains(f.Note, "sts.windows.net") {
				t.Errorf("note does not say who issued it: %q", f.Note)
			}
			if f.Rotate != "" {
				t.Errorf("a dead token has nothing to revoke, got %q", f.Rotate)
			}
		case SecretRotate:
			rotate++
			if !strings.Contains(f.Note, "LIVE") {
				t.Errorf("a live token should say so: %q", f.Note)
			}
		}
	}
	if expired != 1 || rotate != 1 {
		t.Errorf("expired=%d rotate=%d, want one of each", expired, rotate)
	}

	// And the issuer is the host only: the claim carries a tenant id, which
	// is identifying and is not needed to say what the token was for.
	for _, f := range findings {
		if strings.Contains(f.Note, "tenant-id") {
			t.Errorf("the note leaks the issuer path: %q", f.Note)
		}
	}
}

// Eighteen copies of a key Microsoft publishes is eighteen false alarms, and
// false alarms are how a report like this gets ignored.
func TestScanSecretsKnowsThePublicAzuriteKey(t *testing.T) {
	// Built from parts for the same reason as the Slack one above. This is
	// the documented emulator key, not anybody's secret.
	azurite := "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq" +
		"/K1SZFPTOtr/KBHBeksoGMGw=="
	conn := "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" +
		azurite + ";"

	findings := scan(secretSession("aaa", "/w/x", time.Now(), "export CS='"+conn+"'"))

	f := findingFor(t, findings, "azure storage key")
	if f.Status != SecretPublic {
		t.Errorf("status = %q, want public — this key is in Microsoft's own docs", f.Status)
	}
	if f.Rotate != "" {
		t.Errorf("nothing to revoke on a published key, got %q", f.Rotate)
	}
	if !strings.Contains(f.Note, "azurite") {
		t.Errorf("note = %q, want it to say what the key is", f.Note)
	}
}

// A real storage key names the account it belongs to, which is the difference
// between "rotate a key" and a job someone can do.
func TestScanSecretsNamesTheStorageAccount(t *testing.T) {
	conn := "DefaultEndpointsProtocol=https;AccountName=stprod01;AccountKey=" +
		"bXlzdXBlcnNlY3JldGtleXZhbHVlMTIzNDU2Nzg5MA==;EndpointSuffix=core.windows.net"
	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		"az storage blob list --connection-string '"+conn+"'",
		// A different credential on its own line must NOT pick the account
		// up: searching the whole text put "storage account stprod01" on a
		// Slack token that merely shared a transcript with one.
		"slack token "+slackLike))

	f := findingFor(t, findings, "azure storage key")
	if !strings.Contains(f.Note, "stprod01") {
		t.Errorf("note = %q, want the account name", f.Note)
	}
	other := findingFor(t, findings, "slack token")
	if strings.Contains(other.Note, "stprod01") {
		t.Errorf("the slack token was labelled with a storage account: %q", other.Note)
	}
}

// And on the SAME line, which an export line really does put them on. The
// line window alone does not save this one — only knowing that a storage
// account belongs to a storage key does.
func TestOnlyAStorageKeyBelongsToAStorageAccount(t *testing.T) {
	line := "export CS='DefaultEndpointsProtocol=https;AccountName=stprod01;" +
		"AccountKey=bXlzdXBlcnNlY3JldGtleXZhbHVlMTIzNDU2Nzg5MA==' SLACK='" +
		slackLike + "' GH='" + ghToken + "'"

	findings := scan(secretSession("aaa", "/w/x", time.Now(), line))

	if f := findingFor(t, findings, "azure storage key"); !strings.Contains(f.Note, "stprod01") {
		t.Errorf("the storage key lost its account: %q", f.Note)
	}
	for _, kind := range []string{"slack token", "github token"} {
		if f := findingFor(t, findings, kind); strings.Contains(f.Note, "stprod01") {
			t.Errorf("%s was labelled with a storage account it has nothing "+
				"to do with: %q", kind, f.Note)
		}
	}
}

// The same key written two ways is one key to revoke, not two.
func TestScanSecretsFingerprintsTheValueNotTheSpelling(t *testing.T) {
	key := "bXlzdXBlcnNlY3JldGtleXZhbHVlMTIzNDU2Nzg5MA=="
	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		"AccountKey="+key, "accountkey="+key))

	n := 0
	for _, f := range findings {
		if f.Kind == "azure storage key" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d storage-key findings, want 1 — it is one key", n)
	}
}

// A reference is not a credential, and the whole report loses its authority
// if it tells you to go and revoke `$ARM_CLIENT_SECRET`.
func TestScanSecretsIgnoresAReferenceToASecret(t *testing.T) {
	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		`client_secret = $ARM_CLIENT_SECRET`,
		`api_key = os.Getenv("OPENAI_API_KEY")`,
		`password = "<your-password>"`))
	if len(findings) != 0 {
		t.Errorf("%d findings, want none: %+v", len(findings), findings)
	}
}

// A literal assigned to a secret-looking name is a judgement, so it is
// reported as one rather than mixed in with the shapes.
func TestScanSecretsCallsAnAssignedLiteralPossible(t *testing.T) {
	findings := scan(secretSession("aaa", "/w/x", time.Now(),
		`password = "hunter2hunter2"`))
	f := findingFor(t, findings, "assigned secret")
	if f.Status != SecretPossible {
		t.Errorf("status = %q, want possible", f.Status)
	}
}

// Worst first. A report that opens with twenty dead tokens buries the one
// that is live.
func TestScanSecretsPutsWhatMattersFirst(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	findings := scan(secretSession("aaa", "/w/x", old,
		"Authorization: Bearer "+jwtWith(time.Now().Add(-time.Hour), "https://login.microsoftonline.com/x/"),
		`password = "hunter2hunter2"`,
		"token "+ghToken))

	if len(findings) < 3 {
		t.Fatalf("%d findings, want 3", len(findings))
	}
	want := []SecretStatus{SecretRotate, SecretPossible, SecretExpired}
	for i, w := range want {
		if findings[i].Status != w {
			t.Errorf("findings[%d] is %q, want %q — order is the triage",
				i, findings[i].Status, w)
		}
	}
}

func TestScanSecretsOnNothing(t *testing.T) {
	if got := scan(); len(got) != 0 {
		t.Errorf("%d findings from no sessions", len(got))
	}
	if got := scan(nil); len(got) != 0 {
		t.Errorf("%d findings from a nil session", len(got))
	}
	if got := scan(secretSession("aaa", "/w/x", time.Now(), "ls -la")); len(got) != 0 {
		t.Errorf("%d findings from an innocent session: %+v", len(got), got)
	}
}

// Redaction and this report read the same table, so anything verbose hides it
// can also tell you to revoke. Drifting apart means masking a credential the
// report never mentions.
func TestEveryRedactedShapeIsAReportableKind(t *testing.T) {
	for i, r := range redactors {
		if r.kind == "" {
			t.Errorf("redactors[%d] has no kind, so a match of it would be "+
				"masked and never reported", i)
		}
	}
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	a, b := Fingerprint("one"), Fingerprint("one")
	if a != b {
		t.Errorf("not stable: %q vs %q", a, b)
	}
	if a == Fingerprint("two") {
		t.Error("two values share a fingerprint")
	}
	if len(a) != 8 {
		t.Errorf("fingerprint %q is %d characters, want 8", a, len(a))
	}
	if strings.Contains(fmt.Sprint(a), "one") {
		t.Error("the fingerprint contains the value")
	}
}

// Two storage accounts in one transcript must not be confused for each
// other. Without the line window the first AccountName in the text wins, and
// the report sends you to rotate the wrong account's key — which is worse
// than saying nothing, because it looks like an answer.
func TestEachStorageKeyKeepsItsOwnAccount(t *testing.T) {
	text := "export PROD='DefaultEndpointsProtocol=https;AccountName=stprod01;" +
		"AccountKey=cHJvZHVjdGlvbmtleXZhbHVlMTIzNDU2Nzg5MDEyMzQ1Ng=='\n" +
		"export TEST='DefaultEndpointsProtocol=https;AccountName=sttest02;" +
		"AccountKey=dGVzdGluZ2tleXZhbHVlOTg3NjU0MzIxMDk4NzY1NDMy'"

	findings := scan(secretSession("aaa", "/w/x", time.Now(), text))

	accounts := map[string]bool{}
	for _, f := range findings {
		if f.Kind != "azure storage key" {
			continue
		}
		if !strings.HasPrefix(f.Note, "storage account ") {
			t.Fatalf("no account on %s: %q", f.Fingerprint, f.Note)
		}
		accounts[strings.TrimPrefix(f.Note, "storage account ")] = true
	}
	if len(accounts) != 2 || !accounts["stprod01"] || !accounts["sttest02"] {
		t.Errorf("accounts = %v, want one each for stprod01 and sttest02", accounts)
	}
}
