// Package find answers "where did I do that" from the command line.
//
// The TUI can already search a history and a replay, but both require knowing
// which session to open and then reading the answer off a screen. A question
// like "did I ever get that container to export the table" arrives at a
// prompt, not in an application — and the answer is one line that can be piped
// into the next thing.
package find

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fooxytv/verbose/pkg/session"
)

// Hit is one matching step, with enough to go back to it.
type Hit struct {
	SessionID string `json:"session"`
	Project   string `json:"project"`
	Source    string `json:"source"`
	When      string `json:"when"`
	Step      int    `json:"step"`  // numbered from 1, as the replay shows it
	Steps     int    `json:"steps"` // how many the session has
	Title     string `json:"title"`
	Kind      string `json:"kind"` // command, output, code, file, prompt…
	Line      string `json:"line"` // the line the needle was on
}

// Store is what find needs of a session store, so a test can supply its own.
type Store interface {
	GetSessions() []session.SessionInfo
	GetSession(id string) *session.Session
}

// Run executes `verbose find`. It returns a process exit code: 1 on a usage
// error, and 0 whether or not anything matched — nothing matching is an
// answer, not a failure.
func Run(args []string, out, errOut io.Writer, store Store) int {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.SetOutput(errOut)
	project := fs.String("project", "", "only sessions whose project directory matches this")
	limit := fs.Int("limit", 40, "stop after this many hits (0 = no limit)")
	asJSON := fs.Bool("json", false, "print the hits as JSON")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: verbose find [-project NAME] [-limit N] [-json] <text>")
		fmt.Fprintln(errOut, "\nSearches every session's steps: what was run, what it printed,")
		fmt.Fprintln(errOut, "the code written and the files touched.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}

	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fs.Usage()
		return 1
	}

	hits, scanned := search(store, query, *project, *limit)

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if hits == nil {
			hits = []Hit{}
		}
		if err := enc.Encode(hits); err != nil {
			fmt.Fprintf(errOut, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	if len(hits) == 0 {
		// Say what was looked through. "No results" over an empty store and
		// "no results" over nine hundred sessions are different answers.
		fmt.Fprintf(out, "Nothing in %d session%s matches %q\n",
			scanned, plural(scanned), query)
		return 0
	}
	report(out, hits, query, scanned, *limit)
	return 0
}

// search walks every session newest-first and collects the matching steps.
func search(store Store, query, project string, limit int) (hits []Hit, scanned int) {
	needle := strings.ToLower(query)

	for _, info := range store.GetSessions() {
		if project != "" && !matchesProject(info, project) {
			continue
		}
		sess := store.GetSession(info.ID)
		if sess == nil {
			continue
		}
		scanned++

		steps := session.BuildReplay(sess)
		for _, i := range session.FindSteps(sess, steps, query) {
			kind, line := session.StepMatchLine(steps[i], sess, needle)
			hits = append(hits, Hit{
				SessionID: info.ID,
				Project:   projectName(info),
				Source:    sourceName(info.Source),
				When:      info.LastUpdate.Format("2006-01-02 15:04"),
				Step:      i + 1,
				Steps:     len(steps),
				Title:     oneLine(steps[i].Title, 90),
				Kind:      kind,
				Line:      oneLine(line, 110),
			})
			if limit > 0 && len(hits) >= limit {
				return hits, scanned
			}
		}
	}
	return hits, scanned
}

// report prints the hits grouped by session, because several hits in one
// session are one place to go back to, not several.
func report(out io.Writer, hits []Hit, query string, scanned, limit int) {
	bySession := map[string][]Hit{}
	var order []string
	for _, h := range hits {
		if _, seen := bySession[h.SessionID]; !seen {
			order = append(order, h.SessionID)
		}
		bySession[h.SessionID] = append(bySession[h.SessionID], h)
	}

	fmt.Fprintf(out, "%d hit%s for %q in %d of %d session%s\n\n",
		len(hits), plural(len(hits)), query, len(order), scanned, plural(scanned))

	for _, id := range order {
		group := bySession[id]
		head := group[0]
		fmt.Fprintf(out, "%s  %s  %s  %s\n", head.When, head.Source, head.Project, id)
		for _, h := range group {
			fmt.Fprintf(out, "    step %d/%d  %s\n", h.Step, h.Steps, h.Title)
			if h.Line != "" && h.Kind != "headline" {
				fmt.Fprintf(out, "        %s: %s\n", h.Kind, h.Line)
			}
		}
		fmt.Fprintln(out)
	}

	// Say so rather than quietly stopping: a truncated answer that looks
	// complete is worse than no answer.
	if limit > 0 && len(hits) >= limit {
		fmt.Fprintf(out, "stopped at %d — pass -limit 0 for all of them\n", limit)
	}
	fmt.Fprintf(out, "replay one: verbose, open the session, R, then / and the same text\n")
}

func matchesProject(info session.SessionInfo, want string) bool {
	want = strings.ToLower(want)
	return strings.Contains(strings.ToLower(info.CWD), want) ||
		strings.Contains(strings.ToLower(info.ProjectName), want)
}

func projectName(info session.SessionInfo) string {
	if info.CWD != "" {
		return filepath.Base(info.CWD)
	}
	if info.ProjectName != "" {
		return info.ProjectName
	}
	return "?"
}

// sourceName fills in the agent that wrote a session. Claude Code leaves the
// field empty, which would otherwise print as a hole in the row.
func sourceName(src string) string {
	if src == "" {
		return "claude"
	}
	return src
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
