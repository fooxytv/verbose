package session

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Finding out what is sitting in the transcripts, so it can be revoked.
//
// This is the half of the secrets problem that actually reduces risk. Masking
// a view stops a credential reaching a NEW audience; it does nothing about the
// copy already on disk, which outlives the session and is read by every
// backup. Only rotating the credential does that, and you cannot rotate what
// you do not know is there.
//
// The report never prints a secret. A value becomes eight hex characters of
// its SHA-256, which is enough to tell two apart, count them, and say which
// sessions they are in — and safe to paste into a ticket, which is where this
// output is going to end up.

// SecretStatus is the triage: what, if anything, to do about a finding.
type SecretStatus string

const (
	// SecretRotate is a long-lived credential with nothing to expire it.
	SecretRotate SecretStatus = "rotate"
	// SecretExpired is a credential whose own claims say it is already dead.
	SecretExpired SecretStatus = "expired"
	// SecretPublic is a value that is published on purpose and is not a
	// secret at all — the azurite key is the one that matters here, because
	// it looks exactly like a production storage key and is in Microsoft's
	// own documentation.
	SecretPublic SecretStatus = "public"
	// SecretPossible is a judgement rather than a shape: a password or an
	// api_key assigned a literal. Worth seeing, not worth alarming over.
	SecretPossible SecretStatus = "possible"
)

// Sighting is one session a credential appears in.
type Sighting struct {
	SessionID string    `json:"session"`
	Project   string    `json:"project"`
	Source    string    `json:"source"`
	Title     string    `json:"title"`
	When      time.Time `json:"when"`
	Count     int       `json:"count"`
}

// SecretFinding is one distinct credential, wherever it turned up.
type SecretFinding struct {
	Kind        string       `json:"kind"`
	Fingerprint string       `json:"fingerprint"`
	Status      SecretStatus `json:"status"`
	// Note says what is known beyond the shape: which storage account, when a
	// token expired, why something is not a secret.
	Note    string     `json:"note,omitempty"`
	Rotate  string     `json:"rotate,omitempty"`
	Count   int        `json:"count"`
	First   time.Time  `json:"first"`
	Last    time.Time  `json:"last"`
	Seen    []Sighting `json:"seen"`
	expires time.Time
}

// ScanSecrets reports every credential shape in the given sessions.
//
// It uses the same table the redactor does, so a shape verbose knows how to
// hide is a shape it knows to report. Scanning is the expensive part — about
// a second and a half over a 37MB history — so it is a command, not a view.
func ScanSecrets(sessions []*Session, infos map[string]SessionInfo) []SecretFinding {
	byFP := map[string]*SecretFinding{}

	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		info := sess.Info
		if i, ok := infos[sess.Info.ID]; ok {
			info = i
		}
		for _, text := range secretTexts(sess) {
			scanText(text, info, byFP)
		}
	}

	out := make([]SecretFinding, 0, len(byFP))
	for _, f := range byFP {
		sort.Slice(f.Seen, func(i, j int) bool {
			return f.Seen[i].When.After(f.Seen[j].When)
		})
		out = append(out, *f)
	}

	// Worst first: what has to be rotated, then what might, then what is dead
	// or harmless. Within a status, the most recent — a token from this week
	// is likelier to still be live than one from March.
	rank := map[SecretStatus]int{
		SecretRotate: 0, SecretPossible: 1, SecretExpired: 2, SecretPublic: 3,
	}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Status] != rank[out[j].Status] {
			return rank[out[i].Status] < rank[out[j].Status]
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// scanText runs every pattern over one piece of transcript text.
func scanText(text string, info SessionInfo, byFP map[string]*SecretFinding) {
	if text == "" {
		return
	}
	lower := strings.ToLower(text)
	for _, r := range redactors {
		if !gated(lower, r.gates) {
			continue
		}
		for _, at := range r.re.FindAllStringIndex(text, -1) {
			match := text[at[0]:at[1]]
			value := secretValue(match)
			if r.fn != nil && isReference(assignedValue(match)) {
				// An assignment pointing at a secret is not one.
				continue
			}
			if value == "" {
				continue
			}
			fp := Fingerprint(value)
			f := byFP[fp]
			if f == nil {
				f = &SecretFinding{Kind: r.kind, Fingerprint: fp, Rotate: r.rotate}
				f.Status, f.Note, f.expires = classify(r, value, lineAround(text, at[0]))
				if f.Status == SecretPublic || f.Status == SecretExpired {
					f.Rotate = ""
				}
				byFP[fp] = f
			}
			f.Count++
			noteSighting(f, info)
		}
	}
}

// noteSighting records one more appearance, merging by session.
func noteSighting(f *SecretFinding, info SessionInfo) {
	if f.First.IsZero() || info.LastUpdate.Before(f.First) {
		f.First = info.LastUpdate
	}
	if info.LastUpdate.After(f.Last) {
		f.Last = info.LastUpdate
	}
	for i := range f.Seen {
		if f.Seen[i].SessionID == info.ID {
			f.Seen[i].Count++
			return
		}
	}
	f.Seen = append(f.Seen, Sighting{
		SessionID: info.ID, Project: sightingProject(info), Source: info.Source,
		Title: info.Title, When: info.LastUpdate, Count: 1,
	})
}

// sightingProject names the project the way the reader does.
//
// ProjectName comes from the encoded directory, which cannot tell a path
// separator from a hyphen — "powerbi-fabric-workspace-lifecycle" decodes to
// four nested directories and the name comes out as "lifecycle". The cwd the
// transcript recorded is the one that is right.
func sightingProject(info SessionInfo) string {
	if info.CWD != "" {
		return filepath.Base(info.CWD)
	}
	return info.ProjectName
}

// Fingerprint identifies a secret without disclosing it. Eight hex characters
// of SHA-256: enough to tell two apart and to recognise the same one in two
// places, and nothing a reader can work backwards from.
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

// namePrefix is a `name=` or `?name=` lead-in on a match. A base64 value's
// own `==` padding must not be mistaken for one, which is why this is
// anchored and why the name may not contain punctuation.
var namePrefix = regexp.MustCompile(`^[?&]?[A-Za-z_][A-Za-z0-9_-]*=`)

// secretValue is the part of a match that is actually the credential.
//
// The name is not secret, and leaving it in would make one key written
// `AccountKey=` and `accountkey=` look like two different keys — which
// matters, because the count of DISTINCT values is the number of things to
// go and revoke.
func secretValue(match string) string {
	if v := assignedValue(match); v != "" {
		return strings.Trim(strings.TrimSpace(v), `"'`+"`")
	}
	if i := strings.Index(strings.ToLower(match), "bearer "); i >= 0 {
		return strings.TrimSpace(match[i+len("bearer "):])
	}
	if loc := namePrefix.FindStringIndex(match); loc != nil {
		return strings.Trim(strings.TrimSpace(match[loc[1]:]), `"'`)
	}
	return strings.TrimSpace(match)
}

// assignedValue is what an assignment assigns, for the reference test.
func assignedValue(match string) string {
	m := assignRe.FindStringSubmatch(match)
	if m == nil {
		return ""
	}
	return m[4]
}

// lineAround is the line a match sits on. A connection string is one line, so
// that is the window in which another field of it means anything.
func lineAround(text string, at int) string {
	if at < 0 || at > len(text) {
		return text
	}
	start := strings.LastIndexByte(text[:at], '\n') + 1
	end := strings.IndexByte(text[at:], '\n')
	if end < 0 {
		return text[start:]
	}
	return text[start : at+end]
}

// classify decides what to do about a finding, which is the whole point of
// the report. A list of things that look like secrets is a worry; a list that
// separates "revoke this" from "this expired a fortnight ago" is a task.
func classify(r redactor, value, context string) (SecretStatus, string, time.Time) {
	if note, ok := publicValues[Fingerprint(value)]; ok {
		return SecretPublic, note, time.Time{}
	}
	if r.kind == "jwt" || strings.HasPrefix(value, "eyJ") {
		if exp, iss, ok := jwtClaims(value); ok {
			where := ""
			if iss != "" {
				where = ", issued by " + iss
			}
			if exp.Before(time.Now()) {
				return SecretExpired, "expired " + exp.Format("2006-01-02 15:04") + where, exp
			}
			return SecretRotate, "LIVE until " + exp.Format("2006-01-02 15:04") + where, exp
		}
	}
	if r.fn != nil {
		return SecretPossible, "a literal value assigned to a secret-looking name", time.Time{}
	}
	// Only a storage key belongs to a storage account, and only one on the
	// same LINE is the same connection string. Searching the whole text put
	// "storage account stprod01" on a Slack token that happened to share a
	// transcript with one.
	if r.kind == "azure storage key" {
		if name := storageAccount(context); name != "" {
			return SecretRotate, "storage account " + name, time.Time{}
		}
	}
	return SecretRotate, "", time.Time{}
}

// publicValues are credentials that are published on purpose, by fingerprint
// so this file holds no key-shaped literal of its own. Without this the
// azurite key — which Microsoft prints in its own documentation and which
// every local Azure Storage emulator uses — is eighteen alarming findings.
var publicValues = map[string]string{
	// The azurite / Azure Storage emulator devstoreaccount1 key.
	"0011cc25": "the PUBLIC azurite emulator key — documented by Microsoft, not a secret",
}

var storageAccountRe = regexp.MustCompile(`(?i)AccountName=([A-Za-z0-9]+)`)

// storageAccount is which account a key belongs to, which is the difference
// between "rotate a key" and a task someone can actually do.
func storageAccount(context string) string {
	if m := storageAccountRe.FindStringSubmatch(context); m != nil {
		return m[1]
	}
	return ""
}

// jwtClaims reads the expiry and issuer out of a JWT.
//
// No verification and no secret: the payload is base64url of plain JSON, by
// design, so the token says when it dies. That is what turns a row in this
// report from a question into an answer — most of the JWTs in a transcript
// are an hour-long access token that died the day it was minted.
func jwtClaims(token string) (exp time.Time, issuer string, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, "", false
	}
	var claims struct {
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, "", false
	}
	// Only the issuer's HOST. The rest of the claim carries a tenant id, and
	// naming the service is what a reader needs.
	issuer = claims.Iss
	if i := strings.Index(issuer, "://"); i >= 0 {
		issuer = issuer[i+3:]
	}
	if i := strings.Index(issuer, "/"); i >= 0 {
		issuer = issuer[:i]
	}
	return time.Unix(claims.Exp, 0), issuer, true
}

// secretTexts is every piece of a session a credential can hide in. It is
// deliberately the same reach as RedactSession: anything redaction covers has
// to be something this can find, or verbose would mask what it cannot report.
func secretTexts(sess *Session) []string {
	out := []string{sess.Info.Title}
	for i := range sess.Events {
		e := &sess.Events[i]
		out = append(out, e.UserText, e.Text, e.Thinking, e.ToolOutput, e.UserFeedback)
		out = appendInputStrings(out, e.ToolInput)
		if e.Result != nil {
			out = append(out, e.Result.Stdout, e.Result.Stderr, e.Result.Raw,
				e.Result.OldString, e.Result.NewString)
			for _, f := range e.Result.ChangedFiles() {
				for _, h := range f.Hunks {
					out = append(out, h.Lines...)
				}
			}
		}
		for _, d := range e.Diagnostics {
			out = append(out, d.Message)
		}
	}
	return out
}

func appendInputStrings(out []string, in map[string]interface{}) []string {
	for _, v := range in {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case map[string]interface{}:
			out = appendInputStrings(out, t)
		case []interface{}:
			for _, item := range t {
				switch iv := item.(type) {
				case string:
					out = append(out, iv)
				case map[string]interface{}:
					out = appendInputStrings(out, iv)
				}
			}
		}
	}
	return out
}
