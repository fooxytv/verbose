package session

import "strings"

// Searching one session's replay, rather than the whole history.
//
// Search (search.go) answers "which session was that in". This answers "where
// in this one", which is the question a long session raises: the replay here
// reaches 1300 steps, and stepping to the one that ran a command is not
// reading, it is scrolling.
//
// The haystack is deliberately wider than what the step shows. A step's
// headline is a sentence someone has to have guessed — "Ran go test ./..." —
// but the command itself, the file it touched and what it PRINTED are what a
// reader remembers, and the printed part is the only place an outcome lives.

// FindSteps returns the indices of the steps that match, in order. The query
// is matched case-insensitively; an empty one matches nothing, because a
// search that selects everything is the same as no search and should not look
// like a result.
func FindSteps(sess *Session, steps []ReplayStep, query string) []int {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	var out []int
	for i, st := range steps {
		if StepMatches(st, sess, needle) {
			out = append(out, i)
		}
	}
	return out
}

// StepMatches reports whether one step matches an already-lower-cased needle.
// The caller lower-cases once rather than once per step.
func StepMatches(step ReplayStep, sess *Session, needle string) bool {
	if needle == "" {
		return false
	}
	for _, part := range stepHaystack(step, sess) {
		if part.text == "" {
			continue
		}
		if strings.Contains(strings.ToLower(part.text), needle) {
			return true
		}
	}
	return false
}

// StepMatchLine says WHERE in a step the needle was found: which part of it,
// and the line it was on.
//
// A result that only narrows is a list of steps with no stated reason for
// being there — the same rule the history search follows. The kinds are the
// reader's words for the parts of a step: what it ran, what that printed,
// what it wrote.
func StepMatchLine(step ReplayStep, sess *Session, needle string) (kind, line string) {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return "", ""
	}
	for _, part := range stepHaystack(step, sess) {
		if part.text == "" || !strings.Contains(strings.ToLower(part.text), needle) {
			continue
		}
		if l, ok := matchLine(part.text, needle); ok {
			return part.kind, l
		}
	}
	return "", ""
}

// haystackPart is one searchable piece of a step, labelled with what it is.
type haystackPart struct {
	kind, text string
}

// stepHaystack is every piece of text one step can be found by.
//
// The step carries what is on screen; the event behind it carries the rest.
// Both are needed: Detail is a sentence DescribeEvent built, so a word the
// agent actually typed may appear only in the event, and a command's output
// never reaches the step at all.
func stepHaystack(step ReplayStep, sess *Session) []haystackPart {
	// Ordered by how well each one explains a match: the command a reader
	// half-remembers beats the sentence verbose wrote about it.
	out := []haystackPart{
		{"headline", step.Title},
		{"code", step.Code},
		{"file", step.CodePath},
		{"file", step.FilePath},
	}

	if sess == nil || step.EventIndex < 0 || step.EventIndex >= len(sess.Events) {
		return append(out, haystackPart{"detail", step.Detail})
	}
	e := sess.Events[step.EventIndex]

	// What was run and what it wrote to: the two inputs a reader is most
	// likely to search by, and ToolInput is a bag of anything otherwise.
	if cmd, ok := stringInput(e.ToolInput, "command"); ok {
		out = append(out, haystackPart{"command", cmd})
	}
	if fp, ok := stringInput(e.ToolInput, "file_path"); ok {
		out = append(out, haystackPart{"file", fp})
	}
	out = append(out,
		haystackPart{"output", e.ToolOutput},
		haystackPart{"prompt", e.UserText},
		haystackPart{"reply", e.Text},
		haystackPart{"reply", e.Thinking},
		haystackPart{"feedback", e.UserFeedback},
		haystackPart{"file", e.FilePath},
		haystackPart{"detail", step.Detail},
		haystackPart{"tool", e.ToolName},
	)

	// A failing shell command records its reason as a bare string rather than
	// an object, and that string is the only account of the failure there is.
	if e.Result != nil {
		out = append(out,
			haystackPart{"error", e.Result.Raw},
			haystackPart{"output", e.Result.Stdout},
			haystackPart{"output", e.Result.Stderr})
	}
	return out
}

// NextStepMatch is the match to move to from a step, wrapping once. It
// returns the position in hits so a caller can say "match 3 of 7", and -1
// when there are none.
//
// Wrapping is what makes the key worth pressing repeatedly: a search that
// stops at the end of the session asks the reader to work out whether that
// was the last match or a key that stopped working.
func NextStepMatch(hits []int, from, delta int) (step, at int) {
	if len(hits) == 0 {
		return -1, -1
	}
	if delta >= 0 {
		for i, h := range hits {
			if h > from {
				return h, i
			}
		}
		return hits[0], 0
	}
	for i := len(hits) - 1; i >= 0; i-- {
		if hits[i] < from {
			return hits[i], i
		}
	}
	return hits[len(hits)-1], len(hits) - 1
}

// StepMatchAt is the position of a step in a hit list, or -1 if it is not one
// of them. It is what lets the header say which match is on screen after a
// jump that was not made by the search keys.
func StepMatchAt(hits []int, step int) int {
	for i, h := range hits {
		if h == step {
			return i
		}
	}
	return -1
}
