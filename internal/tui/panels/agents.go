package panels

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Compile-time interface check.
var _ Panel = (*AgentPanel)(nil)

// Spinner frames for working agents.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// AgentPanel displays the agent list.
type AgentPanel struct {
	agents  []db.Agent
	cursor  int
	width   int
	height  int
	focused bool
	tick    int    // incremented on each SetData, drives spinner frame
	logsDir string // directory containing agent JSONL logs
}

// NewAgentPanel creates a new agent panel.
func NewAgentPanel() *AgentPanel {
	return &AgentPanel{}
}

// SetLogsDir sets the directory containing agent JSONL logs for context estimation.
func (p *AgentPanel) SetLogsDir(dir string) {
	p.logsDir = dir
}

// SetSize updates the panel dimensions.
func (p *AgentPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused sets whether this panel has focus.
func (p *AgentPanel) SetFocused(focused bool) {
	p.focused = focused
}

// Update handles a Bubble Tea message. AgentPanel uses this for broadcast
// participation but handles most state through SetData.
func (p *AgentPanel) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// SetData refreshes the agent data.
func (p *AgentPanel) SetData(agents []db.Agent) {
	p.agents = agents
	p.tick++
	if p.cursor >= len(agents) {
		p.cursor = max(0, len(agents)-1)
	}
}

// MoveUp moves the cursor up.
func (p *AgentPanel) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// MoveDown moves the cursor down.
func (p *AgentPanel) MoveDown() {
	if p.cursor < len(p.agents)-1 {
		p.cursor++
	}
}

// GoTop moves cursor to first item.
func (p *AgentPanel) GoTop() {
	p.cursor = 0
}

// GoBottom moves cursor to last item.
func (p *AgentPanel) GoBottom() {
	if len(p.agents) > 0 {
		p.cursor = len(p.agents) - 1
	}
}

// SelectedAgent returns the currently selected agent, if any.
func (p *AgentPanel) SelectedAgent() *db.Agent {
	if len(p.agents) == 0 || p.cursor >= len(p.agents) {
		return nil
	}
	return &p.agents[p.cursor]
}

// View renders the agent panel.
func (p *AgentPanel) View() string {
	if p.width < 10 || p.height < 3 {
		return ""
	}

	var b strings.Builder

	// Header
	header := fmt.Sprintf(" AGENTS (%d)", len(p.agents))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if len(p.agents) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		b.WriteString(dimStyle.Render(" (no agents)"))
		b.WriteString("\n")
	} else {
		// Column header
		colHeader := fmt.Sprintf(" %-8s %-10s %-8s %-10s %-5s %s",
			"ID", "ROLE", "STATUS", "TASK", "CTX", "ACTIVITY")
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		b.WriteString(dimStyle.Render(truncateStr(colHeader, p.width-2)))
		b.WriteString("\n")

		// Available rows
		availRows := p.height - 3
		if availRows < 1 {
			availRows = 1
		}

		start := 0
		if p.cursor >= availRows {
			start = p.cursor - availRows + 1
		}
		end := start + availRows
		if end > len(p.agents) {
			end = len(p.agents)
		}

		for i := start; i < end; i++ {
			a := p.agents[i]

			task := "-"
			if a.CurrentTask.Valid {
				task = truncateStr(a.CurrentTask.String, 10)
			}

			ctxStr := p.contextUsageStr(a)

			if i == p.cursor && p.focused {
				// Selected row: build plain text line, then style uniformly
				// (avoids ANSI codes inflating truncateStr length calculation)
				plain := fmt.Sprintf(" %-8s %-10s %-8s %-10s %-5s %s",
					truncateStr(a.ID, 8), a.Role, a.Status, task, ctxStr, p.activityPlain(a))
				line := lipgloss.NewStyle().Background(colorSelected).Foreground(lipgloss.Color("#FFFFFF")).Render(
					padRight(truncateStr(plain, p.width-2), p.width-2))
				b.WriteString(line)
			} else {
				// Non-selected row: style individual columns
				statusColor := colorDim
				if a.Status == "working" || a.Status == "active" {
					statusColor = colorRunning
				}
				statusStr := lipgloss.NewStyle().Foreground(statusColor).Render(fmt.Sprintf("%-8s", a.Status))
				ctxStyled := p.contextUsageStyled(a)
				activity := p.activityStr(a)

				line := fmt.Sprintf(" %-8s %-10s %s %-10s %s %s",
					truncateStr(a.ID, 8), a.Role, statusStr, task, ctxStyled, activity)
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// activityPlain returns the plain-text activity string (no ANSI styling).
func (p *AgentPanel) activityPlain(a db.Agent) string {
	switch a.Status {
	case "working", "active":
		frame := spinnerFrames[p.tick%len(spinnerFrames)]
		elapsed := fmtDur(time.Since(a.CreatedAt))
		return frame + " working " + elapsed
	case "dead":
		return "exited"
	default:
		return "idle"
	}
}

// contextUsageStr returns a plain-text context usage percentage string.
func (p *AgentPanel) contextUsageStr(a db.Agent) string {
	if (a.Status != "working" && a.Status != "active") || !a.CurrentTask.Valid || p.logsDir == "" {
		return "-"
	}
	logFile := filepath.Join(p.logsDir, a.CurrentTask.String+".jsonl")
	_, pct := agent.EstimateContextUsage(logFile)
	if pct <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", int(pct))
}

// contextUsageStyled returns a color-coded context usage string.
func (p *AgentPanel) contextUsageStyled(a db.Agent) string {
	raw := p.contextUsageStr(a)
	if raw == "-" {
		return lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("%-5s", raw))
	}

	// Parse percentage for color coding
	if (a.Status != "working" && a.Status != "active") || !a.CurrentTask.Valid || p.logsDir == "" {
		return lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("%-5s", raw))
	}
	logFile := filepath.Join(p.logsDir, a.CurrentTask.String+".jsonl")
	_, pct := agent.EstimateContextUsage(logFile)

	var color lipgloss.Color
	switch {
	case pct > 80:
		color = colorFailed // red
	case pct > 50:
		color = colorPending // yellow
	default:
		color = colorRunning // green
	}
	return lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%-5s", raw))
}

// activityStr returns a styled activity string for the given agent.
func (p *AgentPanel) activityStr(a db.Agent) string {
	activeStyle := lipgloss.NewStyle().Foreground(colorRunning)
	deadStyle := lipgloss.NewStyle().Foreground(colorFailed)
	idleStyle := lipgloss.NewStyle().Foreground(colorDim)

	switch a.Status {
	case "working", "active":
		frame := spinnerFrames[p.tick%len(spinnerFrames)]
		elapsed := fmtDur(time.Since(a.CreatedAt))
		return activeStyle.Render(frame+" working ") + lipgloss.NewStyle().Foreground(colorDim).Render(elapsed)
	case "dead":
		return deadStyle.Render("exited")
	default:
		return idleStyle.Render("idle")
	}
}
