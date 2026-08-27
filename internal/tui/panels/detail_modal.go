package panels

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// DetailModal renders a full-screen overlay with task details.
type DetailModal struct {
	task   *db.Task
	width  int
	height int
}

// NewDetailModal creates a new detail modal.
func NewDetailModal() *DetailModal {
	return &DetailModal{}
}

// SetTask sets the task to display details for.
func (d *DetailModal) SetTask(t *db.Task) {
	d.task = t
}

// SetSize sets the modal dimensions.
func (d *DetailModal) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// View renders the detail modal overlay.
func (d *DetailModal) View() string {
	if d.task == nil {
		return ""
	}

	t := d.task

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	var b strings.Builder

	b.WriteString(labelStyle.Render("  TASK DETAIL"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  ────────────────────────────────"))
	b.WriteString("\n\n")

	addField := func(label, value string) {
		b.WriteString(labelStyle.Render(fmt.Sprintf("  %-14s", label)))
		b.WriteString(valStyle.Render(value))
		b.WriteString("\n")
	}

	addField("ID:", t.ID)
	addField("Title:", t.Title)
	addField("Status:", t.Status)
	addField("Role:", t.Role)
	if t.PriorityLabel.Valid && t.PriorityLabel.String != "" {
		addField("Priority:", fmt.Sprintf("%d / %s", t.Priority, t.PriorityLabel.String))
	} else {
		addField("Priority:", fmt.Sprintf("%d", t.Priority))
	}

	if t.Description.Valid && t.Description.String != "" {
		addField("Description:", truncateStr(t.Description.String, d.width-20))
	}

	if t.AssignedTo.Valid {
		addField("Assigned To:", t.AssignedTo.String)
	}

	if t.DependsOn.Valid {
		addField("Depends On:", t.DependsOn.String)
	}

	if t.BlockedBy.Valid {
		addField("Blocked By:", t.BlockedBy.String)
	}

	if t.Worktree.Valid {
		addField("Worktree:", t.Worktree.String)
	}

	if t.Branch.Valid {
		addField("Branch:", t.Branch.String)
	}

	if t.Result.Valid {
		addField("Result:", truncateStr(t.Result.String, d.width-20))
	}

	addField("Created:", t.CreatedAt.Format("2006-01-02 15:04:05"))

	if t.StartedAt.Valid {
		addField("Started:", t.StartedAt.Time.Format("2006-01-02 15:04:05"))
	}

	if t.CompletedAt.Valid {
		addField("Completed:", t.CompletedAt.Time.Format("2006-01-02 15:04:05"))
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Press Escape to close"))

	content := b.String()

	modalStyle := lipgloss.NewStyle().
		Width(d.width).
		Height(d.height).
		Background(lipgloss.Color("#111827")).
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(1, 0)

	return modalStyle.Render(content)
}
