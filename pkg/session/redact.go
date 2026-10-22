package session

import (
	"regexp"
	"strings"
)

// Masking secrets out of a transcript, for reading one where someone else can
// see it: a screen share, a screenshot, a `-json` dump handed to a colleague.
//
// Off unless asked for. A transcript is the reader's own and the secrets in it
// are theirs; what redaction is for is the moment it stops being private.
//
// Everything here is shaped by counting the real corpus rather than by
// imagining what a secret looks like. The measurement that mattered: a generic
// `sk-[A-Za-z0-9_-]{20,}` matched 255 times across 33.8MB of transcript, and
// every single one of them was the word "task-". Nought real keys. The same
// pattern anchored to a word boundary matched nothing at all, which is the
// honest answer — there are no OpenAI keys in that history, and a redactor
// that mangles "task-group" to prove otherwise is worse than none.
//
// What IS there, and what these patterns are built for: 136 GitHub tokens
// across 2 sessions, 18 Azure account keys across 5, 9 JWTs, and about 60
// assignments of a password or a secret.

// RedactedMark replaces a secret's value. ASCII on purpose: it is drawn in
// every view, and a glyph that is ambiguous-width in one terminal is a layout
// bug waiting to happen.
const RedactedMark = "[redacted]"

// Redact masks the secrets in a piece of transcript text.
//
// Every pattern is gated on a plain substring first. Running the lot over a
// whole history meant thirteen regex passes across 37MB and took 11 seconds
// to open; one lower-cased copy and thirteen Contains checks cut that to
// about a second, because almost no piece of transcript text contains any of
// these words at all.
func Redact(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	for _, r := range redactors {
		if !gated(lower, r.gates) {
			continue
		}
		if r.fn != nil {
			s = r.re.ReplaceAllStringFunc(s, r.fn)
			continue
		}
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

// gated reports whether any of a pattern's markers is present at all. The
// markers are lower-case, and the haystack has already been folded.
func gated(lower string, gates []string) bool {
	for _, g := range gates {
		if strings.Contains(lower, g) {
			return true
		}
	}
	return false
}

type redactor struct {
	// kind names the credential in a reader's words. It is here rather than
	// in a second table so masking and FINDING cannot drift: a shape that
	// redact knows how to hide is a shape `verbose secrets` knows to report.
	kind  string
	re    *regexp.Regexp
	with  string
	fn    func(string) string
	gates []string

	// rotate is where this kind of credential is revoked, when there is one
	// place. A finding without an action is a worry rather than a task.
	rotate string

	// prevent is how this kind stops reaching a transcript again. Revoking
	// is the first half and the shorter one: the credential came through the
	// agent's hands somehow, and unless that changes the next one will too.
	prevent string
}

var redactors = []redactor{
	// A key block is the one case where the whole thing goes: the body is the
	// secret and the armour is what makes it findable.
	{kind: "private key",
		rotate: "Generate a new pair and remove the old public half from " +
			"anything that trusts it",
		prevent: "A key that has been read into a transcript is a key to " +
			"replace, not to move. Use an agent or a keychain so the private " +
			"half is never in a file an agent can read.",
		re:    regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		with:  "-----BEGIN PRIVATE KEY----- " + RedactedMark + " -----END PRIVATE KEY-----",
		gates: []string{"-----begin "}},

	// Azure. AccountKey is the one that actually appears here, and it arrives
	// inside a connection string that is otherwise worth reading — the account
	// name and the endpoint say which storage it was.
	{kind: "azure storage key", rotate: "Azure portal → the storage account → Access keys → Rotate",
		prevent: "Rotating breaks everything still using the key, so move the " +
			"consumers to Entra ID (az login plus RBAC) or a short-lived SAS " +
			"first, then rotate.",
		re: regexp.MustCompile(`(?i)(AccountKey=)[A-Za-z0-9+/=]{16,}`), with: "${1}" + RedactedMark,
		gates: []string{"accountkey="}},
	{kind: "azure shared access key", rotate: "Azure portal → the resource → Shared access policies",
		prevent: "Issue keys per consumer so one can be revoked without " +
			"touching the others.",
		re: regexp.MustCompile(`(?i)(SharedAccessKey=)[A-Za-z0-9+/=]{16,}`), with: "${1}" + RedactedMark,
		gates: []string{"sharedaccesskey="}},
	{kind: "azure SAS signature", rotate: "Azure portal → rotate the signing key the SAS was issued from",
		prevent: "Issue a SAS from a stored access policy with a short expiry: " +
			"that is what makes one revocable without rotating the account key.",
		re: regexp.MustCompile(`(?i)([?&]sig=)[A-Za-z0-9%+/=]{16,}`), with: "${1}" + RedactedMark,
		gates: []string{"sig="}},

	// Tokens that announce themselves with a prefix. Every one of these is
	// anchored with \b, which is the whole lesson of the measurement above:
	// without it "task-" is an OpenAI key 255 times over.
	{kind: "github token", rotate: "https://github.com/settings/tokens",
		prevent: "A token in a remote URL is written to .git/config too. Reset the " +
			"remote to plain https and let `gh auth login` or a credential helper " +
			"hold it, so it is never in a URL an agent reads.",
		re: regexp.MustCompile(`\b(gh[pousr]_)[A-Za-z0-9]{20,}`), with: "${1}" + RedactedMark,
		gates: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}},
	{kind: "github fine-grained token", rotate: "https://github.com/settings/personal-access-tokens",
		prevent: "Scope the replacement to the one repository and give it an " +
			"expiry — a fine-grained token with no expiry is a classic one with " +
			"extra steps.",
		re: regexp.MustCompile(`\b(github_pat_)[A-Za-z0-9_]{20,}`), with: "${1}" + RedactedMark,
		gates: []string{"github_pat_"}},
	{kind: "anthropic api key", rotate: "https://console.anthropic.com/settings/keys",
		prevent: "Keep it in the environment or a keychain, and never echo it " +
			"into a command — the command line is recorded verbatim.",
		re: regexp.MustCompile(`\b(sk-ant-)[A-Za-z0-9_-]{20,}`), with: "${1}" + RedactedMark,
		gates: []string{"sk-ant-"}},
	{kind: "openai api key", rotate: "https://platform.openai.com/api-keys",
		prevent: "Keep it in the environment or a keychain, and never echo it " +
			"into a command — the command line is recorded verbatim.",
		re: regexp.MustCompile(`\b(sk-)[A-Za-z0-9]{20,}`), with: "${1}" + RedactedMark,
		gates: []string{"sk-"}},
	{kind: "gitlab token", rotate: "GitLab → Settings → Access tokens",
		prevent: "Give the replacement an expiry and the narrowest scope that " +
			"works.",
		re: regexp.MustCompile(`\b(glpat-)[A-Za-z0-9_-]{16,}`), with: "${1}" + RedactedMark,
		gates: []string{"glpat-"}},
	{kind: "slack token", rotate: "https://api.slack.com/apps → the app → OAuth & Permissions",
		prevent: "Rotate from the app configuration rather than re-installing, " +
			"so the scopes stay as they were.",
		re: regexp.MustCompile(`\b(xox[baprs]-)[A-Za-z0-9-]{10,}`), with: "${1}" + RedactedMark,
		gates: []string{"xox"}},
	{kind: "aws access key id", rotate: "AWS IAM → the user → Security credentials",
		prevent: "Prefer IAM Identity Center (aws sso login) or an instance role " +
			"over a long-lived key pair; the pair is the thing that can leak.",
		re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), with: "AKIA" + RedactedMark,
		gates: []string{"akia"}},

	// A JWT carries its own claims, so the header alone is enough to say what
	// was there.
	{kind: "jwt",
		prevent: "Usually nothing: an access token is minted short-lived and is " +
			"dead by the time it is read. If it is long-lived, the fix belongs " +
			"at the issuer, not here.",
		re:   regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}(\.[A-Za-z0-9_-]+)?`),
		with: "eyJ" + RedactedMark, gates: []string{"eyj"}},

	{kind: "bearer token",
		prevent: "Identify the issuer first — it is whoever the request was to — " +
			"then revoke there. An opaque bearer says nothing about itself.",
		re:   regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._~+/=-]{20,}`),
		with: "${1}" + RedactedMark, gates: []string{"bearer"}},

	// Assignments need a decision rather than a replacement, because most of
	// what sits to the right of `client_secret =` is not a secret.
	{kind: "assigned secret",
		prevent: "Move the value out of the file into the environment or a " +
			"secret store, then rotate it — it is in the file's history as well.",
		re: assignRe, fn: redactAssignment, gates: []string{
			"secret", "password", "passwd", "pwd", "api_key", "apikey", "api-key",
			"access_token", "accesstoken", "access-token", "auth_token",
			"authtoken", "auth-token",
		}},
}

// assignRe matches a secret-ish name, a separator, and the value up to
// whatever ends it. The value is deliberately loose: deciding whether it is a
// secret is redactAssignment's job, not the pattern's.
var assignRe = regexp.MustCompile(
	`(?i)\b(client[_-]?secret|api[_-]?key|access[_-]?token|auth[_-]?token|` +
		`secret[_-]?key|password|passwd|pwd)(\s*[:=]+\s*)(["'` + "`" + `]?)([^\s"'` + "`" + `,;)}\]]+)`)

// redactAssignment masks the value of a secret-named assignment unless the
// value is a REFERENCE to one.
//
// This is the difference between a redactor people leave on and one they turn
// off. Of the assignments in this history, 15 of them set client_secret to a
// shell variable and several more to an empty string or a placeholder: masking
// those protects nothing and destroys the one useful fact on the line, which
// is WHICH variable the secret came from. Terraform and GitHub Actions make
// that the normal case — `var.client_secret`, `${{ secrets.AZURE_CLIENT_ID }}`
// — and a consultancy's transcripts are mostly those.
func redactAssignment(match string) string {
	m := assignRe.FindStringSubmatch(match)
	if m == nil {
		return match
	}
	name, sep, quote, value := m[1], m[2], m[3], m[4]
	if isReference(value) {
		return match
	}
	return name + sep + quote + RedactedMark
}

// isReference reports whether a value points at a secret rather than being
// one. Anything it says yes to is left on screen.
func isReference(v string) bool {
	if v == "" {
		return true
	}
	switch strings.ToLower(strings.Trim(v, `"'`)) {
	case "", "null", "none", "nil", "true", "false", "undefined":
		return true
	}
	// A shell or template expansion: $VAR, ${VAR}, ${{ secrets.X }}, %VAR%.
	if strings.HasPrefix(v, "$") || strings.HasPrefix(v, "%") {
		return true
	}
	// A placeholder nobody ever filled in.
	if strings.HasPrefix(v, "<") || strings.HasPrefix(v, "{") {
		return true
	}
	lower := strings.ToLower(v)
	for _, marker := range []string{
		"getenv", "environ", "process.env", "secrets.", "var.", "local.", "data.",
		"your_", "your-", "xxx", "...", "changeme", "replace_me", "replaceme",
		"placeholder", "example", "redacted", "<nil>",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// RedactSession masks every piece of text a session can hold. It rewrites in
// place, because the store hands the same pointer to every view.
//
// Paths are left alone. A directory name is not a secret, and a redacted path
// makes the tree, the relinks and the lost-work survey all wrong.
func RedactSession(sess *Session) {
	if sess == nil {
		return
	}
	sess.Info.Title = Redact(sess.Info.Title)

	for i := range sess.Events {
		e := &sess.Events[i]
		e.UserText = Redact(e.UserText)
		e.Text = Redact(e.Text)
		e.Thinking = Redact(e.Thinking)
		e.ToolOutput = Redact(e.ToolOutput)
		e.UserFeedback = Redact(e.UserFeedback)
		e.AgentDescription = Redact(e.AgentDescription)
		redactInput(e.ToolInput)
		redactResult(e.Result)
		for j := range e.Diagnostics {
			e.Diagnostics[j].Message = Redact(e.Diagnostics[j].Message)
		}
	}
}

// redactInput walks a tool's arguments. They are a bag of anything — a
// command, a file's whole contents, a nested object — so every string in it
// is treated as transcript text.
func redactInput(in map[string]interface{}) {
	for k, v := range in {
		switch t := v.(type) {
		case string:
			in[k] = Redact(t)
		case map[string]interface{}:
			redactInput(t)
		case []interface{}:
			redactList(t)
		}
	}
}

func redactList(list []interface{}) {
	for i, v := range list {
		switch t := v.(type) {
		case string:
			list[i] = Redact(t)
		case map[string]interface{}:
			redactInput(t)
		case []interface{}:
			redactList(t)
		}
	}
}

// redactResult covers what an operation produced, including the diff: a hunk
// of a .env or a terraform.tfvars is a real way for a key to be on screen.
func redactResult(r *ToolResult) {
	if r == nil {
		return
	}
	r.Stdout = Redact(r.Stdout)
	r.Stderr = Redact(r.Stderr)
	r.Raw = Redact(r.Raw)
	// The two halves of an edit: what was there and what replaced it.
	r.OldString = Redact(r.OldString)
	r.NewString = Redact(r.NewString)
	for i := range r.StructuredPatch {
		for j, line := range r.StructuredPatch[i].Lines {
			r.StructuredPatch[i].Lines[j] = Redact(line)
		}
	}
	if r.BashEdit == nil {
		return
	}
	for i := range r.BashEdit.Files {
		for j := range r.BashEdit.Files[i].Hunks {
			for k, line := range r.BashEdit.Files[i].Hunks[j].Lines {
				r.BashEdit.Files[i].Hunks[j].Lines[k] = Redact(line)
			}
		}
	}
}
