package ui

import (
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
	"github.com/mattn/go-runewidth"
)

// mdCorpus is the shapes a real plan is made of, as measured across one
// machine's transcripts: inline code (53% of text blocks), bold (34%),
// headings (17%), fenced code (15%), bullets (11%) and tables (10%).
var mdCorpus = map[string]string{
	"heading and prose": "## What went wrong\n\nI ran the merge and the tag as one chain.\n",
	"bullets":           "- first thing\n- a second thing long enough to need wrapping at any sensible width\n  - nested under it\n",
	"numbered":          "1. bump the Makefile\n2. tag it\n3. check `git show v0.23.0:Makefile` before announcing anything at all\n",
	"table":             "| Step | What it does |\n|---|---|\n| one | first |\n| two | second thing that is a good deal longer than the header |\n",
	"blank header":      "| | |\n|---|---|\n| Bad tag | Deleted, local and remote |\n",
	"fenced code":       "Here:\n\n```go\nfunc main() {\n\tfmt.Println(\"a line long enough to want cutting somewhere\")\n}\n```\n",
	"quote":             "> This is the rule, and it is long enough that it has to wrap somewhere.\n",
	"rule":              "before\n\n---\n\nafter\n",
	"inline":            "The `gh` trap: **it does not persist** between *shells*, see [the docs](https://example.com/a/long/path).\n",
	"long word":         "/Users/someone/workspace/a/deeply/nested/path/that/will/not/fit/anywhere.go\n",
	"shell literals":    "Run `rm *.go *.md`, set _queue_private_dns_zone, and note 2*3*4.\n",
	"unclosed":          "an *unclosed emphasis and a `bare backtick\n",
	"soft wrapped":      "a sentence that the author\nbroke across two source lines\n",
	"empty fence":       "```\n\n```\n",
}

// The bug this file exists for: collapseSpaces joined a whole plan into one
// paragraph, so a heading, the list under it and a table all ran together.
func TestMarkdownKeepsBlocksApart(t *testing.T) {
	src := "## Where it stands\n\nThe tag is deleted.\n\n- first point\n- second point\n"
	lines := renderMarkdown(src, 60, "")

	want := []string{"Where it stands", "The tag is deleted.", "first point", "second point"}
	for _, w := range want {
		found := false
		for _, l := range lines {
			if strings.TrimSpace(stripAnsi(l)) == w || strings.TrimSpace(stripAnsi(l)) == "▪ "+w {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not on a line of its own:\n%s", w, strings.Join(lines, "\n"))
		}
	}
}

// Soft-wrapped source lines are one paragraph, which is the opposite of what
// wrapProse does: it treats every newline as a paragraph break and so turned a
// re-flowed sentence into a column of short lines.
func TestMarkdownJoinsSoftWrappedLines(t *testing.T) {
	lines := renderMarkdown("a sentence that the author\nbroke across two source lines\n", 60, "")
	if len(lines) != 1 {
		t.Fatalf("want one wrapped paragraph, got %d lines: %q", len(lines), lines)
	}
	if got := lines[0]; got != "a sentence that the author broke across two source lines" {
		t.Errorf("paragraph = %q", got)
	}
}

// The width contract. Measured with terminalColumns, which expands tabs: a
// fenced block full of tab-indented code measured 94 columns and drew 179 the
// last time this was got wrong.
func TestMarkdownFitsItsWidth(t *testing.T) {
	for _, prefix := range []string{"", "  "} {
		for _, width := range []int{12, 20, 40, 74, 120} {
			for name, src := range mdCorpus {
				budget := len(prefix) + width
				for i, l := range renderMarkdown(src, width, prefix) {
					if n := terminalColumns(l); n > budget {
						t.Errorf("%s at width %d prefix %q: line %d is %d columns, budget %d\n%q",
							name, width, prefix, i, n, budget, l)
					}
				}
			}
		}
	}
}

// Markers are removed, not shown. A plan full of literal asterisks and
// backticks is the thing a reader notices first.
func TestMarkdownStripsInlineMarkers(t *testing.T) {
	lines := renderMarkdown("The `gh` trap: **it does not persist** between *shells*.", 60, "")
	got := stripAnsi(strings.Join(lines, " "))
	if strings.ContainsAny(got, "`*") {
		t.Errorf("markers survived: %q", got)
	}
	for _, w := range []string{"gh", "it does not persist", "shells"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q was lost: %q", w, got)
		}
	}
}

// Emphasis must not eat code. These three shapes are all over a real plan, and
// treating them as markup would delete characters from a command a reader is
// meant to be able to retype.
func TestMarkdownLeavesShellAndCodeTextAlone(t *testing.T) {
	for _, src := range []string{
		"run rm *.go *.md now",
		"set _queue_private_dns_zone to null",
		"the product 2*3*4 is 24",
		"a glob *.tf and another *.tfvars",
	} {
		got := stripAnsi(strings.Join(renderMarkdown(src, 60, ""), " "))
		if got != src {
			t.Errorf("rewrote shell text\n got %q\nwant %q", got, src)
		}
	}
}

// Real emphasis still works, or the guard above has simply disabled italics.
func TestMarkdownStillReadsRealEmphasis(t *testing.T) {
	got := stripAnsi(strings.Join(renderMarkdown("a *real* emphasis", 60, ""), " "))
	if got != "a real emphasis" {
		t.Errorf("emphasis = %q, want the markers gone", got)
	}
}

// Columns have to line up, and alignment is measured in RUNES: ╌ is three
// bytes to one column, so byte offsets report a shift the terminal never
// draws — the same trap that cut the session list's rows short.
func TestMarkdownTableColumnsAlign(t *testing.T) {
	src := "| Step | What it does |\n|---|---|\n| one | first |\n| twelve | second |\n"
	lines := renderMarkdown(src, 60, "")
	if len(lines) != 4 {
		t.Fatalf("want a header, a divider and two rows, got %d: %q", len(lines), lines)
	}

	// The widest cell in column one is "twelve", so with a two-column gap
	// every second-column cell has to begin at rune 8.
	want := len("twelve") + 2
	for i, cell := range []string{"What it does", "first", "second"} {
		row := []int{0, 2, 3}[i]
		if got := runeIndexOf(stripAnsi(lines[row]), cell); got != want {
			t.Errorf("%q starts at rune %d, want %d\n%s",
				cell, got, want, strings.Join(lines, "\n"))
		}
	}

	if !strings.Contains(stripAnsi(lines[1]), mdRule) {
		t.Errorf("a table with a header should get a divider under it, got %q", lines[1])
	}
}

// A table whose header cells are empty is a real markdown table — the pipes
// are there to align two columns, not to label them — so there is nothing to
// draw in bold and no divider to draw under it.
func TestMarkdownTableSkipsAnEmptyHeader(t *testing.T) {
	lines := renderMarkdown("| | |\n|---|---|\n| Bad tag | Deleted |\n", 60, "")
	for _, l := range lines {
		if strings.Contains(stripAnsi(l), mdRule) {
			t.Errorf("no divider belongs under an empty header:\n%s", strings.Join(lines, "\n"))
		}
		if strings.TrimSpace(stripAnsi(l)) == "" {
			t.Errorf("an empty header row should not be drawn:\n%q", lines)
		}
	}
	if len(lines) != 1 {
		t.Errorf("want just the one data row, got %q", lines)
	}
}

// A wrapped list item hangs under its own text, not back under the marker,
// which is the whole reason the item is a block rather than a paragraph.
func TestMarkdownListTextHangsUnderItself(t *testing.T) {
	lines := renderMarkdown("- a bullet long enough to wrap onto a second line here\n", 30, "")
	if len(lines) < 2 {
		t.Fatalf("want a wrapped item, got %q", lines)
	}
	first, second := stripAnsi(lines[0]), stripAnsi(lines[1])
	if !strings.HasPrefix(first, mdBullets[0]+" ") {
		t.Errorf("first line should carry the marker: %q", first)
	}
	indent := len([]rune(second)) - len([]rune(strings.TrimLeft(second, " ")))
	if indent != len([]rune(mdBullets[0]))+1 {
		t.Errorf("continuation indented %d columns, want %d: %q", indent, len([]rune(mdBullets[0]))+1, second)
	}
}

// A fenced block keeps its code, loses its fence, and carries no tab into the
// output — a tab measured as one column and drawn as four is how a line that
// fits becomes a line that wraps.
func TestMarkdownFenceKeepsTheCodeAndExpandsTabs(t *testing.T) {
	lines := renderMarkdown("```go\nfunc main() {\n\tx := 1\n}\n```\n", 60, "")
	joined := stripAnsi(strings.Join(lines, "\n"))
	if strings.Contains(joined, "```") {
		t.Errorf("the fence itself should not be drawn: %q", joined)
	}
	if !strings.Contains(joined, "func main()") || !strings.Contains(joined, "x := 1") {
		t.Errorf("the code was lost: %q", joined)
	}
	for _, l := range lines {
		if strings.ContainsRune(l, '\t') {
			t.Errorf("a tab reached the output, so its width is a guess: %q", l)
		}
	}
}

// Every glyph this file adds to the screen must be width 1 and NOT
// ambiguous-width. An ambiguous glyph draws in two columns on a terminal
// configured for it, while visibleLen counts it as one — a layout bug waiting
// for the wrong terminal, which is why ●, │ and ─ are not used here.
func TestMarkdownGlyphsAreNotAmbiguousWidth(t *testing.T) {
	glyphs := append([]string{mdRule, mdQuoteBar}, mdBullets...)
	for _, g := range glyphs {
		r := []rune(g)
		if len(r) != 1 {
			t.Errorf("%q should be a single rune", g)
			continue
		}
		if w := runewidth.RuneWidth(r[0]); w != 1 {
			t.Errorf("%q (U+%04X) is %d columns wide, want 1", g, r[0], w)
		}
		if runewidth.IsAmbiguousWidth(r[0]) {
			t.Errorf("%q (U+%04X) is ambiguous-width; pick a glyph that is not", g, r[0])
		}
	}
}

// The replay has to actually use it. A plan step's markdown must come out as
// separate lines in the frame, not as one paragraph.
func TestPlanStepIsFormattedInTheReplay(t *testing.T) {
	sess := &session.Session{
		Info: session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{
			Type: session.EventText,
			Text: "## The plan\n\n- bump the Makefile\n- tag it\n",
		}},
	}
	steps := session.BuildReplay(sess)
	if len(steps) != 1 || steps[0].Kind != session.StepPlan {
		t.Fatalf("want one plan step, got %+v", steps)
	}

	out := stripAnsi(renderReplay(replayView{
		sess: sess, steps: steps, idx: 0, typed: -1,
		delay: replayDefaultDelay, width: 80, height: 24,
	}))
	for _, want := range []string{"The plan", mdBullets[0] + " bump the Makefile", mdBullets[0] + " tag it"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## The plan") {
		t.Error("the heading markers should have been rendered, not printed")
	}
}

// A long plan opens at its TOP. The viewport follows the typing cursor, and a
// prose step has no cursor to follow — before this was guarded, a new plan step
// scrolled straight to its last line, which is the one place a reader is not
// looking.
func TestLongProseStepOpensAtItsTop(t *testing.T) {
	var b strings.Builder
	b.WriteString("ALPHAWORD starts the plan\n\n")
	for i := 0; i < 120; i++ {
		b.WriteString("- a bullet of filler text to push the body past one screen\n")
	}
	b.WriteString("\nOMEGAWORD ends the plan\n")

	sess := &session.Session{
		Info:   session.SessionInfo{Title: "t", CWD: "/repo"},
		Events: []session.Event{{Type: session.EventText, Text: b.String()}},
	}
	steps := session.BuildReplay(sess)

	// typed = 0 is a step that has just started playing.
	out := stripAnsi(renderReplay(replayView{
		sess: sess, steps: steps, idx: 0, typed: 0, playing: true,
		delay: replayDefaultDelay, width: 80, height: 24,
	}))
	if !strings.Contains(out, "ALPHAWORD") {
		t.Error("a plain plan step should open at its first line")
	}
	if strings.Contains(out, "OMEGAWORD") {
		t.Error("the frame scrolled to the end of a step that types nothing")
	}
}

// runeIndexOf is strings.Index in runes, for alignment checks. Byte offsets
// lie about anything non-ASCII: ▪ is three bytes and one column.
func runeIndexOf(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}
