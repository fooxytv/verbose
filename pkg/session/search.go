package session

import (
	"sort"
	"strings"
)

// Searching the whole history, rather than one session's timeline.
//
// The list shows what a session is about; it cannot show what was in it. Most
// of a history is older than the week you remember — on the machine this was
// written for, 44 of 62 sessions — and two thirds of them sit in a directory
// that has been worked in more than once. Finding the earlier visit is the
// step that was missing before a replay could be any use.
//
// There is no index. Every session is already parsed and in memory, and
// scanning all of them is 12ms for 18,000 events, well inside a frame.

// Match is one session that matched a search.
type Match struct {
	SessionID string
	// Hits is every match in the session; Kind, Snippet and EventIndex
	// describe the FIRST one, which is what the list shows.
	Hits    int
	Kind    string // title | prompt | reply | file | command
	Snippet string
	// EventIndex is where the first hit was, so the timeline can open on it.
	// -1 when only the title matched and no event did.
	EventIndex int
}

// Search returns every session whose title or contents match, newest first —
// the order the list is already in, so a search narrows it rather than
// reshuffling it.
func (s *Store) Search(query string) []Match {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}

	s.mu.RLock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Info.LastUpdate.After(sessions[j].Info.LastUpdate)
	})

	var out []Match
	for _, sess := range sessions {
		if m, ok := MatchSession(sess, needle); ok {
			out = append(out, m)
		}
	}
	return out
}

// MatchSession reports whether one session matches, and where. The needle is
// expected lower-cased; the caller does it once rather than per session.
func MatchSession(sess *Session, needle string) (Match, bool) {
	if sess == nil || needle == "" {
		return Match{}, false
	}

	m := Match{SessionID: sess.Info.ID, EventIndex: -1}

	// The title first, because it is what the row already shows: a session
	// that matches on its label needs no explanation of why.
	if line, ok := matchLine(sess.Info.Title, needle); ok {
		m.Hits++
		m.Kind, m.Snippet = "title", line
	}

	for i, e := range sess.Events {
		kind, line, n := matchEvent(e, needle)
		if n == 0 {
			continue
		}
		m.Hits += n
		if m.EventIndex >= 0 {
			continue
		}
		// The first matching event is where the timeline should open, whether
		// or not the title already said why the session matched.
		m.EventIndex = i
		if m.Kind == "" {
			m.Kind, m.Snippet = kind, line
		}
	}

	return m, m.Hits > 0
}

// matchEvent is what one event contributes: where it matched, the line it
// matched on, and how many times.
func matchEvent(e Event, needle string) (kind, line string, hits int) {
	type field struct {
		kind, text string
	}
	fields := []field{
		{"prompt", e.UserText},
		{"reply", e.Text},
		{"reply", e.Thinking},
	}

	// What a tool did is as findable as what was said: the file it touched
	// and the command it ran are usually what you remember.
	if fp, ok := stringInput(e.ToolInput, "file_path"); ok {
		fields = append(fields, field{"file", fp})
	}
	if cmd, ok := stringInput(e.ToolInput, "command"); ok {
		fields = append(fields, field{"command", cmd})
	}

	for _, f := range fields {
		n := strings.Count(strings.ToLower(f.text), needle)
		if n == 0 {
			continue
		}
		hits += n
		if kind == "" {
			kind = f.kind
			line, _ = matchLine(f.text, needle)
		}
	}
	return kind, line, hits
}

// matchLine returns the line the needle appeared on, trimmed and bounded so a
// minified file or a thousand-character prompt cannot take the row with it.
func matchLine(text, needle string) (string, bool) {
	lower := strings.ToLower(text)
	at := strings.Index(lower, needle)
	if at < 0 {
		return "", false
	}

	start := strings.LastIndexByte(text[:at], '\n') + 1
	end := strings.IndexByte(text[at:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += at
	}

	line := strings.TrimSpace(text[start:end])
	const maxSnippet = 160
	if r := []rune(line); len(r) > maxSnippet {
		// Keep the match in view rather than the first 160 characters of a
		// line it might be nowhere near.
		hit := len([]rune(text[start:at]))
		from := max(0, hit-40)
		to := min(len(r), from+maxSnippet)
		line = strings.TrimSpace(string(r[from:to]))
		if from > 0 {
			line = "…" + line
		}
		if to < len(r) {
			line += "…"
		}
	}
	return line, true
}
