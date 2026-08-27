package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSecondary = lipgloss.Color("#06B6D4") // cyan
	colorRunning   = lipgloss.Color("#22C55E") // green
	colorPending   = lipgloss.Color("#EAB308") // yellow
	colorDone      = lipgloss.Color("#6B7280") // gray
	colorFailed    = lipgloss.Color("#EF4444") // red
	colorAssigned  = lipgloss.Color("#3B82F6") // blue
	colorFocused   = lipgloss.Color("#7C3AED") // purple border for focus
	colorDim       = lipgloss.Color("#6B7280") // dim text

	// Title bar
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary).
			Padding(0, 1)

	// Panel borders
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim)

	focusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorFocused)

	// Panel headers
	panelHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSecondary)

	// Status colors for task status text
	statusStyles = map[string]lipgloss.Style{
		"running":  lipgloss.NewStyle().Foreground(colorRunning).Bold(true),
		"pending":  lipgloss.NewStyle().Foreground(colorPending),
		"assigned": lipgloss.NewStyle().Foreground(colorAssigned),
		"done":     lipgloss.NewStyle().Foreground(colorDone),
		"failed":   lipgloss.NewStyle().Foreground(colorFailed).Bold(true),
	}

	// Status bar
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#1F2937")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Padding(0, 1)

	// Agent status
	agentActiveStyle = lipgloss.NewStyle().Foreground(colorRunning)
	agentIdleStyle   = lipgloss.NewStyle().Foreground(colorDim)

	// Error banner
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorFailed).
			Padding(0, 1).
			Bold(true)

	// Selection cursor
	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#374151")).
			Foreground(lipgloss.Color("#FFFFFF"))
)

// styledStatus returns a styled string for a task status.
func styledStatus(status string) string {
	if style, ok := statusStyles[status]; ok {
		return style.Render(status)
	}
	return status
}
