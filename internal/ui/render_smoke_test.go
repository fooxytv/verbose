package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// maxSmokeSessions bounds how many of a source's real sessions a sweep reads.
//
// The corpus only ever grows — one machine's Claude directory for a single
// project reached 60MB in a day — and a sweep over all of it is a suite that
// gets slower every week until it stops being run. That is the same reasoning
// as maxStepsPerSession, one level up: a spread of the corpus catches a broken
// renderer, and reading all of it only catches it again.
const maxSmokeSessions = 12

// smokeSample picks a bounded spread of a corpus, keeping the heaviest and the
// most recent whatever else it takes.
//
// Those two are not arbitrary. The heaviest session is where layout arithmetic
// has the most chances to be wrong, and dropping it would quietly make the
// sweep cheap by skipping the hard case. The newest is the one most likely to
// carry a shape the parser has never seen, because it was written by whatever
// the agents do now rather than by what they did last year.
//
// The rest is a stride, so the sample spreads across the corpus instead of
// taking a prefix: transcripts are discovered in sorted order, so the first N
// are a handful of projects rather than a cross-section.
//
// Deterministic for a given corpus, so a failure can be reproduced. Set
// VERBOSE_SMOKE_ALL to sweep everything, which is what to do when a rendering
// bug is suspected and the sample is not showing it.
func smokeSample[T any](t *testing.T, kind string, items []T, weight func(T) int, when func(T) time.Time) []T {
	t.Helper()
	if os.Getenv("VERBOSE_SMOKE_ALL") != "" || len(items) <= maxSmokeSessions {
		return items
	}

	keep := map[int]bool{}
	heaviest, newest := 0, 0
	for i := range items {
		if weight(items[i]) > weight(items[heaviest]) {
			heaviest = i
		}
		if when(items[i]).After(when(items[newest])) {
			newest = i
		}
	}
	keep[heaviest] = true
	keep[newest] = true

	// Fill the remainder by striding. The two kept above may or may not land
	// on a stride point, so the cap is checked rather than assumed.
	stride := max(1, len(items)/max(1, maxSmokeSessions-len(keep)))
	for i := 0; i < len(items) && len(keep) < maxSmokeSessions; i += stride {
		keep[i] = true
	}

	idx := make([]int, 0, len(keep))
	for i := range keep {
		idx = append(idx, i)
	}
	sort.Ints(idx)

	out := make([]T, 0, len(idx))
	for _, i := range idx {
		out = append(out, items[i])
	}
	t.Logf("sampled %d of %d %s — the heaviest, the newest and a stride; "+
		"VERBOSE_SMOKE_ALL=1 sweeps all of them", len(out), len(items), kind)
	return out
}

// smokeTranscripts is smokeSample over files, weighed by size and dated by
// mtime. A file that cannot be stat'd sorts as empty and ancient rather than
// failing the sweep: it is about to be parsed, and that is where a real
// problem with it will show.
func smokeTranscripts(t *testing.T, paths []string) []string {
	t.Helper()
	type file struct {
		path string
		size int
		mod  time.Time
	}
	files := make([]file, 0, len(paths))
	for _, p := range paths {
		f := file{path: p}
		if fi, err := os.Stat(p); err == nil {
			f.size, f.mod = int(fi.Size()), fi.ModTime()
		}
		files = append(files, f)
	}
	picked := smokeSample(t, "transcripts", files,
		func(f file) int { return f.size },
		func(f file) time.Time { return f.mod })

	out := make([]string, 0, len(picked))
	for _, f := range picked {
		out = append(out, f.path)
	}
	return out
}

// smokeSessions is smokeSample over parsed sessions, weighed by how much there
// is to render and dated by when the session was last written.
func smokeSessions(t *testing.T, kind string, sessions []*session.Session) []*session.Session {
	t.Helper()
	return smokeSample(t, kind, sessions,
		func(s *session.Session) int { return len(s.Events) },
		func(s *session.Session) time.Time { return s.Info.LastUpdate })
}

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

	for _, p := range smokeTranscripts(t, paths) {
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
	sessions = smokeSessions(t, "OpenCode sessions", sessions)

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
		formatSessionLine(sess.Info, 120, "")
	}
}

// The same sweep over the real Codex home. Codex brings input shapes the other
// two do not: a title that is a four-thousand-character paste, a prompt wrapped
// in XML delegation markup, MCP results that are JSON, and a file "diff" that
// is really a whole new file. Each of those has reached a renderer that assumed
// otherwise, so every view and a spread of replay frames are rendered here, at
// a narrow terminal too, with the row width measured the way the terminal
// measures it.
func TestRenderRealCodexSessions(t *testing.T) {
	var sessions []*session.Session
	for _, dir := range session.DefaultCodexDirs() {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		parsed, err := session.ParseCodexDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		sessions = append(sessions, parsed...)
	}
	if len(sessions) == 0 {
		t.Skip("no Codex sessions available")
	}
	sessions = smokeSessions(t, "Codex sessions", sessions)

	sizes := []struct{ w, h int }{{120, 40}, {80, 24}, {40, 10}}

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
		for i := range sess.Events {
			renderEventDetail(sess.Events[i], 0, 120, 40)
			formatEventLine(sess.Events[i], 116, sess.Info.CWD)
		}
		formatSessionLine(sess.Info, 120, "")

		steps := session.BuildReplay(sess)
		if len(steps) == 0 {
			continue
		}
		stride := 1 + len(steps)/maxStepsPerSession

		// Width is measured through Model.View, not renderReplay: the
		// renderer's rows may be exactly `width` and clampFrame is what takes
		// the last column back. Asserting on the producer would be holding it
		// to a contract it does not have.
		m := Model{mode: viewReplay, version: "0.0.0", selectedSession: sess}
		m.replaySteps = steps

		for _, sz := range sizes {
			for i := 0; i < len(steps); i += stride {
				v := m
				v.width, v.height = sz.w, sz.h
				v.replayIndex, v.replayTyped = i, -1

				frame := v.View()
				if strings.TrimSpace(frame) == "" {
					t.Fatalf("%s: step %d rendered empty at %dx%d", sess.Info.ID, i, sz.w, sz.h)
				}
				for n, row := range visibleLines(frame) {
					if got := terminalColumns(row); got >= sz.w {
						t.Fatalf("%s: step %d row %d draws %d columns at width %d\n  %q",
							sess.Info.ID, i, n, got, sz.w, stripAnsi(row))
					}
				}

				// Every intermediate state of the typing animation must render.
				total := TypedLength(steps[i], sess)
				for _, f := range []float64{0, 0.5, 1} {
					v.replayTyped, v.replayPlaying = int(float64(total)*f), true
					v.View()
				}
			}
		}
	}
	t.Logf("rendered %d codex sessions", len(sessions))
}

// And over the real Copilot home. Copilot's shapes are its own: a summary that
// is a whole GitHub issue body, a prompt with a <current_datetime> preamble
// stapled to it, and sessions whose every turn failed and so have prompts with
// no replies.
func TestRenderRealCopilotSessions(t *testing.T) {
	var sessions []*session.Session
	for _, dir := range session.DefaultCopilotDirs() {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		parsed, err := session.ParseCopilotDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		sessions = append(sessions, parsed...)
	}
	if len(sessions) == 0 {
		t.Skip("no Copilot sessions available")
	}
	sessions = smokeSessions(t, "Copilot sessions", sessions)

	for _, sess := range sessions {
		if out := renderSessionOverview(sess, sess.Todos, false, 0, 120, 40); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty overview", sess.Info.ID)
		}
		all := visibleEvents(sess, filterAll, "")
		if out := renderSessionDetail(sess, all, 0, 120, 40, ""); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty timeline", sess.Info.ID)
		}
		for i := range sess.Events {
			renderEventDetail(sess.Events[i], 0, 120, 40)
			formatEventLine(sess.Events[i], 116, sess.Info.CWD)
		}

		// A title carrying a newline costs the list a row it did not count.
		for _, w := range []int{120, 80, 40} {
			row := formatSessionLine(sess.Info, w, "")
			if n := len(visibleLines(row)); n != 1 {
				t.Errorf("%s: the list row took %d rows\n%s", sess.Info.ID, n, row)
			}
			if got := terminalColumns(row); got >= w {
				t.Errorf("%s: the list row draws %d columns at width %d", sess.Info.ID, got, w)
			}
		}

		steps := session.BuildReplay(sess)
		if len(steps) == 0 {
			continue
		}
		m := Model{mode: viewReplay, version: "0.0.0", selectedSession: sess}
		m.replaySteps = steps
		for _, sz := range []struct{ w, h int }{{120, 40}, {40, 10}} {
			for i := range steps {
				v := m
				v.width, v.height = sz.w, sz.h
				v.replayIndex, v.replayTyped = i, -1
				for n, row := range visibleLines(v.View()) {
					if got := terminalColumns(row); got >= sz.w {
						t.Fatalf("%s: step %d row %d draws %d columns at width %d\n  %q",
							sess.Info.ID, i, n, got, sz.w, stripAnsi(row))
					}
				}
			}
		}
	}
	t.Logf("rendered %d copilot sessions", len(sessions))
}

// And over the real pi home. pi is the only source that keeps its reasoning,
// so it is the only one whose replay has a THINKING step with a body — a path
// no other transcript on this machine reaches.
func TestRenderRealPiSessions(t *testing.T) {
	var sessions []*session.Session
	for _, dir := range session.DefaultPiDirs() {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		parsed, err := session.ParsePiDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		sessions = append(sessions, parsed...)
	}
	if len(sessions) == 0 {
		t.Skip("no pi sessions available")
	}
	sessions = smokeSessions(t, "pi sessions", sessions)

	for _, sess := range sessions {
		if out := renderSessionOverview(sess, sess.Todos, false, 0, 120, 40); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty overview", sess.Info.ID)
		}
		all := visibleEvents(sess, filterAll, "")
		if out := renderSessionDetail(sess, all, 0, 120, 40, ""); strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty timeline", sess.Info.ID)
		}
		for i := range sess.Events {
			renderEventDetail(sess.Events[i], 0, 120, 40)
			formatEventLine(sess.Events[i], 116, sess.Info.CWD)
		}

		steps := session.BuildReplay(sess)
		if len(steps) == 0 {
			continue
		}
		stride := 1 + len(steps)/maxStepsPerSession
		m := Model{mode: viewReplay, version: "0.0.0", selectedSession: sess}
		m.replaySteps = steps
		for _, sz := range []struct{ w, h int }{{120, 40}, {40, 10}} {
			for i := 0; i < len(steps); i += stride {
				v := m
				v.width, v.height = sz.w, sz.h
				v.replayIndex, v.replayTyped = i, -1
				for n, row := range visibleLines(v.View()) {
					if got := terminalColumns(row); got >= sz.w {
						t.Fatalf("%s: step %d row %d draws %d columns at width %d\n  %q",
							sess.Info.ID, i, n, got, sz.w, stripAnsi(row))
					}
				}
			}
		}
	}
	t.Logf("rendered %d pi sessions", len(sessions))
}

// maxStepsPerSession bounds the smoke sweep: enough steps to catch a layout
// mistake anywhere in a session, few enough that the suite stays quick enough
// to run on every change.
const maxStepsPerSession = 40

// Renders a spread of replay steps from every real transcript, at a few
// terminal sizes.
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
	for _, p := range smokeTranscripts(t, paths) {
		sess, err := session.ParseSessionFile(p)
		if err != nil || len(sess.Events) == 0 {
			continue
		}
		steps := session.BuildReplay(sess)
		if len(steps) == 0 {
			continue
		}
		withSteps++

		// Sample steps rather than walk every one. Each step may lex and
		// highlight a whole file, so an exhaustive sweep grew to 54 seconds as
		// the transcripts did — slow enough that the suite stops being run.
		// A stride keeps the coverage spread across the session.
		stride := 1 + len(steps)/maxStepsPerSession

		for _, sz := range sizes {
			for i := 0; i < len(steps); i += stride {
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
			// The ends of a session are where off-by-ones live, and a stride
			// can step over the last one.
			for _, i := range []int{0, len(steps) - 1} {
				renderReplay(replayView{sess: sess, steps: steps, idx: i, typed: -1,
					delay: replayDefaultDelay, width: sz.w, height: sz.h})
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

// The sample is the only thing standing between the sweeps and a suite that
// grows without bound, so what it guarantees is worth pinning: a cap, the two
// sessions that are always worth rendering, and a spread rather than a prefix.
//
// The indices here are chosen so the stride cannot cover for a missing rule.
// 61 items and a cap of 12 put the stride points on multiples of 5 or 6, and
// 57 and 59 are on neither — so dropping "keep the heaviest" or "keep the
// newest" really does lose them, rather than picking them up by luck.
func TestSmokeSampleKeepsTheHeaviestTheNewestAndASpread(t *testing.T) {
	// This is about what the sample does, so an inherited override that turns
	// sampling off has to go first.
	t.Setenv("VERBOSE_SMOKE_ALL", "")

	type item struct {
		id   int
		size int
		mod  time.Time
	}
	base := time.Now().Add(-1000 * time.Hour)
	items := make([]item, 61)
	for i := range items {
		items[i] = item{id: i, size: 10, mod: base.Add(time.Duration(i) * time.Minute)}
	}
	items[57].size = 9999
	items[59].mod = time.Now()

	weight := func(i item) int { return i.size }
	when := func(i item) time.Time { return i.mod }
	got := smokeSample(t, "items", items, weight, when)

	if len(got) > maxSmokeSessions {
		t.Fatalf("sampled %d items, want at most %d", len(got), maxSmokeSessions)
	}

	ids := map[int]bool{}
	for _, g := range got {
		ids[g.id] = true
	}
	if !ids[57] {
		t.Error("the heaviest session was dropped; the sweep would skip its hardest case")
	}
	if !ids[59] {
		t.Error("the newest session was dropped; the sweep would miss the latest shapes")
	}
	// A stride reaches the back of the corpus on its own. Without one, the
	// only late items are the two kept by name — which is a prefix wearing a
	// spread's clothes.
	late := 0
	for id := range ids {
		if id > len(items)/2 && id != 57 && id != 59 {
			late++
		}
	}
	if late == 0 {
		t.Error("every strided item came from the front — a prefix, not a spread")
	}

	// Corpus order survives, so a failure reads in the order the corpus does.
	for i := 1; i < len(got); i++ {
		if got[i].id <= got[i-1].id {
			t.Errorf("sample is out of corpus order at %d: %d after %d", i, got[i].id, got[i-1].id)
		}
	}
}

func TestSmokeSampleLeavesASmallCorpusAloneAndObeysTheOverride(t *testing.T) {
	// Half of this is about the variable being UNSET, so an inherited one has
	// to go first: running the suite with VERBOSE_SMOKE_ALL=1 — which is the
	// whole point of having it — otherwise fails this test and nothing else.
	t.Setenv("VERBOSE_SMOKE_ALL", "")

	weight := func(i int) int { return i }
	when := func(int) time.Time { return time.Time{} }

	// An empty corpus is the one a sweep hands over on a machine with no
	// sessions of that source, and asking it for its heaviest member must not
	// be how anyone finds that out.
	if got := smokeSample(t, "items", []int{}, weight, when); len(got) != 0 {
		t.Errorf("an empty corpus sampled to %d items", len(got))
	}

	small := []int{1, 2, 3}
	if got := smokeSample(t, "items", small, weight, when); len(got) != len(small) {
		t.Errorf("sampled %d of a %d-item corpus; nothing should be dropped under the cap",
			len(got), len(small))
	}

	big := make([]int, maxSmokeSessions*3)
	for i := range big {
		big[i] = i
	}
	if got := smokeSample(t, "items", big, weight, when); len(got) > maxSmokeSessions {
		t.Fatalf("sampled %d, want at most %d", len(got), maxSmokeSessions)
	}
	t.Setenv("VERBOSE_SMOKE_ALL", "1")
	if got := smokeSample(t, "items", big, weight, when); len(got) != len(big) {
		t.Errorf("VERBOSE_SMOKE_ALL sampled %d of %d; it must sweep everything",
			len(got), len(big))
	}
}
