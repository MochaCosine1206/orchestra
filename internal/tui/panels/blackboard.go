package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Compile-time interface check.
var _ Panel = (*BlackboardPanel)(nil)

// Filter prefixes cycle: All → conductor: → retry: → result_summary: → All
var filterPrefixes = []string{"", "conductor:", "retry:", "result_summary:"}
var filterLabels = []string{"All", "conductor:*", "retry:*", "result_summary:*"}

// BlackboardPanel displays blackboard key/value entries.
type BlackboardPanel struct {
	entries  []db.BlackboardEntry
	filtered []db.BlackboardEntry
	cursor   int
	width    int
	height   int
	focused  bool
	filterID int // index into filterPrefixes
}

// NewBlackboardPanel creates a new blackboard panel.
func NewBlackboardPanel() *BlackboardPanel {
	return &BlackboardPanel{}
}

// SetSize updates the panel dimensions.
func (p *BlackboardPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused sets whether this panel has focus.
func (p *BlackboardPanel) SetFocused(focused bool) {
	p.focused = focused
}

// Update handles a Bubble Tea message.
func (p *BlackboardPanel) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// SetData refreshes the blackboard data.
func (p *BlackboardPanel) SetData(entries []db.BlackboardEntry) {
	p.entries = entries
	p.applyFilter()
}

// CycleFilter advances to the next filter prefix.
func (p *BlackboardPanel) CycleFilter() {
	p.filterID = (p.filterID + 1) % len(filterPrefixes)
	p.applyFilter()
}

func (p *BlackboardPanel) applyFilter() {
	prefix := filterPrefixes[p.filterID]
	if prefix == "" {
		p.filtered = p.entries
	} else {
		p.filtered = nil
		for _, e := range p.entries {
			if strings.HasPrefix(e.Key, prefix) {
				p.filtered = append(p.filtered, e)
			}
		}
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = max(0, len(p.filtered)-1)
	}
}

// MoveUp moves the cursor up.
func (p *BlackboardPanel) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// MoveDown moves the cursor down.
func (p *BlackboardPanel) MoveDown() {
	if p.cursor < len(p.filtered)-1 {
		p.cursor++
	}
}

// GoTop moves cursor to first item.
func (p *BlackboardPanel) GoTop() {
	p.cursor = 0
}

// GoBottom moves cursor to last item.
func (p *BlackboardPanel) GoBottom() {
	if len(p.filtered) > 0 {
		p.cursor = len(p.filtered) - 1
	}
}

// View renders the blackboard panel.
func (p *BlackboardPanel) View() string {
	if p.width < 10 || p.height < 3 {
		return ""
	}

	var b strings.Builder

	// Header with filter indicator
	header := fmt.Sprintf(" BLACKBOARD (%d) [%s]", len(p.filtered), filterLabels[p.filterID])
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if len(p.filtered) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		b.WriteString(dimStyle.Render(" (no entries)"))
		b.WriteString("\n")
	} else {
		// Each entry takes 1 line normally, 2 lines when selected (detail line).
		// Calculate available rows for scrolling.
		availRows := p.height - 2 // header + padding
		if availRows < 1 {
			availRows = 1
		}

		// Account for the extra detail line of selected entry
		displayRows := availRows
		if p.focused {
			displayRows = max(1, availRows-1) // reserve 1 row for detail line
		}

		start := 0
		if p.cursor >= displayRows {
			start = p.cursor - displayRows + 1
		}
		end := start + displayRows
		if end > len(p.filtered) {
			end = len(p.filtered)
		}

		for i := start; i < end; i++ {
			e := p.filtered[i]

			// Truncate value to fit: key (bold) = value
			maxValLen := p.width - len(e.Key) - 5 // " key = value"
			if maxValLen < 10 {
				maxValLen = 10
			}
			valStr := truncateStr(strings.ReplaceAll(e.Value, "\n", " "), maxValLen)

			if i == p.cursor && p.focused {
				// Selected row: highlight (plain text first, style uniformly to avoid ANSI truncation)
				plain := fmt.Sprintf(" %s = %s", e.Key, valStr)
				line := lipgloss.NewStyle().Background(colorSelected).Foreground(lipgloss.Color("#FFFFFF")).Render(
					padRight(truncateStr(plain, p.width-2), p.width-2))
				b.WriteString(line)
				b.WriteString("\n")

				// Detail line: written_by and updated_at
				writer := "-"
				if e.WrittenBy.Valid {
					writer = e.WrittenBy.String
				}
				detail := fmt.Sprintf("   by %s at %s", writer, e.UpdatedAt.Format("15:04:05"))
				dimStyle := lipgloss.NewStyle().Foreground(colorDim)
				b.WriteString(dimStyle.Render(truncateStr(detail, p.width-2)))
				b.WriteString("\n")
			} else {
				// Non-selected: key = value on one line
				keyStyle := lipgloss.NewStyle().Bold(true)
				dimStyle := lipgloss.NewStyle().Foreground(colorDim)
				line := fmt.Sprintf(" %s = %s", keyStyle.Render(e.Key), dimStyle.Render(valStr))
				b.WriteString(truncateStr(line, p.width))
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}
