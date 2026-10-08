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

	agentStyle = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

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

	// Status message (transient feedback)
	statusStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			PaddingLeft(1)

	// Active filter / search indicator on the timeline
	searchStyle = lipgloss.NewStyle().
			Foreground(colorPurple)

	// Search input prompt
	searchPromptStyle = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorPurple).
				Bold(true)

	// Delete confirmation prompt
	deletePromptStyle = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorRed).
				Bold(true)

	// Warning that a delete cannot be undone
	deleteWarnStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)
)

// Tree view: what a session did to each file.
var (
	// A directory, which the session never touches directly.
	dirStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	// A file this session created.
	createdStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	// A file this session changed. Orange rather than yellow: the mark a flash
	// leaves behind has to stay legible beside the green of a new file, and
	// yellow sat too close to it.
	changedStyle = lipgloss.NewStyle().
			Foreground(colorOrange).
			Bold(true)

	// A file this session removed. Deletion is inferred from shell commands,
	// never recorded, so this is the one state that can be wrong.
	deletedStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)

	// A file the session read but did not change.
	readStyle = lipgloss.NewStyle().
			Foreground(colorText)
)
