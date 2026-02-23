package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors — dark theme inspired by GitHub Dark
	colorBg        = lipgloss.Color("#0d1117")
	colorBgLight   = lipgloss.Color("#161b22")
	colorBorder    = lipgloss.Color("#30363d")
	colorText      = lipgloss.Color("#c9d1d9")
	colorTextDim   = lipgloss.Color("#8b949e")
	colorTextMuted = lipgloss.Color("#6e7681")
	colorBlue      = lipgloss.Color("#58a6ff")
	colorGreen     = lipgloss.Color("#7ee787")
	colorYellow    = lipgloss.Color("#d29922")
	colorRed       = lipgloss.Color("#ff7b72")
	colorPurple    = lipgloss.Color("#bc8cff")
	colorCyan      = lipgloss.Color("#39d353")
	colorOrange    = lipgloss.Color("#f0883e")

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue).
			PaddingLeft(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted).
			PaddingLeft(1)

	headerStyle = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorText).
			Bold(true).
			Padding(0, 1)

	headerLabelStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true).
				Underline(true)

	colorBgSelected = lipgloss.Color("#1c2333")

	selectedStyle = lipgloss.NewStyle().
			Background(colorBgSelected).
			Foreground(colorBlue).
			Bold(true)

	// Selected row: highlighted background variants that preserve foreground colour
	selBg = lipgloss.NewStyle().Background(colorBgSelected)

	normalStyle = lipgloss.NewStyle().
			Foreground(colorText)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorTextDim)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted)

	// Event type styles
	userStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	thinkingStyle = lipgloss.NewStyle().
			Foreground(colorPurple)

	toolUseStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	toolResultStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	toolErrorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)

	textStyle = lipgloss.NewStyle().
			Foreground(colorText)

	systemStyle = lipgloss.NewStyle().
			Foreground(colorYellow)

	// Help bar
	helpStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted).
			PaddingLeft(1)

	keyStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	// Stats
	costStyle = lipgloss.NewStyle().
			Foreground(colorYellow)

	tokenStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	// Token bar colors
	tokenInputStyle = lipgloss.NewStyle().
			Foreground(colorBlue)

	tokenOutputStyle = lipgloss.NewStyle().
				Foreground(colorOrange)

	tokenCacheRStyle = lipgloss.NewStyle().
				Foreground(colorCyan)

	tokenCacheWStyle = lipgloss.NewStyle().
				Foreground(colorPurple)

	// Edit diff styles
	diffAddStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	diffRemoveStyle = lipgloss.NewStyle().
			Foreground(colorRed)

	diffContextStyle = lipgloss.NewStyle().
				Foreground(colorTextDim)
)
