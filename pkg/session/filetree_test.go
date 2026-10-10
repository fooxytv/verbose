package session

import (
	"os"
	"path/filepath"
	"runtime"
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
		// ...but only at the start of a word. A tilde inside a path is an
		// ordinary character, and Windows 8.3 short paths are built from them,
		// so rejecting the lot meant no deletion was ever recognised there.
		{"tilde inside a path", `rm C:\Users\RUNNER~1\Temp\gone.txt`,
			[]string{`C:\Users\RUNNER~1\Temp\gone.txt`}},
		{"tilde inside a unix path", "rm /tmp/build~2/old.go", []string{"/tmp/build~2/old.go"}},
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
	root := testRoot()
	sess := &Session{
		Info: SessionInfo{CWD: root},
		Events: []Event{
			{Type: EventToolUse, ToolName: "Read",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "a.go")}},
			// A Write with an EMPTY patch is the only signal Claude Code gives
			// that a file did not exist before.
			{Type: EventToolUse, ToolName: "Write",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "new.go"), "content": "x"},
				Result:    &ToolResult{FilePath: filepath.Join(root, "new.go")}},
			{Type: EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": filepath.Join(root, "a.go")},
				Result: &ToolResult{FilePath: filepath.Join(root, "a.go"),
					StructuredPatch: []PatchHunk{{Lines: []string{"+one", "+two", "-old"}}}}},
			// A shell write: whether it existed before is not recorded.
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "cat > " + filepath.Join(root, "sh.css") + " <<'CSS'\nbody{}\nCSS"}},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "rm " + filepath.Join(root, "old.go")}},
		},
	}

	fa := BuildFileActivity(sess)

	want := map[string]TouchKind{
		filepath.Join(root, "a.go"):   TouchEdit,
		filepath.Join(root, "new.go"): TouchCreate,
		filepath.Join(root, "sh.css"): TouchWrite,
		filepath.Join(root, "old.go"): TouchDelete,
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
	if _, added, removed, _ := fa[filepath.Join(root, "a.go")].StateAt(len(sess.Events)); added != 2 || removed != 1 {
		t.Errorf("a.go churn = +%d -%d, want +2 -1", added, removed)
	}
	// Inferred touches must say so: a shell write and a deletion both are.
	for _, p := range []string{filepath.Join(root, "sh.css"), filepath.Join(root, "old.go")} {
		if !fa[p].Touches[0].Inferred {
			t.Errorf("%s should be marked inferred", p)
		}
	}
	if fa[filepath.Join(root, "new.go")].Touches[0].Inferred {
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

// A shell command that changes files records a real diff, one per file, in
// bashEditDiff — a different field from the edit tools' structuredPatch. Not
// reading it left 331 recorded diffs unseen, more than the 300 the edit tools
// provided, and made shell edits look diffless when they were not.
func TestChangedFilesUnifiesBothRecordings(t *testing.T) {
	edit := &ToolResult{
		FilePath:        "/repo/a.go",
		StructuredPatch: []PatchHunk{{Lines: []string{"+one", "-two"}}},
	}
	got := edit.ChangedFiles()
	if len(got) != 1 || got[0].FilePath != "/repo/a.go" || len(got[0].Hunks) != 1 {
		t.Errorf("edit-tool result = %+v, want its one file", got)
	}
	if a, d := edit.Churn(); a != 1 || d != 1 {
		t.Errorf("edit churn = +%d -%d, want +1 -1", a, d)
	}

	shell := &ToolResult{BashEdit: &BashEditDiff{Files: []ChangedFile{
		{FilePath: "/repo/x.tf", Hunks: []PatchHunk{{Lines: []string{"+a", "+b"}}}},
		{FilePath: "/repo/y.tf", Hunks: []PatchHunk{{Lines: []string{"-c"}}}},
		// Present but empty: the command touched nothing here.
		{FilePath: "/repo/z.tf"},
	}}}
	got = shell.ChangedFiles()
	if len(got) != 2 {
		t.Fatalf("shell result = %d files, want 2 (the empty one dropped)", len(got))
	}
	if a, d := shell.Churn(); a != 2 || d != 1 {
		t.Errorf("shell churn = +%d -%d, want +2 -1 across both files", a, d)
	}

	// structuredPatch wins when both are somehow present.
	both := &ToolResult{
		FilePath:        "/repo/a.go",
		StructuredPatch: []PatchHunk{{Lines: []string{"+one"}}},
		BashEdit:        &BashEditDiff{Files: []ChangedFile{{FilePath: "/repo/other.go", Hunks: []PatchHunk{{Lines: []string{"+z"}}}}}},
	}
	if got := both.ChangedFiles(); len(got) != 1 || got[0].FilePath != "/repo/a.go" {
		t.Errorf("with both set = %+v, want the structuredPatch file", got)
	}

	if got := (&ToolResult{}).ChangedFiles(); got != nil {
		t.Errorf("empty result = %v, want nil", got)
	}
	var nilResult *ToolResult
	if got := nilResult.ChangedFiles(); got != nil {
		t.Errorf("nil result = %v, want nil", got)
	}
}

// The recorded diff is a fact; the command text is a guess. The diff wins, and
// the heredoc fallback must not record the same file twice.
func TestShellDiffOutranksTheCommandText(t *testing.T) {
	sess := &Session{
		Info: SessionInfo{CWD: "/repo"},
		Events: []Event{{
			Type:      EventToolUse,
			ToolName:  "Bash",
			ToolInput: map[string]interface{}{"command": "cat > /repo/a.go <<'EOF'\nnew\nEOF"},
			Result: &ToolResult{BashEdit: &BashEditDiff{Files: []ChangedFile{
				{FilePath: "/repo/a.go", Hunks: []PatchHunk{{Lines: []string{"+new", "-old"}}}},
				{FilePath: "/repo/b.go", Hunks: []PatchHunk{{Lines: []string{"+also"}}}},
			}}},
		}},
	}

	fa := BuildFileActivity(sess)
	a := fa["/repo/a.go"]
	if a == nil {
		t.Fatal("the changed file is missing from activity")
	}
	if len(a.Touches) != 1 {
		t.Errorf("a.go has %d touches, want 1: the heredoc must not record it again", len(a.Touches))
	}
	kind, added, removed, _ := a.StateAt(len(sess.Events))
	if kind != TouchEdit || added != 1 || removed != 1 {
		t.Errorf("a.go = %v +%d -%d, want an edit of +1 -1", kind, added, removed)
	}
	if a.Touches[0].Inferred {
		t.Error("a recorded diff is not inferred")
	}

	// The second file the one command changed is tracked too.
	if fa["/repo/b.go"] == nil {
		t.Error("the other file the command changed is missing")
	}

	// And the diff is what the panel shows for it.
	ch := FileChanges(sess, "/repo/a.go", -1)
	if len(ch) != 1 || !ch[0].HasDiff() {
		t.Errorf("FileChanges = %+v, want one change carrying the diff", ch)
	}
	if ch[0].Content != "" {
		t.Error("with a diff available the content dump should not be used")
	}
}

// The field has to survive a real transcript line.
func TestParseBashEditDiffFromTranscript(t *testing.T) {
	body := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z",` +
		`"toolUseResult":{"stdout":"","stderr":"","interrupted":false,` +
		`"bashEditDiff":{"files":[{"filePath":"/repo/a.tf","hunks":[{"oldStart":45,` +
		`"oldLines":6,"newStart":45,"newLines":12,"lines":[" ctx","+added"]}]}],` +
		`"moreFiles":0,"shared":true}},` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
`
	sess, err := ParseSessionFile(writeTranscript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	for i := range sess.Events {
		r := sess.Events[i].Result
		if r == nil || r.BashEdit == nil {
			continue
		}
		files := r.ChangedFiles()
		if len(files) != 1 || files[0].FilePath != "/repo/a.tf" {
			t.Fatalf("parsed files = %+v, want the one recorded file", files)
		}
		if h := files[0].Hunks[0]; h.OldStart != 45 || h.NewLines != 12 {
			t.Errorf("hunk = %+v, want its line numbers preserved", h)
		}
		if a, d := r.Churn(); a != 1 || d != 0 {
			t.Errorf("churn = +%d -%d, want +1 -0", a, d)
		}
		return
	}
	t.Fatal("no event carried the parsed bashEditDiff")
}

// testRoot is an absolute project directory for whatever platform is running
// the test.
//
// "/repo" is not absolute on Windows — filepath.IsAbs wants a drive letter —
// so absolutePath treats it as relative and joins it onto the session's cwd,
// producing \repo\repo\a.go. Nothing then matches the paths the fixture asked
// about, and the failure reads as the classifier being broken rather than the
// fixture being unix-only.
func testRoot() string {
	if runtime.GOOS == "windows" {
		return `C:\repo`
	}
	return "/repo"
}
