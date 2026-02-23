package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"verbose/internal/session"
)

// renderSessionOverview renders the detailed overview panel for a session.
func renderSessionOverview(sess *session.Session, scroll int, width, height int) string {
	// Build all lines first, then apply scroll window
	var lines []string

	info := sess.Info

	// Header
	lines = append(lines, headerStyle.Render(fmt.Sprintf(" %s > %s", info.ProjectName, shortID(info.ID))))
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 100))))
	lines = append(lines, "")

	// Session metadata
	lines = append(lines, sectionHeader("Session Info"))
	lines = append(lines, fieldLine("Session ID", info.ID))
	lines = append(lines, fieldLine("Project", info.ProjectDir))
	lines = append(lines, fieldLine("Working Dir", info.CWD))
	if info.Model != "" {
		lines = append(lines, fieldLine("Model", info.Model))
	}
	lines = append(lines, fieldLine("Started", info.StartTime.Format("2006-01-02 15:04:05")))
	lines = append(lines, fieldLine("Last Active", info.LastUpdate.Format("2006-01-02 15:04:05")))
	duration := info.LastUpdate.Sub(info.StartTime)
	lines = append(lines, fieldLine("Duration", duration.Round(1e9).String()))
	if info.IsAgent {
		lines = append(lines, fieldLine("Type", systemStyle.Render("Subagent")))
	}
	lines = append(lines, "")

	// Token breakdown
	lines = append(lines, sectionHeader("Token Usage"))
	totalTokens := info.InputTokens + info.OutputTokens + info.CacheReadTokens + info.CacheWriteTokens
	lines = append(lines, fieldLine("Total Tokens", tokenStyle.Render(formatTokensComma(totalTokens))))
	lines = append(lines, fieldLine("  Input", formatTokensComma(info.InputTokens)))
	lines = append(lines, fieldLine("  Output", formatTokensComma(info.OutputTokens)))
	lines = append(lines, fieldLine("  Cache Read", formatTokensComma(info.CacheReadTokens)))
	lines = append(lines, fieldLine("  Cache Write", formatTokensComma(info.CacheWriteTokens)))
	lines = append(lines, fieldLine("API Equiv.", costStyle.Render(fmt.Sprintf("$%.4f", info.CostUSD))+dimStyle.Render(" (not actual cost on Max plan)")))

	// Token bar visualization
	if totalTokens > 0 {
		lines = append(lines, "")
		lines = append(lines, renderTokenBar(info, min(width-6, 60)))
	}
	lines = append(lines, "")

	// Activity summary
	lines = append(lines, sectionHeader("Activity"))
	lines = append(lines, fieldLine("User Prompts", fmt.Sprintf("%d", info.UserPrompts)))
	lines = append(lines, fieldLine("Tool Calls", fmt.Sprintf("%d", info.ToolCallCount)))
	lines = append(lines, fieldLine("Bash Commands", fmt.Sprintf("%d", info.BashCommands)))
	lines = append(lines, fieldLine("Total Events", fmt.Sprintf("%d", info.EventCount)))
	if info.Errors > 0 {
		lines = append(lines, fieldLine("Errors", toolErrorStyle.Render(fmt.Sprintf("%d", info.Errors))))
	}
	lines = append(lines, "")

	// Files read
	if len(info.FilesRead) > 0 {
		lines = append(lines, sectionHeader(fmt.Sprintf("Files Read (%d)", len(info.FilesRead))))
		sorted := sortedShortPaths(info.FilesRead, info.CWD)
		for _, fp := range sorted {
			lines = append(lines, dimStyle.Render("    ")+normalStyle.Render(fp))
		}
		lines = append(lines, "")
	}

	// Files written/edited
	if len(info.FilesWritten) > 0 {
		lines = append(lines, sectionHeader(fmt.Sprintf("Files Edited (%d)", len(info.FilesWritten))))
		sorted := sortedShortPaths(info.FilesWritten, info.CWD)
		for _, fp := range sorted {
			lines = append(lines, dimStyle.Render("    ")+toolUseStyle.Render(fp))
		}
		lines = append(lines, "")
	}

	// Files created
	if len(info.FilesCreated) > 0 {
		lines = append(lines, sectionHeader(fmt.Sprintf("Files Created (%d)", len(info.FilesCreated))))
		sorted := sortedShortPaths(info.FilesCreated, info.CWD)
		for _, fp := range sorted {
			lines = append(lines, dimStyle.Render("    ")+userStyle.Render(fp))
		}
		lines = append(lines, "")
	}

	lines = append(lines, mutedStyle.Render(strings.Repeat("─", min(width, 60))))
	lines = append(lines, dimStyle.Render("  Press ")+keyStyle.Render("enter")+dimStyle.Render(" or ")+keyStyle.Render("t")+dimStyle.Render(" to view event timeline"))

	// Apply scroll window
	visibleHeight := height - 3 // leave room for help bar
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

	return strings.Join(visible, "\n")
}

func sectionHeader(title string) string {
	return "  " + headerLabelStyle.Render(title)
}

func fieldLine(label, value string) string {
	return fmt.Sprintf("    %s %s", dimStyle.Render(fmt.Sprintf("%-16s", label)), value)
}

func renderTokenBar(info session.SessionInfo, barWidth int) string {
	total := info.InputTokens + info.OutputTokens + info.CacheReadTokens + info.CacheWriteTokens
	if total == 0 || barWidth < 10 {
		return ""
	}

	inputW := int(float64(info.InputTokens) / float64(total) * float64(barWidth))
	outputW := int(float64(info.OutputTokens) / float64(total) * float64(barWidth))
	cacheRW := int(float64(info.CacheReadTokens) / float64(total) * float64(barWidth))
	cacheWW := barWidth - inputW - outputW - cacheRW
	if cacheWW < 0 {
		cacheWW = 0
	}

	bar := tokenInputStyle.Render(strings.Repeat("█", inputW)) +
		tokenOutputStyle.Render(strings.Repeat("█", outputW)) +
		tokenCacheRStyle.Render(strings.Repeat("▓", cacheRW)) +
		tokenCacheWStyle.Render(strings.Repeat("░", cacheWW))

	legend := fmt.Sprintf("    %s input  %s output  %s cache-r  %s cache-w",
		tokenInputStyle.Render("█"),
		tokenOutputStyle.Render("█"),
		tokenCacheRStyle.Render("▓"),
		tokenCacheWStyle.Render("░"),
	)

	return "    " + bar + "\n" + legend
}

func sortedShortPaths(paths []string, cwd string) []string {
	result := make([]string, len(paths))
	copy(result, paths)
	sort.Strings(result)

	// Try to make paths relative to CWD for readability
	if cwd != "" {
		for i, fp := range result {
			if rel, err := filepath.Rel(cwd, fp); err == nil && !strings.HasPrefix(rel, "..") {
				result[i] = rel
			}
		}
	}
	return result
}

func formatTokensComma(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	// Insert commas
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(ch))
	}
	return string(out)
}
