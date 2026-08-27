package tui

import (
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
)

// tickMsg triggers a data refresh from the database.
type tickMsg struct{}

// dataMsg carries fresh data from the database.
type dataMsg struct {
	tasks      []db.Task
	agents     []db.Agent
	locks      []db.FileLock
	events     []db.Event
	blackboard []db.BlackboardEntry
	summary    *db.StatusSummary
	conductors []db.ConductorRecord
	err        error
}

// focusPanelMsg requests focus change to a specific panel.
type focusPanelMsg int

// logLinesMsg carries parsed log entries from the streamer.
type logLinesMsg struct {
	entries []logstream.LogEntry
}

// selectTaskMsg requests switching the log viewer to a task's log file.
type selectTaskMsg struct {
	taskID string
	title  string
}

// actionResultMsg carries the result of an async delegate action.
type actionResultMsg struct {
	action string // "retry", "kill", "goal"
	taskID string
	output string
	err    error
}

// showDetailMsg requests showing the detail modal for a task.
type showDetailMsg struct {
	taskID string
}

// hideDetailMsg requests hiding the detail modal.
type hideDetailMsg struct{}

// goalDispatchedMsg is sent after a goal dispatch completes.
type goalDispatchedMsg struct {
	goal   string
	output string
	err    error
}

// goalProgressMsg reports phased progress from the conductor.
type goalProgressMsg struct {
	phase  string
	detail string
}

// activeSessionMsg reports an in-flight conductor session found on startup.
type activeSessionMsg struct {
	goal      string
	sessionID string
	taskIDs   []string
}

// sessionCheckTickMsg fires every 5 seconds to trigger a session change check.
type sessionCheckTickMsg struct{}

// sessionCheckMsg carries the current conductor session ID from the blackboard.
type sessionCheckMsg struct {
	sessionID string
}

// monitorCycleMsg reports stats from a monitor cycle (for status bar).
type monitorCycleMsg struct {
	checked   int
	completed int
	failed    int
	spawned   int
	err       error
}

// checkActiveSession reads blackboard keys on startup to detect an in-flight conductor session.
func checkActiveSession(d *db.DB) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		active, _ := d.GetBlackboardValue(ctx, "conductor:active")
		if active != "1" {
			return nil
		}
		goal, _ := d.GetBlackboardValue(ctx, "conductor:goal")
		sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
		rawIDs, _ := d.GetBlackboardValue(ctx, "conductor:task_ids")

		// Parse task IDs (simple JSON array)
		var taskIDs []string
		if rawIDs != "" && rawIDs != "[]" {
			// Reuse the same parsing approach as orchestrator
			for _, part := range splitJSONStringArray(rawIDs) {
				if part != "" {
					taskIDs = append(taskIDs, part)
				}
			}
		}

		return activeSessionMsg{
			goal:      goal,
			sessionID: sessionID,
			taskIDs:   taskIDs,
		}
	}
}

// checkSession reads conductor:session_id from the blackboard and returns a sessionCheckMsg.
func checkSession(d *db.DB) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
		return sessionCheckMsg{sessionID: sessionID}
	}
}

// splitJSONStringArray splits a JSON string array like `["a","b"]` into its elements.
func splitJSONStringArray(raw string) []string {
	var result []string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil
	}
	return result
}

// showClarifyMsg is sent when the conductor needs user clarification.
type showClarifyMsg struct {
	questions []orchestrator.ClarifyQuestion
}

// waitForClarify reads clarification questions from the conductor channel.
func waitForClarify(ch <-chan []orchestrator.ClarifyQuestion) tea.Cmd {
	return func() tea.Msg {
		questions, ok := <-ch
		if !ok {
			return nil
		}
		return showClarifyMsg{questions: questions}
	}
}

// conductorLogMsg carries a single conductor log line from the in-process channel.
type conductorLogMsg struct{ text string }

// conductorEventsMsg carries DB events polled for detached conductor sessions.
type conductorEventsMsg struct{ events []db.Event }

// waitForConductorLog blocks on the conductor log channel and returns one message.
func waitForConductorLog(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return conductorLogMsg{text: text}
	}
}

// fetchConductorEvents polls the DB for new conductor/session events since lastID.
func fetchConductorEvents(d *db.DB, sinceID int64, sessionTaskIDs []string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		events, err := d.EventsSinceForSession(ctx, sinceID, sessionTaskIDs)
		if err != nil || len(events) == 0 {
			return nil
		}
		return conductorEventsMsg{events: events}
	}
}

// waitForProgress reads one progress message from the channel and returns it.
func waitForProgress(ch <-chan goalProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// fetchData queries the database and returns a dataMsg.
func fetchData(d *db.DB) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		tasks, err := d.ListTasks(ctx)
		if err != nil {
			return dataMsg{err: err}
		}

		agents, err := d.ListAgents(ctx)
		if err != nil {
			return dataMsg{err: err}
		}

		locks, err := d.ListFileLocks(ctx)
		if err != nil {
			return dataMsg{err: err}
		}

		events, err := d.RecentEvents(ctx, 10)
		if err != nil {
			return dataMsg{err: err}
		}

		blackboard, err := d.ListBlackboardByPrefix(ctx, "")
		if err != nil {
			return dataMsg{err: err}
		}

		summary, err := d.GetStatusSummary(ctx)
		if err != nil {
			return dataMsg{err: err}
		}

		conductors, _ := d.ListAllConductors(ctx) // best-effort

		return dataMsg{
			tasks:      tasks,
			agents:     agents,
			locks:      locks,
			events:     events,
			blackboard: blackboard,
			summary:    summary,
			conductors: conductors,
		}
	}
}
