package session

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The project tree as a session changed it.
//
// Two halves that have to be kept apart. The directory listing comes from disk,
// so it is the project as it is NOW — for a session from weeks ago that is not
// the shape it had at the time, and the view says so rather than pretending.
// The activity comes from the transcript, which is exact for any session however
// old, and carries the event index of every touch so the tree can be drawn as
// of a particular point in a replay.

// TouchKind is what a session did to a file.
type TouchKind int

const (
	TouchRead TouchKind = iota
	TouchCreate
	TouchEdit
	// TouchWrite is a whole file written through the shell. Whether it existed
	// beforehand is not recorded anywhere, so it is neither a create nor an
	// edit — only "this file now has these contents".
	TouchWrite
	// TouchDelete is INFERRED from a shell command, never recorded. Claude Code
	// has no delete tool, so the only evidence is an `rm` in a command line.
	TouchDelete
)

func (k TouchKind) String() string {
	switch k {
	case TouchRead:
		return "read"
	case TouchCreate:
		return "created"
	case TouchEdit:
		return "edited"
	case TouchWrite:
		return "written"
	case TouchDelete:
		return "deleted"
	}
	return "touched"
}

// FileTouch is one operation a session performed on one file.
type FileTouch struct {
	// EventIndex is where in Session.Events this happened, so a tree can be
	// drawn as of any step of a replay.
	EventIndex int
	Kind       TouchKind
	LinesAdded int
	LinesRemo  int
	// Inferred marks a touch deduced from a shell command rather than recorded
	// by a tool, which is every deletion and every shell file write.
	Inferred bool
}

// FileActivity is everything a session did to one file, in order.
type FileActivity struct {
	Path    string
	Touches []FileTouch
}

// StateAt reports what had happened to this file by the given event index, and
// whether anything had at all.
func (a *FileActivity) StateAt(upto int) (kind TouchKind, added, removed int, touched bool) {
	for _, t := range a.Touches {
		if t.EventIndex > upto {
			break
		}
		touched = true
		added += t.LinesAdded
		removed += t.LinesRemo
		// A create outranks later edits: the file is new in this session
		// however many times it was then changed. A delete outranks everything.
		switch {
		case t.Kind == TouchDelete:
			kind = TouchDelete
		case kind == TouchDelete:
			kind = t.Kind
		case t.Kind == TouchCreate:
			kind = TouchCreate
		case (t.Kind == TouchEdit || t.Kind == TouchWrite) && kind != TouchCreate:
			kind = t.Kind
		case t.Kind == TouchRead && !touchedBefore(kind):
			kind = TouchRead
		}
	}
	return kind, added, removed, touched
}

// touchedBefore reports whether a kind already represents a change, so that a
// later read cannot downgrade it.
func touchedBefore(k TouchKind) bool {
	return k == TouchCreate || k == TouchEdit || k == TouchWrite || k == TouchDelete
}

// Changed reports whether a kind represents the file being altered, as opposed
// to merely read.
func (k TouchKind) Changed() bool {
	return k == TouchCreate || k == TouchEdit || k == TouchWrite || k == TouchDelete
}

// TouchedAt reports whether this file was touched by the event at exactly this
// index, which is what a tree highlights as "just now" while a replay plays.
func (a *FileActivity) TouchedAt(index int) (TouchKind, bool) {
	for _, t := range a.Touches {
		if t.EventIndex == index {
			return t.Kind, true
		}
	}
	return 0, false
}

// BuildFileActivity collects what a session did to each file, keyed by absolute
// path.
func BuildFileActivity(sess *Session) map[string]*FileActivity {
	if sess == nil {
		return nil
	}
	out := make(map[string]*FileActivity)

	add := func(path string, t FileTouch) {
		if path == "" {
			return
		}
		path = absolutePath(path, sess.Info.CWD)
		a := out[path]
		if a == nil {
			a = &FileActivity{Path: path}
			out[path] = a
		}
		a.Touches = append(a.Touches, t)
	}

	for i := range sess.Events {
		e := sess.Events[i]
		if e.Type != EventToolUse && e.Type != EventToolResult {
			// A file the user changed outside Claude is still a change to show.
			if e.Type == EventUserFileEdit && e.FilePath != "" {
				add(e.FilePath, FileTouch{EventIndex: i, Kind: TouchEdit})
			}
			continue
		}

		added, removed := 0, 0
		if e.Result != nil {
			added, removed = e.Result.Churn()
		}

		switch e.ToolName {
		case "Read":
			if p, ok := stringInput(e.ToolInput, "file_path", "notebook_path"); ok {
				add(p, FileTouch{EventIndex: i, Kind: TouchRead})
			}

		case "Write":
			p, _ := stringInput(e.ToolInput, "file_path")
			// An empty patch means the file did not exist before, which is the
			// only signal Claude Code gives that a Write created something.
			kind := TouchEdit
			if e.Result == nil || len(e.Result.StructuredPatch) == 0 {
				kind = TouchCreate
			}
			add(p, FileTouch{EventIndex: i, Kind: kind, LinesAdded: added, LinesRemo: removed})

		case "Edit", "MultiEdit", "NotebookEdit":
			p, _ := stringInput(e.ToolInput, "file_path", "notebook_path")
			if p == "" && e.Result != nil {
				p = e.Result.FilePath
			}
			add(p, FileTouch{EventIndex: i, Kind: TouchEdit, LinesAdded: added, LinesRemo: removed})

		case "Bash", "BashOutput":
			cmd, _ := stringInput(e.ToolInput, "command")
			if cmd == "" {
				continue
			}
			// A shell write records no diff and no indication of whether the
			// file already existed, so it is reported as neither created nor
			// edited — just written, and marked inferred.
			if p, body := shellHeredoc(cmd); p != "" && body != "" {
				add(p, FileTouch{EventIndex: i, Kind: TouchWrite,
					LinesAdded: strings.Count(body, "\n") + 1, Inferred: true})
			}
			for _, p := range shellRemovals(cmd) {
				add(p, FileTouch{EventIndex: i, Kind: TouchDelete, Inferred: true})
			}
		}
	}

	for _, a := range out {
		sort.SliceStable(a.Touches, func(i, j int) bool {
			return a.Touches[i].EventIndex < a.Touches[j].EventIndex
		})
	}
	return out
}

// rmCommand matches an `rm` invocation and captures its arguments.
var rmCommand = regexp.MustCompile(`(?:^|[;&|]\s*|&&\s*)rm\s+((?:-[a-zA-Z]+\s+)*)([^;&|]+)`)

// shellRemovals names the files an `rm` deleted, when the command is simple
// enough to be sure.
//
// Deletion is the one change Claude Code does not record: there is no delete
// tool, so an `rm` buried in a shell command is the only evidence. Anything
// with a glob, a variable or a substitution is skipped rather than guessed at —
// reporting the wrong file as deleted is worse than reporting nothing.
func shellRemovals(cmd string) []string {
	var out []string
	for _, m := range rmCommand.FindAllStringSubmatch(cmd, -1) {
		for _, arg := range strings.Fields(m[2]) {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if strings.ContainsAny(arg, "*?[]{}$`\"'~") {
				continue // a glob or an expansion: which files is unknowable here
			}
			out = append(out, arg)
		}
	}
	return out
}

// absolutePath resolves a transcript path against the session's directory.
func absolutePath(path, cwd string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}

// TreeNode is one entry in a project tree.
type TreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Children []*TreeNode

	// Missing marks a file the session touched that is no longer on disk —
	// deleted since, or renamed. It is shown because the session's history is
	// still worth seeing.
	Missing bool
}

// skippedDirs are directories whose contents say nothing about the work and
// would swamp the tree.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true,
	"venv": true, "__pycache__": true, ".next": true, ".nuxt": true,
	"dist": true, "build": true, "target": true, ".terraform": true,
	".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true,
	".gradle": true, ".idea": true, "obj": true, "bin": true,
}

// Tree scanning limits. A large repository would otherwise stall the UI, and a
// tree too big to navigate is no use anyway.
const (
	treeMaxEntries = 4000
	treeMaxDepth   = 8
)

// ScanTree reads the directory at root, skipping build output and version
// control, and grafts on any touched path that is no longer there.
//
// touched is used for two things: deciding to descend into a directory that
// would otherwise be skipped, and adding back files that have since been
// deleted. Passing nil gives a plain listing.
func ScanTree(root string, touched map[string]*FileActivity) *TreeNode {
	if root == "" {
		return nil
	}
	rootNode := &TreeNode{
		Name:  filepath.Base(root),
		Path:  filepath.Clean(root),
		IsDir: true,
	}

	budget := treeMaxEntries
	scanInto(rootNode, 0, &budget, touched)

	// Files the session touched that are not on disk any more.
	for path := range touched {
		if !strings.HasPrefix(path, rootNode.Path+string(filepath.Separator)) {
			continue
		}
		if findNode(rootNode, path) != nil {
			continue
		}
		graft(rootNode, path)
	}

	sortTree(rootNode)
	return rootNode
}

func scanInto(dir *TreeNode, depth int, budget *int, touched map[string]*FileActivity) {
	if depth >= treeMaxDepth || *budget <= 0 {
		return
	}
	entries, err := os.ReadDir(dir.Path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if *budget <= 0 {
			return
		}
		name := entry.Name()
		path := filepath.Join(dir.Path, name)

		if entry.IsDir() {
			// A skipped directory is still worth entering if the session
			// changed something inside it.
			if skippedDirs[name] && !touchedUnder(path, touched) {
				continue
			}
			if strings.HasPrefix(name, ".") && name != "." && !touchedUnder(path, touched) {
				continue
			}
			child := &TreeNode{Name: name, Path: path, IsDir: true}
			*budget--
			scanInto(child, depth+1, budget, touched)
			dir.Children = append(dir.Children, child)
			continue
		}

		if strings.HasPrefix(name, ".") && touched[path] == nil {
			continue
		}
		dir.Children = append(dir.Children, &TreeNode{Name: name, Path: path})
		*budget--
	}
}

// touchedUnder reports whether the session changed anything inside a directory.
func touchedUnder(dir string, touched map[string]*FileActivity) bool {
	prefix := dir + string(filepath.Separator)
	for path := range touched {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func findNode(n *TreeNode, path string) *TreeNode {
	if n.Path == path {
		return n
	}
	for _, c := range n.Children {
		if strings.HasPrefix(path, c.Path) {
			if found := findNode(c, path); found != nil {
				return found
			}
		}
	}
	return nil
}

// graft adds a path that is not on disk, creating any directories it needs.
func graft(root *TreeNode, path string) {
	rel, err := filepath.Rel(root.Path, path)
	if err != nil {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	node := root
	for i, part := range parts {
		last := i == len(parts)-1
		var next *TreeNode
		for _, c := range node.Children {
			if c.Name == part {
				next = c
				break
			}
		}
		if next == nil {
			next = &TreeNode{
				Name:    part,
				Path:    filepath.Join(node.Path, part),
				IsDir:   !last,
				Missing: true,
			}
			node.Children = append(node.Children, next)
		}
		node = next
	}
}

// sortTree puts directories first, then files, each alphabetically — the order
// a file manager uses, so the tree reads the way the project does.
func sortTree(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}
