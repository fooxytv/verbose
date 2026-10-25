package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/pkg/session"
)

// Opening a file in the reader's own editor.
//
// verbose reads transcripts; it does not write code. `e` is the one place it
// hands back a writable buffer — the file under the cursor, as it stands on
// disk now, in whatever editor the reader already uses. Whatever that editor
// brings with it (an LSP, Copilot, their own bindings) comes along for free,
// which is the point: verbose has no business growing an editor of its own.
//
// An editor is never a build or install dependency. Nothing here is reached
// unless `e` is pressed, and a reader with nothing to open is told why rather
// than left watching a key do nothing — the lesson from the ctrl+arrow
// bindings, which failed silently and so read as a broken program.

// editorFallbacks are tried in order when neither $VISUAL nor $EDITOR is set.
//
// vi is POSIX-mandated and macOS ships /usr/bin/vim, so on a Unix box this
// search all but always succeeds. Windows has none of them, which is why the
// list is per-platform and notepad backstops it.
func editorFallbacks() []string {
	if runtime.GOOS == "windows" {
		return []string{"nvim", "vim", "notepad"}
	}
	return []string{"nvim", "vim", "vi", "nano"}
}

// editorCommand is the editor to run: a binary and the arguments that came with
// it, before the file is appended.
type editorCommand struct {
	bin  string
	args []string
}

// resolveEditor picks the editor to run. ok is false when nothing was found.
//
// $VISUAL and $EDITOR may carry arguments of their own — "code -w",
// "emacsclient -nw" — so the value is split into fields rather than taken whole
// as a binary name. git reads them the same way, and a reader who has set one
// expects it honoured.
func resolveEditor() (editorCommand, bool) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if fields := strings.Fields(os.Getenv(name)); len(fields) > 0 {
			return editorCommand{bin: fields[0], args: fields[1:]}, true
		}
	}
	for _, bin := range editorFallbacks() {
		if _, err := exec.LookPath(bin); err == nil {
			return editorCommand{bin: bin}, true
		}
	}
	return editorCommand{}, false
}

// lineArg is how this editor is told to open at a line, or "" when verbose does
// not know how to ask.
//
// Guessing is worse than not jumping. An editor that does not understand the
// flag treats it as a second filename, so the reader gets an empty buffer
// called "+12" beside the file they wanted. Staying quiet costs only the jump.
func lineArg(bin string, line int) string {
	if line <= 0 {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(bin), ".exe")
	switch name {
	case "nvim", "vim", "vi", "view", "gvim", "mvim", "nano", "emacs", "emacsclient", "kak", "micro", "joe":
		return "+" + strconv.Itoa(line)
	}
	return ""
}

// editorArgs is the full argument list for opening path at line.
//
// The file goes last, after the editor's own arguments and the line flag, which
// is the order every editor in the list expects.
func editorArgs(c editorCommand, path string, line int) []string {
	args := append([]string(nil), c.args...)
	if a := lineArg(c.bin, line); a != "" {
		args = append(args, a)
	}
	return append(args, path)
}

// changeLine is the line to open at: where the most recent change to the file
// landed, so the editor opens on the work rather than on line 1.
//
// The last change is the one the reader just watched. Its first hunk is where
// it starts; a change with no recorded hunks — a new file, or a shell write —
// has no line to offer and gets 0.
func changeLine(changes []session.FileChange) int {
	for i := len(changes) - 1; i >= 0; i-- {
		if hunks := changes[i].Hunks; len(hunks) > 0 && hunks[0].NewStart > 0 {
			return hunks[0].NewStart
		}
	}
	return 0
}

// editMode says where the editor goes.
type editMode int

const (
	// editInPlace suspends verbose and gives the editor the whole terminal,
	// handing it back on quit. This is the default, and what $EDITOR does
	// everywhere else — git commit, crontab -e, visudo.
	editInPlace editMode = iota

	// editBeside puts the editor in a tmux pane or a terminal tab next to
	// verbose, leaving both on screen.
	editBeside
)

// editModeFor maps the pressed key to where the editor opens. Shift asks for
// the editor beside verbose; the bare key takes the terminal.
func editModeFor(key string) editMode {
	if key == "E" {
		return editBeside
	}
	return editInPlace
}

// editFile opens path in the reader's editor, at the line of its last recorded
// change. It returns the command to run and sets a status message instead when
// there is nothing to open.
//
// Playback stops either way. Coming back to a replay that ran on without you
// is disorienting, and replayGen is bumped so the ticks already scheduled are
// dropped rather than delivered in a burst on return.
//
// In place is the default because the two keys are pressed at different rates.
// `c` opens a CLI beside verbose once per session; `e` can be pressed on every
// file in a replay, and launchInTmux splits a NEW pane every time with no
// reuse — five files, five panes. Beside also buys less than it looks: playback
// is paused before the editor opens, so there is nothing moving to watch, and
// tmux send-keys is fire-and-forget so verbose never learns when you quit.
// `E` asks for it anyway.
func (m *Model) editFile(path string, mode editMode) tea.Cmd {
	if path == "" {
		m.warn("No file to edit here")
		return clearStatusAfter()
	}

	cwd := ""
	if m.selectedSession != nil {
		cwd = m.selectedSession.Info.CWD
	}
	short := session.ShortPath(path, cwd)

	// A file the session touched may be long gone: the tree deliberately grafts
	// deleted files back so an old session still reads as it happened. Opening
	// one would give an empty buffer that looks like the file was lost rather
	// than never there.
	info, err := os.Stat(path)
	if err != nil {
		m.warn("No longer on disk: " + short)
		return clearStatusAfter()
	}
	if info.IsDir() {
		m.warn("Select a file to edit — " + short + " is a directory")
		return clearStatusAfter()
	}

	ed, ok := resolveEditor()
	if !ok {
		m.warn("No editor found — set $EDITOR")
		return clearStatusAfter()
	}

	m.replayPlaying = false
	m.replayTyped = -1
	m.replayGen++

	line := changeLine(session.FileChanges(m.selectedSession, path, m.treeUpto()))
	args := editorArgs(ed, path, line)
	if mode == editBeside {
		return launchInTerminal(ed.bin, args, cwd)
	}
	return launchInPlace(ed.bin, args, cwd)
}

// editTreeSelection opens whatever the tree cursor is on.
//
// It takes the followed row over the stored one and keeps it, exactly as
// opening a diff does: the reader is now working on this file, not watching
// wherever the replay goes next.
func (m *Model) editTreeSelection(mode editMode) tea.Cmd {
	rows := m.treeRows()
	if len(rows) == 0 {
		m.warn("No file to edit here")
		return clearStatusAfter()
	}

	at := m.treeSelection(rows, m.treeUpto())
	m.treeCursor = clampInt(at, 0, len(rows)-1)
	m.treeFollow = false

	return m.editFile(rows[m.treeCursor].node.Path, mode)
}

// editCurrentStep opens the file the replay is on, for a replay with no tree
// beside it to point at one.
func (m *Model) editCurrentStep(mode editMode) tea.Cmd {
	if m.replayIndex < len(m.replaySteps) {
		if path := m.replaySteps[m.replayIndex].FilePath; path != "" {
			return m.editFile(path, mode)
		}
	}
	m.warn("This step does not touch a file")
	return clearStatusAfter()
}
