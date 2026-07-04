package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "revu: must be run inside a git repository")
		os.Exit(1)
	}
	store, err := LoadStore(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "revu:", err)
		os.Exit(1)
	}
	app, err := NewApp(root, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "revu:", err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "revu:", err)
		os.Exit(1)
	}
}
