package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

func TestHomePathUsesTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	in := filepath.Join(home, "workspace", "proj")
	if got := homePath(in); got != filepath.Join("~", "workspace", "proj") {
		t.Errorf("homePath(%q) = %q", in, got)
	}
	if got := homePath(home); got != "~" {
		t.Errorf("homePath(home) = %q, want ~", got)
	}
	// A path that merely starts with the same letters is not inside home.
	if got := homePath(home + "-other/x"); got != home+"-other/x" {
		t.Errorf("homePath of a sibling directory was rewritten: %q", got)
	}
	if got := homePath(""); got != "" {
		t.Errorf("homePath(\"\") = %q", got)
	}
}

// The end of a path is what names the project, so the middle is what gives way.
// Cutting from the right leaves "~/workspace/home-pro…", which identifies
// nothing and is the same for every checkout under that directory.
func TestShortenMiddleKeepsBothEnds(t *testing.T) {
	p := filepath.Join("~", "workspace", "valorem", "clients", "gh-spec-kit-poc")

	for _, width := range []int{len([]rune(p)), 34, 26, 18} {
		got := shortenMiddle(p, width)
		if n := len([]rune(got)); n > width {
			t.Errorf("width %d: %q is %d runes", width, got, n)
		}
		if !strings.HasSuffix(got, "gh-spec-kit-poc") && !strings.Contains(got, "poc") {
			t.Errorf("width %d: %q lost the project name", width, got)
		}
	}

	// Untouched when it already fits.
	if got := shortenMiddle(p, 200); got != p {
		t.Errorf("a path that fits was changed: %q", got)
	}
}

// When even the first segment has to go, the LAST segment keeps its tail.
func TestShortenMiddleKeepsTheTailOfTheLastSegment(t *testing.T) {
	p := filepath.Join("~", "a", "team-skills-matrix-poc")
	got := shortenMiddle(p, 12)
	if n := len([]rune(got)); n > 12 {
		t.Fatalf("%q is %d runes, want <= 12", got, n)
	}
	if !strings.HasSuffix(got, "poc") {
		t.Errorf("%q cut the end off the project name", got)
	}
}

func TestBranchLabelSaysWhatItMeans(t *testing.T) {
	if got := branchLabel(""); got != "" {
		t.Errorf("no branch should render nothing, got %q", got)
	}
	if got := branchLabel("HEAD"); !strings.Contains(got, "detached") {
		t.Errorf("HEAD = %q, want it to say detached", got)
	}
	if got := branchLabel("main"); !strings.HasSuffix(got, "main") {
		t.Errorf("main = %q", got)
	}
}

// The line is drawn into a pane, so it must never exceed the width it is given
// — a row wider than the frame wraps and strands rows on screen.
func TestContextLineFitsEveryWidth(t *testing.T) {
	info := session.SessionInfo{
		CWD:       "/Users/someone/workspace/valorem/gh-spec-kit-team-skills-matrix-poc",
		GitBranch: "feature/allow-jfrog-artifactory-dev-mdevops-pool",
	}
	for _, width := range []int{12, 20, 40, 60, 80, 120, 200} {
		got := contextLine(info, width)
		if n := terminalColumns(stripAnsi(got)); n > width {
			t.Errorf("width %d: line is %d columns: %q", width, n, stripAnsi(got))
		}
	}
	// Too narrow to say anything useful.
	if got := contextLine(info, 6); got != "" {
		t.Errorf("at width 6 the line should be dropped, got %q", got)
	}
}

func TestContextLineOmitsAMissingBranch(t *testing.T) {
	info := session.SessionInfo{CWD: "/tmp/proj"}
	got := stripAnsi(contextLine(info, 60))
	if strings.Contains(got, branchMark) {
		t.Errorf("a session with no branch showed a branch marker: %q", got)
	}
	if !strings.Contains(got, "proj") {
		t.Errorf("the path is missing: %q", got)
	}
}

// A visual check against the real shapes of path and branch seen on disk, so a
// change in the shortening is obvious in the diff rather than only in a width
// assertion.
func TestContextLineRealShapes(t *testing.T) {
	cases := []struct{ cwd, branch string }{
		{"/Users/someone/workspace/valorem/gh-spec-kit-team-skills-matrix-poc", "main"},
		{"/Users/someone/workspace/valorem/gh-spec-kit-team-skills-matrix-poc",
			"feature/allow-jfrog-artifactory-dev-mdevops-pool"},
		{"/Users/someone/workspace/home-projects/verbose", ""},
		{"/Users/someone/workspace/home-projects/verbose", "HEAD"},
	}
	for _, c := range cases {
		info := session.SessionInfo{CWD: c.cwd, GitBranch: c.branch}
		t.Logf("cwd=%s branch=%q", c.cwd, c.branch)
		for _, w := range []int{100, 60, 40, 24, 14} {
			line := stripAnsi(contextLine(info, w))
			if n := terminalColumns(line); n > w {
				t.Errorf("  width %3d: %d columns — too wide", w, n)
			}
			t.Logf("  %3d |%s|", w, line)
		}
	}
}

// treeVisibleRows must agree with renderTree exactly.
//
// It decides where the follow scrolls to, and nothing checked it: adding the
// context row and leaving the count at height-5 drew one row more than the
// pane held, and every existing test still passed.
//
// The expectation is derived from the CHROME, never from treeVisibleRows. A
// first version of this test compared the rows drawn against that function,
// which is what the renderer already uses — so the two moved together and the
// test passed at height-5 and height-7 alike.
//
// Model.View gives renderTree the full terminal height and then pads to
// height-1, keeping the last row for the footer. renderTree spends four rows
// on chrome — title, context, legend, blank — and one more on the "… N more"
// note when the rows do not all fit.
func TestTreeVisibleRowsAgreesWithTheRenderer(t *testing.T) {
	m := treeModel(t)
	rows := m.treeRows()
	if len(rows) < 6 {
		t.Fatalf("fixture tree has %d rows, need enough to overflow a short pane", len(rows))
	}

	const chrome = 4

	// Short heights matter most: the pane must be SMALLER than the row list
	// for the "… N more" note to appear, and that note is the row the
	// over-draw case spends. At every height tried first the fixture's six
	// rows fitted whole, so the note never appeared and height-5 passed.
	for _, height := range []int{9, 10, 11, 12, 16, 30} {
		v := m.treeViewState(100, height)
		lines := strings.Split(strings.TrimRight(renderTree(v), "\n"), "\n")

		note := 0
		if strings.Contains(lines[len(lines)-1], "more") {
			note = 1
		}
		drawn := len(lines) - chrome - note

		// The footer owns the last row of the terminal.
		if budget := height - 1; len(lines) > budget {
			t.Errorf("height %d: renderTree drew %d lines, the frame allows %d",
				height, len(lines), budget)
		}
		// And the space that is left must actually be used.
		if want := min(len(rows), height-1-chrome-note); drawn != want {
			t.Errorf("height %d: drew %d tree rows, the pane fits %d",
				height, drawn, want)
		}
	}
}

// A look at the real thing: the first few lines of a replay and a tree, drawn
// from whatever sessions are on this machine. Skips when there are none.
func TestContextLineOnRealSessions(t *testing.T) {
	store, err := session.NewStore()
	if err != nil {
		t.Skip("no store")
	}
	defer store.Close()
	if err := store.Scan(); err != nil {
		t.Skip("no transcripts")
	}
	infos := store.GetSessions()
	if len(infos) == 0 {
		t.Skip("no sessions on this machine")
	}
	// Every session, not just a sample: a real path can carry characters no
	// synthetic fixture thinks of, and the width rule has to hold for all of
	// them.
	for _, info := range infos {
		for _, w := range []int{14, 24, 44, 70, 120, 200} {
			line := stripAnsi(contextLine(info, w))
			if n := terminalColumns(line); n > w {
				t.Errorf("width %d: %d columns for %q", w, n, info.CWD)
			}
		}
	}

	// A sample rendered for the record, so a change in the shortening shows up
	// in the test output rather than only on screen.
	for _, info := range infos[:min(3, len(infos))] {
		for _, w := range []int{120, 70, 44} {
			t.Logf("%3d |%s|", w, stripAnsi(contextLine(info, w)))
		}
		t.Log("")
	}
}
