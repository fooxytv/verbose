package session

import (
	"path/filepath"
	"testing"
)

// A two-column diff needs the unified diff's runs paired up: the first removal
// lines up with the first addition, since an edit usually rewrites a line
// rather than deleting one and adding an unrelated other.
func TestSideBySidePairsRemovalsWithAdditions(t *testing.T) {
	hunks := []PatchHunk{{
		OldStart: 10, OldLines: 4, NewStart: 10, NewLines: 5,
		Lines: []string{
			" context",
			"-old one",
			"-old two",
			"+new one",
			"+new two",
			"+new three",
			" tail",
		},
	}}

	rows := SideBySide(hunks)
	if rows[0].Kind != DiffHeader {
		t.Fatalf("first row = %v, want a hunk header", rows[0].Kind)
	}

	want := []struct {
		kind            DiffRowKind
		left, right     string
		leftNo, rightNo int
	}{
		{DiffContext, "context", "context", 10, 10},
		{DiffChange, "old one", "new one", 11, 11},
		{DiffChange, "old two", "new two", 12, 12},
		{DiffAdded, "", "new three", 0, 13},
		{DiffContext, "tail", "tail", 13, 14},
	}
	body := rows[1:]
	if len(body) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(body), len(want), body)
	}
	for i, w := range want {
		got := body[i]
		if got.Kind != w.kind || got.Left != w.left || got.Right != w.right ||
			got.LeftNo != w.leftNo || got.RightNo != w.rightNo {
			t.Errorf("row %d = %+v, want kind=%v left=%q(%d) right=%q(%d)",
				i, got, w.kind, w.left, w.leftNo, w.right, w.rightNo)
		}
	}
}

// More removals than additions leaves the right side blank for the remainder.
func TestSideBySideHandlesUnevenRuns(t *testing.T) {
	rows := SideBySide([]PatchHunk{{
		OldStart: 1, NewStart: 1,
		Lines: []string{"-a", "-b", "-c", "+x"},
	}})[1:]

	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Kind != DiffChange || rows[0].Left != "a" || rows[0].Right != "x" {
		t.Errorf("row 0 = %+v, want a paired change", rows[0])
	}
	for _, i := range []int{1, 2} {
		if rows[i].Kind != DiffRemoved || rows[i].Right != "" {
			t.Errorf("row %d = %+v, want a removal with nothing on the right", i, rows[i])
		}
	}
}

// A new file has no previous version, so the whole content is the change.
func TestContentRowsAreAllAdditions(t *testing.T) {
	rows := ContentRows("one\ntwo\n")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for i, r := range rows {
		if r.Kind != DiffAdded || r.Left != "" || r.LeftNo != 0 {
			t.Errorf("row %d = %+v, want an addition with no left side", i, r)
		}
		if r.RightNo != i+1 {
			t.Errorf("row %d line number = %d, want %d", i, r.RightNo, i+1)
		}
	}
	if got := ContentRows(""); got != nil {
		t.Errorf("ContentRows(\"\") = %v, want nil", got)
	}
}

func TestFileChangesCollectsEveryKind(t *testing.T) {
	root := testRoot()
	sess := &Session{
		Info: SessionInfo{CWD: root},
		Events: []Event{
			// An edit with a real diff.
			{Type: EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "a.go")},
				Result: &ToolResult{FilePath: filepath.Join(root, "a.go"),
					StructuredPatch: []PatchHunk{{Lines: []string{"+x"}}}}},
			// A new file: empty patch, content in the input.
			{Type: EventToolUse, ToolName: "Write",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "b.go"), "content": "body\n"},
				Result:    &ToolResult{FilePath: filepath.Join(root, "b.go")}},
			// A shell write.
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "cat > " + filepath.Join(root, "c.css") + " <<'E'\nbody{}\nE"}},
			// Another file entirely.
			{Type: EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "other.go")},
				Result: &ToolResult{FilePath: filepath.Join(root, "other.go"),
					StructuredPatch: []PatchHunk{{Lines: []string{"+y"}}}}},
		},
	}

	a := FileChanges(sess, filepath.Join(root, "a.go"), -1)
	if len(a) != 1 || !a[0].HasDiff() {
		t.Errorf("a.go = %+v, want one change with a diff", a)
	}

	b := FileChanges(sess, filepath.Join(root, "b.go"), -1)
	if len(b) != 1 || b[0].HasDiff() || b[0].Content != "body\n" || b[0].Kind != TouchCreate {
		t.Errorf("b.go = %+v, want a create carrying its content", b)
	}

	c := FileChanges(sess, filepath.Join(root, "c.css"), -1)
	if len(c) != 1 || c[0].Kind != TouchWrite || !c[0].Inferred {
		t.Errorf("c.css = %+v, want an inferred shell write", c)
	}

	// Bounded by a point in the session.
	if got := FileChanges(sess, filepath.Join(root, "other.go"), 2); len(got) != 0 {
		t.Errorf("other.go before its step = %+v, want none", got)
	}
	if got := FileChanges(sess, filepath.Join(root, "nothing.go"), -1); got != nil {
		t.Errorf("an untouched file = %v, want nil", got)
	}
	if got := FileChanges(nil, filepath.Join(root, "a.go"), -1); got != nil {
		t.Errorf("FileChanges(nil) = %v, want nil", got)
	}
}
