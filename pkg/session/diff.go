package session

import (
	"fmt"
	"strings"
)

// What a session did to one file, as something that can be shown side by side.
//
// The transcript records a unified diff — runs of removals and additions with
// context around them. A two-column view needs those runs paired up, which is
// what SideBySide does. Where there is no diff to pair, because the file was
// new or was written through the shell, the whole content stands as additions:
// that is the honest shape of "there was no before".

// FileChange is one recorded change to a file.
type FileChange struct {
	// EventIndex is where in the session this happened, so a change can be
	// matched to a replay step.
	EventIndex int
	Kind       TouchKind

	// Hunks is the diff Claude Code recorded, when it recorded one.
	Hunks []PatchHunk

	// Content is the whole file, for a change with no diff: a new file has no
	// previous version to compare against, and a shell write records nothing.
	Content string

	// Inferred marks a change deduced from a shell command.
	Inferred bool

	// Outside marks a change the user made themselves, outside Claude. The
	// transcript keeps only a snippet of it.
	Outside bool
}

// HasDiff reports whether this change has a real two-sided diff.
func (c FileChange) HasDiff() bool { return len(c.Hunks) > 0 }

// FileChanges collects every change a session made to one file, in order.
//
// upto bounds it to a point in the session, so a diff opened during a replay
// shows what had happened by then rather than giving away work still to come.
// A negative upto means the whole session.
func FileChanges(sess *Session, path string, upto int) []FileChange {
	if sess == nil || path == "" {
		return nil
	}

	var out []FileChange
	for i := range sess.Events {
		if upto >= 0 && i > upto {
			break
		}
		e := sess.Events[i]

		// A change the user made themselves. The transcript keeps a snippet
		// rather than a diff, and it was being skipped entirely: a file edited
		// outside Claude showed as changed in the tree and empty in the panel.
		if e.Type == EventUserFileEdit {
			if e.FilePath != "" && absolutePath(e.FilePath, sess.Info.CWD) == path {
				out = append(out, FileChange{
					EventIndex: i, Kind: TouchEdit, Content: e.Text, Outside: true,
				})
			}
			continue
		}

		if e.Type != EventToolUse && e.Type != EventToolResult {
			continue
		}

		switch e.ToolName {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
			p, _ := stringInput(e.ToolInput, "file_path", "notebook_path")
			if p == "" && e.Result != nil {
				p = e.Result.FilePath
			}
			if absolutePath(p, sess.Info.CWD) != path {
				continue
			}
			change := FileChange{EventIndex: i, Kind: TouchEdit}
			if e.Result != nil && len(e.Result.StructuredPatch) > 0 {
				change.Hunks = e.Result.StructuredPatch
			} else {
				// No diff: a brand-new file. The content is the change.
				change.Kind = TouchCreate
				change.Content, _ = stringInput(e.ToolInput, "content")
			}
			out = append(out, change)

		case "Bash", "BashOutput":
			cmd, ok := stringInput(e.ToolInput, "command")
			if !ok {
				continue
			}
			// A removal, which records nothing at all: there is no content to
			// show and never was. Reported so the panel can say that instead
			// of looking as though the file was never touched.
			for _, removed := range shellRemovals(cmd) {
				if absolutePath(removed, sess.Info.CWD) == path {
					out = append(out, FileChange{
						EventIndex: i, Kind: TouchDelete, Inferred: true,
					})
				}
			}

			target, body := shellHeredoc(cmd)
			if body == "" || absolutePath(target, sess.Info.CWD) != path {
				continue
			}
			out = append(out, FileChange{
				EventIndex: i, Kind: TouchWrite, Content: body, Inferred: true,
			})
		}
	}
	return out
}

// DiffRowKind says what a side-by-side row represents.
type DiffRowKind int

const (
	// DiffContext is a line present on both sides, unchanged.
	DiffContext DiffRowKind = iota
	// DiffChange is a line replaced: a removal paired with an addition.
	DiffChange
	// DiffRemoved is a line taken away with nothing in its place.
	DiffRemoved
	// DiffAdded is a line put in with nothing removed.
	DiffAdded
	// DiffHeader is a hunk boundary.
	DiffHeader
	// DiffNote is prose explaining why there is nothing to show — a deletion
	// records no content, and a change made outside Claude records a snippet
	// at most.
	DiffNote
)

// DiffRow is one row of a two-column diff. Either side may be absent.
type DiffRow struct {
	Kind DiffRowKind

	Left    string
	LeftNo  int // line number in the old file, 0 when there is no left side
	Right   string
	RightNo int // line number in the new file, 0 when there is no right side

	// Header is the text of a hunk boundary row.
	Header string
}

// SideBySide pairs a unified diff's removals and additions into rows.
//
// Within a run, the first removal lines up with the first addition and so on —
// which is what a reader is looking for, since an edit usually rewrites a line
// rather than deleting one and adding an unrelated other. A run with more of
// one than the other leaves the short side blank for the remainder.
func SideBySide(hunks []PatchHunk) []DiffRow {
	var rows []DiffRow

	for _, h := range hunks {
		rows = append(rows, DiffRow{Kind: DiffHeader, Header: hunkHeader(h)})

		oldNo, newNo := h.OldStart, h.NewStart
		var removed, added []string

		// flush pairs up whatever run has accumulated.
		flush := func() {
			for i := 0; i < len(removed) || i < len(added); i++ {
				var row DiffRow
				switch {
				case i < len(removed) && i < len(added):
					row = DiffRow{Kind: DiffChange,
						Left: removed[i], LeftNo: oldNo, Right: added[i], RightNo: newNo}
					oldNo++
					newNo++
				case i < len(removed):
					row = DiffRow{Kind: DiffRemoved, Left: removed[i], LeftNo: oldNo}
					oldNo++
				default:
					row = DiffRow{Kind: DiffAdded, Right: added[i], RightNo: newNo}
					newNo++
				}
				rows = append(rows, row)
			}
			removed, added = nil, nil
		}

		for _, line := range h.Lines {
			switch {
			case strings.HasPrefix(line, "-"):
				removed = append(removed, line[1:])
			case strings.HasPrefix(line, "+"):
				added = append(added, line[1:])
			default:
				flush()
				text := line
				if text != "" {
					text = text[1:]
				}
				rows = append(rows, DiffRow{Kind: DiffContext,
					Left: text, LeftNo: oldNo, Right: text, RightNo: newNo})
				oldNo++
				newNo++
			}
		}
		flush()
	}
	return rows
}

// ContentRows presents a whole file as additions, for a change with no diff.
func ContentRows(content string) []DiffRow {
	if content == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	rows := make([]DiffRow, 0, len(lines))
	for i, l := range lines {
		rows = append(rows, DiffRow{Kind: DiffAdded, Right: l, RightNo: i + 1})
	}
	return rows
}

func hunkHeader(h PatchHunk) string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
}
