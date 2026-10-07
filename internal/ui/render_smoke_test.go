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
