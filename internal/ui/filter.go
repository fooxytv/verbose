package ui

import (
	"fmt"
	"strings"

	"github.com/fooxytv/verbose/internal/session"
)

// eventFilter narrows the timeline to a class of events.
type eventFilter int

const (
	filterAll eventFilter = iota
	filterOperations
	filterFailures
	filterFileOps
	filterPrompts
)

func (f eventFilter) next() eventFilter {
	if f == filterPrompts {
		return filterAll
	}
	return f + 1
}

func (f eventFilter) label() string {
	switch f {
	case filterOperations:
		return "tools"
	case filterFailures:
		return "failures"
	case filterFileOps:
		return "file ops"
	case filterPrompts:
		return "prompts"
	}
	return "all"
}

// matches reports whether an event belongs in this filter.
func (f eventFilter) matches(e session.Event) bool {
	switch f {
	case filterOperations:
		return e.Type == session.EventToolUse

	case filterFailures:
		switch e.Type {
		case session.EventToolDenied, session.EventDiagnostics:
			return true
		case session.EventToolUse, session.EventToolResult:
			if e.IsError {
				return true
			}
			return e.Result != nil && e.Result.Interrupted
		}
		return false

	case filterFileOps:
		if e.Type == session.EventUserFileEdit {
			return true
		}
		if e.Type != session.EventToolUse {
			return false
		}
		switch e.ToolName {
		case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "NotebookRead":
			return true
		}
		return false

	case filterPrompts:
		return e.Type == session.EventUserPrompt
	}
	return true
}

// eventSearchText is the haystack a query is matched against. It deliberately
// covers the operation's target and its output, so "config.py" finds the reads
// and edits of that file and "exit 1" finds the commands that failed.
func eventSearchText(e session.Event) string {
	var b strings.Builder
	b.WriteString(e.ToolName)
	b.WriteByte(' ')
	b.WriteString(e.UserText)
	b.WriteByte(' ')
	b.WriteString(e.Text)
	b.WriteByte(' ')
	b.WriteString(e.FilePath)
	b.WriteByte(' ')
	b.WriteString(e.DenialKind)
	b.WriteByte(' ')
	b.WriteString(e.UserFeedback)
	b.WriteByte(' ')

	for _, key := range []string{"file_path", "notebook_path", "command", "pattern", "path", "url", "query", "skill", "description", "subagent_type"} {
		if v, ok := e.ToolInput[key].(string); ok {
			b.WriteString(v)
			b.WriteByte(' ')
		}
	}

	// Cap the output contribution: results run to megabytes and the useful part
	// (the error, the first lines) is at the front.
	out := e.ToolOutput
	if len(out) > 4096 {
		out = out[:4096]
	}
	b.WriteString(out)

	if e.Result != nil {
		b.WriteByte(' ')
		b.WriteString(e.Result.Raw)
		b.WriteByte(' ')
		b.WriteString(e.Result.FilePath)
		if len(e.Result.Stderr) > 0 {
			b.WriteByte(' ')
			b.WriteString(e.Result.Stderr)
		}
	}

	return b.String()
}

// visibleEvents returns the indices of events passing the filter and query,
// in timeline order. Indices point back into sess.Events.
func visibleEvents(sess *session.Session, f eventFilter, query string) []int {
	if sess == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))

	out := make([]int, 0, len(sess.Events))
	for i := range sess.Events {
		e := sess.Events[i]
		if !f.matches(e) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(eventSearchText(e)), q) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// filterStatus describes the active filter and query for the timeline header.
func filterStatus(f eventFilter, query string, shown, total int) string {
	if f == filterAll && query == "" {
		return ""
	}
	parts := []string{}
	if f != filterAll {
		parts = append(parts, "filter: "+f.label())
	}
	if query != "" {
		parts = append(parts, fmt.Sprintf("search: %q", query))
	}
	return fmt.Sprintf("%s  (%d of %d events)", strings.Join(parts, "  "), shown, total)
}
