package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Did the work last?
//
// A transcript records an intent at a moment. Nothing in it says whether the
// code it put in the file is still there, so a replay of an old session shows
// every step as equally alive: an edit that is still load-bearing reads the
// same as one a later session quietly replaced.
//
// This compares what was recorded against the files as they are now. The unit
// is a BLOCK — one contiguous run of added lines — because that is what either
// survived or did not. A whole hunk is the wrong unit: two runs separated by a
// context line are two separate pieces of work, and joining them reports both
// as missing the moment anything lands between them.
//
// The comparison is deliberately biased towards saying work survived. A block
// matched verbatim is certain, and everything else is a guess from the text:
// a rename or a reformat moves lines that were never undone. Under-reporting
// is the honest failure here, because the one thing this is FOR is pointing at
// work that disappeared, and a filter that cries wolf is one nobody trusts.

// Survival is what became of one block of recorded work.
type Survival int

const (
	// SurvivalUnknown is a block with nothing to compare: a hunk that only
	// removed lines, or a change with no recorded content at all.
	SurvivalUnknown Survival = iota
	// SurvivalIntact is the block, verbatim, in the file now.
	SurvivalIntact
	// SurvivalChanged is most of the block still in the file, but not as one
	// run — reindented, reordered, or edited around.
	SurvivalChanged
	// SurvivalSuperseded is the block gone, with the same session having
	// changed that file again afterwards. The session undid its own work,
	// which is iteration rather than loss.
	SurvivalSuperseded
	// SurvivalReplaced is the block gone from a file that is still there, with
	// nothing later in the session accounting for it.
	SurvivalReplaced
	// SurvivalFileGone is the file itself no longer on disk.
	SurvivalFileGone
)

func (s Survival) String() string {
	switch s {
	case SurvivalIntact:
		return "intact"
	case SurvivalChanged:
		return "changed"
	case SurvivalSuperseded:
		return "superseded"
	case SurvivalReplaced:
		return "replaced"
	case SurvivalFileGone:
		return "file gone"
	}
	return "unknown"
}

// Lost reports whether this is work that left the code without the session
// itself being what took it out.
func (s Survival) Lost() bool {
	return s == SurvivalReplaced || s == SurvivalFileGone
}

// StepFate is what became of everything one replay step wrote.
type StepFate struct {
	Intact     int
	Changed    int
	Superseded int
	Replaced   int
	Gone       int

	// LostFiles names the files holding the blocks that are gone, in the order
	// the step wrote them, each file once.
	LostFiles []string
}

// Blocks is how many pieces of work were compared.
func (f StepFate) Blocks() int {
	return f.Intact + f.Changed + f.Superseded + f.Replaced + f.Gone
}

// Known reports whether this step wrote anything that could be checked.
func (f StepFate) Known() bool { return f.Blocks() > 0 }

// Lost reports whether any of the step's work is gone.
func (f StepFate) Lost() bool { return f.Replaced+f.Gone > 0 }

func (f *StepFate) add(v Survival, path string) {
	switch v {
	case SurvivalIntact:
		f.Intact++
	case SurvivalChanged:
		f.Changed++
	case SurvivalSuperseded:
		f.Superseded++
	case SurvivalReplaced:
		f.Replaced++
	case SurvivalFileGone:
		f.Gone++
	default:
		return
	}
	if !v.Lost() || path == "" {
		return
	}
	for _, p := range f.LostFiles {
		if p == path {
			return
		}
	}
	f.LostFiles = append(f.LostFiles, path)
}

// Describe says what was lost, in one line, or "" when nothing was.
//
// It never claims to know WHY the work went: a later session, a human editing
// the file and a branch switch are all the same absence on disk.
func (f StepFate) Describe(cwd string) string {
	if !f.Lost() {
		return ""
	}
	where := "a file"
	if len(f.LostFiles) > 0 {
		where = relPath(f.LostFiles[0], cwd)
	}
	if n := len(f.LostFiles) - 1; n == 1 {
		where += " and 1 other file"
	} else if n > 1 {
		where += fmt.Sprintf(" and %d other files", n)
	}
	if f.Replaced == 0 {
		return "Not on disk any more — " + where + " is gone, so this work is too"
	}
	return "Not in " + where + " any more — something replaced it after this session"
}

// Survey compares one session's recorded work against the files on disk,
// reading each file at most once.
//
// It is a snapshot. Nothing watches the files, so a Survey is built when the
// question is asked and thrown away after — which also keeps it off the
// per-frame path, where reading files would not fit in a 30ms tick.
type Survey struct {
	sess     *Session
	activity map[string]*FileActivity
	read     func(string) (string, error)
	files    map[string]string
	gone     map[string]bool

	// rootGone is the project directory itself not being there, which is one
	// fact about the session rather than a loss in every step of it.
	rootGone bool
}

// NewSurvey prepares to check a session's work against the files now.
func NewSurvey(sess *Session) *Survey { return newSurvey(sess, readFileText) }

func newSurvey(sess *Session, read func(string) (string, error)) *Survey {
	s := &Survey{
		sess:     sess,
		activity: BuildFileActivity(sess),
		read:     read,
		files:    make(map[string]string),
		gone:     make(map[string]bool),
	}
	// A project that is not there cannot be asked the question. Checking
	// anyway would report every step of an old session as lost work, when all
	// that has happened is that the folder was removed or renamed — and a
	// rename is what relink is for.
	if sess != nil {
		fi, err := os.Stat(sess.Info.CWD)
		s.rootGone = err != nil || !fi.IsDir()
	}
	return s
}

func readFileText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Step reports what became of the work one step wrote.
func (s *Survey) Step(step ReplayStep) StepFate {
	var fate StepFate
	if s == nil || s.sess == nil {
		return fate
	}
	if s.rootGone || step.EventIndex < 0 || step.EventIndex >= len(s.sess.Events) {
		return fate
	}

	for _, w := range stepWrites(s.sess.Events[step.EventIndex], step, s.sess.Info.CWD) {
		if !underDir(w.path, s.sess.Info.CWD) {
			continue
		}
		cur, here := s.contents(w.path)
		for _, block := range w.blocks {
			v := SurvivalFileGone
			if here {
				v = classifyBlock(block, cur)
			}
			// The session changing that file again afterwards accounts for the
			// absence. Checked for a missing file too: a command that removed
			// it is recorded, and "we deleted it ourselves" is not a loss.
			if v.Lost() && s.changedLater(w.path, step.EventIndex) {
				v = SurvivalSuperseded
			}
			fate.add(v, w.path)
		}
	}
	return fate
}

// underDir reports whether path is root itself or something inside it.
//
// Only work inside the project is checked. Of the files holding "lost" work
// across this machine's history, 140 of 235 were outside it — scratchpads,
// /tmp, /dev — and a temp file is MEANT to vanish. Flagging those buries the
// 95 that were in the project, which are the only ones anyone can act on. A
// session with no recorded cwd therefore checks nothing, which is honest: a
// path that cannot be located cannot be looked for.
func underDir(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	p, r := filepath.Clean(path), filepath.Clean(root)
	if runtime.GOOS == "windows" {
		// Windows paths are case-insensitive, and transcripts do not agree on
		// the drive letter's case.
		p, r = strings.ToLower(p), strings.ToLower(r)
	}
	if p == r {
		return true
	}
	if !strings.HasPrefix(p, r) || len(p) <= len(r) {
		return false
	}
	// The next character has to end the name, or /work/app would swallow
	// /work/app-old. A shell command on Windows records the forward slash.
	return p[len(r)] == filepath.Separator || p[len(r)] == '/'
}

// contents reads a file once, remembering both what it held and that it was
// not there.
func (s *Survey) contents(path string) (string, bool) {
	if s.gone[path] {
		return "", false
	}
	if c, ok := s.files[path]; ok {
		return c, true
	}
	c, err := s.read(path)
	if err != nil {
		s.gone[path] = true
		return "", false
	}
	// A transcript records lines without the carriage return a Windows file
	// carries, so a verbatim match would never land there.
	c = strings.ReplaceAll(c, "\r\n", "\n")
	s.files[path] = c
	return c, true
}

// changedLater reports whether the session touched this file again after the
// given event.
func (s *Survey) changedLater(path string, idx int) bool {
	a := s.activity[path]
	if a == nil {
		return false
	}
	for _, t := range a.Touches {
		if t.EventIndex > idx && t.Kind.Changed() {
			return true
		}
	}
	return false
}

// stepWrite is the work one step put into one file.
type stepWrite struct {
	path   string
	blocks []string
}

// stepWrites is everything a step added, per file.
func stepWrites(e Event, step ReplayStep, cwd string) []stepWrite {
	var out []stepWrite
	for _, f := range e.Result.ChangedFiles() {
		path := absolutePath(f.FilePath, cwd)
		if path == "" {
			continue
		}
		if blocks := addedBlocks(f.Hunks); len(blocks) > 0 {
			out = append(out, stepWrite{path: path, blocks: blocks})
		}
	}
	if len(out) > 0 {
		return out
	}

	// No diff, which is a new file or a write through the shell: there was no
	// "before", so the content IS the addition. Everything with a diff has
	// already been taken above, so this cannot double-count.
	if step.Code == "" || step.FilePath == "" {
		return nil
	}
	block := strings.Trim(step.Code, "\n")
	if strings.TrimSpace(block) == "" {
		return nil
	}
	return []stepWrite{{path: step.FilePath, blocks: []string{block}}}
}

// addedBlocks splits a diff into its contiguous runs of added lines.
func addedBlocks(hunks []PatchHunk) []string {
	var out []string
	for _, h := range hunks {
		var run []string
		flush := func() {
			if len(run) == 0 {
				return
			}
			if block := strings.Join(run, "\n"); strings.TrimSpace(block) != "" {
				out = append(out, block)
			}
			run = nil
		}
		for _, line := range h.Lines {
			if strings.HasPrefix(line, "+") {
				run = append(run, line[1:])
				continue
			}
			flush()
		}
		flush()
	}
	return out
}

// classifyBlock decides what became of one run of added lines.
//
// A verbatim match is the only certain answer. Short of that, the question is
// whether the work is still RECOGNISABLE: a later edit to one line inside a
// ten-line block has not undone the block, so a majority of its lines still
// being present counts as surviving in changed form. Fewer than half, and
// calling it gone is the fairer reading.
func classifyBlock(block, cur string) Survival {
	if block == "" {
		return SurvivalUnknown
	}
	if strings.Contains(cur, block) {
		return SurvivalIntact
	}

	var total, present int
	for _, line := range strings.Split(block, "\n") {
		// Compared without indentation: moving code into a loop or a method
		// reindents every line of it without changing any of the work.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		total++
		if strings.Contains(cur, trimmed) {
			present++
		}
	}
	if total == 0 {
		return SurvivalUnknown
	}
	if present*2 > total {
		return SurvivalChanged
	}
	return SurvivalReplaced
}

// WorkLoss is what became of a session's work, as far as the files now say.
type WorkLoss struct {
	// Steps are the steps whose work is not in the files any more, in
	// transcript order: these are still a replay, read front to back.
	Steps []ReplayStep

	// Fates says what became of each of those steps, keyed by event index.
	Fates map[int]StepFate

	// Checked is how many pieces of work were compared at all. Zero means the
	// question could not be asked — nothing the session wrote is inside the
	// project — which is not the same answer as "everything survived".
	Checked int

	// Files is how many distinct files hold the lost work.
	Files int
}

// LostWork compares every step against the files and collects what is gone.
func LostWork(sess *Session, steps []ReplayStep) WorkLoss {
	var loss WorkLoss
	if sess == nil || len(steps) == 0 {
		return loss
	}
	survey := NewSurvey(sess)
	seen := make(map[string]bool)
	for _, st := range steps {
		fate := survey.Step(st)
		loss.Checked += fate.Blocks()
		if !fate.Lost() {
			continue
		}
		if loss.Fates == nil {
			loss.Fates = make(map[int]StepFate)
		}
		loss.Steps = append(loss.Steps, st)
		loss.Fates[st.EventIndex] = fate
		for _, f := range fate.LostFiles {
			if !seen[f] {
				seen[f] = true
				loss.Files++
			}
		}
	}
	return loss
}
