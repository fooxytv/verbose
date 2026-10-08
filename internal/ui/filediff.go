package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fooxytv/verbose/pkg/session"
)

// The diff panel: what the session did to one file, opened from the tree.
//
// It takes the place of the replay in the right-hand pane rather than floating
// over it. A terminal has no z-order, so "over" would mean compositing two
// frames by hand — and the layout arithmetic that already caused a day of
// wrapped-row bugs is not worth doubling for an effect nobody can see anyway.
// Opening it pauses playback, because reading a diff and watching code stream
// are not things anyone does at once.

// diffSideBySideMin is the pane width below which two columns stop being
// readable and the panel falls back to a single unified column.
const diffSideBySideMin = 86

// diffLineNoWidth is the gutter for a line number.
const diffLineNoWidth = 4

// fileDiffView is everything the diff renderer needs.
type fileDiffView struct {
	path    string
	cwd     string
	changes []session.FileChange
	rows    []session.DiffRow

	scroll int
	// unified is set when the pane is too narrow for two columns.
	unified bool
	// later is how many changes to this file happen after the point the replay
	// has reached, which is why the panel can be empty for a file that is
	// plainly changed by the end.
	later int

	width, height int
}

// diffRows turns a file's changes into rows to draw, in order, with a heading
// before each change so several edits to one file stay distinguishable.
func diffRows(changes []session.FileChange, width int) []session.DiffRow {
	var rows []session.DiffRow
	for i, c := range changes {
		rows = append(rows, session.DiffRow{
			Kind:   session.DiffHeader,
			Header: fmt.Sprintf("change %d of %d · %s", i+1, len(changes), c.Kind),
		})
		// A note is prose and has to wrap: truncated at the pane edge it stops
		// mid-sentence, which is worse than not explaining at all.
		for _, line := range wrapProse(changeNote(c), max(10, width-4), "") {
			if line == "" {
				continue
			}
			rows = append(rows, session.DiffRow{Kind: session.DiffNote, Header: line})
		}

		if c.HasDiff() {
			rows = append(rows, session.SideBySide(c.Hunks)...)
			continue
		}
		rows = append(rows, session.ContentRows(c.Content)...)
	}
	return rows
}

// changeNote explains a change that cannot be shown as a diff, so the panel
// says why rather than appearing to have found nothing.
func changeNote(c session.FileChange) string {
	switch {
	case c.Kind == session.TouchDelete:
		return "Removed by a shell command. Claude Code has no delete tool, so " +
			"this is inferred from the command line — and the contents were " +
			"never recorded, so there is nothing to show."
	case c.Outside:
		if strings.TrimSpace(c.Content) == "" {
			return "You changed this file outside Claude. The transcript notes " +
				"that it happened but keeps none of the content."
		}
		return "You changed this file outside Claude. The transcript keeps only " +
			"the snippet below, not a diff."
	case c.Inferred:
		return "Written through the shell, so no diff was recorded. The whole " +
			"body it was given follows."
	case !c.HasDiff() && c.Content != "":
		return "A new file: there is no previous version to compare against, " +
			"so all of it is an addition."
	case !c.HasDiff():
		return "Claude Code recorded neither a diff nor any content for this " +
			"change."
	}
	return ""
}

// renderFileDiff draws the panel.
func renderFileDiff(v fileDiffView) string {
	var b strings.Builder

	name := session.ShortPath(v.path, v.cwd)
	head := titleStyle.Render("Diff") + "  " +
		toolUseStyle.Render(truncate(name, max(10, v.width-30)))
	b.WriteString(clampWidth(head, v.width) + "\n")
	b.WriteString(clampWidth(diffSummary(v), v.width) + "\n\n")

	if len(v.rows) == 0 {
		// "Nothing recorded" is true but unhelpful when the reason is simply
		// that the replay has not reached the change yet. The panel is bounded
		// to where the replay is, exactly as the tree's colours are, so say
		// which of the two it is.
		if v.later > 0 {
			b.WriteString(dimStyle.Render("  Not changed yet at this point in the session.") + "\n\n")
			b.WriteString(mutedStyle.Render(fmt.Sprintf(
				"  %d change(s) to this file come later. Play on, or press G in the\n"+
					"  replay to jump to the end, and open this again.", v.later)))
			return b.String()
		}
		b.WriteString(dimStyle.Render("  No diff recorded for this file.") + "\n\n")
		b.WriteString(mutedStyle.Render("  A session can touch a file without leaving one: a shell\n" +
			"  command that edits in place records only the command, and\n" +
			"  reading a file records no change at all."))
		return b.String()
	}

	visible := max(1, v.height-5)
	scroll := clampInt(v.scroll, 0, max(0, len(v.rows)-visible))
	end := min(len(v.rows), scroll+visible)

	for i := scroll; i < end; i++ {
		b.WriteString(clampWidth(renderDiffRow(v, v.rows[i]), v.width) + "\n")
	}
	if end < len(v.rows) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … %d more lines", len(v.rows)-end)))
	}
	return b.String()
}

// diffSummary totals the change across every recorded edit to the file.
func diffSummary(v fileDiffView) string {
	added, removed := 0, 0
	for _, c := range v.changes {
		if c.HasDiff() {
			for _, h := range c.Hunks {
				for _, l := range h.Lines {
					switch {
					case strings.HasPrefix(l, "+"):
						added++
					case strings.HasPrefix(l, "-"):
						removed++
					}
				}
			}
			continue
		}
		added += len(session.ContentRows(c.Content))
	}

	parts := []string{
		diffAddStyle.Render(fmt.Sprintf("+%d", added)),
		diffRemoveStyle.Render(fmt.Sprintf("-%d", removed)),
		mutedStyle.Render(fmt.Sprintf("across %d change(s)", len(v.changes))),
	}
	if v.unified {
		parts = append(parts, mutedStyle.Render("· unified (pane too narrow to split)"))
	}
	return "  " + strings.Join(parts, " ")
}

// renderDiffRow draws one row, two columns wide or one.
func renderDiffRow(v fileDiffView, row session.DiffRow) string {
	if row.Kind == session.DiffHeader {
		return "  " + systemStyle.Render(truncateRunes(row.Header, max(4, v.width-4)))
	}
	if row.Kind == session.DiffNote {
		return "  " + mutedStyle.Render(truncateRunes(row.Header, max(4, v.width-4)))
	}
	if v.unified {
		return renderUnifiedRow(v, row)
	}

	// Two columns, each with its own line-number gutter, split down the middle.
	col := (v.width - 3) / 2
	left := diffCell(row.LeftNo, row.Left, col, leftStyleFor(row.Kind))
	right := diffCell(row.RightNo, row.Right, col, rightStyleFor(row.Kind))
	return left + mutedStyle.Render(" │ ") + right
}

// renderUnifiedRow falls back to one column: a removal then an addition, the
// way a patch reads.
func renderUnifiedRow(v fileDiffView, row session.DiffRow) string {
	switch row.Kind {
	case session.DiffContext:
		return diffCell(row.LeftNo, " "+row.Left, v.width, diffContextStyle)
	case session.DiffAdded, session.DiffChange:
		if row.Kind == session.DiffChange {
			// One row cannot hold both halves of a replacement; show the new
			// line, since that is what the file ended up with.
			return diffCell(row.RightNo, "+"+row.Right, v.width, diffAddStyle)
		}
		return diffCell(row.RightNo, "+"+row.Right, v.width, diffAddStyle)
	case session.DiffRemoved:
		return diffCell(row.LeftNo, "-"+row.Left, v.width, diffRemoveStyle)
	}
	return ""
}

// diffCell draws a line number and a line of code in a fixed width.
func diffCell(no int, text string, width int, style lipgloss.Style) string {
	gutter := strings.Repeat(" ", diffLineNoWidth)
	if no > 0 {
		gutter = fmt.Sprintf("%*d", diffLineNoWidth, no)
	}

	body := max(1, width-diffLineNoWidth-1)
	text = truncateRunes(expandTabs(text), body)

	rendered := mutedStyle.Render(gutter) + " " + style.Render(text)
	// Pad to the column's full width so the divider and the right-hand column
	// line up on every row.
	if pad := width - diffLineNoWidth - 1 - visibleLen(style.Render(text)); pad > 0 {
		rendered += strings.Repeat(" ", pad)
	}
	return rendered
}

func leftStyleFor(kind session.DiffRowKind) lipgloss.Style {
	switch kind {
	case session.DiffRemoved, session.DiffChange:
		return diffRemoveStyle
	case session.DiffAdded:
		return mutedStyle
	}
	return diffContextStyle
}

func rightStyleFor(kind session.DiffRowKind) lipgloss.Style {
	switch kind {
	case session.DiffAdded, session.DiffChange:
		return diffAddStyle
	case session.DiffRemoved:
		return mutedStyle
	}
	return diffContextStyle
}
