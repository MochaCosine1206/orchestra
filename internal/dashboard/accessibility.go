package dashboard

import (
	"fmt"
	"time"
)

// AriaLabel returns a descriptive ARIA label for an agent avatar given its role and status.
func AriaLabel(role, status string) string {
	roleName := roleDisplayName(role)
	statusDesc := statusActionText(status)
	return fmt.Sprintf("%s agent, currently %s", roleName, statusDesc)
}

func roleDisplayName(role string) string {
	switch role {
	case "architect":
		return "Architect"
	case "implementer":
		return "Implementer"
	case "reviewer":
		return "Reviewer"
	case "researcher":
		return "Researcher"
	case "scout":
		return "Scout"
	case "editor":
		return "Editor"
	default:
		return "Agent"
	}
}

func statusActionText(status string) string {
	switch status {
	case "pending":
		return "waiting to start"
	case "running", "in_progress":
		return "working"
	case "done", "completed":
		return "finished"
	case "failed":
		return "in error state"
	case "idle":
		return "idle"
	case "merging":
		return "merging changes"
	default:
		return "in unknown state"
	}
}

// StatusDescription returns a human-readable text description for a status dot.
func StatusDescription(status string) string {
	switch status {
	case "pending":
		return "Pending — waiting to be assigned"
	case "running":
		return "Running — session in progress"
	case "in_progress":
		return "In progress — actively executing"
	case "done":
		return "Done — completed successfully"
	case "completed":
		return "Completed — all work finished"
	case "failed":
		return "Failed — terminated with errors"
	case "idle":
		return "Idle — no active work"
	case "merging":
		return "Merging — integrating changes"
	default:
		return "Unknown status"
	}
}

// TimeAgo returns a human-readable relative timestamp string.
func TimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// AlarmCoalescer deduplicates SSE events of the same type within a time window.
// Circuit-breaker and human-needed events always pass through immediately.
type AlarmCoalescer struct {
	Window    time.Duration
	seen      map[string]*coalescedAlarm
	passTypes map[string]bool
}

type coalescedAlarm struct {
	FirstSeen time.Time
	Count     int
	LastEvent CoalescedEvent
}

// NewAlarmCoalescer creates a coalescer with the given dedup window.
func NewAlarmCoalescer(window time.Duration) *AlarmCoalescer {
	return &AlarmCoalescer{
		Window: window,
		seen:   make(map[string]*coalescedAlarm),
		passTypes: map[string]bool{
			"circuit_breaker": true,
			"human_needed":    true,
		},
	}
}

// AlarmResult is the output of processing an event through the coalescer.
type AlarmResult struct {
	Emit  bool
	Event CoalescedEvent
}

// Process takes an event and returns whether it should be emitted.
// Events of passthrough types always emit immediately.
// Other events are deduplicated within the window; only the first is emitted,
// and a coalesced event with count is emitted when the window expires.
func (ac *AlarmCoalescer) Process(eventType, text, dotColor, timeAgo string) AlarmResult {
	if ac.passTypes[eventType] {
		return AlarmResult{
			Emit: true,
			Event: CoalescedEvent{
				EventType: eventType,
				Text:      text,
				DotColor:  dotColor,
				TimeAgo:   timeAgo,
				Count:     1,
			},
		}
	}

	now := time.Now()
	alarm, exists := ac.seen[eventType]

	if exists && now.Sub(alarm.FirstSeen) < ac.Window {
		alarm.Count++
		alarm.LastEvent = CoalescedEvent{
			EventType: eventType,
			Text:      text,
			DotColor:  dotColor,
			TimeAgo:   timeAgo,
			Count:     alarm.Count,
		}
		return AlarmResult{Emit: false}
	}

	ac.seen[eventType] = &coalescedAlarm{
		FirstSeen: now,
		Count:     1,
		LastEvent: CoalescedEvent{
			EventType: eventType,
			Text:      text,
			DotColor:  dotColor,
			TimeAgo:   timeAgo,
			Count:     1,
		},
	}
	return AlarmResult{
		Emit: true,
		Event: CoalescedEvent{
			EventType: eventType,
			Text:      text,
			DotColor:  dotColor,
			TimeAgo:   timeAgo,
			Count:     1,
		},
	}
}

// Flush returns any coalesced events that have accumulated counts > 1
// and resets their state.
func (ac *AlarmCoalescer) Flush(eventType string) *CoalescedEvent {
	alarm, exists := ac.seen[eventType]
	if !exists || alarm.Count <= 1 {
		return nil
	}
	result := alarm.LastEvent
	delete(ac.seen, eventType)
	return &result
}
