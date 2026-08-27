package panels

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Compile-time interface check.
var _ Panel = (*TaskPanel)(nil)

// Colors (duplicated from styles.go to avoid import cycle).
var (
	colorRunning  = lipgloss.Color("#22C55E")
	colorPending  = lipgloss.Color("#EAB308")
	colorDone     = lipgloss.Color("#6B7280")
	colorFailed   = lipgloss.Color("#EF4444")
	colorAssigned = lipgloss.Color("#3B82F6")
	colorDim      = lipgloss.Color("#6B7280")
	colorSelected = lipgloss.Color("#374151")
)

var statusColors = map[string]lipgloss.Color{
	"running":  colorRunning,
	"pending":  colorPending,
	"assigned": colorAssigned,
	"done":     colorDone,
	"failed":   colorFailed,
}

// FilterActivateMsg is sent when the filter is activated via '/'.
type FilterActivateMsg struct{}

// FilterClearMsg is sent when the filter is cleared via Escape.
type FilterClearMsg struct{}

// TaskPanel displays the task list.
type TaskPanel struct {
	tasks         []db.Task
	filteredTasks []db.Task
	cursor        int
	width         int
	height        int
	focused       bool

	// Filter state
	filterActive  bool
	filterInput   textinput.Model
	filterPattern string
	filterRegex   *regexp.Regexp
}

// NewTaskPanel creates a new task panel.
func NewTaskPanel() *TaskPanel {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308"))
	ti.CharLimit = 100
	return &TaskPanel{filterInput: ti}
}

// SetSize updates the panel dimensions.
func (p *TaskPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused sets whether this panel has focus.
func (p *TaskPanel) SetFocused(focused bool) {
	p.focused = focused
}

// Update handles a Bubble Tea message.
func (p *TaskPanel) Update(msg tea.Msg) tea.Cmd {
	if !p.filterActive {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			p.filterActive = false
			p.filterInput.Blur()
			p.applyFilter()
			return nil
		case tea.KeyEscape:
			p.filterActive = false
			p.filterInput.Blur()
			p.filterInput.SetValue("")
			p.filterPattern = ""
			p.filterRegex = nil
			p.filteredTasks = nil
			p.cursor = 0
			return func() tea.Msg { return FilterClearMsg{} }
		}
	}

	var cmd tea.Cmd
	p.filterInput, cmd = p.filterInput.Update(msg)
	// Live-apply filter as user types
	p.applyFilter()
	return cmd
}

// ActivateFilter starts the filter input.
func (p *TaskPanel) ActivateFilter() {
	p.filterActive = true
	p.filterInput.SetValue(p.filterPattern)
	p.filterInput.Focus()
}

// FilterActive returns whether the filter input is active.
func (p *TaskPanel) FilterActive() bool {
	return p.filterActive
}

// SetData refreshes the task data and re-applies the filter.
func (p *TaskPanel) SetData(tasks []db.Task) {
	p.tasks = tasks
	p.applyFilter()
	visibleTasks := p.visibleTasks()
	if p.cursor >= len(visibleTasks) {
		p.cursor = max(0, len(visibleTasks)-1)
	}
}

func (p *TaskPanel) applyFilter() {
	pattern := p.filterInput.Value()
	p.filterPattern = pattern

	if pattern == "" {
		p.filterRegex = nil
		p.filteredTasks = nil
		return
	}

	// Try to compile regex; on failure, use literal match
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		re = nil
	}
	p.filterRegex = re

	p.filteredTasks = make([]db.Task, 0)
	for _, t := range p.tasks {
		if p.matchesFilter(t) {
			p.filteredTasks = append(p.filteredTasks, t)
		}
	}

	if p.cursor >= len(p.filteredTasks) {
		p.cursor = max(0, len(p.filteredTasks)-1)
	}
}

func (p *TaskPanel) matchesFilter(t db.Task) bool {
	if p.filterRegex != nil {
		searchStr := t.Title + " " + t.Role + " " + t.Status + " " + t.ID + " " + t.PriorityLabel.String
		return p.filterRegex.MatchString(searchStr)
	}
	// Fallback: literal substring match
	pattern := strings.ToLower(p.filterPattern)
	searchStr := strings.ToLower(t.Title + " " + t.Role + " " + t.Status + " " + t.ID + " " + t.PriorityLabel.String)
	return strings.Contains(searchStr, pattern)
}

func (p *TaskPanel) visibleTasks() []db.Task {
	if p.filteredTasks != nil {
		return p.filteredTasks
	}
	return p.tasks
}

// MoveUp moves the cursor up.
func (p *TaskPanel) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// MoveDown moves the cursor down.
func (p *TaskPanel) MoveDown() {
	tasks := p.visibleTasks()
	if p.cursor < len(tasks)-1 {
		p.cursor++
	}
}

// GoTop moves cursor to first item.
func (p *TaskPanel) GoTop() {
	p.cursor = 0
}

// GoBottom moves cursor to last item.
func (p *TaskPanel) GoBottom() {
	tasks := p.visibleTasks()
	if len(tasks) > 0 {
		p.cursor = len(tasks) - 1
	}
}

// SelectedTask returns the currently selected task, if any.
func (p *TaskPanel) SelectedTask() *db.Task {
	tasks := p.visibleTasks()
	if len(tasks) == 0 || p.cursor >= len(tasks) {
		return nil
	}
	return &tasks[p.cursor]
}

// View renders the task panel.
func (p *TaskPanel) View() string {
	if p.width < 10 || p.height < 3 {
		return ""
	}

	tasks := p.visibleTasks()
	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
	if p.filterPattern != "" {
		header := fmt.Sprintf(" TASKS (%d/%d) [/%s]", len(tasks), len(p.tasks), p.filterPattern)
		b.WriteString(headerStyle.Render(truncateStr(header, p.width-2)))
	} else {
		header := fmt.Sprintf(" TASKS (%d)", len(tasks))
		b.WriteString(headerStyle.Render(header))
	}

	// Filter input line
	if p.filterActive {
		b.WriteString("\n")
		b.WriteString(p.filterInput.View())
		b.WriteString("\n")
	} else {
		b.WriteString("\n")
	}

	if len(tasks) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		if p.filterPattern != "" {
			b.WriteString(dimStyle.Render(" (no matching tasks)"))
		} else {
			b.WriteString(dimStyle.Render(" (no tasks)"))
		}
		b.WriteString("\n")
	} else {
		// Column header
		colHeader := fmt.Sprintf(" %-8s %-9s %-10s %-14s %-10s %s",
			"ID", "STATUS", "ROLE", "PRIORITY", "AGENT", "TITLE")
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		b.WriteString(dimStyle.Render(truncateStr(colHeader, p.width-2)))
		b.WriteString("\n")

		// Available rows (height minus header, col header, optional filter)
		headerRows := 3
		if p.filterActive {
			headerRows = 4
		}
		availRows := p.height - headerRows
		if availRows < 1 {
			availRows = 1
		}

		// Scrolling window
		start := 0
		if p.cursor >= availRows {
			start = p.cursor - availRows + 1
		}
		end := start + availRows
		if end > len(tasks) {
			end = len(tasks)
		}

		for i := start; i < end; i++ {
			t := tasks[i]

			// Status with color
			sc := colorDim
			if c, ok := statusColors[t.Status]; ok {
				sc = c
			}
			statusStr := lipgloss.NewStyle().Foreground(sc).Width(9).Render(t.Status)

			priDisplay := fmt.Sprintf("%d", t.Priority)
			if t.PriorityLabel.Valid && t.PriorityLabel.String != "" {
				priDisplay = fmt.Sprintf("%d / %s", t.Priority, t.PriorityLabel.String)
			}
			priDisplay = truncateStr(priDisplay, 14)

			agent := "-"
			if t.AssignedTo.Valid {
				agent = truncateStr(t.AssignedTo.String, 10)
			}

			elapsed := ""
			if t.StartedAt.Valid && !t.CompletedAt.Valid {
				dur := time.Since(t.StartedAt.Time)
				elapsed = fmtDur(dur)
			}

			title := truncateStr(t.Title, p.width-63)
			if elapsed != "" {
				title = title + " " + elapsed
			}

			prefix := " "
			if i == p.cursor && p.focused {
				prefix = ">"
			}
			line := fmt.Sprintf("%s%-8s %s %-10s %-14s %-10s %s",
				prefix, truncateStr(t.ID, 8), statusStr, t.Role, priDisplay, agent, title)

			if i == p.cursor && p.focused {
				line = lipgloss.NewStyle().Background(colorSelected).Foreground(lipgloss.Color("#FFFFFF")).Render(
					padRight(truncateStr(line, p.width-2), p.width-2))
			} else {
				line = truncateStr(line, p.width-2)
			}

			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String()
}

func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
