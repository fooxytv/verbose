package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fooxytv/verbose/pkg/session"
)

// Where the work happened: the directory and the branch.
//
// Both are in every transcript and neither was on screen while reading one.
// The project column is a short name, the tree header cut the path to about
// four characters in a split pane, and the branch appeared only in the summary
// view. Reading a replay you could not tell which checkout, or which branch,
// you were looking at.

// branchMark precedes the branch name.
//
// U+2387 is width 1 and NOT ambiguous-width, which makes it a safer choice than
// ●, │ and └ — all already on screen here, all ambiguous. visibleLen counts
// runes rather than display columns, so an ambiguous glyph is a layout bug
// waiting for the wrong terminal.
const branchMark = "⎇"

// contextLine is the directory and branch of a session, fitted to width.
//
// The path gives up its middle rather than its end: the last component is the
// project and the first is where the checkout lives, and those are what tell
// two clones of the same repository apart. Cutting from the right loses the
// name and leaves "/Users/simonwalker/workspa…", which identifies nothing.
func contextLine(info session.SessionInfo, width int) string {
	if width < 12 {
		return ""
	}

	full := homePath(info.CWD)
	branch := branchLabel(info.GitBranch)

	// Nothing is shortened while everything fits. Capping unconditionally
	// trimmed a 47-character branch on a 200-column terminal with room to
	// spare.
	whole := len([]rune(full))
	if branch != "" {
		whole += visibleLen(branch) + 2
	}
	if whole <= width {
		line := dimStyle.Render(full)
		if branch != "" {
			line += "  " + toolUseStyle.Render(branch)
		}
		return line
	}

	// It does not fit. The branch gives up its front first, to a third of the
	// line: real names reach 47 characters, and an unbounded one pushed the
	// row past the pane however short the path was.
	branch = keepTail(branch, min(width, max(10, width/3)))

	// minPath is the least that still says anything: below it the branch is
	// dropped and the path takes the whole line. A floor applied to the path
	// WITHOUT dropping the branch is what overflowed — both kept their minimum
	// and the sum exceeded the pane.
	const minPath = 8
	budget := width
	if branch != "" {
		budget -= visibleLen(branch) + 2
		if budget < minPath {
			branch, budget = "", width
		}
	}

	path := shortenMiddle(full, budget)
	if path == "" && branch == "" {
		return ""
	}

	line := dimStyle.Render(path)
	if branch != "" {
		line += "  " + toolUseStyle.Render(branch)
	}
	return line
}

// branchLabel is how a branch is shown, or "" when the transcript has none.
//
// 8 of 37 sessions on one machine recorded no branch at all, and 4 recorded
// "HEAD" — a detached checkout, which is worth saying plainly rather than
// printing a word that looks like a branch name.
func branchLabel(branch string) string {
	switch branch {
	case "":
		return ""
	case "HEAD":
		return branchMark + " detached"
	}
	return branchMark + " " + branch
}

// homePath replaces the home directory with ~, which is both shorter and how
// everyone writes the path themselves.
func homePath(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return p
}

// shortenMiddle fits a path into width by dropping whole segments from the
// middle, and only then trimming the last one.
//
// Segments go from the middle outwards so the two ends survive longest. When
// even first + … + last will not fit, the last segment keeps its TAIL:
// "…matrix-poc" still names the project, where "gh-spec-kit-te…" does not.
func shortenMiddle(path string, width int) string {
	if path == "" || len([]rune(path)) <= width {
		return path
	}

	sep := string(filepath.Separator)
	segs := strings.Split(path, sep)

	// Drop from just left of the last segment, working backwards, so the root
	// and the project name are the last things to go.
	for len(segs) > 2 {
		cut := len(segs) - 2
		segs = append(segs[:cut], segs[cut+1:]...)
		candidate := strings.Join(segs[:len(segs)-1], sep) + sep + "…" + sep + segs[len(segs)-1]
		if len([]rune(candidate)) <= width {
			return candidate
		}
	}

	// Nothing left to drop: keep the end of the final segment.
	last := segs[len(segs)-1]
	if len([]rune(last))+2 <= width {
		return "…" + sep + last
	}
	return keepTail(last, width)
}

// keepTail trims the FRONT of s to fit width, marking the cut with a leading
// ellipsis.
//
// The tail is the identifying half of both things this line shows: a path's
// last segment is the project, and a branch's last segment is what the branch
// is for — "…dev-mdevops-pool" says more than "feature/allow-jf…". An empty or
// already-short string is returned untouched, so a caller can run it over a
// branch that may not exist.
func keepTail(s string, width int) string {
	r := []rune(s)
	if s == "" || width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(width-1):])
}
