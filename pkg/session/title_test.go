package session

import (
	"strings"
	"testing"
)

func prompt(text string) Event {
	return Event{Type: EventUserPrompt, UserText: text}
}

func TestDeriveTitleUsesFirstRealPrompt(t *testing.T) {
	events := []Event{
		{Type: EventText, Text: "assistant chatter"},
		prompt("  can we wire up opencode sessions?  "),
		prompt("a later prompt"),
	}
	if got := deriveTitle(events); got != "can we wire up opencode sessions?" {
		t.Errorf("title = %q", got)
	}

	if got := deriveTitle([]Event{{Type: EventText, Text: "no prompts"}}); got != "" {
		t.Errorf("title = %q, want empty when there are no prompts", got)
	}
}

// A pasted image arrives as base64 wrapped into many short lines, so the label
// must come from the next prompt that is actually prose.
func TestDeriveTitleSkipsPastedImage(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("iVBORw0KGgoAAAANSUhEUgAABdIAAALBCAYAAABcE1McAAAAAXNSR0IArs4c")
		b.WriteString("gAAAAgAAYdpAAQAAAABAAAAGgAAAAAAAqACAAQAAAABAAAF0qADAAQAAAAB\n")
	}

	got := deriveTitle([]Event{prompt(b.String()), prompt("here is the real question")})
	if got != "here is the real question" {
		t.Errorf("title = %.60q, want the prose prompt", got)
	}
}

// Slash commands and hook output are wrapped in tags; none of it is a label.
func TestDeriveTitleStripsInjectedTags(t *testing.T) {
	cases := map[string]string{
		"<local-command-caveat>Caveat: messages below were generated…</local-command-caveat>\nactual question": "actual question",
		"<command-name>/loop</command-name>\n<command-args>5m</command-args>\nrun the thing":                   "run the thing",
		"<system-reminder>background note</system-reminder>real text":                                          "real text",
		// An unterminated tag drops the rest rather than leaking markup.
		"<system-reminder>never closed": "",
	}
	for in, want := range cases {
		if got := deriveTitle([]Event{prompt(in)}); got != want {
			t.Errorf("deriveTitle(%.40q) = %q, want %q", in, got, want)
		}
	}
}

func TestPromptTitleCollapsesWhitespace(t *testing.T) {
	if got := promptTitle("\n\n   lots   of    space   here\nsecond line"); got != "lots of space here" {
		t.Errorf("got %q", got)
	}
}

func TestIsPastedData(t *testing.T) {
	long := strings.Repeat("abcdefgh", 12) // 96 chars, base64 alphabet, no spaces
	if !isPastedData(long) {
		t.Error("wrapped base64 not detected")
	}
	// Prose of the same length always has spaces.
	prose := "can you look at the pipeline structure and tell me what you think about it"
	if isPastedData(prose) {
		t.Errorf("prose treated as data: %q", prose)
	}
	// A path or URL is not base64 and must survive.
	url := "https://github.com/fooxytv/verbose/blob/main/internal/session/parser.go#L42"
	if isPastedData(url) {
		t.Error("URL treated as data")
	}
	// Short lines are never data.
	if isPastedData("iVBORw0KGgo") {
		t.Error("short line treated as data")
	}
}

// OpenCode names a session before it has content; that placeholder says less
// than the opening prompt.
func TestOpenCodePlaceholderTitleFallsBack(t *testing.T) {
	sessions, err := ParseOpenCodeDB(ocFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := sessions[0].Info.Title; got != "Root work" {
		t.Errorf("title = %q, want the recorded OpenCode title", got)
	}
}
