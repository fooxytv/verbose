package ui

import (
	"fmt"
	"strings"

	"github.com/fooxytv/verbose/internal/session"
)

// renderProjectView renders the project-level view with memory, stats, and session list.
func renderProjectView(proj *session.ProjectInfo, scroll, cursor, width, height int) string {
	var lines []string

	// Header
	lines = append(lines, headerStyle.Render(fmt.Sprintf(" Project: %s", proj.ProjectName)))
	dirLabel := proj.CWD
	if dirLabel == "" {
		dirLabel = proj.ProjectDir
	}
	lines = append(lines, "  "+dimStyle.Render(dirLabel))
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
	lines = append(lines, "")

	// Stats section
	lines = append(lines, sectionHeader("Stats"))
	lines = append(lines, fieldLine("Sessions", fmt.Sprintf("%d", proj.TotalSessions)))
	if !proj.FirstSession.IsZero() {
		lines = append(lines, fieldLine("First Session", proj.FirstSession.Format("2006-01-02 15:04")))
	}
	if !proj.LastSession.IsZero() {
		lines = append(lines, fieldLine("Last Session", proj.LastSession.Format("2006-01-02 15:04")))
	}
	lines = append(lines, fieldLine("Total Cost", costStyle.Render(fmt.Sprintf("$%.4f", proj.TotalCostUSD))))

	totalTokens := proj.TotalInputTokens + proj.TotalOutputTokens + proj.TotalCacheReadTokens + proj.TotalCacheWriteTokens
	lines = append(lines, fieldLine("Total Tokens", tokenStyle.Render(formatTokensComma(totalTokens))))
	lines = append(lines, fieldLine("  Input", formatTokensComma(proj.TotalInputTokens)))
	lines = append(lines, fieldLine("  Output", formatTokensComma(proj.TotalOutputTokens)))
	lines = append(lines, fieldLine("  Cache Read", formatTokensComma(proj.TotalCacheReadTokens)))
	lines = append(lines, fieldLine("  Cache Write", formatTokensComma(proj.TotalCacheWriteTokens)))

	// Token bar
	if totalTokens > 0 {
		info := session.SessionInfo{
			InputTokens:      proj.TotalInputTokens,
			OutputTokens:     proj.TotalOutputTokens,
			CacheReadTokens:  proj.TotalCacheReadTokens,
			CacheWriteTokens: proj.TotalCacheWriteTokens,
		}
		lines = append(lines, "")
		lines = append(lines, renderTokenBar(info, min(width-6, 60)))
	}

	lines = append(lines, fieldLine("Tool Calls", fmt.Sprintf("%d", proj.TotalToolCalls)))
	lines = append(lines, fieldLine("User Prompts", fmt.Sprintf("%d", proj.TotalUserPrompts)))
	lines = append(lines, fieldLine("Code Churn", fmt.Sprintf("%s  %s",
		diffAddStyle.Render(fmt.Sprintf("+%d", proj.TotalLinesAdded)),
		diffRemoveStyle.Render(fmt.Sprintf("-%d", proj.TotalLinesRemoved)))))
	if proj.TotalSubagentCalls > 0 {
		lines = append(lines, fieldLine("Subagents", agentStyle.Render(fmt.Sprintf("%d dispatched", proj.TotalSubagentCalls))))
	}
	if proj.TotalErrors > 0 {
		lines = append(lines, fieldLine("Failed Ops", toolErrorStyle.Render(fmt.Sprintf("%d", proj.TotalErrors))))
	}
	if proj.TotalInterruptions > 0 {
		lines = append(lines, fieldLine("Interrupted", toolErrorStyle.Render(fmt.Sprintf("%d", proj.TotalInterruptions))))
	}
	if proj.TotalDenials > 0 {
		lines = append(lines, fieldLine("Denied by User", toolErrorStyle.Render(fmt.Sprintf("%d", proj.TotalDenials))))
	}
	lines = append(lines, "")

	// Per-tool breakdown across the whole project
	if len(proj.ToolCounts) > 0 {
		lines = append(lines, sectionHeader("Operations by Tool"))
		lines = append(lines, renderToolBreakdown(proj.ToolCounts, min(width-24, 40))...)
		lines = append(lines, "")
	}

	// Most-edited files
	if len(proj.MostEditedFiles) > 0 {
		lines = append(lines, sectionHeader("Most-Edited Files"))
		for _, f := range proj.MostEditedFiles {
			lines = append(lines, fmt.Sprintf("    %s %s %s  %s",
				toolUseStyle.Render(fmt.Sprintf("%3dx", f.Count)),
				diffAddStyle.Render(fmt.Sprintf("%+6d", f.LinesAdded)),
				diffRemoveStyle.Render(fmt.Sprintf("%-6d", -f.LinesRemoved)),
				normalStyle.Render(shortPath(f.Path, proj.CWD))))
		}
		lines = append(lines, "")
	}

	// MEMORY.md section
	lines = append(lines, sectionHeader("Project Memory"))
	if proj.Memory != "" {
		lines = append(lines, "")
		for _, l := range strings.Split(proj.Memory, "\n") {
			lines = append(lines, "  "+normalStyle.Render(truncateRunes(l, width-4)))
		}
	} else {
		lines = append(lines, "  "+dimStyle.Render("No project memory found."))
	}
	lines = append(lines, "")

	// Sessions list
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
	lines = append(lines, sectionHeader(fmt.Sprintf("Sessions (%d)", len(proj.Sessions))))
	lines = append(lines, "")

	for i, s := range proj.Sessions {
		line := formatSessionLine(s, width-4)
		if i == cursor {
			lines = append(lines, selectedStyle.Render("▸ "+line))
		} else {
			lines = append(lines, normalStyle.Render("  "+line))
		}
	}

	// Apply scroll window
	visibleHeight := height - 3
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	if scroll > len(lines)-visibleHeight {
		scroll = max(0, len(lines)-visibleHeight)
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + visibleHeight
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[scroll:end]

	return strings.Join(visible, "\n")
}
