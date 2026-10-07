package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

// Renders every view against the user's real transcripts to catch panics and
// obviously broken output. Skips when no transcripts are present.
func TestRenderRealTranscripts(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	paths, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	if len(paths) == 0 {
		t.Skip("no transcripts available")
	}

	for _, p := range paths {
		sess, err := session.ParseSessionFile(p)
		if err != nil || len(sess.Events) == 0 {
			continue
		}
		if out := renderSessionOverview(sess, nil, false, 0, 120, 40); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty overview", p)
		}
		all := visibleEvents(sess, filterAll, "")
		if out := renderSessionDetail(sess, all, 0, 120, 40, ""); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty timeline", p)
		}
		// Every filter and a search must render without panicking.
		for _, f := range []eventFilter{filterAll, filterOperations, filterFailures, filterFileOps, filterPrompts} {
			v := visibleEvents(sess, f, "")
			renderSessionDetail(sess, v, 0, 120, 40, filterStatus(f, "", len(v), len(sess.Events)))
		}
		q := visibleEvents(sess, filterAll, "config")
		renderSessionDetail(sess, q, 0, 120, 40, filterStatus(filterAll, "config", len(q), len(sess.Events)))
		for i := range sess.Events {
			renderEventDetail(sess.Events[i], 0, 120, 40)
			formatEventLine(sess.Events[i], 116, sess.Info.CWD)
			formatEventLineSelected(sess.Events[i], 116, sess.Info.CWD)
		}
	}
}

// The same sweep over the user's real OpenCode database, which reaches the
// parts of the renderers that only OpenCode sessions exercise.
func TestRenderRealOpenCodeSessions(t *testing.T) {
	var sessions []*session.Session
	for _, db := range session.DefaultOpenCodeDBs() {
		if _, err := os.Stat(db); err != nil {
			continue
		}
		parsed, err := session.ParseOpenCodeDB(db)
		if err != nil {
			t.Fatalf("%s: %v", db, err)
		}
		sessions = append(sessions, parsed...)
	}
	if len(sessions) == 0 {
		t.Skip("no OpenCode sessions available")
	}

	for _, sess := range sessions {
		if out := renderSessionOverview(sess, sess.Todos, false, 0, 120, 40); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty overview", sess.Info.ID)
		}
		all := visibleEvents(sess, filterAll, "")
		if out := renderSessionDetail(sess, all, 0, 120, 40, ""); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty timeline", sess.Info.ID)
		}
		for _, f := range []eventFilter{filterAll, filterOperations, filterFailures, filterFileOps, filterPrompts} {
			v := visibleEvents(sess, f, "")
			renderSessionDetail(sess, v, 0, 120, 40, filterStatus(f, "", len(v), len(sess.Events)))
		}
		q := visibleEvents(sess, filterAll, "config")
		renderSessionDetail(sess, q, 0, 120, 40, filterStatus(filterAll, "config", len(q), len(sess.Events)))
		for i := range sess.Events {
			renderEventDetail(sess.Events[i], 0, 120, 40)
			formatEventLine(sess.Events[i], 116, sess.Info.CWD)
			formatEventLineSelected(sess.Events[i], 116, sess.Info.CWD)
		}
		formatSessionLine(sess.Info, 120)
	}
}

// Renders every replay step of every real transcript, at a few terminal sizes.
// Replay does its own width and scroll arithmetic, so this is where an
// off-by-one or a negative slice bound shows up.
func TestRenderRealReplays(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	paths, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	sub, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*", "subagents", "*.jsonl"))
	paths = append(paths, sub...)
	if len(paths) == 0 {
		t.Skip("no transcripts available")
	}

	// A narrow terminal is the one that breaks layout maths.
	sizes := []struct{ w, h int }{{120, 40}, {80, 24}, {40, 10}}

	var withSteps int
	for _, p := range paths {
		sess, err := session.ParseSessionFile(p)
		if err != nil || len(sess.Events) == 0 {
			continue
		}
		steps := session.BuildReplay(sess)
		if len(steps) == 0 {
			continue
		}
		withSteps++

		for _, sz := range sizes {
			for i := range steps {
				out := renderReplay(replayView{sess: sess, steps: steps, idx: i, scroll: 0, typed: -1, playing: false, delay: replayDefaultDelay, width: sz.w, height: sz.h})
				if strings.TrimSpace(out) == "" {
					t.Fatalf("%s: step %d rendered empty at %dx%d", p, i, sz.w, sz.h)
				}
				// Every intermediate state of the typing animation must render.
				// A partial reveal slices strings by rune, so this is where a
				// bad index would panic.
				// Sample the animation rather than walking every character:
				// the interesting cases are the boundaries and a few points in
				// between, and the full walk is far too slow over real data.
				total := TypedLength(steps[i], sess)
				for _, f := range []float64{0, 0.01, 0.25, 0.5, 0.75, 0.99, 1} {
					renderReplay(replayView{sess: sess, steps: steps, idx: i,
						typed: int(float64(total) * f), playing: true,
						delay: replayDefaultDelay, width: sz.w, height: sz.h})
				}
			}
			// Out-of-range indices, scrolls and reveal budgets must clamp.
			renderReplay(replayView{sess: sess, steps: steps, idx: -5, scroll: -3, typed: -1, playing: true, delay: replayDefaultDelay, width: sz.w, height: sz.h})
			renderReplay(replayView{sess: sess, steps: steps, idx: len(steps) + 10, scroll: 9999, typed: 99999, playing: true, delay: replayDefaultDelay, width: sz.w, height: sz.h})
		}
	}

	if withSteps == 0 {
		t.Skip("no transcript produced replay steps")
	}
	t.Logf("replayed %d sessions", withSteps)
}

// An empty session must render an explanation rather than a blank screen.
func TestRenderReplayEmpty(t *testing.T) {
	out := renderReplay(replayView{sess: &session.Session{}, steps: nil, idx: 0, scroll: 0, typed: -1, playing: false, delay: replayDefaultDelay, width: 80, height: 24})
	if !strings.Contains(out, "no steps to replay") {
		t.Errorf("empty replay did not explain itself: %q", out)
	}
}
