package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// Half of what an agent does to a project goes through the shell, which records
// no path. Without recovering those mentions the tree sits still through most of
// a Bash-heavy session.
func TestAttachShellReadsRecoversMentionedFiles(t *testing.T) {
	sess := &Session{
		Info: SessionInfo{CWD: "/repo"},
		Events: []Event{
			{Type: EventToolUse, ToolName: "Bash", ToolInput: map[string]interface{}{
				"command": "sed -n '1,80p' src/main.go"}},
			{Type: EventToolUse, ToolName: "Bash", ToolInput: map[string]interface{}{
				"command": "grep -n handler src/a.go src/b.go"}},
			// Words that look like paths but are not files here.
			{Type: EventToolUse, ToolName: "Bash", ToolInput: map[string]interface{}{
				"command": "go install example.com/tool@v1.2.3 && echo 0.16.2"}},
			// A write already says more than a read; one touch per step.
			{Type: EventToolUse, ToolName: "Bash", ToolInput: map[string]interface{}{
				"command": "cat > src/main.go <<'EOF'\nx\nEOF"}},
		},
	}

	real := map[string]bool{
		"/repo/src/main.go": true,
		"/repo/src/a.go":    true,
		"/repo/src/b.go":    true,
	}
	activity := BuildFileActivity(sess)
	AttachShellReads(activity, sess, func(p string) bool { return real[p] })

	for _, p := range []string{"/repo/src/main.go", "/repo/src/a.go", "/repo/src/b.go"} {
		if activity[p] == nil {
			t.Errorf("%s was mentioned by a command but not recorded", p)
		}
	}
	// Only files that really exist in the project: a version number and a Go
	// module path are not files.
	if len(activity) != 3 {
		t.Errorf("recorded %d files, want 3: %v", len(activity), keysOf(activity))
	}

	// The read is marked inferred, because a mention is not a tool call.
	main := activity["/repo/src/main.go"]
	if main.Touches[0].Kind != TouchRead || !main.Touches[0].Inferred {
		t.Errorf("first touch = %v inferred=%v, want an inferred read",
			main.Touches[0].Kind, main.Touches[0].Inferred)
	}
	// The heredoc step wrote it; it must not also be recorded as a read.
	writeStep := 3
	n := 0
	for _, tt := range main.Touches {
		if tt.EventIndex == writeStep {
			n++
		}
	}
	if n != 1 {
		t.Errorf("step %d produced %d touches for main.go, want 1 (the write)", writeStep, n)
	}
	if kind, _, _, _ := main.StateAt(len(sess.Events)); kind != TouchWrite {
		t.Errorf("main.go ended as %v, want written: a read must not mask the write", kind)
	}
}

func keysOf(m map[string]*FileActivity) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestTreePathsCollectsFilesOnly(t *testing.T) {
	root := &TreeNode{Name: "r", Path: "/r", IsDir: true, Children: []*TreeNode{
		{Name: "a.go", Path: "/r/a.go"},
		{Name: "sub", Path: "/r/sub", IsDir: true, Children: []*TreeNode{
			{Name: "b.go", Path: "/r/sub/b.go"},
		}},
	}}
	paths := TreePaths(root)
	if len(paths) != 2 || !paths["/r/a.go"] || !paths["/r/sub/b.go"] {
		t.Errorf("TreePaths = %v, want just the two files", paths)
	}
	if paths["/r"] || paths["/r/sub"] {
		t.Error("directories are not files")
	}
	if got := TreePaths(nil); len(got) != 0 {
		t.Errorf("TreePaths(nil) = %v, want empty", got)
	}
}

// A heredoc body is data, not commands. Scanning it found whatever the data
// contained: a Go test fixture with the literal text "rm foo.txt" in it was
// read as a deletion, and source mentioning filenames as the shell reading
// them. Seventeen nonsense paths came from one session this way.
func TestHeredocBodiesAreNotScannedForCommands(t *testing.T) {
	cmd := "cat > fixture_test.go <<'EOF'\n" +
		"cases := []string{\"rm foo.txt\", \"rm -rf build/out.js\"}\n" +
		"_ = shellRemovals(c.cmd)\n" +
		"EOF\n" +
		"rm actually-removed.txt"

	got := shellRemovals(cmd)
	if len(got) != 1 || got[0] != "actually-removed.txt" {
		t.Errorf("shellRemovals = %v, want just the real command outside the heredoc", got)
	}

	if stripped := stripHeredocBodies(cmd); strings.Contains(stripped, "foo.txt") {
		t.Errorf("stripped command still contains the body: %q", stripped)
	}
}

func TestStripHeredocBodies(t *testing.T) {
	cases := []struct{ name, in, wantOut string }{
		{"no heredoc", "ls -la", "ls -la"},
		// The terminator goes with the body: it is part of the heredoc, not a
		// command.
		{"one heredoc", "cat > a <<'E'\nbody\nE", "cat > a <<'E'"},
		{"two heredocs", "cat > a <<'E'\nx\nE\ncat > b <<'F'\ny\nF",
			"cat > a <<'E'\ncat > b <<'F'"},
		{"unterminated", "cat > a <<'E'\nx\ny", "cat > a <<'E'"},
	}
	for _, c := range cases {
		if got := stripHeredocBodies(c.in); got != c.wantOut {
			t.Errorf("%s: stripHeredocBodies = %q, want %q", c.name, got, c.wantOut)
		}
	}
}

func TestPlausiblePathRejectsFragments(t *testing.T) {
	for _, bad := range []string{
		"", "/", ".", "..", "-rf", "/repo/-flag",
		"/repo/len(c.want)", "/repo/c.name,", "/repo/got,",
		"/repo/a b.go", "/repo/$VAR", "/repo/x;y",
	} {
		if plausiblePath(bad) {
			t.Errorf("plausiblePath(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{
		"/repo/main.go", "src/a.ts", "/repo/.github/workflows/ci.yml",
		"/repo/a-b_c.go", "Makefile",
	} {
		if !plausiblePath(good) {
			t.Errorf("plausiblePath(%q) = false, want true", good)
		}
	}
}

// A command often has rm on a line of its own, which a start-of-string anchor
// misses entirely.
func TestShellRemovalsFindsRmOnItsOwnLine(t *testing.T) {
	cases := []struct {
		name, cmd string
		want      int
	}{
		{"own line", "cd /repo\nrm stale.txt\necho done", 1},
		{"after &&", "go build ./... && rm tmp.out", 1},
		{"after ;", "ls; rm a.txt", 1},
		{"several lines", "rm one.txt\nrm two.txt", 2},
		{"first line", "rm only.txt", 1},
	}
	for _, c := range cases {
		if got := shellRemovals(c.cmd); len(got) != c.want {
			t.Errorf("%s: shellRemovals(%q) = %v, want %d path(s)",
				c.name, c.cmd, got, c.want)
		}
	}
}
