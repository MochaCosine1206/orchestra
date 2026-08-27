package panels

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	toastMaxVisible = 3
	toastDuration   = 5 * time.Second
)

// ToastType determines the toast color.
type ToastType int

const (
	ToastInfo    ToastType = iota // blue
	ToastSuccess                  // green
	ToastError                    // red
)

// Toast is a single notification popup.
type Toast struct {
	Message   string
	Type      ToastType
	CreatedAt time.Time
}

// NewToastMsg is sent to create a new toast notification.
type NewToastMsg struct {
	Message string
	Type    ToastType
}

// ToastManager manages a queue of toast notifications.
type ToastManager struct {
	toasts []Toast
}

// NewToastManager creates a new toast manager.
func NewToastManager() *ToastManager {
	return &ToastManager{}
}

// Add creates a new toast. Respects max visible limit.
func (m *ToastManager) Add(msg string, tt ToastType) {
	m.toasts = append(m.toasts, Toast{
		Message:   msg,
		Type:      tt,
		CreatedAt: time.Now(),
	})
	// Trim to max visible
	if len(m.toasts) > toastMaxVisible {
		m.toasts = m.toasts[len(m.toasts)-toastMaxVisible:]
	}
}

// Tick removes expired toasts. Should be called on each TUI tick.
func (m *ToastManager) Tick() {
	now := time.Now()
	live := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Sub(t.CreatedAt) < toastDuration {
			live = append(live, t)
		}
	}
	m.toasts = live
}

// Count returns the number of visible toasts.
func (m *ToastManager) Count() int {
	return len(m.toasts)
}

// View renders toasts as an overlay string. Each toast is one line.
func (m *ToastManager) View(maxWidth int) string {
	if len(m.toasts) == 0 {
		return ""
	}

	var lines string
	for _, t := range m.toasts {
		style := m.styleForType(t.Type)
		msg := fmt.Sprintf(" %s ", truncateStr(t.Message, maxWidth-4))
		lines += style.Render(msg) + "\n"
	}
	return lines
}

func (m *ToastManager) styleForType(tt ToastType) lipgloss.Style {
	switch tt {
	case ToastSuccess:
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#22C55E")).
			Bold(true)
	case ToastError:
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#EF4444")).
			Bold(true)
	default: // ToastInfo
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#3B82F6")).
			Bold(true)
	}
}

// ToastTypeForEvent maps event type strings to toast types.
func ToastTypeForEvent(eventType string) ToastType {
	switch eventType {
	case "agent_spawned", "task_assigned", "task_started":
		return ToastInfo
	case "task_completed", "merge_complete":
		return ToastSuccess
	case "task_failed", "agent_crashed", "circuit_breaker":
		return ToastError
	default:
		return ToastInfo
	}
}
