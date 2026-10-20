package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// slackish is assembled rather than written out, because a literal of this
// shape is a secret as far as a scanner is concerned — GitHub's push
// protection rejected the whole branch over this line, which is the system
// working exactly as it should. The value is invented; the SHAPE is the test.
var slackish = "xox" + "b-" + "1234567890-" + "abcdefghijklmnop"

func TestRedactMasksTheShapesThatAreReallyThere(t *testing.T) {
	// Values are the right SHAPE and deliberately not real.
	for _, c := range []struct {
		name, in, keep, gone string
	}{
		{"github token",
			"gh auth: ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8",
			"ghp_", "A1b2C3d4E5f6G7h8I9j0"},
		{"github fine-grained",
			"token=github_pat_11ABCDEFG0aBcDeFgHiJkL_mNoPqRsTuVwXyZ0123456789",
			"github_pat_", "mNoPqRsTuVwXyZ"},
		{"azure account key",
			"DefaultEndpointsProtocol=https;AccountName=stprod01;AccountKey=bXlzdXBlcnNlY3JldGtleXZhbHVlMTIzNDU2Nzg5MA==;",
			"AccountName=stprod01", "bXlzdXBlcnNlY3JldGtleXZhbHVl"},
		{"sas signature",
			"https://x.blob.core.windows.net/c/b?sv=2022-11-02&sig=aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789%3D",
			"sv=2022-11-02", "aBcDeFgHiJkLmNoPqRs"},
		{"aws key id", "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE", "AKIA", "IOSFODNN7EXAMPLE"},
		{"slack token", slackish, "xoxb-", "abcdefghijklmnop"},
		{"gitlab token", "glpat-AbCdEfGhIjKlMnOpQrSt", "glpat-", "AbCdEfGhIjKlMnOpQrSt"},
		{"anthropic key",
			"ANTHROPIC_API_KEY=sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
			"sk-ant-", "api03-AbCdEfGhIjKlMn"},
		{"jwt",
			"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			"eyJ", "dBjftJeZ4CVPmB92K27uhbUJ"},
		{"private key",
			"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAx4fm\n-----END RSA PRIVATE KEY-----",
			"PRIVATE KEY", "MIIEowIBAAKCAQEAx4fm"},
		{"literal password", `db:\n  password: hunter2hunter2`, "password", "hunter2hunter2"},
		{"literal client secret", `client_secret = "Q~8vZkL2pR5nT7wX1yB3cD6fH9jM0"`, "client_secret", "Q~8vZkL2pR5nT7wX1yB3cD6fH9jM0"},
	} {
		got := Redact(c.in)
		if strings.Contains(got, c.gone) {
			t.Errorf("%s: the secret survived: %q", c.name, got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("%s: the shape was lost, so the line no longer says what was there: %q",
				c.name, got)
		}
		if !strings.Contains(got, RedactedMark) {
			t.Errorf("%s: nothing marked as redacted: %q", c.name, got)
		}
	}
}

// The measurement this whole file is built on: an unanchored sk- pattern
// matched 255 times across one real history and every one was "task-".
func TestRedactLeavesOrdinaryWordsAlone(t *testing.T) {
	for _, in := range []string{
		"templates/task-definition.yaml",
		"see [US2] In `task-orchestrator` for the retry policy",
		"r = run(\"task-runner --watch\")",
		// Long enough to BE a key but for the word boundary: without \b the
		// "sk-" inside "task-" is a match, which is how an unanchored pattern
		// found 255 secrets in a real history and was wrong every time.
		"queue the task-abcdefghijklmnopqrstuvwxyz0123 job",
		"the task-x9k2m4p8q1w5e7r3t6y0u2i4o6 handler",
		"the monkey-patch in ghost_writer_helpers.go",
		"keyboard shortcuts and tokenizer settings",
		"AKIAIOSFODNN", // too short to be a key id
		"sk-short",
		"eyJustAnIdentifier",
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q — it changed ordinary text", in, got)
		}
	}
}

// Of the secret-named assignments in one real history, fifteen set
// client_secret to a shell variable and several more to an empty string or a
// placeholder. Masking those protects nothing and destroys the only useful
// fact on the line: which variable the secret came from.
func TestRedactKeepsAReferenceToASecret(t *testing.T) {
	for _, in := range []string{
		`client_secret = $ARM_CLIENT_SECRET`,
		`client_secret = "${ARM_CLIENT_SECRET}"`,
		`client_secret = var.client_secret`,
		`client_secret = local.secrets["app"]`,
		`client_secret: ${{ secrets.AZURE_CLIENT_SECRET }}`,
		`api_key = os.Getenv("OPENAI_API_KEY")`,
		`apiKey: process.env.API_KEY`,
		`password = "<your-password-here>"`,
		`password = "YOUR_PASSWORD"`,
		`api_key = "xxxxxxxx"`,
		`password = ""`,
		`access_token: null`,
		`password = %DB_PASSWORD%`,
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q — a reference is not a secret", in, got)
		}
	}
}

// Running it twice must not nest marks, because the store can redact a
// session that a transcript already had a mark in.
func TestRedactIsIdempotent(t *testing.T) {
	in := "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 and password: hunter2hunter2"
	once := Redact(in)
	twice := Redact(once)
	if once != twice {
		t.Errorf("second pass changed it:\n  once:  %q\n  twice: %q", once, twice)
	}
	if n := strings.Count(twice, RedactedMark); n != 2 {
		t.Errorf("%d marks, want 2", n)
	}
}

// Every place a secret can hide in a session, in one go. A field left out of
// RedactSession is a field that leaks, and they are easy to miss.
func TestRedactSessionReachesEveryField(t *testing.T) {
	secret := "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	sess := &Session{
		// The CWD is a path that a redactor WOULD rewrite if it were let near
		// one: a short stand-in proves nothing, because no pattern matches it
		// either way.
		Info: SessionInfo{ID: "s1", Title: "fix the auth with " + secret,
			CWD: "/repo/" + secret},
		Events: []Event{{
			Type:             EventToolUse,
			UserText:         "use " + secret,
			Text:             "I will use " + secret,
			Thinking:         "the token is " + secret,
			ToolOutput:       "sent with " + secret,
			UserFeedback:     "not " + secret,
			AgentDescription: "deploy with " + secret,
			FilePath:         "/repo/" + secret + "/deploy.sh",
			ToolInput: map[string]interface{}{
				"command": "curl -H 'Authorization: token " + secret + "'",
				"nested":  map[string]interface{}{"token": secret},
				"list":    []interface{}{"a", secret, []interface{}{secret}},
			},
			Diagnostics: []Diagnostic{{Message: "leaked " + secret}},
			Result: &ToolResult{
				Stdout: "out " + secret, Stderr: "err " + secret, Raw: "raw " + secret,
				OldString: "old " + secret, NewString: "new " + secret,
				FilePath:        "/repo/.env",
				StructuredPatch: []PatchHunk{{Lines: []string{"+TOKEN=" + secret}}},
				BashEdit: &BashEditDiff{Files: []ChangedFile{{
					FilePath: "/repo/.env",
					Hunks:    []PatchHunk{{Lines: []string{"+GH=" + secret}}},
				}}},
			},
		}},
	}

	RedactSession(sess)

	e := sess.Events[0]
	in := e.ToolInput
	nested := in["nested"].(map[string]interface{})
	list := in["list"].([]interface{})
	deeper := list[2].([]interface{})
	for name, got := range map[string]string{
		"Info.Title":        sess.Info.Title,
		"UserText":          e.UserText,
		"Text":              e.Text,
		"Thinking":          e.Thinking,
		"ToolOutput":        e.ToolOutput,
		"UserFeedback":      e.UserFeedback,
		"AgentDescription":  e.AgentDescription,
		"ToolInput.command": in["command"].(string),
		"ToolInput.nested":  nested["token"].(string),
		"ToolInput.list":    list[1].(string),
		"ToolInput.deeper":  deeper[0].(string),
		"Diagnostics":       e.Diagnostics[0].Message,
		"Result.Stdout":     e.Result.Stdout,
		"Result.Stderr":     e.Result.Stderr,
		"Result.Raw":        e.Result.Raw,
		"Result.OldString":  e.Result.OldString,
		"Result.NewString":  e.Result.NewString,
		"StructuredPatch":   e.Result.StructuredPatch[0].Lines[0],
		"BashEdit":          e.Result.BashEdit.Files[0].Hunks[0].Lines[0],
	} {
		if strings.Contains(got, "A1b2C3d4") {
			t.Errorf("%s still holds the token: %q", name, got)
		}
		if !strings.Contains(got, RedactedMark) {
			t.Errorf("%s was not redacted at all: %q", name, got)
		}
	}

	// Paths are not secrets, and a redacted one breaks the tree, the relinks
	// and the survival survey.
	if sess.Info.CWD != "/repo/"+secret {
		t.Errorf("CWD was rewritten to %q", sess.Info.CWD)
	}
	if e.FilePath != "/repo/"+secret+"/deploy.sh" || e.Result.FilePath != "/repo/.env" {
		t.Errorf("a file path was rewritten: %q %q", e.FilePath, e.Result.FilePath)
	}
}

func TestRedactSessionSurvivesNothing(t *testing.T) {
	RedactSession(nil) // must not panic
	sess := &Session{Events: []Event{{Type: EventUserPrompt}}}
	RedactSession(sess)
	if sess.Events[0].Result != nil {
		t.Error("a nil result should stay nil")
	}
}

// The store is where redaction has to happen, because every view and -json
// read the same pointer out of it.
func TestStoreRedactsAsItReads(t *testing.T) {
	secret := "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	home := isolatedHome(t)
	writeFile(t, filepath.Join(home, ".claude", "projects", "-repo", "aaa.jsonl"),
		`{"type":"user","uuid":"u1","timestamp":"2026-01-02T10:00:00.000Z","cwd":"/repo",`+
			`"message":{"role":"user","content":"push with `+secret+` please"}}`+"\n")

	for _, on := range []bool{false, true} {
		store, err := NewStore()
		if err != nil {
			t.Fatal(err)
		}
		store.SetRedaction(on)
		if err := store.Scan(); err != nil {
			t.Fatal(err)
		}
		infos := store.GetSessions()
		if len(infos) != 1 {
			t.Fatalf("redact=%v: %d sessions, want 1", on, len(infos))
		}
		sess := store.GetSession(infos[0].ID)
		if sess == nil {
			t.Fatalf("redact=%v: session missing", on)
		}
		text := sess.Events[0].UserText + " " + sess.Info.Title
		leaked := strings.Contains(text, "A1b2C3d4")
		if on && leaked {
			t.Errorf("the store handed out the token with redaction on: %q", text)
		}
		if !on && !leaked {
			t.Errorf("redaction happened without being asked for: %q", text)
		}
		if store.Redacting() != on {
			t.Errorf("Redacting() = %v, want %v — a view cannot say so otherwise",
				store.Redacting(), on)
		}
		store.Close()
	}
}
