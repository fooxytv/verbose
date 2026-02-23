package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"verbose/internal/session"

	"github.com/charmbracelet/lipgloss"
)

// renderSessionDetail renders the timeline view for a single session.
func renderSessionDetail(sess *session.Session, cursor int, width, height int) string {
	var b strings.Builder

	info := sess.Info
	totalTokens := info.InputTokens + info.OutputTokens + info.CacheReadTokens + info.CacheWriteTokens

	header := headerStyle.Render(fmt.Sprintf(" %s > %s  Timeline", info.ProjectName, shortID(info.ID)))
	stats := mutedStyle.Render(fmt.Sprintf(
		" %s | %s | tools: %d | events: %d",
		tokenStyle.Render(formatTokens(totalTokens)),
		costStyle.Render(fmt.Sprintf("$%.4f", info.CostUSD)),
		info.ToolCallCount,
		info.EventCount,
	))
	b.WriteString(header)
	b.WriteString(stats)
	b.WriteString("\n")

	// Files edited/created bar
	fileParts := []string{}
	if len(info.FilesWritten) > 0 {
		fileParts = append(fileParts, toolUseStyle.Render(fmt.Sprintf("✎ %d edited", len(info.FilesWritten))))
	}
	if len(info.FilesCreated) > 0 {
		fileParts = append(fileParts, userStyle.Render(fmt.Sprintf("+ %d created", len(info.FilesCreated))))
	}
	if len(info.FilesRead) > 0 {
		fileParts = append(fileParts, dimStyle.Render(fmt.Sprintf("◉ %d read", len(info.FilesRead))))
	}
	if len(fileParts) > 0 {
		b.WriteString("  ")
		b.WriteString(strings.Join(fileParts, mutedStyle.Render("  |  ")))
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", min(width, 100))))
	b.WriteString("\n")

	events := sess.Events
	if len(events) == 0 {
		b.WriteString(dimStyle.Render("  No events in this session."))
		return b.String()
	}

	// Calculate visible range
	headerLines := 3
	if len(fileParts) > 0 {
		headerLines = 4
	}
	listHeight := height - headerLines - 2
	if listHeight < 1 {
		listHeight = 1
	}
	start := 0
	if cursor >= listHeight {
		start = cursor - listHeight + 1
	}
	end := start + listHeight
	if end > len(events) {
		end = len(events)
	}

	for i := start; i < end; i++ {
		e := events[i]
		selected := i == cursor

		if selected {
			line := formatEventLineSelected(e, width-4)
			// Pad to full width with selection background
			row := selBg.Render("▸ "+line) + selBg.Render(strings.Repeat(" ", max(0, width-visibleLen(line)-2)))
			b.WriteString(row)
		} else {
			line := formatEventLine(e, width-4)
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}

	if len(events) > listHeight {
		pct := float64(cursor+1) / float64(len(events)) * 100
		b.WriteString(mutedStyle.Render(fmt.Sprintf("\n  [%d/%d %.0f%%]", cursor+1, len(events), pct)))
	}

	return b.String()
}

func formatEventLine(e session.Event, maxWidth int) string {
	ts := e.Timestamp.Format("15:04:05")
	tsStr := mutedStyle.Render(ts)

	switch e.Type {
	case session.EventUserPrompt:
		text := truncate(firstLine(e.UserText), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, userStyle.Render("▶ user  "), dimStyle.Render(text))

	case session.EventThinking:
		text := truncate(firstLine(e.Thinking), maxWidth-25)
		return fmt.Sprintf("%s  %s  %s", tsStr, thinkingStyle.Render("~ think "), dimStyle.Render(text))

	case session.EventText:
		text := truncate(firstLine(e.Text), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, textStyle.Render("◁ text  "), dimStyle.Render(text))

	case session.EventToolUse:
		name := fmt.Sprintf("▷ %-6s", e.ToolName)
		summary := formatToolSummary(e.ToolName, e.ToolInput)
		summary = truncate(summary, maxWidth-25)
		// Colour-code by operation type
		nameStyle := toolUseStyle
		summaryStyle := dimStyle
		switch e.ToolName {
		case "Edit":
			nameStyle = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
			summaryStyle = lipgloss.NewStyle().Foreground(colorYellow)
		case "Write":
			nameStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			summaryStyle = lipgloss.NewStyle().Foreground(colorGreen)
		case "Read":
			nameStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
		case "Bash":
			nameStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, nameStyle.Render(name), summaryStyle.Render(summary))

	case session.EventToolResult:
		if e.IsError {
			text := truncate(firstLine(e.ToolOutput), maxWidth-25)
			return fmt.Sprintf("%s  %s  %s", tsStr, toolErrorStyle.Render("✗ error "), dimStyle.Render(text))
		}
		text := truncate(firstLine(e.ToolOutput), maxWidth-25)
		outputLen := len(e.ToolOutput)
		sizeHint := ""
		if outputLen > 1000 {
			sizeHint = mutedStyle.Render(fmt.Sprintf(" (%s)", formatBytes(outputLen)))
		}
		return fmt.Sprintf("%s  %s  %s%s", tsStr, toolResultStyle.Render("◀ result"), dimStyle.Render(text), sizeHint)

	case session.EventSystem:
		return fmt.Sprintf("%s  %s", tsStr, systemStyle.Render("* system"))

	case session.EventCompaction:
		info := "conversation compacted"
		if e.CompactPreTokens > 0 {
			info = fmt.Sprintf("compacted (%s tokens before)", formatTokensComma(e.CompactPreTokens))
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, systemStyle.Render("⟳ compact"), dimStyle.Render(info))

	default:
		return fmt.Sprintf("%s  %s", tsStr, dimStyle.Render("?"))
	}
}

// formatEventLineSelected renders the same event line but with background highlight.
// Each styled segment gets the selection background added so colours are preserved.
func formatEventLineSelected(e session.Event, maxWidth int) string {
	ts := e.Timestamp.Format("15:04:05")
	bg := colorBgSelected
	tsStr := lipgloss.NewStyle().Foreground(colorText).Background(bg).Render(ts)

	sel := func(base lipgloss.Style) lipgloss.Style {
		return base.Copy().Background(bg)
	}

	switch e.Type {
	case session.EventUserPrompt:
		text := truncate(firstLine(e.UserText), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(userStyle).Render("▶ user  "), sel(normalStyle).Render(text))

	case session.EventThinking:
		text := truncate(firstLine(e.Thinking), maxWidth-25)
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(thinkingStyle).Render("~ think "), sel(dimStyle).Render(text))

	case session.EventText:
		text := truncate(firstLine(e.Text), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(textStyle).Render("◁ text  "), sel(normalStyle).Render(text))

	case session.EventToolUse:
		name := fmt.Sprintf("▷ %-6s", e.ToolName)
		summary := formatToolSummary(e.ToolName, e.ToolInput)
		summary = truncate(summary, maxWidth-25)
		nameStyle := toolUseStyle
		summaryStyle := dimStyle
		switch e.ToolName {
		case "Edit":
			nameStyle = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
			summaryStyle = lipgloss.NewStyle().Foreground(colorYellow)
		case "Write":
			nameStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			summaryStyle = lipgloss.NewStyle().Foreground(colorGreen)
		case "Read":
			nameStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
		case "Bash":
			nameStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(nameStyle).Render(name), sel(summaryStyle).Render(summary))

	case session.EventToolResult:
		if e.IsError {
			text := truncate(firstLine(e.ToolOutput), maxWidth-25)
			return fmt.Sprintf("%s  %s  %s", tsStr, sel(toolErrorStyle).Render("✗ error "), sel(dimStyle).Render(text))
		}
		text := truncate(firstLine(e.ToolOutput), maxWidth-25)
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(toolResultStyle).Render("◀ result"), sel(dimStyle).Render(text))

	case session.EventSystem:
		return fmt.Sprintf("%s  %s", tsStr, sel(systemStyle).Render("* system"))

	case session.EventCompaction:
		info := "conversation compacted"
		if e.CompactPreTokens > 0 {
			info = fmt.Sprintf("compacted (%s tokens before)", formatTokensComma(e.CompactPreTokens))
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(systemStyle).Render("⟳ compact"), sel(dimStyle).Render(info))

	default:
		return fmt.Sprintf("%s  %s", tsStr, sel(dimStyle).Render("?"))
	}
}

// visibleLen estimates the printable character count (strips ANSI escape sequences).
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}

func formatToolSummary(tool string, input map[string]interface{}) string {
	switch tool {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return "$ " + firstLine(cmd)
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Write":
		if fp, ok := input["file_path"].(string); ok {
			return "→ " + fp
		}
	case "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return "✎ " + fp
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok {
			path, _ := input["path"].(string)
			if path == "" {
				path = "."
			}
			return fmt.Sprintf(`"%s" %s`, p, path)
		}
	case "Task":
		if desc, ok := input["description"].(string); ok {
			return desc
		}
	case "TaskCreate":
		if subj, ok := input["subject"].(string); ok {
			return subj
		}
	case "TaskUpdate":
		if id, ok := input["taskId"].(string); ok {
			status, _ := input["status"].(string)
			return fmt.Sprintf("#%s → %s", id, status)
		}
	case "WebFetch":
		if url, ok := input["url"].(string); ok {
			return url
		}
	case "WebSearch":
		if q, ok := input["query"].(string); ok {
			return q
		}
	case "Skill":
		if s, ok := input["skill"].(string); ok {
			return s
		}
	}

	b, _ := json.Marshal(input)
	return string(b)
}

// renderEventDetail renders the drill-down view for a single event.
func renderEventDetail(e session.Event, scroll int, width, height int) string {
	// Build all lines first, then apply scroll
	var lines []string

	ts := e.Timestamp.Format("15:04:05")

	switch e.Type {
	case session.EventUserPrompt:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" User Prompt — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, wrapLines(e.UserText, width-4, "  ")...)

	case session.EventThinking:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Thinking — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, wrapLines(e.Thinking, width-4, "  ")...)

	case session.EventText:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Response — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, wrapLines(e.Text, width-4, "  ")...)

	case session.EventToolUse:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" %s — %s", e.ToolName, ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")

		// Special rendering for Edit tool — show as diff
		if e.ToolName == "Edit" {
			lines = append(lines, renderEditDiff(e.ToolInput, width)...)
		} else if e.ToolName == "Bash" {
			if cmd, ok := e.ToolInput["command"].(string); ok {
				lines = append(lines, "  "+dimStyle.Render("Command:"))
				lines = append(lines, "  "+toolUseStyle.Render("$ "+cmd))
			}
			if desc, ok := e.ToolInput["description"].(string); ok && desc != "" {
				lines = append(lines, "  "+dimStyle.Render("Description: ")+normalStyle.Render(desc))
			}
		} else {
			lines = append(lines, "  "+dimStyle.Render("Input:"))
			inputJSON, _ := json.MarshalIndent(e.ToolInput, "    ", "  ")
			for _, line := range strings.Split(string(inputJSON), "\n") {
				lines = append(lines, "    "+normalStyle.Render(line))
			}
		}

	case session.EventToolResult:
		title := "Tool Result"
		if e.IsError {
			title = "Tool Result (Error)"
		}
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" %s — %s", title, ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("Output (%s):", formatBytes(len(e.ToolOutput)))))
		lines = append(lines, "")
		lines = append(lines, wrapLines(e.ToolOutput, width-4, "  ")...)

	case session.EventCompaction:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Conversation Compacted — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, "  "+systemStyle.Render("⟳ The conversation context was automatically compacted."))
		lines = append(lines, "")
		if e.CompactPreTokens > 0 {
			lines = append(lines, fieldLine("Tokens Before", formatTokensComma(e.CompactPreTokens)))
		}
		if e.CompactTrigger != "" {
			lines = append(lines, fieldLine("Trigger", e.CompactTrigger))
		}
		lines = append(lines, "")
		lines = append(lines, "  "+dimStyle.Render("Claude summarised the conversation to free up context window space."))
		lines = append(lines, "  "+dimStyle.Render("Events before this point are from the pre-compaction conversation."))
	}

	// Token info footer
	if e.InputTokens > 0 || e.OutputTokens > 0 {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 60))))
		lines = append(lines, dimStyle.Render(fmt.Sprintf(
			"  Input: %s  Output: %s",
			tokenStyle.Render(formatTokensComma(e.InputTokens)),
			tokenStyle.Render(formatTokensComma(e.OutputTokens)),
		)))
	}

	// Apply scroll
	visibleHeight := height - 3
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	if scroll > len(lines)-visibleHeight {
		scroll = max(0, len(lines)-visibleHeight)
	}
	end := scroll + visibleHeight
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[scroll:end]

	// Scroll indicator
	if len(lines) > visibleHeight {
		pct := float64(scroll+visibleHeight) / float64(len(lines)) * 100
		if pct > 100 {
			pct = 100
		}
		visible = append(visible, mutedStyle.Render(fmt.Sprintf("  [%.0f%%]", pct)))
	}

	return strings.Join(visible, "\n")
}

// renderEditDiff shows Edit tool input as a colored diff.
func renderEditDiff(input map[string]interface{}, width int) []string {
	var lines []string

	fp, _ := input["file_path"].(string)
	oldStr, _ := input["old_string"].(string)
	newStr, _ := input["new_string"].(string)

	if fp != "" {
		lines = append(lines, "  "+dimStyle.Render("File: ")+toolUseStyle.Render(fp))
		lines = append(lines, "")
	}

	if oldStr != "" || newStr != "" {
		maxW := min(width-6, 120)

		if oldStr != "" {
			lines = append(lines, "  "+diffRemoveStyle.Render("--- removed"))
			for _, l := range strings.Split(oldStr, "\n") {
				if len(l) > maxW {
					l = l[:maxW]
				}
				lines = append(lines, "  "+diffRemoveStyle.Render("- "+l))
			}
		}
		if newStr != "" {
			lines = append(lines, "  "+diffAddStyle.Render("+++ added"))
			for _, l := range strings.Split(newStr, "\n") {
				if len(l) > maxW {
					l = l[:maxW]
				}
				lines = append(lines, "  "+diffAddStyle.Render("+ "+l))
			}
		}
	}

	replaceAll, _ := input["replace_all"].(bool)
	if replaceAll {
		lines = append(lines, "")
		lines = append(lines, "  "+systemStyle.Render("(replace_all: true)"))
	}

	return lines
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 40
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func wrapLines(s string, maxWidth int, prefix string) []string {
	if maxWidth <= 0 {
		maxWidth = 76
	}
	var result []string
	lines := strings.Split(s, "\n")

	maxLines := 500
	for i, line := range lines {
		if i >= maxLines {
			result = append(result, prefix+mutedStyle.Render(fmt.Sprintf("... (%d more lines)", len(lines)-maxLines)))
			break
		}
		if len(line) > maxWidth {
			line = line[:maxWidth]
		}
		result = append(result, prefix+line)
	}
	return result
}

func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
