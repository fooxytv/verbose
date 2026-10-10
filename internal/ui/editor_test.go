package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/pkg/session"
)

func TestResolveEditorPrefersVisualThenEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	ed, ok := resolveEditor()
	if !ok || ed.bin != "nano" {
		t.Fatalf("with EDITOR=nano got %q (ok=%v), want nano", ed.bin, ok)
	}

	// VISUAL outranks EDITOR, the convention every other tool follows.
	t.Setenv("VISUAL", "vim")
	ed, _ = resolveEditor()
	if ed.bin != "vim" {
		t.Errorf("VISUAL should win over EDITOR, got %q", ed.bin)
	}
}

// $EDITOR routinely carries arguments — "code -w", "emacsclient -nw". Taking
// the value whole as a binary name looks for a program called "code -w", which
// does not exist, so the key would die on exactly the editors that need it.
func TestResolveEditorSplitsArguments(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code -w -n")
	ed, ok := resolveEditor()
	if !ok {
		t.Fatal("resolveEditor found nothing")
	}
	if ed.bin != "code" {
		t.Errorf("bin = %q, want code", ed.bin)
	}
	if strings.Join(ed.args, " ") != "-w -n" {
		t.Errorf("args = %q, want [-w -n]", ed.args)
	}
}

// With neither set, fall back to something that exists on this machine rather
// than failing. vi is POSIX-mandated, so on a Unix box this must succeed.
func TestResolveEditorFallsBackToPath(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	ed, ok := resolveEditor()
	if runtime.GOOS == "windows" {
		return
	}
	if !ok {
		t.Fatal("no editor found with VISUAL and EDITOR unset")
	}
	if ed.bin == "" {
		t.Error("resolved an empty binary")
	}
}

// Guessing a line flag is worse than not jumping: an editor that does not know
// the flag opens a second, empty buffer named "+12" beside the real file.
func TestLineArgOnlyForEditorsThatUnderstandIt(t *testing.T) {
	for _, bin := range []string{"nvim", "vim", "vi", "nano", "/usr/bin/vim", "nvim.exe"} {
		if got := lineArg(bin, 12); got != "+12" {
			t.Errorf("lineArg(%q, 12) = %q, want +12", bin, got)
		}
	}
	for _, bin := range []string{"code", "hx", "helix", "subl", "idea"} {
		if got := lineArg(bin, 12); got != "" {
			t.Errorf("lineArg(%q, 12) = %q, want empty — the flag differs", bin, got)
		}
	}
	if got := lineArg("vim", 0); got != "" {
		t.Errorf("lineArg with no line = %q, want empty", got)
	}
}

// The file goes last, after the editor's own arguments and the line flag.
func TestEditorArgsPutsFileLast(t *testing.T) {
	ed := editorCommand{bin: "nvim", args: []string{"-R"}}
	got := editorArgs(ed, "/tmp/x.go", 7)
	want := []string{"-R", "+7", "/tmp/x.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("editorArgs = %v, want %v", got, want)
	}

	// An editor with no known line flag still gets the file.
	got = editorArgs(editorCommand{bin: "code"}, "/tmp/x.go", 7)
	if len(got) != 1 || got[0] != "/tmp/x.go" {
		t.Errorf("editorArgs for code = %v, want just the path", got)
	}
}

// editorArgs must not write into the command's own argument slice.
//
// The args slice has to carry spare capacity for this to bite: append only
// shares a backing array when there is room in it, so a test built from a
// literal passes whether the copy is there or not and proves nothing.
func TestEditorArgsDoesNotMutateTheCommand(t *testing.T) {
	args := make([]string, 1, 8)
	args[0] = "-R"
	ed := editorCommand{bin: "nvim", args: args}

	first := editorArgs(ed, "/tmp/a.go", 7)
	second := editorArgs(ed, "/tmp/b.go", 9)

	if strings.Join(second, " ") != "-R +9 /tmp/b.go" {
		t.Errorf("second call = %v, want [-R +9 /tmp/b.go]", second)
	}
	// The first result must survive the second call untouched.
	if strings.Join(first, " ") != "-R +7 /tmp/a.go" {
		t.Errorf("first call became %v after a second — the slice is shared", first)
	}
}

func TestChangeLineTakesTheLatestHunk(t *testing.T) {
	changes := []session.FileChange{
		{Hunks: []session.PatchHunk{{NewStart: 3}}},
		{Hunks: []session.PatchHunk{{NewStart: 41}, {NewStart: 90}}},
	}
	if got := changeLine(changes); got != 41 {
		t.Errorf("changeLine = %d, want 41 (first hunk of the last change)", got)
	}

	// A new file or a shell write records no hunks, so there is no line to
	// offer and the editor opens at the top.
	if got := changeLine([]session.FileChange{{Content: "x"}}); got != 0 {
		t.Errorf("changeLine with no hunks = %d, want 0", got)
	}
	if got := changeLine(nil); got != 0 {
		t.Errorf("changeLine(nil) = %d, want 0", got)
	}
}

// The tree grafts back files the session touched that have since been deleted,
// so a path on screen is not proof of a file on disk. Opening one would hand
// the reader an empty buffer that looks like their work was lost.
func TestEditFileRefusesAPathThatIsGone(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := treeModel(t)
	cmd := m.editFile(filepath.Join(t.TempDir(), "deleted.go"))
	if cmd == nil {
		t.Fatal("expected a status-clearing command")
	}
	if !strings.Contains(m.statusMsg, "No longer on disk") {
		t.Errorf("statusMsg = %q, want it to say the file is gone", m.statusMsg)
	}
}

func TestEditFileRefusesADirectory(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := treeModel(t)
	m.editFile(t.TempDir())
	if !strings.Contains(m.statusMsg, "is a directory") {
		t.Errorf("statusMsg = %q, want it to name the directory", m.statusMsg)
	}
}

// A key that does nothing reads as a broken program — the ctrl+arrow lesson.
func TestEditFileSaysSoWhenThereIsNoEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("PATH", t.TempDir()) // nothing to find
	m := treeModel(t)
	f := filepath.Join(t.TempDir(), "real.go")
	if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.editFile(f)
	if !strings.Contains(m.statusMsg, "$EDITOR") {
		t.Errorf("statusMsg = %q, want it to name $EDITOR", m.statusMsg)
	}
}

// Playback must stop before the editor takes the terminal. The generation bump
// is what drops the ticks already scheduled: without it they are delivered on
// return and the replay jumps forward by however long the edit took.
func TestEditingStopsPlaybackAndDropsPendingTicks(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := treeModel(t)
	m.mode = viewReplay
	m.replayPlaying = true
	m.replayTyped = 4
	gen := m.replayGen

	rows := m.treeRows()
	var file string
	for _, r := range rows {
		if !r.node.IsDir {
			file = r.node.Path
			break
		}
	}
	if file == "" {
		t.Skip("fixture tree has no files")
	}

	if cmd := m.editFile(file); cmd == nil {
		t.Fatal("editFile returned no command for a real file")
	}
	if m.replayPlaying {
		t.Error("playback still running with an editor open")
	}
	if m.replayTyped != -1 {
		t.Errorf("replayTyped = %d, want -1 so the whole step shows", m.replayTyped)
	}
	if m.replayGen == gen {
		t.Error("replayGen not bumped — scheduled ticks will arrive on return")
	}
}

// Pressing e in the tree acts on the row the reader can see, which is the
// followed row while the tree is following — the same bug that made d do
// nothing until the cursor had been moved by hand.
func TestEditFromTreeUsesTheVisibleSelection(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := treeModel(t)
	m.mode = viewTree
	m.treeFollow = true

	// The tree only follows while something is driving it, so park the replay
	// on a step that touches a file and leave it playing. Without this the
	// selection stays on row 0 and the test proves nothing.
	m.replayPlaying = true
	for i, st := range m.replaySteps {
		if st.FilePath != "" {
			m.replayIndex = i
		}
	}

	rows := m.treeRows()
	want := m.treeSelection(rows, m.treeUpto())
	if want == 0 {
		t.Fatalf("tree is not following: selection stayed on row 0 of %d", len(rows))
	}

	res, _ := m.handleTreeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, "e")
	after := res.(Model)

	// The cursor must be written back, not just used for drawing. Row 0 is the
	// root directory, so trusting the stored cursor gives "is a directory"
	// rather than the file the reader is looking at.
	if after.treeCursor != want {
		t.Errorf("treeCursor = %d after e, want the followed row %d", after.treeCursor, want)
	}
	if strings.Contains(after.statusMsg, "is a directory") {
		t.Errorf("e acted on row 0 instead of the followed row: %q", after.statusMsg)
	}
	if after.treeFollow {
		t.Error("following should stop once the reader opens a file")
	}
}

// A step that recorded a real diff has no CodePath at all — stepCode returns
// nothing when a patch exists — so the replay needs its own absolute path or e
// works on new files only.
func TestReplayStepCarriesTheFileItTouched(t *testing.T) {
	cwd := "/work/proj"
	sess := &session.Session{
		Info: session.SessionInfo{CWD: cwd},
		Events: []session.Event{
			{Type: session.EventToolUse, ToolName: "Edit",
				ToolInput: map[string]interface{}{"file_path": "src/app.go"},
				Result: &session.ToolResult{FilePath: "src/app.go",
					StructuredPatch: []session.PatchHunk{{NewStart: 10, Lines: []string{"+a"}}}}},
		},
	}
	steps := session.BuildReplay(sess)
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if steps[0].CodePath != "" {
		t.Fatalf("fixture no longer exercises the diff case: CodePath = %q", steps[0].CodePath)
	}
	want := filepath.Join(cwd, "src/app.go")
	if steps[0].FilePath != want {
		t.Errorf("FilePath = %q, want %q", steps[0].FilePath, want)
	}
}

func TestReplayEditSaysSoWhenTheStepTouchesNoFile(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := treeModel(t)
	m.mode = viewReplay
	m.replaySteps = []session.ReplayStep{{Title: "You asked", Kind: session.StepGoal}}
	m.replayIndex = 0

	res, _ := m.handleReplayKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, "e")
	after := res.(Model)
	if !strings.Contains(after.statusMsg, "does not touch a file") {
		t.Errorf("statusMsg = %q, want it to explain there is no file", after.statusMsg)
	}
}
