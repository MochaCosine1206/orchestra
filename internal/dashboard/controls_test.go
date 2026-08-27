package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestExecuteControlPause(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/api/project/proj-1/pause", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "pause", "")

	if w.Code != 200 {
		t.Errorf("pause: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "paused") {
		t.Errorf("pause response should contain 'paused', got: %s", body)
	}
}

func TestExecuteControlResume(t *testing.T) {
	s, _ := setupTestServer(t)

	// First pause, then resume
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	s.ExecuteControl(w, req, "proj-1", "pause", "")

	req = httptest.NewRequest("POST", "/", nil)
	w = httptest.NewRecorder()
	s.ExecuteControl(w, req, "proj-1", "resume", "")

	if w.Code != 200 {
		t.Errorf("resume: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "resumed") {
		t.Errorf("resume response should contain 'resumed', got: %s", body)
	}
}

func TestExecuteControlStop(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "stop", "")

	if w.Code != 200 {
		t.Errorf("stop: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "stop") {
		t.Errorf("stop response should contain 'stop', got: %s", body)
	}
}

func TestExecuteControlRetryNoTaskID(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "retry", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("retry without task: want 400, got %d", w.Code)
	}
}

func TestExecuteControlSkipNoTaskID(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "skip", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("skip without task: want 400, got %d", w.Code)
	}
}

func TestExecuteControlReplan(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "replan", "")

	if w.Code != 200 {
		t.Errorf("replan: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Replan") {
		t.Errorf("replan response should contain 'Replan', got: %s", body)
	}
}

func TestExecuteControlAddTaskJSON(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	body := `{"title":"New task","description":"Do something","role":"architect"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "cond-test", "add-task", "")

	if w.Code != 200 {
		t.Errorf("add-task JSON: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	if !strings.Contains(resp, "created") {
		t.Errorf("add-task should contain 'created', got: %s", resp)
	}
}

func TestExecuteControlAddTaskPlainText(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("POST", "/", strings.NewReader("Simple task title"))
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "cond-test", "add-task", "")

	if w.Code != 200 {
		t.Errorf("add-task plain text: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestExecuteControlAddTaskEmpty(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "add-task", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("add-task empty: want 400, got %d", w.Code)
	}
}

func TestExecuteControlUnknownAction(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "proj-1", "unknown", "")

	if w.Code != 404 {
		t.Errorf("unknown action: want 404, got %d", w.Code)
	}
}

func TestWriteControlFragmentError(t *testing.T) {
	s, _ := setupTestServer(t)
	w := httptest.NewRecorder()

	s.writeControlFragment(w, ControlResult{
		Action: ActionPause,
		OK:     false,
		Error:  "test error",
	})

	body := w.Body.String()
	if !strings.Contains(body, "ctrl-error") {
		t.Error("error fragment should have ctrl-error class")
	}
	if !strings.Contains(body, "test error") {
		t.Error("error fragment should contain error message")
	}
}

func TestWriteControlFragmentSuccess(t *testing.T) {
	s, _ := setupTestServer(t)

	actions := []ControlAction{ActionPause, ActionResume, ActionStop, ActionRetry, ActionSkip, ActionReplan, ActionAddTask}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			w := httptest.NewRecorder()
			s.writeControlFragment(w, ControlResult{
				Action:    action,
				OK:        true,
				ProjectID: "proj-1",
				TaskID:    "task-1",
			})
			body := w.Body.String()
			if !strings.Contains(body, "ctrl-ok") && !strings.Contains(body, "ctrl-danger") {
				t.Errorf("success fragment for %q should have ctrl-ok or ctrl-danger class", action)
			}
		})
	}
}

func TestStatusIconForAction(t *testing.T) {
	actions := []ControlAction{ActionPause, ActionResume, ActionStop, ActionRetry, ActionSkip, ActionReplan, ActionAddTask, "unknown"}
	for _, action := range actions {
		icon := statusIconForAction(action)
		if icon == "" {
			t.Errorf("statusIconForAction(%q) returned empty string", action)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is t\u2026"},
		{"", 5, ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := truncate(tc.input, tc.maxLen)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestBuildControlsBarData(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/", nil)
	data, err := s.BuildControlsBarData(req, "cond-test")
	if err != nil {
		t.Fatalf("BuildControlsBarData: %v", err)
	}
	if data == nil {
		t.Fatal("BuildControlsBarData returned nil")
	}
	if data.ProjectID != "cond-test" {
		t.Errorf("ProjectID = %q, want %q", data.ProjectID, "cond-test")
	}
	if !data.HasFailed {
		t.Error("expected HasFailed=true with failed task in seed data")
	}
	if len(data.FailedTasks) != 1 {
		t.Errorf("len(FailedTasks) = %d, want 1", len(data.FailedTasks))
	}
}

func TestBuildControlsBarDataPaused(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	// Set pause state
	ctx := context.Background()
	_ = s.DB.SetBlackboard(ctx, "auto:pause", "cond-test", "test")

	req := httptest.NewRequest("GET", "/", nil)
	data, err := s.BuildControlsBarData(req, "cond-test")
	if err != nil {
		t.Fatalf("BuildControlsBarData: %v", err)
	}
	if !data.IsPaused {
		t.Error("expected IsPaused=true after setting pause blackboard")
	}
}

func TestRenderControlsBar(t *testing.T) {
	s, _ := setupTestServer(t)
	w := httptest.NewRecorder()

	data := &ControlsBarData{
		ProjectID:   "proj-1",
		HasFailed:   false,
		IsPaused:    false,
		TaskIDs:     []string{"t-1", "t-2"},
		FailedTasks: nil,
	}

	s.RenderControlsBar(w, data)

	body := w.Body.String()
	if !strings.Contains(body, "controls-bar") {
		t.Error("expected controls-bar element")
	}
	if !strings.Contains(body, "Pause") {
		t.Error("expected Pause button when not paused")
	}
	if !strings.Contains(body, "Stop Session") {
		t.Error("expected Stop Session button")
	}
	if !strings.Contains(body, "Add Task") {
		t.Error("expected Add Task button")
	}
}

func TestRenderControlsBarPaused(t *testing.T) {
	s, _ := setupTestServer(t)
	w := httptest.NewRecorder()

	data := &ControlsBarData{
		ProjectID: "proj-1",
		IsPaused:  true,
	}

	s.RenderControlsBar(w, data)

	body := w.Body.String()
	if !strings.Contains(body, "Resume") {
		t.Error("expected Resume button when paused")
	}
}

func TestRenderControlsBarWithFailedTasks(t *testing.T) {
	s, _ := setupTestServer(t)
	w := httptest.NewRecorder()

	data := &ControlsBarData{
		ProjectID: "proj-1",
		HasFailed: true,
		FailedTasks: []db.Task{
			{ID: "t-fail-1", Title: "Failed task"},
		},
	}

	s.RenderControlsBar(w, data)

	body := w.Body.String()
	if !strings.Contains(body, "Retry") {
		t.Error("expected Retry button for failed tasks")
	}
	if !strings.Contains(body, "Skip") {
		t.Error("expected Skip button for failed tasks")
	}
}
