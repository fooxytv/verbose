// Package relink is the `verbose relink` command: run in a folder's new
// location, it finds the sessions whose recorded folder no longer exists, picks
// out the one this most likely was, and links the old path to here.
package relink

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// Run executes the command and returns its exit code. infos are the sessions
// the store found, with any existing links already applied, and here is the
// folder the command was run in.
func Run(args []string, in io.Reader, out, errOut io.Writer, infos []session.SessionInfo, here string) int {
	fs := flag.NewFlagSet("relink", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: verbose relink [-y] [old-folder]")
		fmt.Fprintln(errOut, "       verbose relink -list")
		fmt.Fprintln(errOut, "       verbose relink -remove <old-folder>")
		fmt.Fprintln(errOut)
		fmt.Fprintln(errOut, "Run in a folder's new location after renaming or moving it. Lists the")
		fmt.Fprintln(errOut, "folders sessions ran in that no longer exist, best match first, and links")
		fmt.Fprintln(errOut, "the one you pick to here. old-folder may be a name or a full path.")
		fmt.Fprintln(errOut)
		fs.PrintDefaults()
	}
	list := fs.Bool("list", false, "show the folders already relinked")
	remove := fs.String("remove", "", "drop the link for this old folder")
	yes := fs.Bool("y", false, "take the best match without asking")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	links, err := session.LoadRelinks()
	if err != nil {
		fmt.Fprintf(errOut, "Error reading relinks: %v\n", err)
		return 1
	}

	switch {
	case *list:
		if len(links) == 0 {
			fmt.Fprintln(out, "No folders relinked.")
		}
		for _, l := range links {
			fmt.Fprintf(out, "%s → %s\n", l.From, l.To)
		}
		return 0

	case *remove != "":
		kept := links.Remove(*remove)
		if len(kept) == len(links) {
			fmt.Fprintf(errOut, "No link for %s. `verbose relink -list` shows them.\n", *remove)
			return 1
		}
		if err := kept.Save(); err != nil {
			fmt.Fprintf(errOut, "Error saving relinks: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Removed the link for %s.\n", *remove)
		return 0
	}

	cands := session.MissingFolders(infos, here)
	if len(cands) == 0 {
		fmt.Fprintln(out, "Every session's folder still exists; there is nothing to relink.")
		return 0
	}
	if q := fs.Arg(0); q != "" {
		cands = matching(cands, q)
		if len(cands) == 0 {
			fmt.Fprintf(errOut, "No missing folder matches %q. Run `verbose relink` to see them all.\n", q)
			return 1
		}
	}

	pick, ok := choose(cands, here, *yes, bufio.NewReader(in), out, errOut)
	if !ok {
		return 1
	}

	if err := links.Add(pick.Path, here).Save(); err != nil {
		fmt.Fprintf(errOut, "Error saving relinks: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Linked %s → %s\n", pick.Path, here)
	fmt.Fprintf(out, "%s now open here. Restart verbose if it is already running.\n", plural(pick.Sessions, "session"))
	return 0
}

// choose asks which folder to link, or confirms the only one.
func choose(cands []session.MissingFolder, here string, yes bool, in *bufio.Reader, out, errOut io.Writer) (session.MissingFolder, bool) {
	best := cands[0]
	// A default is only offered when this folder backs it up: a renamed folder
	// keeps most of its files, while one shared README is a coincidence.
	// Otherwise Enter would link a guess.
	hasDefault := (best.Found > 0 && best.Found*2 >= best.Files) || len(cands) == 1

	if yes {
		if !hasDefault {
			fmt.Fprintln(errOut, "No missing folder's files are here, so there is no best match to take. Name one, or run without -y.")
			return session.MissingFolder{}, false
		}
		return best, true
	}

	if len(cands) == 1 {
		fmt.Fprintf(out, "%s\n  %s\n\n", best.Path, describe(best))
		fmt.Fprintf(out, "Relink it to %s? [Y/n] ", here)
		answer := readLine(in)
		if answer == "" || strings.HasPrefix(strings.ToLower(answer), "y") {
			return best, true
		}
		fmt.Fprintln(out, "Nothing changed.")
		return session.MissingFolder{}, false
	}

	fmt.Fprintln(out, "Folders that no longer exist, best match first:")
	for i, c := range cands {
		fmt.Fprintf(out, "  %d. %s\n     %s\n", i+1, c.Path, describe(c))
	}
	fmt.Fprintln(out)
	if hasDefault {
		fmt.Fprintf(out, "Relink which to %s? [1] ", here)
	} else {
		fmt.Fprintf(out, "Relink which to %s? (number, Enter to cancel) ", here)
	}
	answer := readLine(in)
	if answer == "" && hasDefault {
		return best, true
	}
	n, err := strconv.Atoi(answer)
	if err != nil || n < 1 || n > len(cands) {
		fmt.Fprintln(out, "Nothing changed.")
		return session.MissingFolder{}, false
	}
	return cands[n-1], true
}

// matching keeps the folders a name or path picks out: the whole path, or the
// last segment, compared without regard to case.
func matching(cands []session.MissingFolder, q string) []session.MissingFolder {
	q = strings.TrimRight(q, `/\`)
	var out []session.MissingFolder
	for _, c := range cands {
		if strings.EqualFold(c.Path, filepath.Clean(q)) || strings.EqualFold(filepath.Base(c.Path), q) {
			out = append(out, c)
		}
	}
	return out
}

func describe(c session.MissingFolder) string {
	parts := []string{plural(c.Sessions, "session"), "last active " + ago(time.Since(c.LastActive))}
	if c.Files > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d files found here", c.Found, c.Files))
	} else {
		parts = append(parts, "no files to compare")
	}
	return strings.Join(parts, " · ")
}

func readLine(in *bufio.Reader) string {
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
