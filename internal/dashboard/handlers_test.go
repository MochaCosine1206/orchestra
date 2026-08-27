package dashboard

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// --- extractID tests ---

func TestExtractID(t *testing.T) {
	cases := []struct {
		path, prefix, want string
	}{
		{"/project/abc", "/project/", "abc"},
		{"/project/abc/", "/project/", "abc"},
		{"/project/abc/extra", "/project/", "abc"},
		{"/project/", "/project/", ""},
		{"/task/t-123", "/task/", "t-123"},
		{"/api/task/t-1", "/api/task/", "t-1"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := extractID(tc.path, tc.prefix)
			if got != tc.want {
				t.Errorf("extractID(%q, %q) = %q, want %q", tc.path, tc.prefix, got, tc.want)
			}
		})
	}
}

// --- formatTimeAgo tests ---

func TestFormatTimeAgo(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s ago"},
		{30 * time.Second, "30s ago"},
		{3 * time.Minute, "3m ago"},
		{90 * time.Minute, "1h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := formatTimeAgo(tc.d)
			if got != tc.want {
				t.Errorf("formatTimeAgo(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

// --- formatElapsed tests ---

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{5*time.Minute + 10*time.Second, "5m 10s"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := formatElapsed(tc.d)
			if got != tc.want {
				t.Errorf("formatElapsed(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

// --- taskStatusDisplay tests ---

func TestTaskStatusDisplay(t *testing.T) {
	cases := []struct {
		status, wantClass, wantIcon string
	}{
		{"done", "done", "\u2713"},
		{"merged", "done", "\u2713"},
		{"in_progress", "running", "\u25cf"},
		{"assigned", "running", "\u25cf"},
		{"running", "running", "\u25cf"},
		{"pending", "pending", ""},
		{"failed", "failed", "\u25cf"},
		{"unknown", "pending", ""},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			class, icon := taskStatusDisplay(tc.status)
			if class != tc.wantClass {
				t.Errorf("taskStatusDisplay(%q) class = %q, want %q", tc.status, class, tc.wantClass)
			}
			if icon != tc.wantIcon {
				t.Errorf("taskStatusDisplay(%q) icon = %q, want %q", tc.status, icon, tc.wantIcon)
			}
		})
	}
}

// --- roleDisplay tests ---

func TestRoleDisplay(t *testing.T) {
	cases := []struct {
		role, wantClass string
	}{
		{"implementer", "impl"},
		{"architect", "arch"},
		{"reviewer", "rev"},
		{"scout", "scout"},
		{"researcher", "research"},
		{"editor", "editor"},
		{"unknown", "impl"},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			label, class := roleDisplay(tc.role)
			if class != tc.wantClass {
				t.Errorf("roleDisplay(%q) class = %q, want %q", tc.role, class, tc.wantClass)
			}
			if label != strings.ToUpper(tc.role) {
				t.Errorf("roleDisplay(%q) label = %q, want %q", tc.role, label, strings.ToUpper(tc.role))
			}
		})
	}
}

// --- roleEmoji tests ---

func TestRoleEmoji(t *testing.T) {
	roles := []string{"architect", "implementer", "reviewer", "scout", "researcher", "editor", "unknown"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			emoji := roleEmoji(role)
			if emoji == "" {
				t.Errorf("roleEmoji(%q) returned empty string", role)
			}
		})
	}
}

// --- agentStatusColor tests ---

func TestAgentStatusColor(t *testing.T) {
	cases := []struct {
		status, want string
	}{
		{"active", "green"},
		{"working", "green"},
		{"idle", "dim"},
		{"failed", "red"},
		{"dead", "red"},
		{"other", "yellow"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			got := agentStatusColor(tc.status)
			if got != tc.want {
				t.Errorf("agentStatusColor(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// --- roleColorName tests ---

func TestRoleColorName(t *testing.T) {
	cases := []struct {
		role, want string
	}{
		{"implementer", "green"},
		{"architect", "blue"},
		{"reviewer", "yellow"},
		{"scout", "accent"},
		{"researcher", "blue"},
		{"editor", "dim"},
		{"unknown", "dim"},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			got := roleColorName(tc.role)
			if got != tc.want {
				t.Errorf("roleColorName(%q) = %q, want %q", tc.role, got, tc.want)
			}
		})
	}
}

// --- eventColorName tests ---

func TestEventColorName(t *testing.T) {
	cases := []struct {
		eventType, want string
	}{
		{"task_done", "green"},
		{"branch_merged", "green"},
		{"task_failed", "red"},
		{"circuit_breaker", "red"},
		{"agent_spawned", "blue"},
		{"task_assigned", "blue"},
		{"rate_limit", "dim"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			got := eventColorName(tc.eventType)
			if got != tc.want {
				t.Errorf("eventColorName(%q) = %q, want %q", tc.eventType, got, tc.want)
			}
		})
	}
}

// --- eventDotColor tests ---

func TestEventDotColor(t *testing.T) {
	cases := []struct {
		eventType, wantContains string
	}{
		{"task_done", "green"},
		{"task_failed", "red"},
		{"agent_spawned", "blue"},
		{"rate_limit", "yellow"},
		{"unknown_event", "dim"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			got := eventDotColor(tc.eventType)
			if !strings.Contains(got, tc.wantContains) {
				t.Errorf("eventDotColor(%q) = %q, want to contain %q", tc.eventType, got, tc.wantContains)
			}
		})
	}
}

// --- nullStr tests ---

func TestNullStr(t *testing.T) {
	if got := nullStr(sql.NullString{String: "hello", Valid: true}); got != "hello" {
		t.Errorf("nullStr(valid) = %q, want %q", got, "hello")
	}
	if got := nullStr(sql.NullString{Valid: false}); got != "" {
		t.Errorf("nullStr(null) = %q, want empty", got)
	}
}

// --- computeSuccessRate tests ---

func TestComputeSuccessRate(t *testing.T) {
	cases := []struct {
		done, failed int
		want         string
	}{
		{0, 0, "\u2014"},
		{10, 0, "100%"},
		{0, 10, "0%"},
		{3, 1, "75%"},
		{7, 3, "70%"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := computeSuccessRate(tc.done, tc.failed)
			if got != tc.want {
				t.Errorf("computeSuccessRate(%d, %d) = %q, want %q", tc.done, tc.failed, got, tc.want)
			}
		})
	}
}

// --- resolutionTierClass tests ---

func TestResolutionTierClassAll(t *testing.T) {
	cases := []struct {
		tier, want string
	}{
		{"1", "auto"},
		{"2", "auto"},
		{"3", "auto"},
		{"import", "auto"},
		{"non-overlapping", "auto"},
		{"trivial", "auto"},
		{"4", "llm"},
		{"llm", "llm"},
		{"unknown", "failed"},
		{"", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			got := resolutionTierClass(tc.tier)
			if got != tc.want {
				t.Errorf("resolutionTierClass(%q) = %q, want %q", tc.tier, got, tc.want)
			}
		})
	}
}

// --- formatConductorName tests ---

func TestFormatConductorName(t *testing.T) {
	// Phase: line in goal
	c1 := db.ConductorRecord{ID: "s-1", Goal: "Phase: Dashboard v2\nSome details"}
	if got := formatConductorName(c1, nil); got != "Dashboard v2" {
		t.Errorf("formatConductorName(Phase:) = %q, want %q", got, "Dashboard v2")
	}

	// Project: line in goal
	c2 := db.ConductorRecord{ID: "s-2", Goal: "# Header\nProject: My Project\nMore text"}
	if got := formatConductorName(c2, nil); got != "My Project" {
		t.Errorf("formatConductorName(Project:) = %q, want %q", got, "My Project")
	}

	// Fallback to first task title
	c3 := db.ConductorRecord{ID: "cond-3", Goal: ""}
	tasks := []db.Task{
		{ID: "t1", Title: "First Task", ConductorID: sql.NullString{String: "cond-3", Valid: true}},
	}
	if got := formatConductorName(c3, tasks); got != "First Task" {
		t.Errorf("formatConductorName(task fallback) = %q, want %q", got, "First Task")
	}

	// Session ID format
	c4 := db.ConductorRecord{ID: "s-20260323-230019-9852", Goal: ""}
	if got := formatConductorName(c4, nil); !strings.Contains(got, "Session") {
		t.Errorf("formatConductorName(session ID) = %q, want to contain 'Session'", got)
	}

	// Raw ID fallback
	c5 := db.ConductorRecord{ID: "custom-id", Goal: ""}
	if got := formatConductorName(c5, nil); got != "custom-id" {
		t.Errorf("formatConductorName(raw ID) = %q, want %q", got, "custom-id")
	}
}

// --- formatConductorGoal tests ---

func TestFormatConductorGoal(t *testing.T) {
	// Empty goal
	c1 := db.ConductorRecord{Goal: ""}
	if got := formatConductorGoal(c1); got != "" {
		t.Errorf("formatConductorGoal(empty) = %q, want empty", got)
	}

	// Skips metadata lines
	c2 := db.ConductorRecord{Goal: "Phase: X\nProject: Y\nDescription: Z\nActual goal content here"}
	if got := formatConductorGoal(c2); got != "Actual goal content here" {
		t.Errorf("formatConductorGoal(metadata) = %q, want %q", got, "Actual goal content here")
	}

	// Skips comments and HTML
	c3 := db.ConductorRecord{Goal: "# Header\n<div>\n{json}\nReal content"}
	if got := formatConductorGoal(c3); got != "Real content" {
		t.Errorf("formatConductorGoal(comments) = %q, want %q", got, "Real content")
	}

	// Truncates long lines
	c4 := db.ConductorRecord{Goal: strings.Repeat("a", 100)}
	got := formatConductorGoal(c4)
	if len(got) > 80 {
		t.Errorf("formatConductorGoal(long) length = %d, want <= 80", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("formatConductorGoal(long) should end with '...'")
	}
}

// --- formatTextToHTML tests ---

func TestFormatTextToHTML(t *testing.T) {
	// Empty
	if got := formatTextToHTML(""); got != "" {
		t.Errorf("formatTextToHTML(empty) = %q, want empty", got)
	}

	// Simple paragraph
	got := formatTextToHTML("Hello world")
	if !strings.Contains(got, "<p>Hello world</p>") {
		t.Errorf("formatTextToHTML(simple) = %q, want <p> wrapping", got)
	}

	// Code block
	got = formatTextToHTML("```go\nfunc main() {}\n```")
	if !strings.Contains(got, "<pre><code") {
		t.Errorf("formatTextToHTML(code) = %q, want <pre><code>", got)
	}
	if !strings.Contains(got, "lang-go") {
		t.Errorf("formatTextToHTML(code) = %q, want lang-go class", got)
	}

	// Truncation
	longText := strings.Repeat("Line of text here.\n", 50)
	got = formatTextToHTML(longText)
	if !strings.Contains(got, "truncated") {
		t.Errorf("formatTextToHTML(long) should contain truncated indicator")
	}

	// HTML escaping
	got = formatTextToHTML("<script>alert('xss')</script>")
	if strings.Contains(got, "<script>") {
		t.Errorf("formatTextToHTML should escape HTML: %q", got)
	}
}

// --- parseAcceptanceCriteria tests ---

func TestParseAcceptanceCriteria(t *testing.T) {
	// Empty
	if items := parseAcceptanceCriteria(""); items != nil {
		t.Errorf("parseAcceptanceCriteria(empty) = %v, want nil", items)
	}

	// Checkbox items
	text := "- [x] First done\n- [ ] Second pending\n- [X] Third done"
	items := parseAcceptanceCriteria(text)
	if len(items) != 3 {
		t.Fatalf("parseAcceptanceCriteria: got %d items, want 3", len(items))
	}
	if !items[0].Done {
		t.Error("item 0 should be done")
	}
	if items[1].Done {
		t.Error("item 1 should not be done")
	}
	if !items[2].Done {
		t.Error("item 2 should be done")
	}

	// Numbered items
	text = "1. First item\n2. Second item"
	items = parseAcceptanceCriteria(text)
	if len(items) != 2 {
		t.Fatalf("parseAcceptanceCriteria(numbered): got %d items, want 2", len(items))
	}
	if items[0].Text != "First item" {
		t.Errorf("item 0 text = %q, want %q", items[0].Text, "First item")
	}

	// Bullet items
	text = "- Item A\n* Item B"
	items = parseAcceptanceCriteria(text)
	if len(items) != 2 {
		t.Fatalf("parseAcceptanceCriteria(bullets): got %d items, want 2", len(items))
	}
}

// --- formatEventText tests ---

func TestFormatEventText(t *testing.T) {
	cases := []struct {
		eventType, payload, want string
	}{
		{"branch_merged", `{"branch":"feature/test"}`, "Merged feature/test"},
		{"branch_merged", "simple payload", "Branch merged"},
		{"spawner_completed", "", "Agent completed"},
		{"agent_spawned", "", "Agent spawned"},
		{"agent_respawning", "", "Agent respawning"},
		{"task_killed", "", "Task killed"},
		{"spec_generated", "", "Spec generated"},
		{"spec_size_warning", "", "Spec size warning"},
		{"context_budget_check", "", "Context budget check"},
		{"merge_queue_enqueued", "", "Queued for merge"},
		{"staging_merged_to_dev", "", "Staging merged to dev"},
		{"conductor_completed", "", "Session completed"},
		{"validation_failed", "", "Validation failed"},
		{"spawner_validation_failed", "", "Build/test failed"},
		{"custom_event", "", "custom event"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			got := formatEventText(tc.eventType, tc.payload)
			if got != tc.want {
				t.Errorf("formatEventText(%q, %q) = %q, want %q", tc.eventType, tc.payload, got, tc.want)
			}
		})
	}
}

// --- coalesceEvents tests ---

func TestCoalesceEvents(t *testing.T) {
	now := time.Now()
	events := []db.Event{
		{EventType: "agent_spawned", Payload: sql.NullString{String: "agent-1", Valid: true}, Timestamp: now},
		{EventType: "agent_spawned", Payload: sql.NullString{String: "agent-2", Valid: true}, Timestamp: now},
		{EventType: "task_done", Payload: sql.NullString{String: "task-1", Valid: true}, Timestamp: now},
		{EventType: "task_done", Payload: sql.NullString{String: "task-2", Valid: true}, Timestamp: now},
		{EventType: "task_done", Payload: sql.NullString{String: "task-3", Valid: true}, Timestamp: now},
		{EventType: "task_failed", Timestamp: now},
	}

	result := coalesceEvents(events)
	if len(result) != 3 {
		t.Fatalf("coalesceEvents: got %d groups, want 3", len(result))
	}
	if result[0].EventType != "agent_spawned" || result[0].Count != 2 {
		t.Errorf("group 0: type=%q count=%d, want agent_spawned count=2", result[0].EventType, result[0].Count)
	}
	if result[1].EventType != "task_done" || result[1].Count != 3 {
		t.Errorf("group 1: type=%q count=%d, want task_done count=3", result[1].EventType, result[1].Count)
	}
	if result[2].EventType != "task_failed" || result[2].Count != 1 {
		t.Errorf("group 2: type=%q count=%d, want task_failed count=1", result[2].EventType, result[2].Count)
	}
}

func TestCoalesceEventsEmpty(t *testing.T) {
	result := coalesceEvents(nil)
	if len(result) != 0 {
		t.Errorf("coalesceEvents(nil) = %d, want 0", len(result))
	}
}

// --- Handler integration tests with seeded data ---

func TestHandleNavStatus(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/nav-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /api/nav-status: want 200, got %d", w.Code)
	}

	// POST should be rejected
	req = httptest.NewRequest("POST", "/api/nav-status", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST /api/nav-status: want 405, got %d", w.Code)
	}
}

func TestHandleAPIBriefing(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/briefing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /api/briefing: want 200, got %d", w.Code)
	}

	// POST should be rejected
	req = httptest.NewRequest("POST", "/api/briefing", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST /api/briefing: want 405, got %d", w.Code)
	}
}

func TestHandleAPIFactoryMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/factory", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST /api/factory: want 405, got %d", w.Code)
	}
}

func TestHandleAPITaskMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/task/t-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST /api/task/t-1: want 405, got %d", w.Code)
	}
}

func TestHandleAPIAgentMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/agent/a-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST /api/agent/a-1: want 405, got %d", w.Code)
	}
}

func TestHandleConfigThemeInvalidPayload(t *testing.T) {
	_, mux := setupTestServer(t)

	// Empty theme
	req := httptest.NewRequest("POST", "/api/config/theme", strings.NewReader(`{"theme":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("POST /api/config/theme (empty): want 400, got %d", w.Code)
	}

	// Invalid JSON
	req = httptest.NewRequest("POST", "/api/config/theme", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("POST /api/config/theme (bad json): want 400, got %d", w.Code)
	}

	// GET should be rejected
	req = httptest.NewRequest("GET", "/api/config/theme", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("GET /api/config/theme: want 405, got %d", w.Code)
	}
}

func TestHandleFactoryPauseAllMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/factory/pause-all", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("GET /api/factory/pause-all: want 405, got %d", w.Code)
	}
}

func TestHandleAPIProjectOrActionNoProject(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/project/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /api/project/ (no ID): want 404, got %d", w.Code)
	}
}

func TestHandleProjectActionUnknownAction(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/project/test-id/invalid-action", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("POST /api/project/test-id/invalid-action: want 404, got %d", w.Code)
	}
}

func TestHandleProjectActionRetryNoTaskID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/project/test-id/retry", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("POST retry without task ID: want 400, got %d", w.Code)
	}
}

func TestHandleProjectActionSkipNoTaskID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/project/test-id/skip", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("POST skip without task ID: want 400, got %d", w.Code)
	}
}

func TestHandleProjectActionAddTaskEmpty(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/project/test-id/add-task", strings.NewReader(""))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("POST add-task with empty body: want 400, got %d", w.Code)
	}
}

// --- Seeded data handler tests ---

func seedTestData(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()

	// Seed conductor
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-test', 123, 'Phase: Test Phase\nBuild test suite', 'active', 'staging/cond-test', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("seed conductor: %v", err)
	}

	// Seed tasks
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, conductor_id, title, description, status, role, priority, branch, created_at, started_at, completed_at)
		 VALUES
		   ('task-a', 'cond-test', 'Implement feature A', 'Description of A', 'done', 'implementer', 1, 'feature/task-a', datetime('now', '-10 minutes'), datetime('now', '-10 minutes'), datetime('now', '-5 minutes')),
		   ('task-b', 'cond-test', 'Review feature A', NULL, 'in_progress', 'reviewer', 2, 'feature/task-b', datetime('now', '-5 minutes'), datetime('now', '-5 minutes'), NULL),
		   ('task-c', 'cond-test', 'Fix bug C', 'Bug description', 'failed', 'implementer', 3, 'feature/task-c', datetime('now', '-3 minutes'), datetime('now', '-3 minutes'), NULL)`)
	if err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	// Seed agents
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, current_task, pid, created_at)
		 VALUES
		   ('agent-a', 'implementer', 'done', 'task-a', 1234, datetime('now')),
		   ('agent-b', 'reviewer', 'active', 'task-b', 5678, datetime('now'))`)
	if err != nil {
		t.Fatalf("seed agents: %v", err)
	}

	// Seed events
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO events (event_type, payload, task_id)
		 VALUES
		   ('agent_spawned', 'agent-a started', 'task-a'),
		   ('task_done', 'task-a completed', 'task-a'),
		   ('agent_spawned', 'agent-b started', 'task-b'),
		   ('task_failed', 'task-c error', 'task-c')`)
	if err != nil {
		t.Fatalf("seed events: %v", err)
	}
}

func TestHandleAPIProjectFragment(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/api/project/cond-test", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /api/project/cond-test: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAPIProjectFragmentNotFound(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/project/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /api/project/nonexistent: want 404, got %d", w.Code)
	}
}

func TestHandleAPITaskWithData(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/api/task/task-a", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /api/task/task-a: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Error("expected JSON content type")
	}
}

func TestHandleAPITaskNotFound(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/task/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /api/task/nonexistent: want 404, got %d", w.Code)
	}
}

func TestHandleAPIAgentWithData(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/api/agent/agent-a", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /api/agent/agent-a: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Error("expected JSON content type")
	}
}

func TestHandleAPIAgentNotFound(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/agent/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /api/agent/nonexistent: want 404, got %d", w.Code)
	}
}

func TestBuildFactoryDataWithSeededData(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	data := s.buildFactoryData(context.Background())
	if data == nil {
		t.Fatal("buildFactoryData returned nil")
	}

	projects, ok := data["projects"].([]ProjectRow)
	if !ok || len(projects) == 0 {
		t.Error("buildFactoryData should contain projects")
	}
	if projects[0].ID != "cond-test" {
		t.Errorf("project ID = %q, want %q", projects[0].ID, "cond-test")
	}
	if projects[0].DotClass != "running" {
		t.Errorf("project DotClass = %q, want %q", projects[0].DotClass, "running")
	}

	// Verify stat counts
	if data["tasksDone"].(int) != 1 {
		t.Errorf("tasksDone = %d, want 1", data["tasksDone"].(int))
	}
	if data["tasksFailed"].(int) != 1 {
		t.Errorf("tasksFailed = %d, want 1", data["tasksFailed"].(int))
	}
}

func TestBuildProjectDetailData(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	data, err := s.buildProjectDetailData(context.Background(), "cond-test")
	if err != nil {
		t.Fatalf("buildProjectDetailData: %v", err)
	}
	if data == nil {
		t.Fatal("buildProjectDetailData returned nil")
	}

	if !strings.HasPrefix(data.Name, "Test Phase") {
		t.Errorf("Name = %q, want prefix %q", data.Name, "Test Phase")
	}
	if data.DotClass != "running" {
		t.Errorf("DotClass = %q, want %q", data.DotClass, "running")
	}
	if data.TasksTotal != 3 {
		t.Errorf("TasksTotal = %d, want 3", data.TasksTotal)
	}
	if data.TasksDone != 1 {
		t.Errorf("TasksDone = %d, want 1", data.TasksDone)
	}
	if len(data.Tasks) != 3 {
		t.Errorf("len(Tasks) = %d, want 3", len(data.Tasks))
	}
}

func TestBuildProjectDetailDataNotFound(t *testing.T) {
	s, _ := setupTestServer(t)

	data, err := s.buildProjectDetailData(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("buildProjectDetailData: %v", err)
	}
	if data != nil {
		t.Error("expected nil for nonexistent conductor")
	}
}

func TestBriefingWithCookie(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	// Request with last_seen cookie
	req := httptest.NewRequest("GET", "/briefing", nil)
	req.AddCookie(&http.Cookie{Name: "orchestra_last_seen", Value: "1"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GET /briefing: want 200, got %d", w.Code)
	}

	// Should set updated cookie
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "orchestra_last_seen" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected orchestra_last_seen cookie to be set")
	}
}

func TestHandleIndex404ForNonRoot(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /nonexistent: want 404, got %d", w.Code)
	}
}

// --- handleGit latest tests ---

func TestHandleGitLatestNoConductors(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/git/latest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GET /git/latest (no conductors): want 200, got %d", w.Code)
	}
}

func TestHandleGitLatestWithConductor(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/git/latest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GET /git/latest: want 200, got %d", w.Code)
	}
}

// --- Project action tests ---

func TestHandleProjectActionStop(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/project/test-cond/stop", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST stop: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "conductor:test-cond:signal")
	if val != "stop" {
		t.Errorf("blackboard signal = %q, want %q", val, "stop")
	}
}

func TestHandleProjectActionReplan(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/project/test-cond/replan", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST replan: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:replan")
	if val != "test-cond" {
		t.Errorf("auto:replan = %q, want %q", val, "test-cond")
	}
}

func TestHandleProjectActionRetryWithTaskID(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/project/test-cond/retry/task-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST retry with task: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:retry:task-123")
	if val != "requested" {
		t.Errorf("auto:retry = %q, want %q", val, "requested")
	}
}

func TestHandleProjectActionSkipWithTaskID(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/project/test-cond/skip/task-456", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST skip with task: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:skip:task-456")
	if val != "requested" {
		t.Errorf("auto:skip = %q, want %q", val, "requested")
	}
}

func TestHandleProjectActionAddTaskWithBody(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/project/test-cond/add-task", strings.NewReader("Build the feature"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST add-task with body: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- handleFactoryPauseAll with seeded conductors ---

func TestHandleFactoryPauseAllWithConductors(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("POST", "/api/factory/pause-all", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST pause-all: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "pause-all") {
		t.Error("response should contain pause-all action")
	}
}

// --- handleNavStatus with seeded data ---

func TestHandleNavStatusWithData(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/api/nav-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GET /api/nav-status with data: want 200, got %d", w.Code)
	}
}

// --- handleConfigTheme success ---

func TestHandleConfigThemeSuccess(t *testing.T) {
	s, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/config/theme", strings.NewReader(`{"theme":"midnight"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST theme: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	val, _ := s.DB.GetBlackboardValue(context.Background(), "config:theme")
	if val != "midnight" {
		t.Errorf("config:theme = %q, want %q", val, "midnight")
	}
}

// --- ExecuteControl retry/skip with valid task IDs ---

func TestExecuteControlRetryWithTask(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "cond-test", "retry", "task-c")

	if w.Code != 200 {
		t.Errorf("retry with task: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestExecuteControlSkipWithTask(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	s.ExecuteControl(w, req, "cond-test", "skip", "task-c")

	if w.Code != 200 {
		t.Errorf("skip with task: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- buildBriefingData with cookie ---

func TestBuildBriefingDataWithCookie(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	req := httptest.NewRequest("GET", "/briefing", nil)
	req.AddCookie(&http.Cookie{Name: "orchestra_last_seen", Value: "0"})
	data, lastSeen := s.buildBriefingData(req)

	if data == nil {
		t.Fatal("buildBriefingData returned nil")
	}
	if lastSeen != 0 {
		t.Errorf("lastSeenID = %d, want 0", lastSeen)
	}

	cards, ok := data["cards"].([]BriefingCard)
	if !ok {
		t.Fatal("expected cards in briefing data")
	}
	if len(cards) == 0 {
		t.Error("expected at least one briefing card with seeded data")
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"key": "value"})

	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Error("expected application/json content type")
	}
	if !strings.Contains(w.Body.String(), "key") {
		t.Error("expected JSON body to contain key")
	}
}
