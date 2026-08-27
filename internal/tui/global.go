package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/core"
	"github.com/MochaCosine1206/orchestra/internal/version"
)

// globalTickMsg triggers a global data refresh.
type globalTickMsg struct{}

// globalDataMsg carries refreshed project summaries.
type globalDataMsg struct {
	status *core.GlobalStatus
	err    error
}

// GlobalModel is the root Bubble Tea model for the multi-project global dashboard.
type GlobalModel struct {
	projects []*config.ProjectEntry
	status   *core.GlobalStatus

	selected int
	width    int
	height   int
	ready    bool
	err      error
}

// NewGlobalModel creates a GlobalModel from a list of registered projects.
func NewGlobalModel(projects []*config.ProjectEntry) GlobalModel {
	return GlobalModel{
		projects: projects,
	}
}

// Init starts the initial data fetch and polling timer.
func (m GlobalModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchGlobalData(),
		tea.Tick(pollInterval, func(time.Time) tea.Msg { return globalTickMsg{} }),
	)
}

// Update handles messages for the global dashboard.
func (m GlobalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Up):
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case key.Matches(msg, keys.Down):
			max := m.projectCount() - 1
			if max < 0 {
				max = 0
			}
			if m.selected < max {
				m.selected++
			}
			return m, nil
		case key.Matches(msg, keys.Help):
			// Could add help overlay; for now just ignore
			return m, nil
		}

		// Number keys 1-9 for quick selection
		if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
			idx := int(msg.String()[0] - '1')
			if idx < m.projectCount() {
				m.selected = idx
			}
			return m, nil
		}

	case globalTickMsg:
		return m, tea.Batch(
			m.fetchGlobalData(),
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return globalTickMsg{} }),
		)

	case globalDataMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.status = msg.status
			m.err = nil
		}
		return m, nil
	}

	return m, nil
}

// View renders the global dashboard.
func (m GlobalModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	// Title bar
	title := fmt.Sprintf("  CLAUDE ORCHESTRA — GLOBAL DASHBOARD %s", version.Short())
	titleRendered := titleStyle.Width(m.width).Render(title)
	b.WriteString(titleRendered)
	b.WriteString("\n")

	// Aggregate summary top bar
	b.WriteString(m.summaryBar())
	b.WriteString("\n")

	// Content area: sidebar (30%) + main detail (70%)
	sidebarWidth := m.width * 30 / 100
	if sidebarWidth < 25 {
		sidebarWidth = 25
	}
	mainWidth := m.width - sidebarWidth
	if mainWidth < 30 {
		mainWidth = 30
	}

	contentHeight := m.height - 5 // title(1) + summary(1) + status(2) + newline
	if contentHeight < 4 {
		contentHeight = 4
	}

	// Left sidebar: project list
	sidebarStyle := focusedPanelStyle.Width(sidebarWidth - 2).Height(contentHeight - 2).MaxHeight(contentHeight)
	sidebar := sidebarStyle.Render(m.projectListView(sidebarWidth-4, contentHeight-4))

	// Right main: selected project detail
	mainStyle := panelStyle.Width(mainWidth - 2).Height(contentHeight - 2).MaxHeight(contentHeight)
	main := mainStyle.Render(m.projectDetailView(mainWidth-4, contentHeight-4))

	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, main)
	b.WriteString(mainContent)
	b.WriteString("\n")

	// Status bar
	b.WriteString(m.statusBarView())

	return b.String()
}

// summaryBar renders aggregate stats across all projects.
func (m GlobalModel) summaryBar() string {
	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#1F2937")).
		Width(m.width)

	if m.status == nil {
		return barStyle.Render(" Loading project data...")
	}

	var totalAgents, totalDone, totalPending, activeCount int
	for _, p := range m.status.Projects {
		totalAgents += p.AgentCount
		totalDone += p.TasksDone
		totalPending += p.TasksPending
		if p.ActiveSession != "" {
			activeCount++
		}
	}

	text := fmt.Sprintf(" %d projects  |  %d active  |  %d agents  |  %d tasks done  |  %d pending",
		len(m.status.Projects), activeCount, totalAgents, totalDone, totalPending)
	return barStyle.Render(text)
}

// projectListView renders the sidebar project list with status indicators.
func (m GlobalModel) projectListView(w, h int) string {
	header := panelHeaderStyle.Render("PROJECTS")
	var lines []string
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("─", w))

	if m.status == nil || len(m.status.Projects) == 0 {
		lines = append(lines, "  (no projects)")
		return strings.Join(lines, "\n")
	}

	for i, p := range m.status.Projects {
		// Status indicator
		indicator := "●"
		var indicatorStyle lipgloss.Style
		if p.ActiveSession != "" {
			indicatorStyle = lipgloss.NewStyle().Foreground(colorRunning) // green = active
		} else if p.AgentCount > 0 || p.TasksPending > 0 {
			indicatorStyle = lipgloss.NewStyle().Foreground(colorPending) // yellow = has work
		} else if p.TasksDone == 0 && p.TasksPending == 0 && p.AgentCount == 0 {
			indicatorStyle = lipgloss.NewStyle().Foreground(colorDim) // gray = idle
		} else {
			indicatorStyle = lipgloss.NewStyle().Foreground(colorDim)
		}

		name := p.Name
		if len(name) > w-6 {
			name = name[:w-9] + "..."
		}

		prefix := "  "
		if i == m.selected {
			prefix = "▶ "
		}

		line := fmt.Sprintf("%s%s %s", prefix, indicatorStyle.Render(indicator), name)
		if i == m.selected {
			line = selectedStyle.Width(w).Render(line)
		}

		lines = append(lines, line)

		// Stop if we'd exceed available height
		if len(lines) >= h {
			break
		}
	}

	return strings.Join(lines, "\n")
}

// projectDetailView renders detail for the selected project.
func (m GlobalModel) projectDetailView(w, h int) string {
	header := panelHeaderStyle.Render("PROJECT DETAIL")

	if m.status == nil || len(m.status.Projects) == 0 {
		return header + "\n\n  Select a project from the sidebar."
	}

	if m.selected >= len(m.status.Projects) {
		return header + "\n\n  (invalid selection)"
	}

	p := m.status.Projects[m.selected]

	var lines []string
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("─", w))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  Name:      %s", p.Name))
	lines = append(lines, fmt.Sprintf("  Path:      %s", p.Path))

	sessionText := "(idle)"
	if p.ActiveSession != "" {
		sessionText = p.ActiveSession
	}
	lines = append(lines, fmt.Sprintf("  Session:   %s", sessionText))
	lines = append(lines, "")

	// Stats
	agentLabel := fmt.Sprintf("%d", p.AgentCount)
	if p.AgentCount > 0 {
		agentLabel = lipgloss.NewStyle().Foreground(colorRunning).Bold(true).Render(agentLabel)
	}
	lines = append(lines, fmt.Sprintf("  Agents:    %s running", agentLabel))
	lines = append(lines, fmt.Sprintf("  Done:      %d tasks", p.TasksDone))
	lines = append(lines, fmt.Sprintf("  Pending:   %d tasks", p.TasksPending))

	// Last activity
	activity := "(no activity)"
	if !p.LastActivity.IsZero() {
		age := time.Since(p.LastActivity)
		if age < time.Minute {
			activity = "just now"
		} else if age < time.Hour {
			activity = fmt.Sprintf("%dm ago", int(age.Minutes()))
		} else if age < 24*time.Hour {
			activity = fmt.Sprintf("%dh ago", int(age.Hours()))
		} else {
			activity = p.LastActivity.Format("2006-01-02 15:04")
		}
	}
	lines = append(lines, fmt.Sprintf("  Activity:  %s", activity))

	return strings.Join(lines, "\n")
}

// statusBarView renders the bottom status/help bar.
func (m GlobalModel) statusBarView() string {
	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#1F2937")).
		Width(m.width)

	helpStyle := lipgloss.NewStyle().
		Foreground(colorDim).
		Width(m.width)

	if m.err != nil {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#EF4444")).
			Width(m.width).
			Bold(true)
		return errStyle.Render(fmt.Sprintf(" Error: %v", m.err))
	}

	statusLine := fmt.Sprintf(" Selected: %s", m.selectedName())
	helpLine := " [j/k] navigate  [1-9] quick select  [q] quit"
	return barStyle.Render(statusLine) + "\n" + helpStyle.Render(helpLine)
}

// fetchGlobalData queries all project databases.
func (m GlobalModel) fetchGlobalData() tea.Cmd {
	projects := m.projects
	return func() tea.Msg {
		var paths []string
		for _, p := range projects {
			paths = append(paths, p.Path)
		}
		ctx := context.Background()
		status, err := core.GlobalStatusSummary(ctx, paths)
		if err != nil {
			return globalDataMsg{err: err}
		}
		return globalDataMsg{status: status}
	}
}

// projectCount returns the number of projects with status data.
func (m GlobalModel) projectCount() int {
	if m.status != nil {
		return len(m.status.Projects)
	}
	return len(m.projects)
}

// selectedName returns the name of the currently selected project.
func (m GlobalModel) selectedName() string {
	if m.status != nil && m.selected < len(m.status.Projects) {
		return m.status.Projects[m.selected].Name
	}
	if m.selected < len(m.projects) {
		return m.projects[m.selected].Name
	}
	return "(none)"
}
