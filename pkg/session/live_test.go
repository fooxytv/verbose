package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// liveFixture covers what a read can be cut in the middle of: usage repeated
// across one message's lines, a call whose result arrives lines later, one
// entry carrying two results, a background agent launch, a sidechain entry, a
// turn duration and an attachment.
const liveFixture = splitMessageTranscript +
	`{"type":"assistant","uuid":"a5","timestamp":"2026-01-01T10:00:07.000Z","message":{"id":"msg_2","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t4","name":"Write","input":{"file_path":"/repo/b.go","content":"package b"}},{"type":"tool_use","id":"t5","name":"Skill","input":{"skill":"review"}}],"usage":{"input_tokens":5,"output_tokens":7}}}
{"type":"user","uuid":"u4","timestamp":"2026-01-01T10:00:08.000Z","toolUseResult":{"filePath":"/repo/b.go","structuredPatch":[{"oldStart":0,"oldLines":0,"newStart":1,"newLines":1,"lines":["+package b"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t4","content":"written"},{"type":"tool_result","tool_use_id":"t5","content":"loaded"}]}}
{"type":"assistant","uuid":"a6","timestamp":"2026-01-01T10:00:09.000Z","message":{"id":"msg_3","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu_X","name":"Agent","input":{"subagent_type":"scout"}}]}}
{"type":"user","uuid":"u5","timestamp":"2026-01-01T10:00:10.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_X","content":[{"type":"text","text":"Async agent launched.\nagentId: abc123def4567890"}]}]}}
{"type":"assistant","uuid":"a7","isSidechain":true,"timestamp":"2026-01-01T10:00:11.000Z","message":{"id":"msg_4","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"inline work"}]}}
{"type":"system","subtype":"turn_duration","uuid":"y1","timestamp":"2026-01-01T10:00:12.000Z","durationMs":12000}
{"type":"attachment","uuid":"at1","timestamp":"2026-01-01T10:00:13.000Z","attachment":{"type":"edited_text_file","filename":"/repo/c.go","snippet":"x := 1"}}

{"type":"user","uuid":"u6","timestamp":"2026-01-01T10:00:14.000Z","message":{"role":"user","content":"and then"}}
`

// sameSession fails unless got and want hold the same session. The file lists
// are built from map iteration, so their order is not part of the result.
func sameSession(t *testing.T, label string, got, want *Session) {
	t.Helper()
	norm := func(s *Session) Session {
		c := *s
		for _, list := range []*[]string{&c.Info.FilesRead, &c.Info.FilesWritten, &c.Info.FilesCreated} {
			*list = append([]string(nil), (*list)...)
			sort.Strings(*list)
		}
		return c
	}
	g, w := norm(got), norm(want)
	if !reflect.DeepEqual(g.Info, w.Info) {
		t.Fatalf("%s: info differs\n got %+v\nwant %+v", label, g.Info, w.Info)
	}
	if len(g.Events) != len(w.Events) {
		t.Fatalf("%s: %d events, want %d", label, len(g.Events), len(w.Events))
	}
	for i := range g.Events {
		if !reflect.DeepEqual(g.Events[i], w.Events[i]) {
			t.Fatalf("%s: event %d differs\n got %+v\nwant %+v", label, i, g.Events[i], w.Events[i])
		}
	}
	if !reflect.DeepEqual(g.SubagentLaunches, w.SubagentLaunches) {
		t.Fatalf("%s: launches %v, want %v", label, g.SubagentLaunches, w.SubagentLaunches)
	}
}

func fullParse(t *testing.T, path string) *Session {
	t.Helper()
	sess, err := ParseSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// Growing a transcript a few bytes at a time — cutting lines, and JSON, at
// every point — must leave the incremental parse equal to a full parse of the
// same bytes after every single read.
func TestLiveParseMatchesFullParseAsTheFileGrows(t *testing.T) {
	for _, step := range []int{11, 64, 333} {
		path := writeTranscript(t, "")
		s := &Store{}
		for n := 0; n <= len(liveFixture); n += step {
			if n > len(liveFixture) {
				n = len(liveFixture)
			}
			if err := os.WriteFile(path, []byte(liveFixture[:n]), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := s.parseLive(path)
			if err != nil {
				t.Fatal(err)
			}
			sameSession(t, "step "+strconv.Itoa(step)+" at byte "+strconv.Itoa(n), got, fullParse(t, path))
		}
		// And the whole file, whatever the stride left off.
		if err := os.WriteFile(path, []byte(liveFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := s.parseLive(path)
		if err != nil {
			t.Fatal(err)
		}
		sameSession(t, "complete", got, fullParse(t, path))
	}
}

// A session handed out must not share anything with the parse that is kept:
// what is done to it afterwards would otherwise be done again, to the same
// data, on the next read.
func TestLiveParseHandsOutIndependentSessions(t *testing.T) {
	path := writeTranscript(t, liveFixture)
	s := &Store{}
	first, err := s.parseLive(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Events {
		e := &first.Events[i]
		e.Text = "scribbled"
		for k := range e.ToolInput {
			e.ToolInput[k] = "scribbled"
		}
		if e.Result != nil {
			e.Result.FilePath = "scribbled"
			for h := range e.Result.StructuredPatch {
				for l := range e.Result.StructuredPatch[h].Lines {
					e.Result.StructuredPatch[h].Lines[l] = "scribbled"
				}
			}
		}
	}
	first.Info.ToolCounts["Read"] = 99
	first.SubagentLaunches["toolu_X"] = "scribbled"

	again, err := s.parseLive(path)
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "after scribbling on the first", again, fullParse(t, path))
}

// Relinking is not idempotent — a folder moved INTO itself turns /repo into
// /repo/moved, and a second pass makes /repo/moved/moved — so the store must
// relink each read once, from the transcript as written. Redaction rides along
// to cover the other half of prepare.
func TestReloadRelinksEachReadOnce(t *testing.T) {
	root := testRoot()
	moved := filepath.Join(root, "moved")
	file := filepath.Join(root, "a.go")
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	path := writeTranscript(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00Z","cwd":`+q(root)+`,"message":{"role":"user","content":"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 please"}}`+"\n"+
			`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T10:00:01Z","message":{"id":"m1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":`+q(file)+`}}]}}`+"\n")

	s := &Store{
		sessions: make(map[string]*Session),
		updates:  make(chan struct{}, 1),
		relinks:  Relinks{{From: root, To: moved}},
		redact:   true,
	}
	s.reload(path)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","uuid":"u2","timestamp":"2026-01-01T10:00:02Z","toolUseResult":{"filePath":` + q(file) + `,"structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.reload(path)
	s.reload(path)

	got := s.GetSession("sess")
	if got == nil {
		t.Fatal("session not in the store after reload")
	}
	want := fullParse(t, path)
	s.prepare(want)
	sameSession(t, "after three reloads", got, want)

	if got.Info.CWD != moved {
		t.Fatalf("cwd %q, want %q", got.Info.CWD, moved)
	}
	if fp, _ := got.Events[1].ToolInput["file_path"].(string); fp != filepath.Join(moved, "a.go") {
		t.Fatalf("file_path %q, want it relinked exactly once", fp)
	}
}

// Claude Code only appends, so a file shorter than what was read has been
// replaced, and must be read again from its start.
func TestLiveParseRereadsAReplacedFile(t *testing.T) {
	path := writeTranscript(t, liveFixture)
	s := &Store{}
	if _, err := s.parseLive(path); err != nil {
		t.Fatal(err)
	}
	short := `{"type":"user","uuid":"z1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"a different session"}}` + "\n"
	if err := os.WriteFile(path, []byte(short), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.parseLive(path)
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "replaced", got, fullParse(t, path))
}

// The real transcripts on this machine, cut part way through a line and then
// completed, must read the same incrementally as in one go. Skipped where there
// are none. Large files are left out to keep the suite quick; the cut is what
// is being tested, not the size.
func TestLiveParseMatchesFullParseOnRealTranscripts(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	var files []string
	_ = filepath.WalkDir(filepath.Join(home, ".claude", "projects"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > 0 && info.Size() < 8<<20 {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no transcripts on this machine")
	}
	const sample = 12
	stride := len(files)/sample + 1

	for i := 0; i < len(files); i += stride {
		body, err := os.ReadFile(files[i])
		if err != nil {
			continue
		}
		path := writeTranscript(t, "")
		s := &Store{}
		for _, cut := range []int{len(body) / 3, len(body) * 2 / 3, len(body)} {
			if err := os.WriteFile(path, body[:cut], 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := s.parseLive(path)
			if err != nil {
				t.Fatal(err)
			}
			sameSession(t, filepath.Base(files[i])+" cut at "+strconv.Itoa(cut), got, fullParse(t, path))
		}
	}
}
