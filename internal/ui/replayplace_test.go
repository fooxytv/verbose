package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/pkg/session"
)

// placeFixture is a session whose events and steps deliberately do not line up:
// every third event is a turn duration, which is bookkeeping and never a step.
// A replay opening on the timeline cursor has to map one to the other, and a
// fixture where index 7 is step 7 would pass whatever the mapping did.
func placeFixture(t *testing.T) (Model, *session.Session) {
	t.Helper()

	var events []session.Event
	for i := 0; i < 18; i++ {
		if i%3 == 2 {
			events = append(events, session.Event{
				Type: session.EventTurnDuration, TurnDurationMs: 1000 + i,
			})
			continue
		}
		events = append(events, session.Event{
			Type: session.EventToolUse, ToolName: "Bash",
			ToolInput: map[string]interface{}{"command": "echo step-" + strconv.Itoa(i)},
		})
	}

	sess := &session.Session{
		Info:   session.SessionInfo{ID: "s1", Title: "a long session", CWD: "/repo"},
		Events: events,
	}
	m := Model{width: 120, height: 40, version: "0.0.0",
		selectedSession: sess, mode: viewDetail, replayDelay: replayDefaultDelay}

	steps := session.BuildReplay(sess)
	if len(steps) != 12 {
		t.Fatalf("fixture built %d steps from %d events, want 12", len(steps), len(events))
	}
	return m, sess
}

// The reported bug: pause to read something, leave the replay, come back with
// R and the whole session started again. Pausing is meant to be a position you
// can return to.
func TestReplayComesBackToTheStepItWasLeftOn(t *testing.T) {
	m, _ := placeFixture(t)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	for i := 0; i < 5; i++ {
		res, _ = m.handleKey(namedKey(tea.KeyRight))
		m = res.(Model)
	}
	left := m.replayIndex
	if left != 5 {
		t.Fatalf("five rights reached step index %d, want 5", left)
	}

	res, _ = m.handleKey(namedKey(tea.KeyEsc))
	m = res.(Model)
	if m.mode != viewDetail {
		t.Fatalf("esc left mode %v, want the timeline", m.mode)
	}

	res, _ = m.handleKey(runeKey("R"))
	m = res.(Model)
	if m.replayIndex != left {
		t.Errorf("R came back at step index %d, want %d — the position was lost",
			m.replayIndex, left)
	}
}

// Leaving the replay has to put the timeline cursor on the step that was
// showing, because that cursor is the only record of where the reader was.
func TestLeavingAReplayPointsTheTimelineAtTheStep(t *testing.T) {
	m, _ := placeFixture(t)
	m.autoFollow = true // as it is when a session is opened from the list

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	for i := 0; i < 4; i++ {
		res, _ = m.handleKey(namedKey(tea.KeyRight))
		m = res.(Model)
	}
	want := m.replaySteps[m.replayIndex].EventIndex

	res, _ = m.handleKey(namedKey(tea.KeyEsc))
	m = res.(Model)

	if m.autoFollow {
		t.Error("the cursor is a position now, so it must stop following the end")
	}
	visible := m.visibleEvents()
	if m.detailCursor >= len(visible) || visible[m.detailCursor] != want {
		t.Errorf("cursor sits on event %v, want %d",
			visible[min(m.detailCursor, len(visible)-1)], want)
	}
}

// The other half of the same mechanism, and the third thing asked for: a
// cursor the reader moved themselves is where the replay should start.
func TestReplayStartsAtTheTimelineCursor(t *testing.T) {
	m, _ := placeFixture(t)

	// Nine rows down the timeline, by hand.
	for i := 0; i < 9; i++ {
		res, _ := m.handleKey(namedKey(tea.KeyDown))
		m = res.(Model)
	}
	if m.autoFollow {
		t.Fatal("moving the cursor should have stopped the follow")
	}
	at := m.visibleEvents()[m.detailCursor]

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)

	step := m.replaySteps[m.replayIndex]
	if step.EventIndex != at {
		t.Errorf("replay opened on event %d, want %d", step.EventIndex, at)
	}
	if !strings.Contains(m.statusMsg, "Starting at step") {
		t.Errorf("no word about where it started: %q", m.statusMsg)
	}
}

// An event that is not itself a step — a turn duration, say — still has to
// land somewhere sensible: the work that comes after it.
func TestReplayStartsAtTheFirstStepAfterANonStepCursor(t *testing.T) {
	m, sess := placeFixture(t)

	// Event 8 is a turn duration (every third), so no step carries it.
	if sess.Events[8].Type != session.EventTurnDuration {
		t.Fatalf("fixture changed: event 8 is %v", sess.Events[8].Type)
	}
	m.autoFollow = false
	m.pointAtEvent(8)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)

	if got := m.replaySteps[m.replayIndex].EventIndex; got != 9 {
		t.Errorf("opened on event %d, want 9 — the first step after the cursor", got)
	}
}

// Opening a session and pressing R is the plain case, and it must still begin
// at the beginning. The cursor is at the END of the session there, because it
// is tracking a possibly-live transcript rather than pointing anywhere.
func TestReplayFollowingTheEndStartsAtStepOne(t *testing.T) {
	m, sess := placeFixture(t)
	m.autoFollow = true
	m.detailCursor = len(sess.Events) - 1

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)

	if m.replayIndex != 0 {
		t.Errorf("replayIndex = %d, want 0 — a followed cursor is not a chosen place",
			m.replayIndex)
	}
	if m.statusMsg != "" {
		t.Errorf("nothing to announce when it starts at step 1, got %q", m.statusMsg)
	}
}

// A search hit in the LIST is the reason that row is on screen, so R on it
// starts there rather than at step 1 of a session you were not looking for.
func TestReplayEntryTakesASearchHitFromTheList(t *testing.T) {
	m, _ := placeFixture(t)
	m.mode = viewSessions
	m.sessions = []session.SessionInfo{{ID: "s1", Title: "a long session"}}
	m.listQuery = "echo"
	m.listMatches = map[string]session.Match{"s1": {SessionID: "s1", Hits: 2, EventIndex: 7}}

	if got := m.replayEntryEvent(m.selectedSession); got != 7 {
		t.Errorf("entry event = %d, want 7", got)
	}

	// A title-only match has no event, and the whole session is the answer.
	m.listMatches = map[string]session.Match{"s1": {SessionID: "s1", Hits: 1, EventIndex: -1}}
	if got := m.replayEntryEvent(m.selectedSession); got != -1 {
		t.Errorf("entry event = %d, want -1 for a title match", got)
	}

	// And with no search at all the list row says nothing about where to
	// start. The match has to be the one with a real event on it, or the
	// stale -1 above would pass this whether the query were checked or not.
	m.listMatches = map[string]session.Match{"s1": {SessionID: "s1", Hits: 2, EventIndex: 7}}
	m.listQuery = ""
	if got := m.replayEntryEvent(m.selectedSession); got != -1 {
		t.Errorf("entry event = %d, want -1 with no query — a match left over "+
			"from a search that is no longer on screen is not a place to start", got)
	}
}

func TestStepForEventTakesTheFirstStepAtOrAfterIt(t *testing.T) {
	steps := []session.ReplayStep{{EventIndex: 2}, {EventIndex: 5}, {EventIndex: 9}}

	for _, c := range []struct {
		event, want int
		why         string
	}{
		{-1, 0, "no event means the beginning"},
		{0, 0, "before every step"},
		{2, 0, "the step's own event"},
		{3, 1, "between two steps takes the later one"},
		{5, 1, "exact again"},
		{9, 2, "the last step"},
		{40, 2, "past the end holds the last step"},
	} {
		if got := stepForEvent(steps, c.event); got != c.want {
			t.Errorf("stepForEvent(%d) = %d, want %d — %s", c.event, got, c.want, c.why)
		}
	}

	if got := stepForEvent(nil, 4); got != 0 {
		t.Errorf("stepForEvent(nil) = %d, want 0", got)
	}
}

// A timeline filter is the reader's, so leaving the replay keeps it — the
// cursor can be placed through it as long as the step is one of the rows.
func TestLeavingAReplayKeepsAFilterThatShowsTheStep(t *testing.T) {
	m, _ := placeFixture(t)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	for i := 0; i < 3; i++ {
		res, _ = m.handleKey(namedKey(tea.KeyRight))
		m = res.(Model)
	}
	want := m.replaySteps[m.replayIndex].EventIndex

	// Tools only: the fixture's steps are all Bash calls, so the step being
	// replayed is still on the list.
	m.eventFilter = filterOperations

	res, _ = m.handleKey(namedKey(tea.KeyEsc))
	m = res.(Model)

	if m.eventFilter != filterOperations {
		t.Error("the filter was dropped although it showed the step")
	}
	if got := m.visibleEvents()[m.detailCursor]; got != want {
		t.Errorf("cursor on event %d, want %d", got, want)
	}
}

// But a filter that hides the step would leave the cursor somewhere
// unrelated, and an unrelated cursor is what loses the place.
func TestLeavingAReplayDropsAFilterThatHidesTheStep(t *testing.T) {
	m, _ := placeFixture(t)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	for i := 0; i < 3; i++ {
		res, _ = m.handleKey(namedKey(tea.KeyRight))
		m = res.(Model)
	}
	want := m.replaySteps[m.replayIndex].EventIndex

	// Prompts only, and the fixture has none: nothing the replay is showing
	// can be pointed at through this filter.
	m.eventFilter = filterPrompts
	if len(visibleEvents(m.selectedSession, filterPrompts, "")) != 0 {
		t.Fatal("fixture changed: the prompt filter was supposed to hide everything")
	}

	res, _ = m.handleKey(namedKey(tea.KeyEsc))
	m = res.(Model)

	if m.eventFilter == filterPrompts {
		t.Fatal("the filter hid the step and was kept anyway")
	}
	if got := m.visibleEvents()[m.detailCursor]; got != want {
		t.Errorf("cursor on event %d, want %d", got, want)
	}
}

// Space on a finished replay does restart it — that part is deliberate — but
// it used to do so in silence, which is indistinguishable from a pause key
// that threw the reader back to the beginning.
func TestPlayOnAFinishedReplaySaysItStartedAgain(t *testing.T) {
	m, _ := placeFixture(t)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	res, _ = m.handleKey(namedKey(tea.KeyEnd))
	m = res.(Model)
	if m.replayIndex != len(m.replaySteps)-1 {
		t.Fatalf("End left step index %d, want the last", m.replayIndex)
	}

	res, _ = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = res.(Model)

	if m.replayIndex != 0 {
		t.Errorf("replayIndex = %d, want 0", m.replayIndex)
	}
	if !strings.Contains(m.statusMsg, "finished") {
		t.Errorf("statusMsg = %q, want it to say the replay had finished", m.statusMsg)
	}
}

// Pausing mid-step and playing again carries on from the character it stopped
// on. This is the mechanism the report was about, and it has to keep working
// now that leaving and coming back is a second way to hold a position.
func TestPlayAfterAPauseCarriesOnMidStep(t *testing.T) {
	m, _ := placeFixture(t)

	res, _ := m.handleKey(runeKey("R"))
	m = res.(Model)
	res, _ = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = res.(Model)
	if !m.replayPlaying {
		t.Fatal("space should have started playback")
	}

	// Type a little way into the first step.
	for i := 0; i < 3; i++ {
		res, _ = m.Update(replayTypeMsg{gen: m.replayGen})
		m = res.(Model)
	}
	typed := m.replayTyped
	if typed <= 0 {
		t.Fatalf("nothing was typed: %d", typed)
	}

	res, _ = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = res.(Model)
	if m.replayPlaying {
		t.Fatal("space should have paused")
	}
	if m.replayPausedTyped != typed {
		t.Fatalf("pause marked %d characters, want %d", m.replayPausedTyped, typed)
	}

	res, _ = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = res.(Model)
	if m.replayTyped != typed {
		t.Errorf("resumed typing at %d, want %d — it started the step again",
			m.replayTyped, typed)
	}
}
