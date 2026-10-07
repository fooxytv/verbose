package session

import "strings"

// Claude Code has no concept of a session title, so the first thing the user
// actually typed stands in for one. Prompts carry machinery around them —
// slash-command wrappers, hook output, pasted images — none of which reads as a
// label, so those are skipped in favour of the first prompt that looks like
// prose.

// noisyPromptTags wrap content that Claude Code injects into a user turn rather
// than anything the user wrote.
var noisyPromptTags = []string{
	"local-command-caveat",
	"local-command-stdout",
	"local-command-stderr",
	"command-name",
	"command-message",
	"command-args",
	"system-reminder",
	"user-prompt-submit-hook",
}

// deriveTitle returns a human label for a session: the first user prompt that
// reads like something a person typed, or "" when there is none.
func deriveTitle(events []Event) string {
	for _, e := range events {
		if e.Type != EventUserPrompt {
			continue
		}
		if title := promptTitle(e.UserText); title != "" {
			return title
		}
	}
	return ""
}

// promptTitle reduces one prompt to a single readable line.
func promptTitle(text string) string {
	text = stripPromptTags(text)

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isPastedData(line) {
			continue
		}
		// Long prompts are stored whole; the renderer decides how much fits.
		return collapseSpaces(line)
	}
	return ""
}

// stripPromptTags removes the tagged blocks Claude Code adds to a user turn.
func stripPromptTags(s string) string {
	for _, tag := range noisyPromptTags {
		open, closing := "<"+tag+">", "</"+tag+">"
		for {
			i := strings.Index(s, open)
			if i < 0 {
				break
			}
			rest := s[i+len(open):]
			j := strings.Index(rest, closing)
			if j < 0 {
				// Unterminated: drop everything from the tag onwards.
				s = s[:i]
				break
			}
			s = s[:i] + rest[j+len(closing):]
		}
	}
	return s
}

// isPastedData reports whether a line is encoded data rather than prose. A
// pasted image arrives as base64 wrapped into hundreds of short lines, so
// length alone does not identify it — the restricted alphabet does. Prose of
// this length always contains a space.
func isPastedData(line string) bool {
	const minRun = 64
	if len(line) < minRun {
		return false
	}
	if !strings.Contains(line[:minRun], " ") && isBase64Alphabet(line) {
		return true
	}
	// An unwrapped blob: one very long run with no spaces at all.
	const unwrapped = 200
	return len(line) >= unwrapped && !strings.Contains(line[:unwrapped], " ")
}

// isBase64Alphabet reports whether every byte could belong to base64 data.
func isBase64Alphabet(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

// collapseSpaces squeezes runs of whitespace so a label occupies one line.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
