package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRelinkPathRespectsNameBoundaries(t *testing.T) {
	old := filepath.Join(testRoot(), "app")
	moved := filepath.Join(testRoot(), "renamed")
	r := Relinks{}.Add(old, moved)

	cases := map[string]string{
		old:                                    moved,
		filepath.Join(old, "src", "main.go"):   filepath.Join(moved, "src", "main.go"),
		"rm " + filepath.Join(old, "a.go"):     "rm " + filepath.Join(moved, "a.go"),
		`"` + old + `" and ` + old:             `"` + moved + `" and ` + moved,
		filepath.Join(testRoot(), "app-old"):   filepath.Join(testRoot(), "app-old"),
		filepath.Join(testRoot(), "app.bak"):   filepath.Join(testRoot(), "app.bak"),
		filepath.Join(testRoot(), "apparatus"): filepath.Join(testRoot(), "apparatus"),
		"":                                     "",
	}
	if runtime.GOOS == "windows" {
		// The same folder as a shell command spells it, and in another case.
		cases[filepath.ToSlash(old)+"/x.go"] = filepath.ToSlash(moved) + "/x.go"
		cases[strings.ToLower(old)+`\x.go`] = moved + `\x.go`
	}
	for in, want := range cases {
		if got := r.Path(in); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
}

// A link for a folder must win over one for its parent, whatever order they
// were made in.
func TestRelinkLongestFirst(t *testing.T) {
	parent := filepath.Join(testRoot(), "work")
	child := filepath.Join(parent, "app")
	r := Relinks{}.Add(parent, filepath.Join(testRoot(), "jobs")).Add(child, filepath.Join(testRoot(), "app2"))
	if got, want := r.Path(filepath.Join(child, "x.go")), filepath.Join(testRoot(), "app2", "x.go"); got != want {
		t.Errorf("child link: got %q, want %q", got, want)
	}
	if got, want := r.Path(filepath.Join(parent, "y.go")), filepath.Join(testRoot(), "jobs", "y.go"); got != want {
		t.Errorf("parent link: got %q, want %q", got, want)
	}
}

func TestRelinkApplyRewritesEveryPath(t *testing.T) {
	old := filepath.Join(testRoot(), "app")
	moved := filepath.Join(testRoot(), "renamed")
	at := func(p string) string { return filepath.Join(old, p) }
	to := func(p string) string { return filepath.Join(moved, p) }

	sess := &Session{
		Info: SessionInfo{
			CWD:          old,
			FilesRead:    []string{at("r.go")},
			FilesWritten: []string{at("w.go")},
			FilesCreated: []string{at("c.go")},
			FileChurns:   []FileChurn{{Path: at("w.go")}},
		},
		Events: []Event{{
			FilePath:   at("u.go"),
			ToolOutput: "wrote " + at("w.go"),
			ToolInput: map[string]interface{}{
				"file_path": at("w.go"),
				"command":   "rm " + at("gone.go"),
				"edits":     []interface{}{map[string]interface{}{"file_path": at("m.go")}},
				"limit":     float64(5),
			},
			Diagnostics: []Diagnostic{{File: at("d.go")}},
			Result: &ToolResult{
				FilePath: at("w.go"),
				File:     &ReadFile{FilePath: at("r.go")},
				BashEdit: &BashEditDiff{Files: []ChangedFile{{FilePath: at("b.go")}}},
			},
		}},
	}
	Relinks{}.Add(old, moved).Apply(sess)

	e := sess.Events[0]
	edits := e.ToolInput["edits"].([]interface{})
	checks := []struct{ name, got, want string }{
		{"CWD", sess.Info.CWD, moved},
		{"FilesRead", sess.Info.FilesRead[0], to("r.go")},
		{"FilesWritten", sess.Info.FilesWritten[0], to("w.go")},
		{"FilesCreated", sess.Info.FilesCreated[0], to("c.go")},
		{"FileChurns", sess.Info.FileChurns[0].Path, to("w.go")},
		{"Event.FilePath", e.FilePath, to("u.go")},
		{"ToolOutput", e.ToolOutput, "wrote " + to("w.go")},
		{"input file_path", e.ToolInput["file_path"].(string), to("w.go")},
		{"input command", e.ToolInput["command"].(string), "rm " + to("gone.go")},
		{"nested edit", edits[0].(map[string]interface{})["file_path"].(string), to("m.go")},
		{"Diagnostics", e.Diagnostics[0].File, to("d.go")},
		{"Result.FilePath", e.Result.FilePath, to("w.go")},
		{"Result.File", e.Result.File.FilePath, to("r.go")},
		{"BashEdit", e.Result.BashEdit.Files[0].FilePath, to("b.go")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if e.ToolInput["limit"] != float64(5) {
		t.Errorf("a number in the input changed: %v", e.ToolInput["limit"])
	}
}

func TestRelinksSaveLoadRemove(t *testing.T) {
	home := isolatedHome(t)
	if links, err := LoadRelinks(); err != nil || len(links) != 0 {
		t.Fatalf("no file: got %v, %v; want no links and no error", links, err)
	}

	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	if err := (Relinks{}).Add(a, b).Save(); err != nil {
		t.Fatal(err)
	}
	links, err := LoadRelinks()
	if err != nil || len(links) != 1 || links[0].From != a || links[0].To != b {
		t.Fatalf("round trip = %v, %v", links, err)
	}
	// Linking the same folder again replaces the link rather than adding one.
	if got := links.Add(a, filepath.Join(home, "c")); len(got) != 1 {
		t.Errorf("relinking a folder kept %d links, want 1", len(got))
	}
	if got := links.Remove(a); len(got) != 0 {
		t.Errorf("remove left %v", got)
	}
}

func TestMissingFoldersRanksByFilesFoundHere(t *testing.T) {
	root := t.TempDir()
	here := filepath.Join(root, "new-name")
	writeFile(t, filepath.Join(here, "main.go"), "")
	writeFile(t, filepath.Join(here, "pkg", "util.go"), "")
	exists := filepath.Join(root, "still-here")
	if err := os.MkdirAll(exists, 0o755); err != nil {
		t.Fatal(err)
	}

	renamed := filepath.Join(root, "old-name")
	unrelated := filepath.Join(root, "scratch")
	now := time.Now()
	infos := []SessionInfo{
		// The unrelated folder is the more recent, so only the files can rank
		// the renamed one first.
		{CWD: unrelated, LastUpdate: now, FilesWritten: []string{filepath.Join(unrelated, "notes.md")}},
		{CWD: renamed, LastUpdate: now.Add(-time.Hour), FilesWritten: []string{filepath.Join(renamed, "main.go")}},
		{CWD: renamed, LastUpdate: now.Add(-2 * time.Hour), FilesRead: []string{"pkg/util.go", filepath.Join(renamed, "gone.go")}},
		{CWD: exists, LastUpdate: now},
		{CWD: ""},
	}

	got := MissingFolders(infos, here)
	if len(got) != 2 {
		t.Fatalf("got %d folders, want the 2 that are gone: %+v", len(got), got)
	}
	if got[0].Path != renamed {
		t.Fatalf("best match = %s, want %s", got[0].Path, renamed)
	}
	if got[0].Sessions != 2 || got[0].Files != 3 || got[0].Found != 2 {
		t.Errorf("renamed folder = %+v, want 2 sessions, 2 of 3 files found", got[0])
	}
	if got[1].Path != unrelated || got[1].Found != 0 {
		t.Errorf("second = %+v, want the unrelated folder with nothing found", got[1])
	}
}

// The store is where links take effect: a session loaded from disk arrives with
// its paths already pointing at the new folder.
func TestStoreAppliesRelinks(t *testing.T) {
	home := isolatedHome(t)
	old := filepath.Join(home, "old-name")
	moved := filepath.Join(home, "new-name")
	if err := (Relinks{}).Add(old, moved).Save(); err != nil {
		t.Fatal(err)
	}

	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	base := filepath.Join(home, ".claude", "projects")
	writeFile(t, filepath.Join(base, "-old-name", "sess-1.jsonl"),
		`{"type":"user","uuid":"u1","sessionId":"sess-1","cwd":`+quote(old)+`,"timestamp":"2026-10-01T12:00:00Z",`+
			`"message":{"role":"user","content":"read it"}}`+"\n"+
			`{"type":"assistant","uuid":"a1","sessionId":"sess-1","cwd":`+quote(old)+`,"timestamp":"2026-10-01T12:00:10Z",`+
			`"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use",`+
			`"id":"toolu_1","name":"Read","input":{"file_path":`+quote(filepath.Join(old, "main.go"))+`}}]}}`+"\n")

	sess := scanned(t, base).GetSession("sess-1")
	if sess == nil {
		t.Fatal("session not loaded")
	}
	if sess.Info.CWD != moved {
		t.Errorf("CWD = %q, want %q", sess.Info.CWD, moved)
	}
	var read string
	for _, e := range sess.Events {
		if e.ToolName == "Read" {
			read, _ = e.ToolInput["file_path"].(string)
		}
	}
	if want := filepath.Join(moved, "main.go"); read != want {
		t.Errorf("Read file_path = %q, want %q", read, want)
	}
}
