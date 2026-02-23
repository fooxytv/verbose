package ui

import (
	"fmt"
	"strings"
	"time"

	"verbose/internal/session"
)

// renderSessionsList renders the sessions list view.
func renderSessionsList(sessions []session.SessionInfo, cursor int, width, height int) string {
	var b strings.Builder

	// Header
	header := headerStyle.Render(fmt.Sprintf(" verbose — Sessions (%d)", len(sessions)))
	b.WriteString(header)
	b.WriteString("\n")

	// Column headers
	cols := mutedStyle.Render(fmt.Sprintf("  %-4s  %-18s  %-10s  %7s  %9s  %8s  %5s  %5s",
		"", "PROJECT", "SESSION", "AGO", "TOKENS", "COST", "TOOLS", "EDITS"))
	b.WriteString(cols)
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", min(width, 90))))
	b.WriteString("\n")

	if len(sessions) == 0 {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  No sessions found. Start Claude Code to see sessions here."))
		b.WriteString("\n")
		return b.String()
	}

	// Calculate visible range for scrolling
	listHeight := height - 6
	if listHeight < 1 {
		listHeight = 1
	}

	start := 0
	if cursor >= listHeight {
		start = cursor - listHeight + 1
	}
	end := start + listHeight
	if end > len(sessions) {
		end = len(sessions)
	}

	for i := start; i < end; i++ {
		s := sessions[i]
		line := formatSessionLine(s, width)

		if i == cursor {
			b.WriteString(selectedStyle.Render("▸ " + line))
		} else {
			b.WriteString(normalStyle.Render("  " + line))
		}
		b.WriteString("\n")
	}

	// Scroll indicator
	if len(sessions) > listHeight {
		pct := float64(cursor+1) / float64(len(sessions)) * 100
		b.WriteString(mutedStyle.Render(fmt.Sprintf("\n  [%d/%d %.0f%%]", cursor+1, len(sessions), pct)))
		b.WriteString("\n")
	}

	return b.String()
}

func formatSessionLine(s session.SessionInfo, width int) string {
	// Status indicator
	status := "●"
	if s.IsAgent {
		status = "◦"
	}

	ago := timeAgo(s.LastUpdate)
	totalTokens := s.InputTokens + s.OutputTokens + s.CacheReadTokens + s.CacheWriteTokens
	tokenStr := formatTokens(totalTokens)
	costStr := fmt.Sprintf("$%.4f", s.CostUSD)

	shortID := s.ID
	if len(shortID) > 10 {
		shortID = shortID[:8] + ".."
	}

	project := s.ProjectName
	if len(project) > 18 {
		project = project[:15] + "..."
	}

	toolStr := fmt.Sprintf("%d", s.ToolCallCount)
	editStr := fmt.Sprintf("%d", len(s.FilesWritten)+len(s.FilesCreated))

	return fmt.Sprintf("%-4s  %-18s  %-10s  %7s  %9s  %8s  %5s  %5s",
		status, project, shortID, ago, tokenStr, costStr, toolStr, editStr)
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if h == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
