package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellRemovalsOnlyWhenCertain(t *testing.T) {
	cases := []struct {
		name, cmd string
		want      []string
	}{
		{"plain", "rm foo.txt", []string{"foo.txt"}},
		{"with flags", "rm -rf build/output.js", []string{"build/output.js"}},
		{"several files", "rm a.go b.go", []string{"a.go", "b.go"}},
		{"after a chain", "cd /tmp && rm note.md", []string{"note.md"}},
		// A glob or an expansion: which files it removed is not knowable from
		// the transcript, and naming the wrong file is worse than naming none.
		{"glob", "rm -rf build/*", nil},
		{"brace", "rm {a,b}.go", nil},
		{"variable", "rm $TMP/x", nil},
		{"substitution", "rm `ls`", nil},
		{"home", "rm ~/thing", nil},
		{"not rm at all", "rmdir empty", nil},
		{"rm inside a word", "npm run rm-stuff", nil},
	}
	for _, c := range cases {
		got := shellRemovals(c.cmd)
		if len(got) != len(c.want) {
			t.Errorf("%s: shellRemovals(%q) = %v, want %v", c.name, c.cmd, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestBuildFileActivityClassifiesTouches(t *testing.T) {
	sess := &Session{
		Info: SessionInfo{CWD: "/repo"},
		Events: []Event{
			{Type: EventToolUse, ToolName: "Read",
				ToolInput: map[string]interface{}{"file_path": "/repo/a.go"}},
			// A Write with an EMPTY patch is the only signal Claude Code gives
			// that a file did not exist before.
			{Type: EventToolUse, ToolName: "Write",
				ToolInput: map[string]interface{}{"file_path": "/repo/new.go", "content": "x"},
				Result:    &ToolResult{FilePath: "/repo/new.go"}},
			{Type: EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": "/repo/a.go"},
				Result: &ToolResult{FilePath: "/repo/a.go",
					StructuredPatch: []PatchHunk{{Lines: []string{"+one", "+two", "-old"}}}}},
			// A shell write: whether it existed before is not recorded.
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "cat > /repo/sh.css <<'CSS'\nbody{}\nCSS"}},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "rm /repo/old.go"}},
		},
	}

	fa := BuildFileActivity(sess)

	want := map[string]TouchKind{
		"/repo/a.go":   TouchEdit,
		"/repo/new.go": TouchCreate,
		"/repo/sh.css": TouchWrite,
		"/repo/old.go": TouchDelete,
	}
	for path, kind := range want {
		a := fa[path]
		if a == nil {
			t.Errorf("%s missing from activity", path)
			continue
		}
		got, _, _, touched := a.StateAt(len(sess.Events))
		if !touched || got != kind {
			t.Errorf("%s = %v, want %v", path, got, kind)
		}
	}

	// Churn comes from the recorded diff, not the request.
	if _, added, removed, _ := fa["/repo/a.go"].StateAt(len(sess.Events)); added != 2 || removed != 1 {
		t.Errorf("a.go churn = +%d -%d, want +2 -1", added, removed)
	}
	// Inferred touches must say so: a shell write and a deletion both are.
	for _, p := range []string{"/repo/sh.css", "/repo/old.go"} {
		if !fa[p].Touches[0].Inferred {
			t.Errorf("%s should be marked inferred", p)
		}
	}
	if fa["/repo/new.go"].Touches[0].Inferred {
		t.Error("a Write is recorded, not inferred")
	}
}

// The tree is drawn as of a point in a replay, so a file must not appear before
// the step that created it.
func TestStateAtRespectsTheReplayPosition(t *testing.T) {
	a := &FileActivity{Path: "/repo/x.go", Touches: []FileTouch{
		{EventIndex: 5, Kind: TouchCreate, LinesAdded: 10},
		{EventIndex: 9, Kind: TouchEdit, LinesAdded: 3, LinesRemo: 1},
	}}

	if _, _, _, touched := a.StateAt(4); touched {
		t.Error("the file should not exist before the step that created it")
	}
	kind, added, _, touched := a.StateAt(5)
	if !touched || kind != TouchCreate || added != 10 {
		t.Errorf("at step 5: kind=%v added=%d, want created/10", kind, added)
	}
	// A create outranks later edits: in this session the file is still new.
	kind, added, removed, _ := a.StateAt(20)
	if kind != TouchCreate || added != 13 || removed != 1 {
		t.Errorf("at the end: kind=%v +%d -%d, want created/+13/-1", kind, added, removed)
	}

	if _, ok := a.TouchedAt(9); !ok {
		t.Error("TouchedAt(9) should report the edit")
	}
	if _, ok := a.TouchedAt(7); ok {
		t.Error("nothing happened at step 7")
	}
}

// A deletion outranks everything before it, and a later write brings the file
// back.
func TestStateAtHandlesDeleteThenRecreate(t *testing.T) {
	a := &FileActivity{Touches: []FileTouch{
		{EventIndex: 1, Kind: TouchCreate},
		{EventIndex: 2, Kind: TouchDelete},
		{EventIndex: 3, Kind: TouchWrite},
	}}
	if kind, _, _, _ := a.StateAt(2); kind != TouchDelete {
		t.Errorf("after the delete, kind = %v, want deleted", kind)
	}
	if kind, _, _, _ := a.StateAt(3); kind != TouchWrite {
		t.Errorf("after the rewrite, kind = %v, want written", kind)
	}
}

func TestScanTreeSkipsNoiseAndGraftsMissingFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mk("src/main.go")
	mk("node_modules/dep/index.js")
	mk(".git/config")
	mk("README.md")

	// A file the session changed that is no longer on disk.
	gone := filepath.Join(root, "src", "deleted.go")
	activity := map[string]*FileActivity{
		filepath.Join(root, "src", "main.go"): {Path: filepath.Join(root, "src", "main.go")},
		gone:                                  {Path: gone},
	}

	tree := ScanTree(root, activity)
	if tree == nil {
		t.Fatal("no tree")
	}

	names := map[string]bool{}
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		names[n.Name] = true
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)

	for _, want := range []string{"src", "main.go", "README.md", "deleted.go"} {
		if !names[want] {
			t.Errorf("tree is missing %q", want)
		}
	}
	for _, unwanted := range []string{"node_modules", ".git", "config"} {
		if names[unwanted] {
			t.Errorf("tree should skip %q", unwanted)
		}
	}

	// The grafted file must be marked as not on disk.
	var found *TreeNode
	var find func(n *TreeNode)
	find = func(n *TreeNode) {
		if n.Path == gone {
			found = n
		}
		for _, c := range n.Children {
			find(c)
		}
	}
	find(tree)
	if found == nil {
		t.Fatal("deleted file was not grafted on")
	}
	if !found.Missing {
		t.Error("a file that is gone from disk should be marked Missing")
	}
}

// A directory normally skipped is still worth entering if the session changed
// something inside it.
func TestScanTreeDescendsIntoSkippedDirWhenTouched(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "dist", "bundle.js")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if tree := ScanTree(root, nil); len(tree.Children) != 0 {
		t.Errorf("dist should be skipped when untouched, got %d children", len(tree.Children))
	}

	tree := ScanTree(root, map[string]*FileActivity{p: {Path: p}})
	if len(tree.Children) != 1 || tree.Children[0].Name != "dist" {
		t.Fatalf("dist should be included when touched, got %+v", tree.Children)
	}
}
