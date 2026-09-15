package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fooxytv/verbose/internal/session"

	"github.com/charmbracelet/lipgloss"
)

// renderSessionDetail renders the timeline view for a single session. visible
// holds the indices of the events passing the active filter and search, in
// timeline order; cursor indexes into that list rather than into sess.Events.
func renderSessionDetail(sess *session.Session, visible []int, cursor int, width, height int, status string) string {
	var b strings.Builder

	info := sess.Info
	totalTokens := info.InputTokens + info.OutputTokens + info.CacheReadTokens + info.CacheWriteTokens

	header := headerStyle.Render(fmt.Sprintf(" %s > %s  Timeline", info.ProjectName, shortID(info.ID)))
	statParts := []string{
		tokenStyle.Render(formatTokens(totalTokens)),
		costStyle.Render(fmt.Sprintf("$%.4f", info.CostUSD)),
		fmt.Sprintf("tools: %d", info.ToolCallCount),
		fmt.Sprintf("events: %d", info.EventCount),
	}
	if info.LinesAdded > 0 || info.LinesRemoved > 0 {
		statParts = append(statParts, diffAddStyle.Render(fmt.Sprintf("+%d", info.LinesAdded))+
			" "+diffRemoveStyle.Render(fmt.Sprintf("-%d", info.LinesRemoved)))
	}
	if info.Errors > 0 {
		statParts = append(statParts, toolErrorStyle.Render(fmt.Sprintf("failed: %d", info.Errors)))
	}
	if info.SubagentCalls > 0 {
		statParts = append(statParts, agentStyle.Render(fmt.Sprintf("subagents: %d", info.SubagentCalls)))
	}
	stats := mutedStyle.Render(" " + strings.Join(statParts, " | "))
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

	headerLines := 3
	if len(fileParts) > 0 {
		headerLines = 4
	}
	if status != "" {
		b.WriteString("  " + searchStyle.Render(status))
		b.WriteString("\n")
		headerLines++
	}

	if len(sess.Events) == 0 {
		b.WriteString(dimStyle.Render("  No events in this session."))
		return b.String()
	}
	if len(visible) == 0 {
		b.WriteString(dimStyle.Render("  Nothing matches the active filter or search."))
		return b.String()
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
	if end > len(visible) {
		end = len(visible)
	}

	for i := start; i < end; i++ {
		e := sess.Events[visible[i]]
		selected := i == cursor

		if selected {
			line := formatEventLineSelected(e, width-5, info.CWD)
			// Pad to full width with selection background
			row := selBg.Render("▸") + sidechainMark(e, true) + selBg.Render(line) +
				selBg.Render(strings.Repeat(" ", max(0, width-visibleLen(line)-2)))
			b.WriteString(row)
		} else {
			line := formatEventLine(e, width-5, info.CWD)
			b.WriteString(" " + sidechainMark(e, false) + line)
		}
		b.WriteString("\n")
	}

	if len(visible) > listHeight {
		pct := float64(cursor+1) / float64(len(visible)) * 100
		b.WriteString(mutedStyle.Render(fmt.Sprintf("\n  [%d/%d %.0f%%]", cursor+1, len(visible), pct)))
	}

	return b.String()
}

func formatEventLine(e session.Event, maxWidth int, cwd string) string {
	ts := e.Timestamp.Format("15:04:05")
	tsStr := mutedStyle.Render(ts)

	switch e.Type {
	case session.EventUserPrompt:
		text := truncate(firstLine(e.UserText), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, userStyle.Render("▶ user  "), dimStyle.Render(text))

	case session.EventThinking:
		return fmt.Sprintf("%s  %s  %s", tsStr, thinkingStyle.Render("~ think "),
			mutedStyle.Render(thinkingSummary(e, maxWidth-25)))

	case session.EventText:
		text := truncate(firstLine(e.Text), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, textStyle.Render("◁ text  "), dimStyle.Render(text))

	case session.EventToolUse:
		name := toolColumn(e.ToolName)
		summary := truncate(formatToolSummary(e.ToolName, e.ToolInput, cwd), maxWidth-40)
		// Colour-code by operation type
		nameStyle, summaryStyle := toolStyles(e)
		return fmt.Sprintf("%s  %s  %s%s", tsStr, nameStyle.Render(name),
			summaryStyle.Render(summary), opOutcome(e, false))

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

	case session.EventAgentProgress:
		desc := truncate(e.AgentDescription, maxWidth-30)
		id := e.AgentID
		if len(id) > 8 {
			id = id[:8]
		}
		return fmt.Sprintf("%s  %s  %s %s", tsStr, agentStyle.Render("⊞ agent "), mutedStyle.Render(id), dimStyle.Render(desc))

	case session.EventHookProgress:
		name := e.HookName
		if name == "" {
			name = e.HookEvent
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, dimStyle.Render("⚡ hook  "), mutedStyle.Render(name))

	case session.EventBashProgress:
		return fmt.Sprintf("%s  %s  %s", tsStr, dimStyle.Render("… bash  "), mutedStyle.Render(fmt.Sprintf("%ds", e.BashElapsedSec)))

	case session.EventTurnDuration:
		return fmt.Sprintf("%s  %s  %s", tsStr, dimStyle.Render("⏱ turn  "), mutedStyle.Render(formatDuration(e.TurnDurationMs)))

	case session.EventToolDenied:
		text := truncate(firstLine(e.UserFeedback), maxWidth-30)
		return fmt.Sprintf("%s  %s  %s %s", tsStr, toolErrorStyle.Render("⊘ denied"),
			mutedStyle.Render(e.DenialKind), dimStyle.Render(text))

	case session.EventUserFileEdit:
		return fmt.Sprintf("%s  %s  %s", tsStr, userStyle.Render("✍ you   "),
			dimStyle.Render(truncate(shortPath(e.FilePath, cwd), maxWidth-25)))

	case session.EventDiagnostics:
		return fmt.Sprintf("%s  %s  %s", tsStr, toolErrorStyle.Render("⚠ diag  "),
			dimStyle.Render(fmt.Sprintf("%s", summariseDiagnostics(e.Diagnostics))))

	default:
		return fmt.Sprintf("%s  %s", tsStr, dimStyle.Render("?"))
	}
}

// formatEventLineSelected renders the same event line but with background highlight.
// Each styled segment gets the selection background added so colours are preserved.
func formatEventLineSelected(e session.Event, maxWidth int, cwd string) string {
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
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(thinkingStyle).Render("~ think "),
			sel(mutedStyle).Render(thinkingSummary(e, maxWidth-25)))

	case session.EventText:
		text := truncate(firstLine(e.Text), maxWidth-20)
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(textStyle).Render("◁ text  "), sel(normalStyle).Render(text))

	case session.EventToolUse:
		name := toolColumn(e.ToolName)
		summary := truncate(formatToolSummary(e.ToolName, e.ToolInput, cwd), maxWidth-40)
		nameStyle, summaryStyle := toolStyles(e)
		return fmt.Sprintf("%s  %s  %s%s", tsStr, sel(nameStyle).Render(name),
			sel(summaryStyle).Render(summary), opOutcome(e, true))

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

	case session.EventAgentProgress:
		desc := truncate(e.AgentDescription, maxWidth-30)
		id := e.AgentID
		if len(id) > 8 {
			id = id[:8]
		}
		return fmt.Sprintf("%s  %s  %s %s", tsStr, sel(agentStyle).Render("⊞ agent "), sel(mutedStyle).Render(id), sel(dimStyle).Render(desc))

	case session.EventHookProgress:
		name := e.HookName
		if name == "" {
			name = e.HookEvent
		}
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(dimStyle).Render("⚡ hook  "), sel(mutedStyle).Render(name))

	case session.EventBashProgress:
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(dimStyle).Render("… bash  "), sel(mutedStyle).Render(fmt.Sprintf("%ds", e.BashElapsedSec)))

	case session.EventTurnDuration:
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(dimStyle).Render("⏱ turn  "), sel(mutedStyle).Render(formatDuration(e.TurnDurationMs)))

	case session.EventToolDenied:
		text := truncate(firstLine(e.UserFeedback), maxWidth-30)
		return fmt.Sprintf("%s  %s  %s %s", tsStr, sel(toolErrorStyle).Render("⊘ denied"),
			sel(mutedStyle).Render(e.DenialKind), sel(dimStyle).Render(text))

	case session.EventUserFileEdit:
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(userStyle).Render("✍ you   "),
			sel(normalStyle).Render(truncate(shortPath(e.FilePath, cwd), maxWidth-25)))

	case session.EventDiagnostics:
		return fmt.Sprintf("%s  %s  %s", tsStr, sel(toolErrorStyle).Render("⚠ diag  "),
			sel(dimStyle).Render(summariseDiagnostics(e.Diagnostics)))

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

func formatToolSummary(tool string, input map[string]interface{}, cwd string) string {
	switch tool {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return "$ " + firstLine(cmd)
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return shortPath(fp, cwd)
		}
	case "Write":
		if fp, ok := input["file_path"].(string); ok {
			return "→ " + shortPath(fp, cwd)
		}
	case "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return "✎ " + shortPath(fp, cwd)
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
			return fmt.Sprintf(`"%s" %s`, p, shortPath(path, cwd))
		}
	case "Task":
		desc, _ := input["description"].(string)
		agentType, _ := input["subagent_type"].(string)
		if agentType != "" && desc != "" {
			return fmt.Sprintf("[%s] %s", agentType, desc)
		}
		if desc != "" {
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
	case "EnterPlanMode":
		return "entering plan mode"
	case "ExitPlanMode":
		return "plan ready for approval"
	case "AskUserQuestion":
		if qs, ok := input["questions"].([]interface{}); ok && len(qs) > 0 {
			if q, ok := qs[0].(map[string]interface{}); ok {
				if text, ok := q["question"].(string); ok {
					return text
				}
			}
		}
		return "asking user"
	case "TaskList":
		return "listing tasks"
	case "TaskGet":
		if id, ok := input["taskId"].(string); ok {
			return "#" + id
		}
	case "NotebookEdit":
		if fp, ok := input["notebook_path"].(string); ok {
			return shortPath(fp, cwd)
		}
	case "TaskStop":
		if id, ok := input["task_id"].(string); ok {
			return "stop #" + id
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
		if strings.TrimSpace(e.Thinking) == "" {
			lines = append(lines, "  "+dimStyle.Render("The agent reasoned at this point, but extended thinking is"))
			lines = append(lines, "  "+dimStyle.Render("signed rather than stored — the transcript keeps no body text."))
		} else {
			lines = append(lines, wrapLines(e.Thinking, width-4, "  ")...)
		}

	case session.EventText:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Response — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, wrapLines(e.Text, width-4, "  ")...)

	case session.EventToolUse:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" %s — %s", e.ToolName, ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")

		meta := []string{}
		if e.DurationMs >= 0 {
			meta = append(meta, "took "+formatDuration(e.DurationMs))
		} else {
			meta = append(meta, toolErrorStyle.Render("no result recorded"))
		}
		if e.LinesAdded > 0 || e.LinesRemoved > 0 {
			meta = append(meta, fmt.Sprintf("%s %s",
				diffAddStyle.Render(fmt.Sprintf("+%d", e.LinesAdded)),
				diffRemoveStyle.Render(fmt.Sprintf("-%d", e.LinesRemoved))))
		}
		if e.IsSidechain {
			meta = append(meta, agentStyle.Render("subagent"))
		}
		lines = append(lines, "  "+mutedStyle.Render(strings.Join(meta, "  ·  ")))
		lines = append(lines, "")

		// Special rendering for Edit tool — show as diff
		if e.ToolName == "Edit" {
			if e.Result != nil && len(e.Result.StructuredPatch) > 0 {
				lines = append(lines, "  "+dimStyle.Render("File: ")+
					toolUseStyle.Render(shortPath(e.Result.FilePath, "")))
				lines = append(lines, "")
				lines = append(lines, renderPatch(e.Result.StructuredPatch, width)...)
			} else {
				lines = append(lines, renderEditDiff(e.ToolInput, width)...)
			}
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

		// The result is folded into the call, so show it on the same screen.
		if e.Result != nil || e.ToolOutput != "" {
			lines = append(lines, "")
			lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
			label := " Result"
			if e.IsError {
				label = " Result (failed)"
			}
			lines = append(lines, headerLabelStyle.Render(label))
			lines = append(lines, "")
			lines = append(lines, renderToolResult(e, width)...)
		}

	case session.EventToolResult:
		title := "Tool Result"
		if e.IsError {
			title = "Tool Result (Error)"
		}
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" %s — %s", title, ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, renderToolResult(e, width)...)

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

	case session.EventAgentProgress:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Agent Progress — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		if e.AgentID != "" {
			lines = append(lines, fieldLine("Agent ID", e.AgentID))
		}
		if e.AgentDescription != "" {
			lines = append(lines, "")
			lines = append(lines, "  "+dimStyle.Render("Description:"))
			lines = append(lines, wrapLines(e.AgentDescription, width-4, "  ")...)
		}

	case session.EventHookProgress:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Hook — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		if e.HookEvent != "" {
			lines = append(lines, fieldLine("Event", e.HookEvent))
		}
		if e.HookName != "" {
			lines = append(lines, fieldLine("Hook", e.HookName))
		}

	case session.EventBashProgress:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Bash Progress — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, fieldLine("Elapsed", fmt.Sprintf("%ds", e.BashElapsedSec)))

	case session.EventTurnDuration:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Turn Duration — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, fieldLine("Duration", formatDuration(e.TurnDurationMs)))

	case session.EventToolDenied:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Tool Call Denied — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, fieldLine("Kind", toolErrorStyle.Render(e.DenialKind)))
		if e.UserFeedback != "" {
			lines = append(lines, "")
			lines = append(lines, "  "+dimStyle.Render("Feedback given to the agent:"))
			lines = append(lines, wrapLines(e.UserFeedback, width-4, "  ")...)
		}

	case session.EventUserFileEdit:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" File Edited Outside Claude — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		lines = append(lines, fieldLine("File", userStyle.Render(e.FilePath)))
		if e.Text != "" {
			lines = append(lines, "")
			lines = append(lines, "  "+dimStyle.Render("Snippet:"))
			lines = append(lines, wrapLines(e.Text, width-4, "  ")...)
		}

	case session.EventDiagnostics:
		lines = append(lines, headerStyle.Render(fmt.Sprintf(" Diagnostics — %s", ts)))
		lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
		lines = append(lines, "")
		for _, d := range e.Diagnostics {
			style := dimStyle
			if strings.EqualFold(d.Severity, "error") {
				style = toolErrorStyle
			}
			lines = append(lines, fmt.Sprintf("  %s %s",
				style.Render(fmt.Sprintf("[%s]", d.Severity)), normalStyle.Render(d.Message)))
			lines = append(lines, "      "+mutedStyle.Render(d.File))
		}
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
				lines = append(lines, "  "+diffRemoveStyle.Render("- "+truncateRunes(l, maxW)))
			}
		}
		if newStr != "" {
			lines = append(lines, "  "+diffAddStyle.Render("+++ added"))
			for _, l := range strings.Split(newStr, "\n") {
				lines = append(lines, "  "+diffAddStyle.Render("+ "+truncateRunes(l, maxW)))
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

// truncate cuts to a rune count, not a byte count. Paths, box-drawing glyphs and
// any accented text are multi-byte, so slicing by byte both cut lines far short
// of the terminal width and could split a rune in half.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 40
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}

// truncateRunes cuts to a rune count without appending an ellipsis.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
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
		result = append(result, prefix+truncateRunes(line, maxWidth))
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

// sidechainMark flags events that happened inside a subagent turn.
func sidechainMark(e session.Event, selected bool) string {
	if !e.IsSidechain {
		return " "
	}
	if selected {
		return lipgloss.NewStyle().Foreground(colorPurple).Background(colorBgSelected).Render("│")
	}
	return agentStyle.Render("│")
}

// opOutcome renders what a tool call produced. The result is folded into the
// call event, so one row carries both the operation and how it went.
func opOutcome(e session.Event, selected bool) string {
	bg := func(base lipgloss.Style) lipgloss.Style {
		if selected {
			return base.Copy().Background(colorBgSelected)
		}
		return base
	}

	if e.IsError {
		return bg(toolErrorStyle).Render("  ✗ " + truncate(firstLine(errorText(e)), 28))
	}

	parts := []string{}
	switch {
	case e.LinesAdded > 0 || e.LinesRemoved > 0:
		parts = append(parts,
			bg(diffAddStyle).Render(fmt.Sprintf("+%d", e.LinesAdded))+
				bg(diffRemoveStyle).Render(fmt.Sprintf(" -%d", e.LinesRemoved)))
	case len(e.ToolOutput) > 1024:
		parts = append(parts, bg(mutedStyle).Render(formatBytes(len(e.ToolOutput))))
	}
	if e.DurationMs >= 1000 {
		parts = append(parts, bg(mutedStyle).Render(formatDuration(e.DurationMs)))
	}
	if e.DurationMs < 0 && e.Type == session.EventToolUse {
		parts = append(parts, bg(mutedStyle).Render("no result"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, bg(mutedStyle).Render(" "))
}

// errorText prefers the structured failure record over the raw content block.
func errorText(e session.Event) string {
	if e.Result != nil {
		if e.Result.Raw != "" {
			return e.Result.Raw
		}
		if e.Result.Stderr != "" {
			return e.Result.Stderr
		}
	}
	return e.ToolOutput
}

func formatDuration(ms int) string {
	switch {
	case ms < 0:
		return "—"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
	}
}

func summariseDiagnostics(diags []session.Diagnostic) string {
	errs := 0
	for _, d := range diags {
		if strings.EqualFold(d.Severity, "error") {
			errs++
		}
	}
	if errs > 0 {
		return fmt.Sprintf("%d diagnostics (%d errors)", len(diags), errs)
	}
	return fmt.Sprintf("%d diagnostics", len(diags))
}

// renderToolResult renders the structured record of a completed operation,
// falling back to the raw text block when no structured form was recorded.
func renderToolResult(e session.Event, width int) []string {
	var lines []string
	r := e.Result

	if r == nil {
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("Output (%s):", formatBytes(len(e.ToolOutput)))))
		lines = append(lines, "")
		return append(lines, wrapLines(e.ToolOutput, width-4, "  ")...)
	}

	if r.Raw != "" {
		lines = append(lines, "  "+toolErrorStyle.Render("The operation failed:"))
		lines = append(lines, "")
		return append(lines, wrapLines(r.Raw, width-4, "  ")...)
	}

	// Edit / Write — show the diff Claude Code actually recorded, not the request.
	if len(r.StructuredPatch) > 0 {
		added, removed := r.Churn()
		if r.FilePath != "" {
			lines = append(lines, "  "+dimStyle.Render("File: ")+toolUseStyle.Render(r.FilePath))
		}
		lines = append(lines, "  "+diffAddStyle.Render(fmt.Sprintf("+%d", added))+" "+
			diffRemoveStyle.Render(fmt.Sprintf("-%d", removed))+
			mutedStyle.Render(fmt.Sprintf("  across %d hunk(s)", len(r.StructuredPatch))))
		if r.UserModified {
			lines = append(lines, "  "+systemStyle.Render("(the file had been modified by you since the agent last read it)"))
		}
		lines = append(lines, "")
		lines = append(lines, renderPatch(r.StructuredPatch, width)...)
		return lines
	}

	// Bash — stdout and stderr are recorded separately.
	if r.Stdout != "" || r.Stderr != "" || r.Interrupted {
		if r.Interrupted {
			lines = append(lines, "  "+toolErrorStyle.Render("⊘ interrupted before completion"))
			lines = append(lines, "")
		}
		if r.Stdout != "" {
			lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("stdout (%s):", formatBytes(len(r.Stdout)))))
			lines = append(lines, wrapLines(r.Stdout, width-4, "  ")...)
		}
		if r.Stderr != "" {
			lines = append(lines, "")
			lines = append(lines, "  "+toolErrorStyle.Render(fmt.Sprintf("stderr (%s):", formatBytes(len(r.Stderr)))))
			for _, l := range wrapLines(r.Stderr, width-4, "  ") {
				lines = append(lines, toolErrorStyle.Render(l))
			}
		}
		return lines
	}

	// Read — report what slice of the file was pulled into context.
	if r.File != nil {
		lines = append(lines, "  "+dimStyle.Render("File: ")+toolUseStyle.Render(r.File.FilePath))
		lines = append(lines, fieldLine("Lines Read", fmt.Sprintf("%d of %d (from line %d)",
			r.File.NumLines, r.File.TotalLines, r.File.StartLine)))
		lines = append(lines, "")
	}

	lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("Output (%s):", formatBytes(len(e.ToolOutput)))))
	lines = append(lines, "")
	return append(lines, wrapLines(e.ToolOutput, width-4, "  ")...)
}

// renderPatch colours a set of unified-diff hunks.
func renderPatch(hunks []session.PatchHunk, width int) []string {
	maxW := min(width-6, 160)
	var lines []string
	for _, h := range hunks {
		lines = append(lines, "  "+systemStyle.Render(fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			h.OldStart, h.OldLines, h.NewStart, h.NewLines)))
		for _, l := range h.Lines {
			l = truncateRunes(l, maxW)
			switch {
			case strings.HasPrefix(l, "+"):
				lines = append(lines, "  "+diffAddStyle.Render(l))
			case strings.HasPrefix(l, "-"):
				lines = append(lines, "  "+diffRemoveStyle.Render(l))
			default:
				lines = append(lines, "  "+dimStyle.Render(l))
			}
		}
		lines = append(lines, "")
	}
	return lines
}

// toolStyles colours a tool row by operation kind, with failures overriding.
func toolStyles(e session.Event) (lipgloss.Style, lipgloss.Style) {
	if e.IsError {
		return toolErrorStyle, dimStyle
	}
	switch e.ToolName {
	case "Edit", "MultiEdit", "NotebookEdit":
		return lipgloss.NewStyle().Foreground(colorYellow).Bold(true),
			lipgloss.NewStyle().Foreground(colorYellow)
	case "Write":
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true),
			lipgloss.NewStyle().Foreground(colorGreen)
	case "Read":
		return lipgloss.NewStyle().Foreground(colorCyan).Bold(true), dimStyle
	case "Bash":
		return lipgloss.NewStyle().Foreground(colorOrange).Bold(true), dimStyle
	case "Task":
		return agentStyle.Copy().Bold(true), agentStyle
	}
	return toolUseStyle, dimStyle
}

// thinkingSummary describes a thinking step. Extended thinking is signed but not
// retained in the transcript, so most of these have no body to show.
func thinkingSummary(e session.Event, maxWidth int) string {
	if strings.TrimSpace(e.Thinking) == "" {
		return "(reasoning not retained in transcript)"
	}
	return truncate(firstLine(e.Thinking), maxWidth)
}

// toolColumn renders the tool name in a fixed-width column. Names longer than
// the column are abbreviated rather than allowed to shift everything right.
func toolColumn(name string) string {
	const w = 7
	if len([]rune(name)) > w {
		name = truncate(name, w)
	}
	return fmt.Sprintf("▷ %-*s", w, name)
}
