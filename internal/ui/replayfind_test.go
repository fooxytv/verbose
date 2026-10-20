package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/pkg/session"
)

// findFixture is a replay of 40 steps where three are findable by what they
// ran and one by what it printed. Forty so that a jump is a real jump: the
// point of searching a replay is a session too long to step through.
func findFixture(t *testing.T) Model {
	t.Helper()

	var events []session.Event
	for i := 0; i < 40; i++ {
		cmd := "go build ./..."
		out := "ok\n"
		switch i {
		case 6, 19, 33:
			cmd = "go test ./pkg/session/"
		case 25:
			out = "Error: connection refused on port 5432\n"
		}
		events = append(events, session.Event{
			Type: session.EventToolUse, ToolName: "Bash",
			ToolInput:  map[string]interface{}{"command": cmd + " #" + strconv.Itoa(i)},
			ToolOutput: out,
		})
	}

	sess := &session.Session{
		Info:   session.SessionInfo{ID: "s1", Title: "a long session", CWD: "/repo"},
		Events: events,
	}
	m := Model{width: 120, height: 40, version: "0.0.0", replayDelay: replayDefaultDelay}
	m.startReplay(sess)
	if len(m.replaySteps) != 40 {
		t.Fatalf("fixture built %d steps, want 40", len(m.replaySteps))
	}
	return m
}

// typeFind types into the step-or-find prompt and presses enter.
func typeFind(t *testing.T, m Model, query string) Model {
	t.Helper()
	res, _ := m.handleKey(runeKey("/"))
	m = res.(Model)
	if !m.replayGotoTyping {
		t.Fatal("/ did not open the prompt")
	}
	for _, r := range query {
		if r == ' ' {
			res, _ = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
		} else {
			res, _ = m.handleKey(runeKey(string(r)))
		}
		m = res.(Model)
	}
	if m.replayGotoDraft != query {
		t.Fatalf("draft = %q, want %q", m.replayGotoDraft, query)
	}
	res, _ = m.handleKey(namedKey(tea.KeyEnter))
	return res.(Model)
}

// The ask: a session big enough that finding the step is the problem.
func TestFindJumpsToTheFirstMatchingStep(t *testing.T) {
	m := findFixture(t)

	m = typeFind(t, m, "go test")

	if m.replayFind != "go test" {
		t.Errorf("replayFind = %q", m.replayFind)
	}
	if want := []int{6, 19, 33}; len(m.replayHits) != 3 {
		t.Fatalf("hits = %v, want %v", m.replayHits, want)
	}
	if m.replayIndex != 6 {
		t.Errorf("replayIndex = %d, want 6 — the first match", m.replayIndex)
	}
	if m.replayPlaying {
		t.Error("arriving by search is a deliberate look, so it should be paused")
	}
	if m.replayTyped != -1 {
		t.Error("the step arrived at should be fully revealed")
	}
	if !strings.Contains(m.statusMsg, "Match 1 of 3") {
		t.Errorf("statusMsg = %q, want it to count the matches", m.statusMsg)
	}
}

// What a step PRINTED is the only place an outcome lives, and finding one is
// half of what was asked for.
func TestFindReachesAnOutcomeInTheOutput(t *testing.T) {
	m := findFixture(t)

	m = typeFind(t, m, "connection refused")

	if m.replayIndex != 25 {
		t.Errorf("replayIndex = %d, want 25 — the step that failed", m.replayIndex)
	}
	if len(m.replayHits) != 1 {
		t.Errorf("hits = %v, want one", m.replayHits)
	}
}

func TestNextAndPreviousMatchWrap(t *testing.T) {
	m := findFixture(t)
	m = typeFind(t, m, "go test")

	for _, want := range []int{19, 33, 6, 19} {
		res, _ := m.handleKey(runeKey("n"))
		m = res.(Model)
		if m.replayIndex != want {
			t.Fatalf("n landed on %d, want %d", m.replayIndex, want)
		}
	}
	for _, want := range []int{6, 33, 19} {
		res, _ := m.handleKey(runeKey("N"))
		m = res.(Model)
		if m.replayIndex != want {
			t.Fatalf("N landed on %d, want %d", m.replayIndex, want)
		}
	}
	if !strings.Contains(m.statusMsg, "of 3") {
		t.Errorf("statusMsg = %q, want the match count", m.statusMsg)
	}
}

// n was a spare alias for → long before it was the search convention, and it
// still is until there is something to step through.
func TestNStepsForwardWithNoSearchOpen(t *testing.T) {
	m := findFixture(t)

	res, _ := m.handleKey(runeKey("n"))
	m = res.(Model)
	if m.replayIndex != 1 {
		t.Errorf("replayIndex = %d, want 1 — n is the step forward with no search",
			m.replayIndex)
	}

	// And N does nothing rather than something surprising.
	res, _ = m.handleKey(runeKey("N"))
	m = res.(Model)
	if m.replayIndex != 1 {
		t.Errorf("N moved to %d with no search open", m.replayIndex)
	}
}

// A query that matches nothing must not throw away the search that does, and
// must not move the reader. The sessions list has the same rule.
func TestAFindThatMatchesNothingChangesNothing(t *testing.T) {
	m := findFixture(t)
	m = typeFind(t, m, "go test")
	at, hits := m.replayIndex, m.replayHits

	m = typeFind(t, m, "nothing-like-this-at-all")

	if m.replayIndex != at {
		t.Errorf("replayIndex = %d, want %d — a failed search should not move", m.replayIndex, at)
	}
	if m.replayFind != "go test" || len(m.replayHits) != len(hits) {
		t.Errorf("the working search was lost: %q %v", m.replayFind, m.replayHits)
	}
	if !strings.Contains(m.statusMsg, "No step in this replay matches") {
		t.Errorf("statusMsg = %q, want it to say nothing matched", m.statusMsg)
	}
}

func TestAnEmptyFindClearsTheSearch(t *testing.T) {
	m := findFixture(t)
	m = typeFind(t, m, "go test")

	res, _ := m.handleKey(runeKey("/"))
	m = res.(Model)
	res, _ = m.handleKey(namedKey(tea.KeyEnter))
	m = res.(Model)

	if m.replayFind != "" || m.replayHits != nil {
		t.Errorf("the search survived an empty query: %q %v", m.replayFind, m.replayHits)
	}
	if m.replayFindBadge() != "" {
		t.Errorf("badge = %q, want none", m.replayFindBadge())
	}

	// And n goes back to being the step forward.
	before := m.replayIndex
	res, _ = m.handleKey(runeKey("n"))
	m = res.(Model)
	if m.replayIndex != before+1 {
		t.Errorf("n moved to %d, want %d", m.replayIndex, before+1)
	}
}

// Digits are still a step number. That is the one thing the prompt cannot do
// both of, and the number is by far the likelier intent.
func TestDigitsStillGoToAStepNumber(t *testing.T) {
	m := findFixture(t)

	m = typeFind(t, m, "24")

	if m.replayIndex != 23 {
		t.Errorf("replayIndex = %d, want 23 — steps are numbered from 1", m.replayIndex)
	}
	if m.replayFind != "" {
		t.Errorf("replayFind = %q, want no search started", m.replayFind)
	}
}

// The badge says which match is on screen, and stops claiming one when the
// reader steps off the matches.
func TestTheBadgeSaysWhichMatchIsShowing(t *testing.T) {
	m := findFixture(t)
	m = typeFind(t, m, "go test")

	badge := m.replayFindBadge()
	if !strings.Contains(badge, "match 1/3") {
		t.Errorf("badge = %q, want the position in it", badge)
	}
	if !strings.Contains(stripAnsi(m.View()), "match 1/3") {
		t.Error("the badge is not on the frame")
	}

	// One step on by hand is not a match any more.
	res, _ := m.handleKey(namedKey(tea.KeyRight))
	m = res.(Model)
	badge = m.replayFindBadge()
	// "3 matches" contains the word, so the position is what to look for.
	if strings.Contains(badge, "/3") {
		t.Errorf("badge = %q, want it to stop claiming to BE a match", badge)
	}
	if !strings.Contains(badge, "3 matches") {
		t.Errorf("badge = %q, want the count still", badge)
	}
}

// The hits are indices into the step list, so a filter that drops steps
// renumbers every match after it. Stale indices would jump to the wrong step.
func TestAFilterRenumbersTheMatches(t *testing.T) {
	m := findFixture(t)

	// One step that wrote code, among forty that did not.
	m.selectedSession.Events = append(m.selectedSession.Events, session.Event{
		Type: session.EventToolUse, ToolName: "Write",
		ToolInput: map[string]interface{}{
			"file_path": "/repo/go-test-helper.go",
			"content":   "package main\n"},
	})
	m.startReplay(m.selectedSession)

	m = typeFind(t, m, "go-test")
	if len(m.replayHits) != 1 || m.replayHits[0] != 40 {
		t.Fatalf("hits = %v, want [40]", m.replayHits)
	}

	// Code only: the written file is now the only step in the list.
	res, _ := m.handleKey(runeKey("tab"))
	m = res.(Model)
	if !m.replayCodeOnly {
		t.Fatal("tab did not open the code-only filter")
	}
	if len(m.replaySteps) != 1 {
		t.Fatalf("code-only kept %d steps, want 1", len(m.replaySteps))
	}
	if len(m.replayHits) != 1 || m.replayHits[0] != 0 {
		t.Errorf("hits = %v, want [0] — the match is step 1 of the filtered list",
			m.replayHits)
	}
}

// The badge lives in the header, which already exists, so a search must not
// cost the body a row — the bug this view has had twice.
func TestASearchCostsTheBodyNoRows(t *testing.T) {
	m := findFixture(t)
	plain := len(strings.Split(m.View(), "\n"))

	m = typeFind(t, m, "go test")
	found := len(strings.Split(m.View(), "\n"))

	if plain != found {
		t.Errorf("the frame is %d rows with a search and %d without", found, plain)
	}
	if found != m.height {
		t.Errorf("frame is %d rows, want the terminal's %d", found, m.height)
	}
}

// The prompt says which of its two jobs the draft is currently doing, because
// the keystroke that decides is the first one typed.
func TestThePromptSaysWhetherItIsAStepOrASearch(t *testing.T) {
	m := findFixture(t)

	res, _ := m.handleKey(runeKey("/"))
	m = res.(Model)
	if p := stripAnsi(m.replayGotoPrompt()); !strings.Contains(p, "of 40") {
		t.Errorf("empty prompt = %q, want the step count", p)
	}

	res, _ = m.handleKey(runeKey("2"))
	m = res.(Model)
	if p := stripAnsi(m.replayGotoPrompt()); !strings.Contains(p, "enter to jump") {
		t.Errorf("digit prompt = %q, want the jump wording", p)
	}

	res, _ = m.handleKey(runeKey("x"))
	m = res.(Model)
	if p := stripAnsi(m.replayGotoPrompt()); !strings.Contains(p, "enter to find it") {
		t.Errorf("text prompt = %q, want the find wording", p)
	}
}

// The footer offers n/N only once there is something to step through.
func TestTheFooterOffersTheMatchKeysOnlyWithASearchOpen(t *testing.T) {
	m := findFixture(t)
	if foot := stripAnsi(lastRow(m.View())); strings.Contains(foot, "n/N") {
		t.Errorf("footer offers match keys with no search: %q", foot)
	}

	m = typeFind(t, m, "go test")
	// A status message owns the footer row while it is showing, and the
	// search sets one — the keys are what is there once it clears.
	m.statusMsg = ""
	if foot := stripAnsi(lastRow(m.View())); !strings.Contains(foot, "n/N match 3") {
		t.Errorf("footer = %q, want the match keys and the count", foot)
	}
}

// A search from a step that is itself a match reports THAT one, rather than
// skipping to the next and making the reader wonder what it found first.
func TestFindCountsTheStepAlreadyShowingAsTheFirstMatch(t *testing.T) {
	m := findFixture(t)

	m = typeFind(t, m, "20") // step 20 is index 19, one of the matches
	if m.replayIndex != 19 {
		t.Fatalf("replayIndex = %d, want 19", m.replayIndex)
	}

	m = typeFind(t, m, "go test")

	if m.replayIndex != 19 {
		t.Errorf("replayIndex = %d, want 19 — it was already on a match", m.replayIndex)
	}
	if !strings.Contains(m.statusMsg, "Match 2 of 3") {
		t.Errorf("statusMsg = %q, want the match it is on (the second)", m.statusMsg)
	}
}

// Stepping to a match mid-playback stops it. Arriving somewhere by searching
// is a deliberate look, and a step still typing underneath the jump would
// scroll the thing that was searched for off the screen again.
//
// It has to be n that is tested, not the prompt: `/` pauses on the way in, so
// a search made from a standing start proves nothing about the jump.
func TestSteppingToAMatchStopsPlaybackAndRevealsTheStep(t *testing.T) {
	m := findFixture(t)
	m = typeFind(t, m, "go test")

	// Playing again from the match it found.
	res, _ := m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = res.(Model)
	for i := 0; i < 4; i++ {
		res, _ = m.Update(replayTypeMsg{gen: m.replayGen})
		m = res.(Model)
	}
	if !m.replayPlaying || m.replayTyped <= 0 {
		t.Fatalf("not playing: playing=%v typed=%d", m.replayPlaying, m.replayTyped)
	}

	res, _ = m.handleKey(runeKey("n"))
	m = res.(Model)

	if m.replayIndex != 19 {
		t.Errorf("replayIndex = %d, want 19 — the next match", m.replayIndex)
	}
	if m.replayPlaying {
		t.Error("stepping to a match left playback running")
	}
	if m.replayTyped != -1 {
		t.Errorf("replayTyped = %d, want -1 — the step found should be whole",
			m.replayTyped)
	}
	if m.replayScroll != 0 {
		t.Errorf("replayScroll = %d, want the top of the step found", m.replayScroll)
	}
}
