package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Relinking: a folder renamed or moved after a session ran in it.
//
// A transcript records the absolute folder it ran in, and every file it touched
// by absolute path. Rename the folder and all of it points at nothing: the CLI
// cannot be started there, the tree roots at a directory that is gone, and the
// editor has no file to open. Claude Code itself copes — `claude --resume <id>`
// finds a session from any folder and records the new one on the lines it
// appends — so the transcripts are left alone. What gets fixed is verbose's
// reading of them: a link from the old path to the new is kept in verbose's own
// config, and applied to each session as the store loads it.

// Relink maps one folder's old absolute path to where it is now.
type Relink struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Relinks is every link, kept longest From first so that a link for a folder
// wins over one for its parent.
type Relinks []Relink

// RelinksPath is where the links are kept. It hangs off the home directory,
// like everything else verbose reads, so a test can redirect it.
func RelinksPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "verbose", "relinks.json"), nil
}

// LoadRelinks reads the links. A missing file is no links, not an error.
func LoadRelinks() (Relinks, error) {
	path, err := RelinksPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var links Relinks
	if err := json.Unmarshal(data, &links); err != nil {
		return nil, err
	}
	links.sort()
	return links, nil
}

// Save writes the links back.
func (r Relinks) Save() error {
	path, err := RelinksPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	r.sort()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Add links from to to, replacing any link already made for from.
func (r Relinks) Add(from, to string) Relinks {
	r = r.Remove(from)
	r = append(r, Relink{From: filepath.Clean(from), To: filepath.Clean(to)})
	r.sort()
	return r
}

// Remove drops the link for from, if there is one.
func (r Relinks) Remove(from string) Relinks {
	from = filepath.Clean(from)
	out := r[:0:0]
	for _, l := range r {
		if !samePath(l.From, from) {
			out = append(out, l)
		}
	}
	return out
}

func (r Relinks) sort() {
	sort.SliceStable(r, func(i, j int) bool { return len(r[i].From) > len(r[j].From) })
}

// Path rewrites one path, or text that contains paths, through the links.
func (r Relinks) Path(s string) string {
	for _, l := range r {
		s = replacePathPrefix(s, l.From, l.To)
		// A shell command on Windows may spell the same folder with forward
		// slashes; the link has to catch that too or an `rm` goes unrecognised.
		if filepath.Separator == '\\' {
			s = replacePathPrefix(s, filepath.ToSlash(l.From), filepath.ToSlash(l.To))
		}
	}
	return s
}

// Apply rewrites every path a session carries. It runs once, as the store
// loads the session, so nothing downstream needs to know links exist.
func (r Relinks) Apply(sess *Session) {
	if len(r) == 0 || sess == nil {
		return
	}
	info := &sess.Info
	info.CWD = r.Path(info.CWD)
	for _, list := range [][]string{info.FilesRead, info.FilesWritten, info.FilesCreated} {
		for i := range list {
			list[i] = r.Path(list[i])
		}
	}
	for i := range info.FileChurns {
		info.FileChurns[i].Path = r.Path(info.FileChurns[i].Path)
	}

	for i := range sess.Events {
		e := &sess.Events[i]
		e.FilePath = r.Path(e.FilePath)
		e.ToolOutput = r.Path(e.ToolOutput)
		e.ToolInput = r.input(e.ToolInput)
		for j := range e.Diagnostics {
			e.Diagnostics[j].File = r.Path(e.Diagnostics[j].File)
		}
		if res := e.Result; res != nil {
			res.FilePath = r.Path(res.FilePath)
			if res.File != nil {
				res.File.FilePath = r.Path(res.File.FilePath)
			}
			if res.BashEdit != nil {
				for j := range res.BashEdit.Files {
					res.BashEdit.Files[j].FilePath = r.Path(res.BashEdit.Files[j].FilePath)
				}
			}
		}
	}
}

// input rewrites the string values of a tool's input, however deeply nested:
// a file_path, a command line, a list of edits each with its own path.
func (r Relinks) input(v map[string]interface{}) map[string]interface{} {
	for k, val := range v {
		v[k] = r.value(val)
	}
	return v
}

func (r Relinks) value(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		return r.Path(t)
	case map[string]interface{}:
		return r.input(t)
	case []interface{}:
		for i := range t {
			t[i] = r.value(t[i])
		}
	}
	return v
}

// replacePathPrefix replaces each occurrence of from in s that is a whole path
// prefix: followed by a separator or by anything that cannot continue a file
// name. Without that check, relinking C:\work\app would also rewrite
// C:\work\app-old, a different folder that merely shares the start.
func replacePathPrefix(s, from, to string) string {
	if from == "" || from == to || len(s) < len(from) {
		return s
	}
	// Windows paths are case-insensitive and transcripts do not agree on the
	// drive letter's case. Lowering is only safe when it keeps byte offsets.
	hay, needle := s, from
	if runtime.GOOS == "windows" {
		if l, n := strings.ToLower(s), strings.ToLower(from); len(l) == len(s) && len(n) == len(from) {
			hay, needle = l, n
		}
	}

	var b strings.Builder
	last := 0
	for i := 0; i+len(needle) <= len(hay); {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		start, end := i+j, i+j+len(needle)
		if end == len(s) || !continuesName(s[end]) {
			b.WriteString(s[last:start])
			b.WriteString(to)
			last = end
		}
		i = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// continuesName reports whether c could be the next character of a file or
// folder name, which would make a match a different, longer name.
func continuesName(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.' || c == '~' || c >= 0x80:
		return true
	}
	return false
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// MissingFolder is a folder sessions ran in that is no longer on disk, scored
// against a candidate new location.
type MissingFolder struct {
	Path       string
	Sessions   int
	LastActive time.Time
	// Files the sessions touched inside the folder, and how many of them exist
	// at the same place under the candidate. A renamed folder keeps its
	// contents, so a high share is the evidence that this is the one.
	Files int
	Found int
}

// MissingFolders lists the folders sessions ran in that no longer exist, most
// likely to be the old name of here first.
func MissingFolders(infos []SessionInfo, here string) []MissingFolder {
	byPath := map[string]*MissingFolder{}
	files := map[string]map[string]bool{}
	exists := map[string]bool{}

	for _, info := range infos {
		cwd := info.CWD
		if cwd == "" {
			continue
		}
		gone, seen := exists[cwd]
		if !seen {
			fi, err := os.Stat(cwd)
			gone = err != nil || !fi.IsDir()
			exists[cwd] = gone
		}
		if !gone {
			continue
		}
		m := byPath[cwd]
		if m == nil {
			m = &MissingFolder{Path: cwd}
			byPath[cwd] = m
			files[cwd] = map[string]bool{}
		}
		m.Sessions++
		if info.LastUpdate.After(m.LastActive) {
			m.LastActive = info.LastUpdate
		}
		for _, list := range [][]string{info.FilesRead, info.FilesWritten, info.FilesCreated} {
			for _, p := range list {
				if rel, err := filepath.Rel(cwd, absolutePath(p, cwd)); err == nil && !strings.HasPrefix(rel, "..") {
					files[cwd][rel] = true
				}
			}
		}
	}

	out := make([]MissingFolder, 0, len(byPath))
	for cwd, m := range byPath {
		for rel := range files[cwd] {
			m.Files++
			if _, err := os.Stat(filepath.Join(here, rel)); err == nil {
				m.Found++
			}
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Found != out[j].Found {
			return out[i].Found > out[j].Found
		}
		// Nothing to tell them apart by contents: a folder with the same name
		// one level over is the next best sign, then whichever was used last.
		si, sj := filepath.Base(out[i].Path) == filepath.Base(here), filepath.Base(out[j].Path) == filepath.Base(here)
		if si != sj {
			return si
		}
		return out[i].LastActive.After(out[j].LastActive)
	})
	return out
}
