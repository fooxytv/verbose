// Package secrets reports the credentials sitting in the transcripts, so
// they can be revoked.
//
// Masking a view (--redact) stops a credential reaching a new audience. It
// does nothing about the copy already on disk, which outlives the session and
// is read by every backup — and nothing about the fact that it has already
// been through a model. Rotating the credential is the only thing that does,
// and you cannot rotate what you do not know is there.
package secrets

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// Store is what this needs of a session store, so a test can supply its own.
type Store interface {
	GetSessions() []session.SessionInfo
	GetSession(id string) *session.Session
}

// Run executes `verbose secrets`.
//
// Exit code 0 whether or not anything is found: this is a report, and
// "nothing to revoke" is its best answer rather than a failure.
func Run(args []string, out, errOut io.Writer, store Store, now time.Time) int {
	fs := flag.NewFlagSet("secrets", flag.ContinueOnError)
	fs.SetOutput(errOut)
	project := fs.String("project", "", "only sessions whose project directory matches this")
	asJSON := fs.Bool("json", false, "print the findings as JSON")
	all := fs.Bool("all", false, "include expired, public and possible findings")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: verbose secrets [-project NAME] [-all] [-json]")
		fmt.Fprintln(errOut, "\nLists the credentials left in your transcripts, by fingerprint —")
		fmt.Fprintln(errOut, "never the value — so you know what to revoke.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}

	var sessions []*session.Session
	infos := map[string]session.SessionInfo{}
	for _, info := range store.GetSessions() {
		if *project != "" && !matchesProject(info, *project) {
			continue
		}
		if sess := store.GetSession(info.ID); sess != nil {
			sessions = append(sessions, sess)
			infos[info.ID] = info
		}
	}

	findings := session.ScanSecrets(sessions, infos)
	if !*all {
		findings = onlyActionable(findings)
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if findings == nil {
			findings = []session.SecretFinding{}
		}
		if err := enc.Encode(findings); err != nil {
			fmt.Fprintf(errOut, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	report(out, findings, len(sessions), *all, now)
	return 0
}

// onlyActionable keeps what someone has to do something about. The rest is
// shown on request: it is reassurance, and reassurance every time would bury
// the one row that matters.
func onlyActionable(findings []session.SecretFinding) []session.SecretFinding {
	var out []session.SecretFinding
	for _, f := range findings {
		if f.Status == session.SecretRotate {
			out = append(out, f)
		}
	}
	return out
}

func report(out io.Writer, findings []session.SecretFinding, scanned int, all bool, now time.Time) {
	if len(findings) == 0 {
		fmt.Fprintf(out, "Nothing to revoke across %d session%s.\n", scanned, plural(scanned))
		if !all {
			fmt.Fprintln(out, "\nExpired, public and possible findings are hidden — pass -all for those.")
		}
		return
	}

	byStatus := map[session.SecretStatus][]session.SecretFinding{}
	for _, f := range findings {
		byStatus[f.Status] = append(byStatus[f.Status], f)
	}

	headings := []struct {
		status session.SecretStatus
		title  string
		why    string
	}{
		{session.SecretRotate, "REVOKE THESE",
			"long-lived, and nothing expires them on their own"},
		{session.SecretPossible, "WORTH A LOOK",
			"a literal assigned to a secret-looking name — a judgement, not a shape"},
		{session.SecretExpired, "ALREADY DEAD",
			"the token's own claims say so; nothing to do"},
		{session.SecretPublic, "NOT SECRETS",
			"published on purpose, and only look alarming"},
	}

	total := 0
	for _, f := range findings {
		total += f.Count
	}
	fmt.Fprintf(out, "%d distinct credential%s (%d occurrence%s) across %d session%s\n",
		len(findings), plural(len(findings)), total, plural(total), scanned, plural(scanned))

	for _, h := range headings {
		group := byStatus[h.status]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s — %s\n\n", h.title, h.why)
		for _, f := range group {
			printFinding(out, f, now)
		}
	}

	if !all {
		fmt.Fprintln(out, "Expired, public and possible findings are hidden — pass -all for those.")
	}
}

func printFinding(out io.Writer, f session.SecretFinding, now time.Time) {
	fmt.Fprintf(out, "  %-26s %s   %d× in %d session%s, last %s\n",
		f.Kind, f.Fingerprint, f.Count, len(f.Seen), plural(len(f.Seen)),
		ago(f.Last, now))
	if f.Note != "" {
		fmt.Fprintf(out, "      %s\n", f.Note)
	}
	for _, s := range f.Seen {
		fmt.Fprintf(out, "      %s  %-14s %s\n",
			s.When.Format("2006-01-02"), project(s), s.SessionID)
		if t := oneLine(s.Title, 66); t != "" {
			fmt.Fprintf(out, "        %s\n", t)
		}
	}
	if f.Rotate != "" {
		fmt.Fprintf(out, "      revoke: %s\n", f.Rotate)
	}
	fmt.Fprintln(out)
}

func project(s session.Sighting) string {
	if s.Project != "" {
		return s.Project
	}
	return "?"
}

// ago is how long since, in the words a reader uses. A date alone makes the
// reader do the arithmetic that decides whether they care.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return t.Format("2006-01-02")
	case d < 24*time.Hour:
		return "today"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func matchesProject(info session.SessionInfo, want string) bool {
	want = strings.ToLower(want)
	return strings.Contains(strings.ToLower(info.CWD), want) ||
		strings.Contains(strings.ToLower(info.ProjectName), want)
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
