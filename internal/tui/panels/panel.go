package panels

import tea "github.com/charmbracelet/bubbletea"

// Panel is the interface that all dashboard panels must implement.
// Panels receive all tea.Msg via Update and render via View.
type Panel interface {
	// Update handles a Bubble Tea message and returns an optional command.
	Update(msg tea.Msg) tea.Cmd

	// View renders the panel content (without border — the layout adds borders).
	View() string

	// SetSize sets the available width and height for this panel.
	SetSize(w, h int)

	// SetFocused sets whether this panel currently has keyboard focus.
	SetFocused(focused bool)
}
