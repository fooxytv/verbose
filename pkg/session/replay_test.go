package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildReplaySkipsBookkeepingAndEmptyThinking(t *testing.T) {
	root := testRoot()
	sess := &Session{
		Info: SessionInfo{CWD: root},
		Events: []Event{
			{Type: EventUserPrompt, UserText: "add a health check"},
			// Extended thinking is signed but never retained, so every thinking
			// block on disk looks like this. A step with no body teaches nothing.
			{Type: EventThinking, Thinking: ""},
			{Type: EventTurnDuration, TurnDurationMs: 1200},
			{Type: EventHookProgress, HookName: "PostToolUse:Read"},
			{Type: EventBashProgress, BashElapsedSec: 3},
			{Type: EventSystem},
			{Type: EventText, Text: "I'll add the endpoint first."},
			{Type: EventToolUse, ToolName: "Write", ToolInput: map[string]interface{}{
				"file_path": filepath.Join(root, "health.go")}},
		},
	}

	steps := BuildReplay(sess)
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3 (prompt, narration, write): %+v", len(steps), steps)
	}
	want := []ReplayKind{StepGoal, StepPlan, StepAction}
	for i, k := range want {
		if steps[i].Kind != k {
			t.Errorf("step %d kind = %v, want %v", i, steps[i].Kind, k)
		}
	}
	if steps[2].Title != "Created health.go" {
		t.Errorf("title = %q, want %q", steps[2].Title, "Created health.go")
	}
}

func TestBuildReplayMarksFailuresAsProblems(t *testing.T) {
	sess := &Session{
		Events: []Event{
			// A failing shell command leaves is_error false and records the
			// error as a bare string, so Raw is the signal that matters.
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{"command": "make build"},
				Result:    &ToolResult{Raw: "Error: Exit code 127"}},
			{Type: EventToolDenied, ToolName: "Write", UserFeedback: "not that file"},
		},
	}

	steps := BuildReplay(sess)
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(steps))
	}
	for i, s := range steps {
		if s.Kind != StepProblem {
			t.Errorf("step %d kind = %v, want StepProblem", i, s.Kind)
		}
	}
	if !strings.Contains(steps[0].Detail, "Exit code 127") {
		t.Errorf("failure detail lost the error text: %q", steps[0].Detail)
	}
	if !strings.Contains(steps[1].Detail, "not that file") {
		t.Errorf("denial detail lost the feedback: %q", steps[1].Detail)
	}
}

func TestDescribeEventReportsRealDiffNotRequestedEdit(t *testing.T) {
	e := Event{
		Type:      EventToolUse,
		ToolName:  "Edit",
		ToolInput: map[string]interface{}{"file_path": "/repo/storage.tf"},
		Result: &ToolResult{
			FilePath: "/repo/storage.tf",
			StructuredPatch: []PatchHunk{{
				OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 3,
				Lines: []string{" ctx", "-old", "+new", "+extra"},
			}},
		},
	}

	got := DescribeEvent(e, "/repo")
	for _, want := range []string{"storage.tf", "2 line(s) added", "1 removed", "1 hunk(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe = %q, missing %q", got, want)
		}
	}
}

func TestDescribeEventReportsPartialRead(t *testing.T) {
	e := Event{
		Type:     EventToolUse,
		ToolName: "Read",
		Result: &ToolResult{File: &ReadFile{
			FilePath: "/repo/main.go", NumLines: 50, StartLine: 1, TotalLines: 400,
		}},
	}
	got := DescribeEvent(e, "/repo")
	if !strings.Contains(got, "50 of its 400 lines") {
		t.Errorf("describe = %q, want it to say only part of the file was read", got)
	}
}

// Paths are shown relative to the session's cwd, so a step reads as a filename
// rather than a full home-directory path.
func TestRelPathShortensAgainstCWD(t *testing.T) {
	root := testRoot()
	// Outside the project, so it is returned whole rather than shortened.
	elsewhere := filepath.Join(filepath.Dir(root), "elsewhere", "c.go")

	cases := []struct{ path, cwd, want string }{
		// relPath produces DISPLAY text, so it carries the platform's
		// separator: a Windows reader looking at a Windows transcript wants
		// a\b.go, not a/b.go. The expectation is built the same way rather
		// than hardcoding a slash.
		{filepath.Join(root, "a", "b.go"), root, filepath.Join("a", "b.go")},
		{elsewhere, root, elsewhere},
		{"", root, "a file"},
	}
	for _, c := range cases {
		if got := relPath(c.path, c.cwd); got != c.want {
			t.Errorf("relPath(%q, %q) = %q, want %q", c.path, c.cwd, got, c.want)
		}
	}
}

func TestBuildReplayCarriesSubagentFlagAndOrder(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	sess := &Session{
		Events: []Event{
			{Type: EventToolUse, ToolName: "Agent", Timestamp: base,
				ToolInput: map[string]interface{}{"subagent_type": "general-purpose"}},
			{Type: EventToolUse, ToolName: "Grep", Timestamp: base.Add(time.Second),
				ToolInput: map[string]interface{}{"pattern": "func"}, IsSidechain: true},
			{Type: EventText, Text: "done", Timestamp: base.Add(2 * time.Second)},
		},
	}

	steps := BuildReplay(sess)
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[0].IsSidechain {
		t.Error("the dispatching call is the parent's, not the subagent's")
	}
	if !steps[1].IsSidechain {
		t.Error("subagent step lost its sidechain flag")
	}
	// Steps must stay in transcript order: the replay is a narrative.
	for i := 1; i < len(steps); i++ {
		if steps[i].EventIndex <= steps[i-1].EventIndex {
			t.Fatalf("steps out of order at %d", i)
		}
	}
}

func TestBuildReplayHandlesNilAndEmpty(t *testing.T) {
	if got := BuildReplay(nil); got != nil {
		t.Errorf("BuildReplay(nil) = %v, want nil", got)
	}
	if got := BuildReplay(&Session{}); len(got) != 0 {
		t.Errorf("BuildReplay(empty) = %v, want no steps", got)
	}
}

// A prompt that is nothing but command and reminder tags strips down to
// nothing. Filtering on the raw text let those through as a headline with an
// empty body.
func TestBuildReplayDropsPromptsThatStripToNothing(t *testing.T) {
	sess := &Session{
		Events: []Event{
			{Type: EventUserPrompt, UserText: "<system-reminder>noise</system-reminder>"},
			{Type: EventUserPrompt, UserText: "<command-name>/clear</command-name>"},
			{Type: EventUserPrompt, UserText: "real question"},
		},
	}
	steps := BuildReplay(sess)
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1; tag-only prompts should be dropped", len(steps))
	}
	if steps[0].Detail != "real question" {
		t.Errorf("detail = %q, want %q", steps[0].Detail, "real question")
	}
}

// Inside a subagent run the "user" is the agent that dispatched it.
func TestReplayTitleAttributesSubagentBriefCorrectly(t *testing.T) {
	own := Event{Type: EventUserPrompt, UserText: "do the thing"}
	if got := replayTitle(own, ""); got != "You asked" {
		t.Errorf("own prompt title = %q, want %q", got, "You asked")
	}

	brief := Event{Type: EventUserPrompt, UserText: "do the thing", IsSidechain: true}
	if got := replayTitle(brief, ""); got != "The subagent's brief" {
		t.Errorf("subagent prompt title = %q, want %q", got, "The subagent's brief")
	}
}

// collapseSpaces turns newlines into spaces, so splitting after it flattened a
// whole heredoc — body included — onto one line and called it the command.
func TestFirstCommandLineDoesNotFlattenHeredocs(t *testing.T) {
	cmd := "cat > app.js <<'EOF'\nconst x = 1\nconsole.log(x)\nEOF"
	if got := firstCommandLine(cmd); got != "cat > app.js <<'EOF'" {
		t.Errorf("firstCommandLine = %q, want just the invocation", got)
	}
}

func TestShellHeredocExtractsBodyAndTarget(t *testing.T) {
	cases := []struct {
		name, cmd, wantPath, wantBody string
	}{{
		name:     "cat to a file",
		cmd:      "cat > src/app.js <<'EOF'\nconst a = 1\nconst b = 2\nEOF",
		wantPath: "src/app.js",
		wantBody: "const a = 1\nconst b = 2",
	}, {
		// Piped to an interpreter: real code, but it writes no file, so
		// claiming a path would be wrong.
		name:     "python on stdin",
		cmd:      "python3 - <<'PY'\nprint(1)\nPY",
		wantPath: "",
		wantBody: "print(1)",
	}, {
		name:     "append with a compound command",
		cmd:      "mkdir -p d && cat >> d/notes.md <<'MD'\n# hi\nMD",
		wantPath: "d/notes.md",
		wantBody: "# hi",
	}, {
		name:     "indented terminator",
		cmd:      "cat > f.txt <<-END\n\tbody\n\tEND",
		wantPath: "f.txt",
		wantBody: "\tbody",
	}, {
		name:     "no heredoc at all",
		cmd:      "go test ./...",
		wantPath: "",
		wantBody: "",
	}}

	for _, c := range cases {
		path, body := shellHeredoc(c.cmd)
		if path != c.wantPath || body != c.wantBody {
			t.Errorf("%s: got (%q, %q), want (%q, %q)",
				c.name, path, body, c.wantPath, c.wantBody)
		}
	}
}

// Claude Code records an EMPTY structuredPatch for a brand-new file, so without
// recovering the input the whole file is invisible.
func TestStepCodeRecoversNewFileFromWriteInput(t *testing.T) {
	e := Event{
		Type:     EventToolUse,
		ToolName: "Write",
		ToolInput: map[string]interface{}{
			"file_path": "/repo/new.go", "content": "package main\n",
		},
		Result: &ToolResult{FilePath: "/repo/new.go", StructuredPatch: nil},
	}
	path, code := stepCode(e)
	if path != "/repo/new.go" || code != "package main\n" {
		t.Errorf("stepCode = (%q, %q), want the file and its contents", path, code)
	}
}

// When a real diff exists it says more than the file's full contents, so the
// diff wins and no code is recovered.
func TestStepCodeDefersToARealDiff(t *testing.T) {
	e := Event{
		Type:      EventToolUse,
		ToolName:  "Write",
		ToolInput: map[string]interface{}{"file_path": "/repo/a.go", "content": "whole file"},
		Result: &ToolResult{
			StructuredPatch: []PatchHunk{{Lines: []string{"+one"}}},
		},
	}
	if path, code := stepCode(e); path != "" || code != "" {
		t.Errorf("stepCode = (%q, %q), want empty when a diff exists", path, code)
	}
}

func TestStepCodeRecoversShellWrites(t *testing.T) {
	e := Event{
		Type:      EventToolUse,
		ToolName:  "Bash",
		ToolInput: map[string]interface{}{"command": "cat > x.css <<'CSS'\nbody{}\nCSS"},
	}
	path, code := stepCode(e)
	if path != "x.css" || code != "body{}" {
		t.Errorf("stepCode = (%q, %q), want the shell-written file", path, code)
	}
}

// A step that wrote a file is titled by the file, not by the command that
// carried it — "Ran mkdir -p … && cat > app.js <<'EOF'" says much less.
func TestBuildReplayTitlesShellWritesByFile(t *testing.T) {
	root := testRoot()
	sess := &Session{
		Info: SessionInfo{CWD: root},
		Events: []Event{{
			Type:     EventToolUse,
			ToolName: "Bash",
			ToolInput: map[string]interface{}{
				"command": "mkdir -p " + filepath.Join(root, "src") +
					" && cat > " + filepath.Join(root, "src", "app.js") + " <<'EOF'\nlet a\nEOF",
			},
		}},
	}
	steps := BuildReplay(sess)
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	wantTitle := "Wrote " + filepath.Join("src", "app.js")
	if steps[0].Title != wantTitle {
		t.Errorf("title = %q, want %q", steps[0].Title, wantTitle)
	}
	if steps[0].Code != "let a" {
		t.Errorf("code = %q, want %q", steps[0].Code, "let a")
	}
}

// A heredoc with no target file must not claim one.
func TestBuildReplayLeavesCodePathEmptyForStdinHeredocs(t *testing.T) {
	sess := &Session{
		Events: []Event{{
			Type:      EventToolUse,
			ToolName:  "Bash",
			ToolInput: map[string]interface{}{"command": "python3 - <<'PY'\nprint(1)\nPY"},
		}},
	}
	steps := BuildReplay(sess)
	if steps[0].CodePath != "" {
		t.Errorf("CodePath = %q, want empty: a stdin heredoc writes no file", steps[0].CodePath)
	}
	if steps[0].Code == "" {
		t.Error("the script itself is still code worth showing")
	}
}
