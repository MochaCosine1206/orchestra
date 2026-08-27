package panels

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Compile-time interface check.
var _ Panel = (*ConductorPanel)(nil)

// ConductorPanel displays active conductors and allows filtering by session.
type ConductorPanel struct {
	conductors []db.ConductorRecord
	taskCounts map[string][2]int // conductorID -> [done, total]
	cursor     int
	width      int
	height     int
	focused    bool
	selected   string // conductor ID currently selected for filtering ("" = show all)
}

// NewConductorPanel creates a new conductor panel.
func NewConductorPanel() *ConductorPanel {
	return &ConductorPanel{
		taskCounts: make(map[string][2]int),
	}
}

// SetSize updates the panel dimensions.
func (p *ConductorPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused sets whether this panel has focus.
func (p *ConductorPanel) SetFocused(focused bool) {
	p.focused = focused
}

// Update handles a Bubble Tea message.
func (p *ConductorPanel) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// SetData refreshes the conductor data.
func (p *ConductorPanel) SetData(conductors []db.ConductorRecord) {
	p.conductors = conductors
	if p.cursor >= len(conductors) {
		p.cursor = max(0, len(conductors)-1)
	}
}

// SetTaskCounts sets the done/total task counts per conductor.
func (p *ConductorPanel) SetTaskCounts(counts map[string][2]int) {
	p.taskCounts = counts
}

// MoveUp moves the cursor up.
func (p *ConductorPanel) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// MoveDown moves the cursor down.
func (p *ConductorPanel) MoveDown() {
	if p.cursor < len(p.conductors)-1 {
		p.cursor++
	}
}

// Select toggles selection of the current conductor for filtering.
// Returns the selected conductor ID, or "" if deselected.
func (p *ConductorPanel) Select() string {
	if len(p.conductors) == 0 {
		return ""
	}
	id := p.conductors[p.cursor].ID
	if p.selected == id {
		p.selected = "" // deselect
	} else {
		p.selected = id
	}
	return p.selected
}

// Deselect clears the conductor selection filter.
func (p *ConductorPanel) Deselect() {
	p.selected = ""
}

// SelectedID returns the currently selected conductor ID for filtering.
func (p *ConductorPanel) SelectedID() string {
	return p.selected
}

// View renders the conductor panel.
func (p *ConductorPanel) View() string {
	title := "CONDUCTORS"
	if p.selected != "" {
		title += " (filtered)"
	}

	if len(p.conductors) == 0 {
		return fmt.Sprintf("%s\n  No active conductors", title)
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")

	availLines := p.height - 2
	if availLines < 1 {
		availLines = 1
	}

	for i, c := range p.conductors {
		if i >= availLines {
			break
		}

		// Status indicator
		statusIcon := "●"
		statusColor := lipgloss.Color("#10B981") // green
		switch c.Status {
		case "completed":
			statusIcon = "✓"
			statusColor = lipgloss.Color("#6B7280") // gray
		case "failed":
			statusIcon = "✗"
			statusColor = lipgloss.Color("#EF4444") // red
		case "abandoned":
			statusIcon = "⊘"
			statusColor = lipgloss.Color("#F59E0B") // yellow
		}

		// Truncate ID and goal
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		goal := c.Goal
		maxGoal := p.width - 30
		if maxGoal < 10 {
			maxGoal = 10
		}
		if len(goal) > maxGoal {
			goal = goal[:maxGoal-3] + "..."
		}

		// Task progress
		counts := p.taskCounts[c.ID]
		progress := fmt.Sprintf("%d/%d", counts[0], counts[1])

		// Duration
		dur := time.Since(c.StartedAt).Round(time.Second)
		durStr := formatDuration(dur)

		// Cursor and selection indicators
		prefix := "  "
		if i == p.cursor && p.focused {
			prefix = "▸ "
		}
		if c.ID == p.selected {
			prefix = "▶ "
		}

		styledStatus := lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon)
		line := fmt.Sprintf("%s%s %s %s  %s  %s", prefix, styledStatus, id, goal, progress, durStr)
		b.WriteString(line)
		if i < len(p.conductors)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// formatDuration formats a duration into a compact string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
