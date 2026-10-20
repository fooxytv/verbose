package session

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// findFixture is a session whose steps can only be told apart by a different
// field each: one by its command, one by what that command printed, one by the
// code it wrote, one by the file it touched, one by the prompt.
func findFixture() (*Session, []ReplayStep) {
	sess := &Session{
		Info: SessionInfo{ID: "s1", Title: "a session", CWD: "/repo"},
		Events: []Event{
			{Type: EventUserPrompt, UserText: "make the migration idempotent"},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput:  map[string]interface{}{"command": "go test ./pkg/..."},
				ToolOutput: "ok  \tgithub.com/x/pkg\t0.2s\n"},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput:  map[string]interface{}{"command": "./deploy.sh"},
				ToolOutput: "Error: Connection REFUSED on port 5432\n"},
			{Type: EventToolUse, ToolName: "Write",
				ToolInput: map[string]interface{}{
					"file_path": "/repo/internal/pool/limiter.go",
					"content":   "package pool\n\nfunc Acquire() {}\n"}},
			{Type: EventToolUse, ToolName: "Bash",
				ToolInput: map[string]interface{}{
					"command": "mkdir -p gen && cat > /repo/gen/ids.go <<'EOF'\n" +
						"package gen\n\nconst Prefix = \"ix\"\nEOF"}},
		},
	}
	return sess, BuildReplay(sess)
}

func TestFindStepsReachesTheCommandAndWhatItPrinted(t *testing.T) {
	sess, steps := findFixture()
	if len(steps) != 5 {
		t.Fatalf("fixture built %d steps, want 5", len(steps))
	}

	// A heredoc step is titled by the file it wrote ("Wrote gen/ids.go"), so
	// the shell that wrote it is in the event's command and nowhere else. A
	// fixture of plain commands cannot tell whether the input is searched at
	// all, because replayTitle quotes those in full.
	if title := steps[4].Title; strings.Contains(title, "mkdir") {
		t.Fatalf("step 5 is titled %q — the heredoc case needs a title "+
			"that does NOT carry the command", title)
	}

	for _, c := range []struct {
		query string
		want  []int
		why   string
	}{
		{"go test", []int{1}, "the command that was run"},
		{"connection refused", []int{2}, "what the command printed — the outcome"},
		{"5432", []int{2}, "a number in the output"},
		{"package pool", []int{3}, "the code the step wrote"},
		{"limiter.go", []int{3}, "the file it wrote to"},
		{"idempotent", []int{0}, "the prompt"},
		{"mkdir", []int{4}, "a command its own step headline leaves out"},
		{"const Prefix", []int{4}, "the code a heredoc wrote"},
		{"deploy", []int{2}, "a word that appears once"},
		{"nothing-like-this", nil, "a needle that is not there"},
		{"", nil, "an empty query selects nothing, not everything"},
		{"  ", nil, "whitespace is empty too"},
	} {
		got := FindSteps(sess, steps, c.query)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("FindSteps(%q) = %v, want %v — %s", c.query, got, c.want, c.why)
		}
	}
}

// FindSteps' own empty check is an early-out: StepMatches is where an empty
// needle is refused, and a caller can reach it directly.
func TestStepMatchesRefusesAnEmptyNeedle(t *testing.T) {
	sess, steps := findFixture()
	if StepMatches(steps[0], sess, "") {
		t.Error("an empty needle matched a step; a search that selects " +
			"everything is the same as no search")
	}
	if !StepMatches(steps[0], sess, "idempotent") {
		t.Error("the fixture should match at all")
	}
}

func TestFindStepsIgnoresCase(t *testing.T) {
	sess, steps := findFixture()
	lower := FindSteps(sess, steps, "connection refused")
	upper := FindSteps(sess, steps, "Connection REFUSED")
	if !reflect.DeepEqual(lower, upper) {
		t.Errorf("case changed the result: %v vs %v", lower, upper)
	}
	if len(lower) == 0 {
		t.Fatal("the fixture should match at all")
	}
}

// A step whose event is gone — a step list outliving a reparse — must not
// index into the events slice.
func TestFindStepsSurvivesAStepWithNoEvent(t *testing.T) {
	sess, _ := findFixture()
	steps := []ReplayStep{
		{EventIndex: 99, Title: "Ran a vanished thing"},
		{EventIndex: -1, Title: "Ran go test ./..."},
	}
	if got := FindSteps(sess, steps, "go test"); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("FindSteps = %v, want [1] — the title is still searchable", got)
	}
	if got := FindSteps(nil, steps, "vanished"); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("FindSteps(nil session) = %v, want [0]", got)
	}
}

func TestNextStepMatchWrapsBothWays(t *testing.T) {
	hits := []int{3, 7, 12}

	for _, c := range []struct {
		from, delta, step, at int
		why                   string
	}{
		{0, 1, 3, 0, "forward to the first match"},
		{3, 1, 7, 1, "forward from a match takes the next one"},
		{12, 1, 3, 0, "forward from the last wraps to the first"},
		{20, 1, 3, 0, "forward from past the end wraps"},
		{12, -1, 7, 1, "back from a match takes the previous one"},
		{3, -1, 12, 2, "back from the first wraps to the last"},
		{0, -1, 12, 2, "back from before the first wraps"},
		{2, 1, 3, 0, "a step before the first match goes to it"},
		{8, -1, 7, 1, "back from between two matches"},
		{8, 1, 12, 2, "forward from between two matches"},
	} {
		step, at := NextStepMatch(hits, c.from, c.delta)
		if step != c.step || at != c.at {
			t.Errorf("NextStepMatch(%v, %d, %d) = (%d, %d), want (%d, %d) — %s",
				hits, c.from, c.delta, step, at, c.step, c.at, c.why)
		}
	}

	if step, at := NextStepMatch(nil, 0, 1); step != -1 || at != -1 {
		t.Errorf("NextStepMatch(nil) = (%d, %d), want (-1, -1)", step, at)
	}
}

func TestStepMatchAtReportsWhichMatchIsShowing(t *testing.T) {
	hits := []int{3, 7, 12}
	for step, want := range map[int]int{3: 0, 7: 1, 12: 2, 0: -1, 8: -1, 99: -1} {
		if got := StepMatchAt(hits, step); got != want {
			t.Errorf("StepMatchAt(%d) = %d, want %d", step, got, want)
		}
	}
	if got := StepMatchAt(nil, 3); got != -1 {
		t.Errorf("StepMatchAt(nil) = %d, want -1", got)
	}
}

// The point of the feature: a session long enough that stepping to the thing
// you remember is not reading.
func TestFindStepsOnALongSession(t *testing.T) {
	var events []Event
	for i := 0; i < 1200; i++ {
		cmd := "go build ./..."
		if i == 851 {
			cmd = "psql -c 'vacuum analyze'"
		}
		events = append(events, Event{Type: EventToolUse, ToolName: "Bash",
			ToolInput:  map[string]interface{}{"command": cmd},
			ToolOutput: "line " + strconv.Itoa(i)})
	}
	sess := &Session{Info: SessionInfo{ID: "s", CWD: "/repo"}, Events: events}
	steps := BuildReplay(sess)

	hits := FindSteps(sess, steps, "vacuum")
	if !reflect.DeepEqual(hits, []int{851}) {
		t.Fatalf("FindSteps = %v, want [851]", hits)
	}
}
