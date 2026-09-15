package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fooxytv/verbose/internal/session"
)

// renderSessionOverview renders the detailed overview panel for a session.
func renderSessionOverview(sess *session.Session, todos []session.TodoItem, hasProjectMemory bool, scroll int, width, height int) string {
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
	if info.GitBranch != "" {
		lines = append(lines, fieldLine("Git Branch", toolUseStyle.Render(info.GitBranch)))
	}
	if info.Model != "" {
		lines = append(lines, fieldLine("Model", info.Model))
	}
	lines = append(lines, fieldLine("Started", info.StartTime.Format("2006-01-02 15:04:05")))
	lines = append(lines, fieldLine("Last Active", info.LastUpdate.Format("2006-01-02 15:04:05")))
	duration := info.LastUpdate.Sub(info.StartTime)
	lines = append(lines, fieldLine("Duration", duration.Round(time.Second).String()))
	if info.ActiveDuration > 0 {
		lines = append(lines, fieldLine("Active Time", info.ActiveDuration.Round(time.Second).String()+
			dimStyle.Render(fmt.Sprintf("  (%.0f%% of elapsed)", activePercent(info.ActiveDuration, duration)))))
	}
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
	lines = append(lines, fieldLine("Total Events", fmt.Sprintf("%d", info.EventCount)))
	if info.SubagentCalls > 0 {
		lines = append(lines, fieldLine("Subagents", agentStyle.Render(fmt.Sprintf("%d dispatched", info.SubagentCalls))))
	}
	if info.SubagentEvents > 0 {
		lines = append(lines, fieldLine("  Subagent Ops", fmt.Sprintf("%d", info.SubagentEvents)))
	}
	if info.WebRequests > 0 {
		lines = append(lines, fieldLine("Web Requests", fmt.Sprintf("%d", info.WebRequests)))
	}
	if len(info.SkillsUsed) > 0 {
		lines = append(lines, fieldLine("Skills Used", strings.Join(info.SkillsUsed, ", ")))
	}
	if info.Errors > 0 {
		lines = append(lines, fieldLine("Failed Ops", toolErrorStyle.Render(fmt.Sprintf("%d", info.Errors))))
	}
	if info.Interruptions > 0 {
		lines = append(lines, fieldLine("Interrupted", toolErrorStyle.Render(fmt.Sprintf("%d", info.Interruptions))))
	}
	if info.Denials > 0 {
		lines = append(lines, fieldLine("Denied by User", toolErrorStyle.Render(fmt.Sprintf("%d", info.Denials))))
	}
	lines = append(lines, "")

	// Per-tool breakdown — what the agent actually spent its calls on
	if len(info.ToolCounts) > 0 {
		lines = append(lines, sectionHeader("Operations by Tool"))
		lines = append(lines, renderToolBreakdown(info.ToolCounts, min(width-24, 40))...)
		lines = append(lines, "")
	}

	// Code churn
	if info.LinesAdded > 0 || info.LinesRemoved > 0 {
		lines = append(lines, sectionHeader("Code Changes"))
		lines = append(lines, fieldLine("Net Churn", fmt.Sprintf("%s  %s",
			diffAddStyle.Render(fmt.Sprintf("+%d", info.LinesAdded)),
			diffRemoveStyle.Render(fmt.Sprintf("-%d", info.LinesRemoved)))))
		lines = append(lines, "")
		shown := info.FileChurns
		if len(shown) > 12 {
			shown = shown[:12]
		}
		for _, c := range shown {
			if c.LinesAdded == 0 && c.LinesRemoved == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("    %s %s %s  %s",
				diffAddStyle.Render(fmt.Sprintf("%+5d", c.LinesAdded)),
				diffRemoveStyle.Render(fmt.Sprintf("%-5d", -c.LinesRemoved)),
				mutedStyle.Render(fmt.Sprintf("%dx", c.Edits)),
				normalStyle.Render(shortPath(c.Path, info.CWD))))
		}
		if len(info.FileChurns) > 12 {
			lines = append(lines, "    "+mutedStyle.Render(fmt.Sprintf("... and %d more files", len(info.FileChurns)-12)))
		}
		lines = append(lines, "")
	}

	// Todos section
	if len(todos) > 0 {
		lines = append(lines, sectionHeader(fmt.Sprintf("Todos (%d)", len(todos))))
		for _, t := range todos {
			var icon string
			switch t.Status {
			case "completed":
				icon = userStyle.Render("[x]")
			case "in_progress":
				icon = toolUseStyle.Render("[~]")
			default:
				icon = dimStyle.Render("[ ]")
			}
			lines = append(lines, fmt.Sprintf("    %s %s", icon, normalStyle.Render(t.Subject)))
		}
		lines = append(lines, "")
	}

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
	if hasProjectMemory {
		lines = append(lines, dimStyle.Render("  Press ")+keyStyle.Render("p")+dimStyle.Render(" to view project memory and stats"))
	}

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

// activePercent expresses reported turn time as a share of wall-clock elapsed time.
func activePercent(active, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(active) / float64(elapsed) * 100
}

// renderToolBreakdown draws a sorted bar chart of tool call counts.
func renderToolBreakdown(counts map[string]int, barWidth int) []string {
	type kv struct {
		name string
		n    int
	}
	items := make([]kv, 0, len(counts))
	maxN, maxName := 0, 0
	for name, n := range counts {
		items = append(items, kv{name, n})
		if n > maxN {
			maxN = n
		}
		if len(name) > maxName {
			maxName = len(name)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].n != items[j].n {
			return items[i].n > items[j].n
		}
		return items[i].name < items[j].name
	})

	if barWidth < 8 {
		barWidth = 8
	}
	var out []string
	for _, it := range items {
		w := 0
		if maxN > 0 {
			w = it.n * barWidth / maxN
		}
		if w == 0 && it.n > 0 {
			w = 1
		}
		out = append(out, fmt.Sprintf("    %s %s %s",
			dimStyle.Render(fmt.Sprintf("%-*s", maxName, it.name)),
			toolUseStyle.Render(strings.Repeat("▪", w)),
			mutedStyle.Render(fmt.Sprintf("%d", it.n))))
	}
	return out
}

// shortPath renders a path relative to cwd when it sits underneath it.
func shortPath(path, cwd string) string {
	if cwd == "" {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
