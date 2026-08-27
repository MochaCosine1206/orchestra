package panels

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// StatusBar renders the bottom status and help bar.
type StatusBar struct {
	summary      *db.StatusSummary
	width        int
	dbErr        error
	focusedPanel int
	actionMsg    string
}

// NewStatusBar creates a new status bar.
func NewStatusBar() *StatusBar {
	return &StatusBar{}
}

// SetWidth sets the bar width.
func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

// SetFocusedPanel updates the focused panel index for context-sensitive help.
func (s *StatusBar) SetFocusedPanel(panel int) {
	s.focusedPanel = panel
}

// SetActionMsg sets a temporary action feedback message (e.g. "Retrying task t1...").
func (s *StatusBar) SetActionMsg(msg string) {
	s.actionMsg = msg
}

// ClearActionMsg clears the action feedback message.
func (s *StatusBar) ClearActionMsg() {
	s.actionMsg = ""
}

// Update refreshes the status data.
func (s *StatusBar) Update(summary *db.StatusSummary, dbErr error) {
	s.summary = summary
	s.dbErr = dbErr
}

// View renders the status bar (2 lines: status + help).
func (s *StatusBar) View() string {
	if s.width < 10 {
		return ""
	}

	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#1F2937")).
		Width(s.width)

	helpStyle := lipgloss.NewStyle().
		Foreground(colorDim).
		Width(s.width)

	// Error banner
	if s.dbErr != nil {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#EF4444")).
			Width(s.width).
			Bold(true)
		return errStyle.Render(fmt.Sprintf(" DB Error: %v", s.dbErr))
	}

	// Status line
	var parts []string
	if s.summary != nil {
		for _, status := range []string{"running", "pending", "assigned", "done", "failed"} {
			if c, ok := s.summary.Tasks.ByStatus[status]; ok && c > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", c, status))
			}
		}
	}

	statusText := "(no tasks)"
	if len(parts) > 0 {
		statusText = strings.Join(parts, " | ")
	}

	agentCount := 0
	lockCount := 0
	if s.summary != nil {
		agentCount = s.summary.Agents.Total
		lockCount = s.summary.LockCount
	}

	statusLine := fmt.Sprintf(" %s    %d agents    %d locks",
		statusText, agentCount, lockCount)

	// Action feedback overrides help line if set
	if s.actionMsg != "" {
		actionStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E")).
			Width(s.width)
		return barStyle.Render(statusLine) + "\n" + actionStyle.Render(" "+s.actionMsg)
	}

	helpLine := s.helpHints()
	return barStyle.Render(statusLine) + "\n" + helpStyle.Render(helpLine)
}

// helpHints returns context-sensitive help text based on focused panel.
func (s *StatusBar) helpHints() string {
	base := " [tab] panel  [?] help  [q] quit"
	switch s.focusedPanel {
	case 0: // conductors
		return " [j/k] nav  [enter] select/filter" + base
	case 1: // tasks
		return " [j/k] nav  [enter] view logs  [/] filter" + base
	case 2: // agents
		return " [j/k] nav  [enter] view logs" + base
	case 3: // blackboard
		return " [j/k] nav  [f] cycle filter" + base
	case 4: // logs
		return " [j/k] scroll  [space] pause/resume  [c] conductor" + base
	default:
		return " [j/k] nav" + base
	}
}
