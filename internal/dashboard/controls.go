package dashboard

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// ControlAction represents a steering control action type.
type ControlAction string

const (
	ActionPause   ControlAction = "pause"
	ActionResume  ControlAction = "resume"
	ActionStop    ControlAction = "stop"
	ActionRetry   ControlAction = "retry"
	ActionSkip    ControlAction = "skip"
	ActionReplan  ControlAction = "replan"
	ActionAddTask ControlAction = "add-task"
)

// ControlResult holds the outcome of a control action for HTMX fragment rendering.
type ControlResult struct {
	Action    ControlAction
	ProjectID string
	TaskID    string
	OK        bool
	Error     string
	Timestamp time.Time
}

// AddTaskPayload is the JSON body for the add-task control action.
type AddTaskPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Role        string `json:"role"`
}

// ControlsBarData provides template data for rendering the controls bar.
type ControlsBarData struct {
	ProjectID   string
	HasFailed   bool
	IsPaused    bool
	TaskIDs     []string
	FailedTasks []db.Task
}

// ExecuteControl performs a steering action and returns the result.
// This is the core dispatcher called by handleProjectAction.
func (s *Server) ExecuteControl(w http.ResponseWriter, r *http.Request, projectID, action, subID string) {
	ctx := r.Context()
	writer := "dashboard"

	switch ControlAction(action) {
	case ActionPause:
		err := s.DB.SetBlackboard(ctx, "auto:pause", projectID, writer)
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionPause, ProjectID: projectID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionPause, ProjectID: projectID, OK: true, Timestamp: time.Now()})

	case ActionResume:
		err := s.DB.DeleteBlackboard(ctx, "auto:pause")
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionResume, ProjectID: projectID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionResume, ProjectID: projectID, OK: true, Timestamp: time.Now()})

	case ActionStop:
		err := s.DB.SetBlackboard(ctx, "auto:stop", projectID, writer)
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionStop, ProjectID: projectID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionStop, ProjectID: projectID, OK: true, Timestamp: time.Now()})

	case ActionRetry:
		if subID == "" {
			http.Error(w, "task ID required", http.StatusBadRequest)
			return
		}
		err := s.DB.SoftResetTask(ctx, subID)
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionRetry, ProjectID: projectID, TaskID: subID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionRetry, ProjectID: projectID, TaskID: subID, OK: true, Timestamp: time.Now()})

	case ActionSkip:
		if subID == "" {
			http.Error(w, "task ID required", http.StatusBadRequest)
			return
		}
		err := s.DB.FailTask(ctx, subID, "abandoned via dashboard")
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionSkip, ProjectID: projectID, TaskID: subID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionSkip, ProjectID: projectID, TaskID: subID, OK: true, Timestamp: time.Now()})

	case ActionReplan:
		err := s.DB.SetBlackboard(ctx, "auto:replan", projectID, writer)
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionReplan, ProjectID: projectID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionReplan, ProjectID: projectID, OK: true, Timestamp: time.Now()})

	case ActionAddTask:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var payload AddTaskPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			// Fallback: treat body as plain-text title
			payload.Title = strings.TrimSpace(string(body))
			payload.Role = "implementer"
		}
		if payload.Title == "" {
			http.Error(w, "task title required", http.StatusBadRequest)
			return
		}
		if payload.Role == "" {
			payload.Role = "implementer"
		}
		taskID := fmt.Sprintf("t-dash-%d", time.Now().UnixMilli())
		newTask := db.Task{
			ID:          taskID,
			Title:       payload.Title,
			Description: sql.NullString{String: payload.Description, Valid: payload.Description != ""},
			Status:      "pending",
			Priority:    5,
			Role:        payload.Role,
			ConductorID: sql.NullString{String: projectID, Valid: true},
			CreatedAt:   time.Now(),
		}
		err = s.DB.CreateTask(ctx, newTask)
		if err != nil {
			s.writeControlFragment(w, ControlResult{Action: ActionAddTask, ProjectID: projectID, Error: err.Error()})
			return
		}
		s.writeControlFragment(w, ControlResult{Action: ActionAddTask, ProjectID: projectID, TaskID: taskID, OK: true, Timestamp: time.Now()})

	default:
		http.NotFound(w, r)
	}
}

// writeControlFragment writes an HTMX fragment for a control action result.
func (s *Server) writeControlFragment(w http.ResponseWriter, result ControlResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if !result.OK {
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-error" role="alert">%s %s failed: %s</div>`,
			statusIconForAction(result.Action), result.Action, result.Error)
		return
	}

	switch result.Action {
	case ActionPause:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Session paused</div>`+
			`<button hx-post="/api/project/%s/resume" hx-target="#controls-bar" hx-swap="innerHTML" class="ctrl primary">Resume</button>`,
			result.ProjectID)

	case ActionResume:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Session resumed</div>`+
			`<button hx-post="/api/project/%s/pause" hx-target="#controls-bar" hx-swap="innerHTML" class="ctrl primary">Pause</button>`,
			result.ProjectID)

	case ActionStop:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok ctrl-danger">Session stop signal sent</div>`)

	case ActionRetry:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Task %s re-queued</div>`, result.TaskID)

	case ActionSkip:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Task %s abandoned</div>`, result.TaskID)

	case ActionReplan:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Replan signal sent</div>`)

	case ActionAddTask:
		fmt.Fprintf(w, `<div class="ctrl-feedback ctrl-ok">Task %s created</div>`, result.TaskID)
	}
}

// BuildControlsBarData constructs data for the controls bar template fragment.
func (s *Server) BuildControlsBarData(r *http.Request, projectID string) (*ControlsBarData, error) {
	ctx := r.Context()

	taskIDs, err := s.DB.ListTaskIDsByConductor(ctx, projectID)
	if err != nil {
		return nil, err
	}

	tasks, err := s.DB.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	// Check pause state
	pauseVal, _ := s.DB.GetBlackboardValue(ctx, "auto:pause")
	isPaused := pauseVal == projectID

	// Collect failed tasks for this conductor
	var failedTasks []db.Task
	conductorTaskSet := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		conductorTaskSet[id] = true
	}
	for _, t := range tasks {
		if conductorTaskSet[t.ID] && t.Status == "failed" {
			failedTasks = append(failedTasks, t)
		}
	}

	return &ControlsBarData{
		ProjectID:   projectID,
		HasFailed:   len(failedTasks) > 0,
		IsPaused:    isPaused,
		TaskIDs:     taskIDs,
		FailedTasks: failedTasks,
	}, nil
}

// RenderControlsBar writes the HTMX controls bar fragment.
func (s *Server) RenderControlsBar(w http.ResponseWriter, data *ControlsBarData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	pid := data.ProjectID

	// Constructive actions (left)
	fmt.Fprintf(w, `<div id="controls-bar" class="controls-bar">`)
	fmt.Fprintf(w, `<div class="controls-left">`)

	// Retry Failed — only shown when there are failed tasks
	if data.HasFailed {
		for _, t := range data.FailedTasks {
			fmt.Fprintf(w, `<button hx-post="/api/project/%s/retry/%s" hx-target="#ctrl-feedback" hx-swap="innerHTML" class="ctrl primary" title="Retry %s">Retry %s</button>`,
				pid, t.ID, t.ID, truncate(t.Title, 20))
		}
	}

	// Pause/Resume toggle
	if data.IsPaused {
		fmt.Fprintf(w, `<button hx-post="/api/project/%s/resume" hx-target="#ctrl-feedback" hx-swap="innerHTML" class="ctrl primary">Resume</button>`, pid)
	} else {
		fmt.Fprintf(w, `<button hx-post="/api/project/%s/pause" hx-target="#ctrl-feedback" hx-swap="innerHTML" class="ctrl primary">Pause</button>`, pid)
	}

	// Add Task
	fmt.Fprintf(w, `<button hx-post="/api/project/%s/add-task" hx-target="#ctrl-feedback" hx-swap="innerHTML" hx-include="#add-task-form" class="ctrl primary">Add Task</button>`, pid)

	// Skip Task — only when failed tasks exist
	if data.HasFailed {
		for _, t := range data.FailedTasks {
			fmt.Fprintf(w, `<button hx-post="/api/project/%s/skip/%s" hx-target="#ctrl-feedback" hx-swap="innerHTML" class="ctrl primary" title="Skip %s">Skip %s</button>`,
				pid, t.ID, t.ID, truncate(t.Title, 20))
		}
	}

	// Replan
	fmt.Fprintf(w, `<button hx-post="/api/project/%s/replan" hx-target="#ctrl-feedback" hx-swap="innerHTML" class="ctrl primary">Replan</button>`, pid)

	fmt.Fprintf(w, `</div>`) // end controls-left

	// Flex spacer
	fmt.Fprintf(w, `<div class="controls-spacer"></div>`)

	// Destructive actions (right)
	fmt.Fprintf(w, `<div class="controls-right">`)
	fmt.Fprintf(w, `<button hx-post="/api/project/%s/stop" hx-target="#ctrl-feedback" hx-swap="innerHTML" hx-confirm="Stop this session?" class="ctrl danger">Stop Session</button>`, pid)
	fmt.Fprintf(w, `</div>`) // end controls-right

	fmt.Fprintf(w, `<div id="ctrl-feedback"></div>`)
	fmt.Fprintf(w, `</div>`) // end controls-bar
}

// statusIconForAction returns a Unicode icon for the action type.
func statusIconForAction(action ControlAction) string {
	switch action {
	case ActionPause:
		return "\u23f8" // pause
	case ActionResume:
		return "\u25b6" // play
	case ActionStop:
		return "\u23f9" // stop
	case ActionRetry:
		return "\u21bb" // retry arrow
	case ActionSkip:
		return "\u23ed" // skip
	case ActionReplan:
		return "\u21ba" // replan arrow
	case ActionAddTask:
		return "+"
	default:
		return "\u2022"
	}
}

// truncate shortens a string to maxLen, adding ellipsis if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "\u2026"
}
