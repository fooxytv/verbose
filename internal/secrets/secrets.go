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
	"sort"
	"strconv"
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
	ignore := fs.String("ignore", "",
		"acknowledge these fingerprints (comma separated) and stop reporting them")
	unignore := fs.String("unignore", "", "report these fingerprints again")
	note := fs.String("note", "", "why, recorded against the fingerprints being ignored")
	listIgnored := fs.Bool("ignored", false, "list what has been acknowledged")
	details := fs.Bool("details", false, "list the sessions each credential appears in")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: verbose secrets [-project NAME] [-all] [-json]")
		fmt.Fprintln(errOut, "       verbose secrets -ignore <fingerprint>[,...] [-note WHY]")
		fmt.Fprintln(errOut, "       verbose secrets -unignore <fingerprint>[,...]")
		fmt.Fprintln(errOut, "       verbose secrets -ignored")
		fmt.Fprintln(errOut, "\nLists the credentials left in your transcripts, by fingerprint —")
		fmt.Fprintln(errOut, "never the value — so you know what to revoke.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}

	ignored, err := session.LoadIgnoredSecrets()
	if err != nil {
		// A broken list must not stop the report: the findings are what
		// matter, and the worst case is seeing one again.
		fmt.Fprintf(errOut, "Warning: could not read the ignore list: %v\n", err)
	}

	if *ignore != "" || *unignore != "" {
		return editIgnored(out, errOut, ignored, *ignore, *unignore, *note, now)
	}
	if *listIgnored {
		return showIgnored(out, ignored)
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
	findings, acknowledged := ignored.Apply(findings)
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

	report(out, findings, len(sessions), *all, *details, acknowledged, now)
	return 0
}

// editIgnored acknowledges or un-acknowledges fingerprints.
func editIgnored(out, errOut io.Writer, ignored session.IgnoredSecrets,
	add, remove, note string, now time.Time) int {
	changed := 0
	for _, fp := range fingerprints(add) {
		ignored.Add(fp, "", note, now)
		fmt.Fprintf(out, "ignoring %s\n", fp)
		changed++
	}
	for _, fp := range fingerprints(remove) {
		if ignored.Remove(fp) {
			fmt.Fprintf(out, "reporting %s again\n", fp)
			changed++
			continue
		}
		fmt.Fprintf(errOut, "%s was not being ignored\n", fp)
	}
	if changed == 0 {
		return 1
	}
	if err := ignored.Save(); err != nil {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	return 0
}

func showIgnored(out io.Writer, ignored session.IgnoredSecrets) int {
	if len(ignored) == 0 {
		fmt.Fprintln(out, "Nothing is being ignored.")
		return 0
	}
	list := make([]session.IgnoredSecret, 0, len(ignored))
	for _, ig := range ignored {
		list = append(list, ig)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].When.Before(list[j].When) })
	fmt.Fprintf(out, "%d acknowledged finding%s\n\n", len(list), plural(len(list)))
	for _, ig := range list {
		fmt.Fprintf(out, "  %s  %s", ig.Fingerprint, ig.When.Format("2006-01-02"))
		if ig.Note != "" {
			fmt.Fprintf(out, "  %s", ig.Note)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "\nverbose secrets -unignore <fingerprint> reports one again.")
	return 0
}

// fingerprints splits a comma-separated list, ignoring the empties a trailing
// comma leaves.
func fingerprints(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if fp := strings.TrimSpace(part); fp != "" {
			out = append(out, fp)
		}
	}
	return out
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

// report prints the findings as a table, then what to do about each kind.
//
// A table because the question being asked of this output is a comparison —
// which of these matters most, which is oldest, which is in work I remember —
// and comparison is what columns are for. The detail each row summarises is
// one -details away.
func report(out io.Writer, findings []session.SecretFinding, scanned int,
	all, details bool, acknowledged int, now time.Time) {
	fmt.Fprintf(out, "%d session%s scanned · %d to deal with", scanned, plural(scanned), len(findings))
	if acknowledged > 0 {
		fmt.Fprintf(out, " · %d acknowledged", acknowledged)
	}
	if !all {
		fmt.Fprint(out, " · expired, public and possible hidden (-all)")
	}
	fmt.Fprintln(out)

	if len(findings) == 0 {
		fmt.Fprintln(out, "\nNothing to deal with.")
		if acknowledged > 0 {
			fmt.Fprintln(out, "verbose secrets -ignored lists what has been acknowledged.")
		}
		return
	}

	fmt.Fprintln(out)
	printTable(out, findings, all, now)

	if details {
		printSightings(out, findings)
	}
	printMitigations(out, findings)

	fmt.Fprintln(out, "\nverbose secrets -details        where each one appears")
	fmt.Fprintln(out, "verbose secrets -ignore <id>    stop reporting one you have dealt with")
}

// printTable lays the findings out in columns sized to their contents.
func printTable(out io.Writer, findings []session.SecretFinding, all bool, now time.Time) {
	head := []string{"KIND", "ID", "WHERE", "SEEN", "LAST", "WHAT TO DO"}
	if all {
		head = []string{"KIND", "ID", "WHERE", "SEEN", "LAST", "STATUS", "WHAT TO DO"}
	}
	rows := [][]string{head}
	for _, f := range findings {
		row := []string{
			f.Kind,
			f.Fingerprint,
			where(f),
			strconv.Itoa(f.Count),
			ago(f.Last, now),
		}
		if all {
			row = append(row, string(f.Status))
		}
		rows = append(rows, append(row, action(f)))
	}
	writeRows(out, rows, map[int]bool{3: true})
}

// where names the projects a credential turned up in, which is what a reader
// recognises — an id is for the machine.
func where(f session.SecretFinding) string {
	seen := map[string]bool{}
	var names []string
	for _, s := range f.Seen {
		if p := project(s); !seen[p] {
			seen[p] = true
			names = append(names, p)
		}
	}
	out := strings.Join(names, ", ")
	if len(f.Seen) > 1 {
		out += fmt.Sprintf(" (%d sessions)", len(f.Seen))
	}
	return oneLine(out, 34)
}

// action is the short form of what to do, for the table. The long form is in
// the mitigations below it.
func action(f session.SecretFinding) string {
	switch f.Status {
	case session.SecretExpired:
		return "nothing — already dead"
	case session.SecretPublic:
		return "nothing — not a secret"
	case session.SecretInert:
		return "nothing — signs nothing"
	case session.SecretPossible:
		return "check whether it is real"
	}
	if f.Rotate != "" {
		return "revoke and re-issue"
	}
	return "identify the issuer, then revoke"
}

// printSightings is where each credential actually appears, for going back to
// it with the replay.
func printSightings(out io.Writer, findings []session.SecretFinding) {
	fmt.Fprintln(out, "\nWHERE THEY APPEAR")
	for _, f := range findings {
		fmt.Fprintf(out, "\n  %s  %s\n", f.Fingerprint, f.Kind)
		if f.Note != "" {
			fmt.Fprintf(out, "      %s\n", f.Note)
		}
		for _, s := range f.Seen {
			fmt.Fprintf(out, "      %s  %-14s %s\n",
				s.When.Format("2006-01-02"), project(s), s.SessionID)
			if t := oneLine(s.Title, 62); t != "" {
				fmt.Fprintf(out, "        %s\n", t)
			}
		}
	}
}

// printMitigations is the half that is not a URL: revoking ends this
// credential, and the credential reached a transcript somehow, so unless that
// changes the next one will too.
//
// Grouped by KIND rather than by finding, because the advice is the same for
// every GitHub token and repeating it four times buries it.
func printMitigations(out io.Writer, findings []session.SecretFinding) {
	type note struct{ rotate, prevent string }
	seen := map[string]note{}
	var order []string
	for _, f := range findings {
		if f.Rotate == "" && f.Prevent == "" {
			continue
		}
		if f.Status != session.SecretRotate && f.Status != session.SecretPossible {
			continue
		}
		if _, ok := seen[f.Kind]; !ok {
			order = append(order, f.Kind)
			seen[f.Kind] = note{f.Rotate, f.Prevent}
		}
	}
	if len(order) == 0 {
		return
	}

	fmt.Fprintln(out, "\nMITIGATIONS")
	for _, kind := range order {
		n := seen[kind]
		fmt.Fprintf(out, "\n  %s\n", kind)
		if n.rotate != "" {
			writeWrapped(out, "      revoke   ", n.rotate)
		}
		if n.prevent != "" {
			writeWrapped(out, "      prevent  ", n.prevent)
		}
	}
	fmt.Fprintln(out, "\nHOW THEY GOT THERE")
	fmt.Fprintln(out)
	writeWrapped(out, "  ",
		"Every one of these reached a transcript because it passed through the "+
			"agent's hands — in a command line, a file it read, or a URL. The "+
			"transcript keeps it in plain text until the agent's own retention "+
			"removes it, and every backup copies it in the meantime. "+
			"`verbose --redact` hides them on screen; only revoking ends them.")
}

// writeWrapped prints a label and wraps the text under it, so a paragraph of
// advice does not run off an 80-column terminal.
func writeWrapped(out io.Writer, label, text string) {
	const width = 72
	indent := strings.Repeat(" ", len(label))
	line := label
	col := len(label)
	for _, word := range strings.Fields(text) {
		if col > len(label) && col+1+len(word) > width {
			fmt.Fprintln(out, line)
			line, col = indent+word, len(indent)+len(word)
			continue
		}
		if col > len(label) {
			line += " "
			col++
		}
		line += word
		col += len(word)
	}
	if strings.TrimSpace(line) != "" {
		fmt.Fprintln(out, line)
	}
}

// writeRows prints a table, sizing each column to its widest cell. Columns
// named in rightAlign are numbers, which read wrong ragged.
func writeRows(out io.Writer, rows [][]string, rightAlign map[int]bool) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len([]rune(cell)) > widths[i] {
				widths[i] = len([]rune(cell))
			}
		}
	}
	for _, row := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for i, cell := range row {
			pad := widths[i] - len([]rune(cell))
			if rightAlign[i] {
				b.WriteString(strings.Repeat(" ", pad) + cell)
			} else {
				b.WriteString(cell)
				if i < len(row)-1 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			}
			if i < len(row)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(out, strings.TrimRight(b.String(), " "))
	}
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
