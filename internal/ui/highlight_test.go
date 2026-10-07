package ui

import (
	"strings"
	"testing"

	"github.com/fooxytv/verbose/pkg/session"
)

// The typing budget counts visible characters, so colouring a line must not
// change how long it is. If this drifts, playback finishes early or stalls.
func TestHighlightPreservesVisibleLength(t *testing.T) {
	cases := []struct{ path, code string }{
		{"a.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}"},
		{"a.js", "const x = 42;\n// a comment\nexport default x;"},
		{"a.py", "import os\n\ndef f(n):\n    return n * 2"},
		{"a.css", "body { color: #fff; }"},
		{"", "just some text with no language at all"},
		{"a.go", "héllo := \"wörld → ok\""},
	}

	for _, c := range cases {
		plain := strings.Split(c.code, "\n")
		got := highlightCode(c.code, c.path)
		if len(got) != len(plain) {
			t.Errorf("%s: %d lines highlighted, want %d", c.path, len(got), len(plain))
			continue
		}
		for i := range plain {
			if visibleLen(got[i]) != len([]rune(plain[i])) {
				t.Errorf("%s line %d: visible length %d, want %d\n  plain: %q",
					c.path, i, visibleLen(got[i]), len([]rune(plain[i])), plain[i])
			}
		}
	}
}

func TestHighlightEmptyAndCache(t *testing.T) {
	if got := highlightCode("", "a.go"); got != nil {
		t.Errorf("empty code = %v, want nil", got)
	}
	// Same input twice must give an identical result, cached or not.
	a := highlightCode("package main", "a.go")
	b := highlightCode("package main", "a.go")
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Error("cached highlight differs from the first render")
	}
}

// Cutting a coloured line mid-token must not slice through an escape sequence.
func TestAnsiLineCutsBetweenEscapes(t *testing.T) {
	lines := highlightCode("const answer = 42;", "a.js")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}

	rv := newReveal(5)
	out, ok := rv.ansiLine(lines[0])
	if !ok {
		t.Fatal("partial line must still be drawn")
	}
	// Five visible characters plus the cursor.
	if n := visibleLen(out); n != 6 {
		t.Errorf("visible length = %d, want 6 (5 typed + cursor)", n)
	}
	if !strings.HasSuffix(out, "▌") {
		t.Errorf("cut line lost its cursor: %q", out)
	}
	// An unterminated escape would leak colour into the rest of the screen.
	if strings.Count(out, "\x1b[") > 0 && !strings.HasSuffix(strings.TrimSuffix(out, "▌"), "\x1b[0m") {
		t.Errorf("cut line does not reset its style: %q", out)
	}
}

func TestAnsiLineUnlimitedPassesThrough(t *testing.T) {
	rv := newReveal(-1)
	in := highlightCode("x := 1", "a.go")[0]
	out, ok := rv.ansiLine(in)
	if !ok || out != in {
		t.Error("an unlimited reveal must return the line untouched")
	}
}

// A replay revisits the same step on every 30ms tick, so a frame has to stay
// far cheaper than that. The cache is what makes it so.
func BenchmarkReplayFrame(b *testing.B) {
	store, err := session.NewStore()
	if err != nil {
		b.Skip(err)
	}
	defer store.Close()
	store.Scan()

	// The largest piece of code anywhere on this machine: the worst frame.
	var worst []session.ReplayStep
	var worstSess *session.Session
	wi, wn := 0, 0
	for _, info := range store.GetSessions() {
		s := store.GetSession(info.ID)
		if s == nil {
			continue
		}
		steps := codeSteps(session.BuildReplay(s), s)
		for i, st := range steps {
			if n := TypedLength(st, s); n > wn {
				worst, worstSess, wi, wn = steps, s, i, n
			}
		}
	}
	if worstSess == nil {
		b.Skip("no code steps available")
	}
	b.Logf("worst step: %s (%d chars)", worst[wi].Title, wn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderReplay(replayView{sess: worstSess, steps: worst, idx: wi,
			typed: wn / 2, playing: true, codeOnly: true,
			delay: replayDefaultDelay, width: 100, height: 40})
	}
}
