package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"verbose/internal/session"
	"verbose/internal/ui"
)

func main() {
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

	model := ui.NewModel(store, *project)
	model.SetUpdates(updates)

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
