package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fooxytv/verbose/internal/session"
	"github.com/fooxytv/verbose/internal/ui"
)

// Set via ldflags: go build -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// Check for --version/-version before anything else
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" || arg == "-v" {
			fmt.Println("verbose " + version)
			os.Exit(0)
		}
	}

	project := flag.String("project", "", "filter to a specific project name")
	flag.Parse()

	store, err := session.NewStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Initial scan of all sessions
	if err := store.Scan(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to scan sessions: %v\n", err)
	}

	// Start watching for file changes
	updates := store.Watch()

	model := ui.NewModel(store, *project, version)
	model.SetUpdates(updates)

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
