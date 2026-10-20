package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture puts a file on disk and returns its absolute path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// editEvent is an Edit that recorded a diff for one file.
func editEvent(path string, lines ...string) Event {
	return Event{
		Type:      EventToolUse,
		ToolName:  "Edit",
		ToolInput: map[string]interface{}{"file_path": path},
		Result: &ToolResult{
			FilePath:        path,
			StructuredPatch: []PatchHunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: len(lines), Lines: lines}},
		},
	}
}

func TestSurvivalKeepsWorkStillInTheFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {\n\tbackoff()\n}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+func retry() {", "+\tbackoff()", "+}"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Lost() {
		t.Fatalf("work that is still in the file reported as lost: %+v", fate)
	}
	if fate.Intact != 1 {
		t.Errorf("Intact = %d, want 1: %+v", fate.Intact, fate)
	}
}

func TestSurvivalReportsWorkReplacedAfterTheSession(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {\n\twaitAndGiveUp()\n}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+\texponentialBackoff(attempt)", "+\tjitter(attempt)"),
	}}

	steps := BuildReplay(sess)
	fate := NewSurvey(sess).Step(steps[0])
	if !fate.Lost() {
		t.Fatalf("work no longer in the file not reported as lost: %+v", fate)
	}
	if fate.Replaced != 1 {
		t.Errorf("Replaced = %d, want 1: %+v", fate.Replaced, fate)
	}
	if len(fate.LostFiles) != 1 || fate.LostFiles[0] != path {
		t.Errorf("LostFiles = %v, want [%s]", fate.LostFiles, path)
	}
	if d := fate.Describe(dir); !strings.Contains(d, "retry.go") || !strings.Contains(d, "replaced") {
		t.Errorf("Describe = %q, want it to name the file and say it was replaced", d)
	}

	loss := LostWork(sess, steps)
	if len(loss.Steps) != 1 {
		t.Fatalf("LostWork returned %d steps, want 1", len(loss.Steps))
	}
	if _, ok := loss.Fates[steps[0].EventIndex]; !ok {
		t.Errorf("fates keyed by %v, want the step's event index %d", loss.Fates, steps[0].EventIndex)
	}
	if loss.Checked != 1 || loss.Files != 1 {
		t.Errorf("Checked = %d, Files = %d, want 1 and 1", loss.Checked, loss.Files)
	}
}

// The session changing the same file again is what accounts for the earlier
// edit being gone. Iterating is not losing work, and reporting it is what
// would make the filter useless: an agent that edits one file eight times
// would report seven losses every time.
func TestSurvivalIgnoresWorkTheSessionItselfChangedAgain(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {\n\texponentialBackoff(attempt)\n}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+\tsleep(1)"),
		editEvent(path, "+\texponentialBackoff(attempt)"),
	}}

	steps := BuildReplay(sess)
	survey := NewSurvey(sess)
	first := survey.Step(steps[0])
	if first.Lost() {
		t.Errorf("an edit the session later redid reported as lost: %+v", first)
	}
	if first.Superseded != 1 {
		t.Errorf("Superseded = %d, want 1: %+v", first.Superseded, first)
	}
	if second := survey.Step(steps[1]); second.Intact != 1 {
		t.Errorf("the surviving edit: Intact = %d, want 1: %+v", second.Intact, second)
	}
	if loss := LostWork(sess, steps); len(loss.Steps) != 0 {
		t.Errorf("LostWork returned %d steps, want none", len(loss.Steps))
	} else if loss.Checked != 2 {
		t.Errorf("Checked = %d, want both blocks compared", loss.Checked)
	}
}

func TestSurvivalReportsAFileThatIsNoLongerOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scratch.go") // never written

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+package scratch"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Gone != 1 || !fate.Lost() {
		t.Fatalf("Gone = %d, Lost = %v, want 1 and true: %+v", fate.Gone, fate.Lost(), fate)
	}
	if d := fate.Describe(dir); !strings.Contains(d, "scratch.go") || !strings.Contains(d, "gone") {
		t.Errorf("Describe = %q, want it to say the file is gone", d)
	}
}

// A hunk is not the unit of work. Two runs of added lines with a context line
// between them are two separate pieces, and one of them surviving says nothing
// about the other.
func TestSurvivalSplitsAHunkIntoItsRunsOfAddedLines(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "server.go", "import \"net/http\"\n\nfunc serve() {}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{{
		Type:      EventToolUse,
		ToolName:  "Edit",
		ToolInput: map[string]interface{}{"file_path": path},
		Result: &ToolResult{FilePath: path, StructuredPatch: []PatchHunk{{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 4,
			Lines: []string{
				"+import \"net/http\"",
				" ",
				"+func handleTimeout(w http.ResponseWriter) {",
				"+\tw.WriteHeader(504)",
				"+}",
			},
		}}},
	}}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Blocks() != 2 {
		t.Fatalf("Blocks = %d, want 2 runs: %+v", fate.Blocks(), fate)
	}
	if fate.Intact != 1 || fate.Replaced != 1 {
		t.Errorf("Intact = %d, Replaced = %d, want 1 and 1: %+v", fate.Intact, fate.Replaced, fate)
	}
}

// Moving code into a function reindents every line of it without undoing any
// of the work, and the comparison has to see that or every refactor reads as
// a loss.
func TestSurvivalKeepsReindentedWorkAsChanged(t *testing.T) {
	dir := t.TempDir()
	// Reformatted since: the tabs the transcript recorded are spaces now, so
	// no recorded line matches with its indentation attached.
	path := writeFixture(t, dir, "retry.go",
		"func retry() {\n    for i := range 3 {\n        backoff(i)\n        attempt(i)\n    }\n}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+\t\tbackoff(i)", "+\t\tattempt(i)"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Lost() {
		t.Fatalf("reindented work reported as lost: %+v", fate)
	}
	if fate.Changed != 1 {
		t.Errorf("Changed = %d, want 1: %+v", fate.Changed, fate)
	}
}

// One line edited inside a block has not undone the block.
func TestSurvivalKeepsAMostlyPresentBlockAsChanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go",
		"func retry(n int) error {\n\tfor i := range n {\n\t\tbackoff(i)\n\t}\n\treturn nil\n}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path,
			"+func retry(n int) error {",
			"+\tfor i := 0; i < n; i++ {",
			"+\t\tbackoff(i)",
			"+\t}",
			"+\treturn nil",
			"+}"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Lost() {
		t.Fatalf("a block with one line changed reported as lost: %+v", fate)
	}
	if fate.Changed != 1 {
		t.Errorf("Changed = %d, want 1: %+v", fate.Changed, fate)
	}
}

// A new file records an EMPTY diff, so its survival can only be checked
// against the content the Write carried.
func TestSurvivalChecksANewFileAgainstTheContentItWrote(t *testing.T) {
	dir := t.TempDir()
	kept := writeFixture(t, dir, "kept.go", "package kept\n\nfunc Kept() {}\n")
	gonePath := filepath.Join(dir, "thrown.go")
	writeFixture(t, dir, "thrown.go", "package thrown\n\nfunc Rewritten() {}\n")

	write := func(path, content string) Event {
		return Event{
			Type:     EventToolUse,
			ToolName: "Write",
			ToolInput: map[string]interface{}{
				"file_path": path, "content": content,
			},
			// An empty patch is what Claude Code records for a create: there
			// is no "before" to diff against.
			Result: &ToolResult{FilePath: path},
		}
	}

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		write(kept, "package kept\n\nfunc Kept() {}\n"),
		write(gonePath, "package thrown\n\nfunc Original() {}\n"),
	}}

	steps := BuildReplay(sess)
	survey := NewSurvey(sess)
	if fate := survey.Step(steps[0]); fate.Intact != 1 || fate.Lost() {
		t.Errorf("a new file still on disk: %+v", fate)
	}
	if fate := survey.Step(steps[1]); !fate.Lost() {
		t.Errorf("a new file since rewritten reported as surviving: %+v", fate)
	}
}

// A hunk that only removes lines added nothing, so there is nothing to look
// for in the file. Counting it as lost would report every deletion as a loss.
func TestSurvivalIgnoresADeletionOnlyHunk(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "-\tlegacyRetry()", " func retry() {}"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Known() || fate.Lost() {
		t.Errorf("a removal-only hunk: Known = %v, Lost = %v, want false and false: %+v",
			fate.Known(), fate.Lost(), fate)
	}
}

// A Windows file carries the carriage return a transcript never records, so a
// verbatim match has to be made on normalised text or nothing matches there.
func TestSurvivalMatchesAcrossWindowsLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {\r\n\tbackoff()\r\n}\r\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+func retry() {", "+\tbackoff()", "+}"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Intact != 1 {
		t.Errorf("Intact = %d, want 1 — CRLF in the file blocked the match: %+v", fate.Intact, fate)
	}
}

func TestLostWorkKeepsTranscriptOrder(t *testing.T) {
	dir := t.TempDir()
	first := writeFixture(t, dir, "a.go", "package a\n")
	second := writeFixture(t, dir, "b.go", "package b\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(first, "+func GoneFromA() {}"),
		editEvent(second, "+package b"),
		editEvent(second, "+func GoneFromB() {}"),
	}}

	steps := BuildReplay(sess)
	loss := LostWork(sess, steps)
	if len(loss.Steps) != 2 {
		t.Fatalf("LostWork returned %d steps, want 2: %+v", len(loss.Steps), loss.Steps)
	}
	if loss.Steps[0].EventIndex >= loss.Steps[1].EventIndex {
		t.Errorf("out of order: %d then %d", loss.Steps[0].EventIndex, loss.Steps[1].EventIndex)
	}
	if len(loss.Fates) != 2 {
		t.Errorf("got %d fates, want 2", len(loss.Fates))
	}
	if loss.Files != 2 {
		t.Errorf("Files = %d, want 2 distinct files", loss.Files)
	}
}

// Blank lines are not work. A run of them joins into a block of newlines,
// which any multi-line file "contains" — counting that as surviving work
// would report an intact edit for a step that only added spacing.
func TestSurvivalIgnoresABlockOfBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "retry.go", "func retry() {}\n\n\nfunc give() {}\n")

	sess := &Session{Info: SessionInfo{CWD: dir}, Events: []Event{
		editEvent(path, "+", "+", " func give() {}"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Known() || fate.Lost() {
		t.Errorf("blank lines counted as work: Known = %v, Lost = %v: %+v",
			fate.Known(), fate.Lost(), fate)
	}
}

// Only the two states that mean "gone, and not by this session" are a loss.
// Widening this is what would make the filter cry wolf, and nothing else in
// the package would notice.
func TestSurvivalLostIsOnlyReplacedAndFileGone(t *testing.T) {
	want := map[Survival]bool{
		SurvivalUnknown:    false,
		SurvivalIntact:     false,
		SurvivalChanged:    false,
		SurvivalSuperseded: false,
		SurvivalReplaced:   true,
		SurvivalFileGone:   true,
	}
	for v, lost := range want {
		if v.Lost() != lost {
			t.Errorf("%v.Lost() = %v, want %v", v, v.Lost(), lost)
		}
	}
	if got := classifyBlock("", "anything"); got != SurvivalUnknown {
		t.Errorf("classifyBlock with nothing to compare = %v, want unknown", got)
	}
}

// A scratchpad is MEANT to vanish. Work outside the project is not work the
// project lost, and on this machine those paths outnumbered the real losses.
func TestSurvivalIgnoresWorkOutsideTheProject(t *testing.T) {
	project := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "notes.md") // never written

	sess := &Session{Info: SessionInfo{CWD: project}, Events: []Event{
		editEvent(elsewhere, "+a scratch note"),
	}}

	fate := NewSurvey(sess).Step(BuildReplay(sess)[0])
	if fate.Known() || fate.Lost() {
		t.Errorf("a vanished file outside the project counted: Known = %v, Lost = %v: %+v",
			fate.Known(), fate.Lost(), fate)
	}
}

// Without a cwd a path cannot be located, so nothing can be looked for. That
// has to read as "not checked", never as "nothing survived".
func TestSurvivalChecksNothingWithoutAProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.go")

	sess := &Session{Events: []Event{editEvent(path, "+package gone")}}

	loss := LostWork(sess, BuildReplay(sess))
	if loss.Checked != 0 || len(loss.Steps) != 0 {
		t.Errorf("Checked = %d, lost steps = %d, want nothing checked and nothing lost",
			loss.Checked, len(loss.Steps))
	}
}

// The project has to match whole segments: /work/app must not take in
// /work/app-old, which is a different checkout.
func TestUnderDirMatchesWholeSegments(t *testing.T) {
	root := testRoot()
	cases := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "main.go"), true},
		{filepath.Join(root, "pkg", "session", "survival.go"), true},
		{root + "-old", false},
		{root + "2", false},
		{filepath.Dir(root), false},
		{"", false},
	}
	for _, c := range cases {
		if got := underDir(c.path, root); got != c.want {
			t.Errorf("underDir(%q, %q) = %v, want %v", c.path, root, got, c.want)
		}
	}
	if underDir(filepath.Join(root, "main.go"), "") {
		t.Error("underDir with no root reported a match")
	}
}

// A folder that was removed or renamed is one fact about the session, not a
// loss in every step of it. Reporting 90 losses for one rename is both wrong
// and useless, and relink is what a rename actually needs.
func TestSurvivalChecksNothingWhenTheProjectIsGone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "renamed-away")
	path := filepath.Join(root, "main.go")

	sess := &Session{Info: SessionInfo{CWD: root}, Events: []Event{
		editEvent(path, "+package main"),
	}}

	loss := LostWork(sess, BuildReplay(sess))
	if loss.Checked != 0 || len(loss.Steps) != 0 {
		t.Errorf("Checked = %d, lost = %d, want nothing checked in a project that is gone",
			loss.Checked, len(loss.Steps))
	}
}
