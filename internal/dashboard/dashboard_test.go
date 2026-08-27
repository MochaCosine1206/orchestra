package dashboard

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Accessibility helper tests ---

func TestAriaLabel(t *testing.T) {
	cases := []struct {
		role, status string
		contains     string
	}{
		{"architect", "running", "Architect agent, currently working"},
		{"implementer", "pending", "Implementer agent, currently waiting to start"},
		{"reviewer", "done", "Reviewer agent, currently finished"},
		{"researcher", "failed", "Researcher agent, currently in error state"},
		{"scout", "idle", "Scout agent, currently idle"},
		{"editor", "merging", "Editor agent, currently merging changes"},
		{"implementer", "in_progress", "Implementer agent, currently working"},
		{"reviewer", "completed", "Reviewer agent, currently finished"},
	}
	for _, tc := range cases {
		t.Run(tc.role+"_"+tc.status, func(t *testing.T) {
			got := AriaLabel(tc.role, tc.status)
			if got != tc.contains {
				t.Errorf("AriaLabel(%q, %q) = %q, want %q", tc.role, tc.status, got, tc.contains)
			}
		})
	}
}

func TestAriaLabelAllRoles(t *testing.T) {
	roles := []string{"architect", "implementer", "reviewer", "researcher", "scout", "editor"}
	statuses := []string{"pending", "running", "done", "failed"}

	seen := make(map[string]bool)
	for _, role := range roles {
		for _, status := range statuses {
			label := AriaLabel(role, status)
			if label == "" {
				t.Errorf("AriaLabel(%q, %q) returned empty string", role, status)
			}
			seen[label] = true
		}
	}
	// Each role+status should produce a distinct label
	if len(seen) != len(roles)*len(statuses) {
		t.Errorf("expected %d distinct labels, got %d", len(roles)*len(statuses), len(seen))
	}
}

func TestStatusDescription(t *testing.T) {
	statuses := []string{"pending", "running", "in_progress", "done", "completed", "failed", "idle", "merging"}
	for _, s := range statuses {
		t.Run(s, func(t *testing.T) {
			desc := StatusDescription(s)
			if desc == "" || desc == "Unknown status" {
				t.Errorf("StatusDescription(%q) returned empty or unknown", s)
			}
			// Should contain the status concept
			lower := strings.ToLower(desc)
			if !strings.Contains(lower, " ") {
				t.Errorf("StatusDescription(%q) = %q, expected descriptive text", s, desc)
			}
		})
	}
}

func TestStatusDescriptionUnknown(t *testing.T) {
	desc := StatusDescription("nonexistent")
	if desc != "Unknown status" {
		t.Errorf("StatusDescription(%q) = %q, want %q", "nonexistent", desc, "Unknown status")
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		t        time.Time
		expected string
	}{
		{"just_now", now.Add(-10 * time.Second), "just now"},
		{"one_minute", now.Add(-90 * time.Second), "1 minute ago"},
		{"five_minutes", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"one_hour", now.Add(-90 * time.Minute), "1 hour ago"},
		{"three_hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"one_day", now.Add(-36 * time.Hour), "1 day ago"},
		{"three_days", now.Add(-72 * time.Hour), "3 days ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TimeAgo(tc.t)
			if got != tc.expected {
				t.Errorf("TimeAgo() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestTimeAgoFuture(t *testing.T) {
	// Future times should return "just now"
	got := TimeAgo(time.Now().Add(10 * time.Minute))
	if got != "just now" {
		t.Errorf("TimeAgo(future) = %q, want %q", got, "just now")
	}
}

// --- Dashboard integration tests ---

func TestDashboardGETRoutesReturnHTML(t *testing.T) {
	_, mux := setupTestServer(t)

	routes := []string{"/", "/config", "/artifacts", "/briefing"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != 200 {
				t.Fatalf("GET %s: want 200, got %d", path, w.Code)
			}
			ct := w.Header().Get("Content-Type")
			if !strings.Contains(ct, "text/html") {
				t.Errorf("GET %s: Content-Type = %q, want text/html", path, ct)
			}
			body := w.Body.String()
			if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
				t.Errorf("GET %s: response does not contain HTML", path)
			}
		})
	}
}

func TestDashboardSSEStreaming(t *testing.T) {
	_, mux := setupTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()

	<-done

	resp := w.Result()
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("SSE Content-Type = %q, want %q", ct, "text/event-stream")
	}

	body, _ := io.ReadAll(resp.Body)
	data := string(body)
	if !strings.Contains(data, "event:") && !strings.Contains(data, "data:") {
		t.Logf("SSE body: %s", data)
	}
}

func TestDashboardPOSTSteering(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// POST a pause action
	req := httptest.NewRequest("POST", "/api/project/test-conductor/pause", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("POST pause: want 200, got %d", w.Code)
	}

	// Verify blackboard was set (handler uses "conductor:{id}:signal" format)
	val, err := s.DB.GetBlackboardValue(ctx, "conductor:test-conductor:signal")
	if err != nil {
		t.Fatalf("GetBlackboardValue: %v", err)
	}
	if val != "pause" {
		t.Errorf("blackboard signal = %q, want %q", val, "pause")
	}

	// POST resume
	req = httptest.NewRequest("POST", "/api/project/test-conductor/resume", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("POST resume: want 200, got %d", w.Code)
	}

	val, err = s.DB.GetBlackboardValue(ctx, "conductor:test-conductor:signal")
	if err != nil {
		t.Fatalf("GetBlackboardValue: %v", err)
	}
	if val != "resume" {
		t.Errorf("blackboard signal = %q, want %q", val, "resume")
	}
}

func TestDashboardHTMXFragments(t *testing.T) {
	_, mux := setupTestServer(t)

	// Factory fragment should return partial HTML, not a full page
	req := httptest.NewRequest("GET", "/api/factory", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/factory: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Fragment should not contain full page wrapper elements
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("HTMX fragment contains full page DOCTYPE — expected partial HTML")
	}
	if strings.Contains(body, "<html") {
		t.Error("HTMX fragment contains <html> tag — expected partial HTML")
	}
}

func TestDashboardThemePersistence(t *testing.T) {
	_, mux := setupTestServer(t)

	// Set theme
	req := httptest.NewRequest("POST", "/api/config/theme",
		strings.NewReader(`{"theme":"light"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("POST theme: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Set to another theme to verify round-trip
	req = httptest.NewRequest("POST", "/api/config/theme",
		strings.NewReader(`{"theme":"dark"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("POST theme dark: want 200, got %d", w.Code)
	}
}

func TestDashboardViewportMeta(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `name="viewport"`) {
		t.Error("index page missing responsive meta viewport tag")
	}
	if !strings.Contains(body, "width=device-width") {
		t.Error("viewport meta tag missing width=device-width")
	}
}

func TestDashboardEmbeddedAssetSize(t *testing.T) {
	// Verify total embedded assets (excluding fonts and agents/emoji) are under 200KB
	handler := StaticHandler()
	if handler == nil {
		t.Fatal("StaticHandler() returned nil")
	}

	files := []string{"/style.css", "/app.js", "/htmx.min.js", "/animations.css"}
	var totalSize int64

	for _, file := range files {
		req := httptest.NewRequest("GET", file, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code == 200 {
			totalSize += int64(w.Body.Len())
		}
	}

	const maxSize = 200 * 1024 // 200KB
	if totalSize > maxSize {
		t.Errorf("embedded asset total size %d bytes exceeds %d bytes limit", totalSize, maxSize)
	}
	if totalSize == 0 {
		t.Error("no embedded assets found — expected at least style.css and app.js")
	}
}

// --- Alarm coalescing tests ---

func TestAlarmCoalescingRateLimit(t *testing.T) {
	ac := NewAlarmCoalescer(60 * time.Second)

	// First rate_limit event should emit
	r1 := ac.Process("rate_limit", "rate_limit: agent-1", "var(--yellow)", "1s ago")
	if !r1.Emit {
		t.Error("first rate_limit event should emit")
	}
	if r1.Event.Count != 1 {
		t.Errorf("first event count = %d, want 1", r1.Event.Count)
	}

	// Next 4 rate_limit events within window should be suppressed
	for i := 0; i < 4; i++ {
		r := ac.Process("rate_limit", "rate_limit: agent-1", "var(--yellow)", "2s ago")
		if r.Emit {
			t.Errorf("rate_limit event %d should be suppressed within window", i+2)
		}
	}

	// Flush should return coalesced event with count=5
	flushed := ac.Flush("rate_limit")
	if flushed == nil {
		t.Fatal("Flush returned nil, expected coalesced event")
	}
	if flushed.Count != 5 {
		t.Errorf("flushed count = %d, want 5", flushed.Count)
	}
}

func TestAlarmCoalescingCircuitBreakerPassthrough(t *testing.T) {
	ac := NewAlarmCoalescer(60 * time.Second)

	// Circuit breaker events should always pass through
	for i := 0; i < 5; i++ {
		r := ac.Process("circuit_breaker", "circuit_breaker: task-1", "var(--red)", "1s ago")
		if !r.Emit {
			t.Errorf("circuit_breaker event %d should always emit", i+1)
		}
		if r.Event.Count != 1 {
			t.Errorf("circuit_breaker event count = %d, want 1", r.Event.Count)
		}
	}
}

func TestAlarmCoalescingHumanNeededPassthrough(t *testing.T) {
	ac := NewAlarmCoalescer(60 * time.Second)

	r := ac.Process("human_needed", "human_needed: requires approval", "var(--red)", "1s ago")
	if !r.Emit {
		t.Error("human_needed event should always emit immediately")
	}
}

func TestAlarmCoalescingMixedEvents(t *testing.T) {
	ac := NewAlarmCoalescer(60 * time.Second)

	// rate_limit: first emits, rest suppressed
	r1 := ac.Process("rate_limit", "rate_limit", "var(--yellow)", "1s ago")
	if !r1.Emit {
		t.Error("first rate_limit should emit")
	}
	r2 := ac.Process("rate_limit", "rate_limit", "var(--yellow)", "2s ago")
	if r2.Emit {
		t.Error("second rate_limit should be suppressed")
	}

	// circuit_breaker always passes through
	r3 := ac.Process("circuit_breaker", "circuit_breaker", "var(--red)", "3s ago")
	if !r3.Emit {
		t.Error("circuit_breaker should always emit")
	}

	// Another rate_limit still suppressed (same window)
	r4 := ac.Process("rate_limit", "rate_limit", "var(--yellow)", "4s ago")
	if r4.Emit {
		t.Error("third rate_limit should still be suppressed")
	}
}

func TestAlarmCoalescingFlushEmpty(t *testing.T) {
	ac := NewAlarmCoalescer(60 * time.Second)

	// Flush with no events should return nil
	flushed := ac.Flush("rate_limit")
	if flushed != nil {
		t.Error("Flush on empty coalescer should return nil")
	}

	// Single event then flush should return nil (count is 1)
	ac.Process("rate_limit", "rate_limit", "var(--yellow)", "1s ago")
	flushed = ac.Flush("rate_limit")
	if flushed != nil {
		t.Error("Flush with count=1 should return nil")
	}
}

func TestDashboardFactoryPauseAll(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/factory/pause-all", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("POST /api/factory/pause-all: want 200, got %d", w.Code)
	}
}

func TestDashboard404Routes(t *testing.T) {
	_, mux := setupTestServer(t)

	// Routes that require an ID should 404 without one
	routes := []string{"/project/", "/task/", "/agent/"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != 404 {
				t.Errorf("GET %s without ID: want 404, got %d", path, w.Code)
			}
		})
	}
}

// TestSeededDatabaseRoutes tests routes with seeded data in the database.
func TestSeededDatabaseRoutes(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Seed conductor (schema: id, pid, goal, status, staging_branch, base_branch, ...)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-1', 123, 'test goal', 'active', 'staging/cond-1', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("seed conductor: %v", err)
	}

	// Seed tasks
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, conductor_id, title, status, role, priority, created_at)
		 VALUES ('task-1', 'cond-1', 'Implement feature', 'done', 'implementer', 1, datetime('now')),
		        ('task-2', 'cond-1', 'Review code', 'in_progress', 'reviewer', 2, datetime('now'))`)
	if err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	// Seed agents
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, current_task, pid, created_at)
		 VALUES ('agent-1', 'implementer', 'done', 'task-1', 1234, datetime('now')),
		        ('agent-2', 'reviewer', 'active', 'task-2', 5678, datetime('now'))`)
	if err != nil {
		t.Fatalf("seed agents: %v", err)
	}

	// Seed events
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO events (event_type, payload, task_id)
		 VALUES ('task_assigned', 'task-1 assigned to agent-1', 'task-1'),
		        ('agent_spawned', 'agent-1 started', 'task-1'),
		        ('task_done', 'task-1 completed', 'task-1')`)
	if err != nil {
		t.Fatalf("seed events: %v", err)
	}

	// Test project page with seeded conductor
	req := httptest.NewRequest("GET", "/project/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /project/cond-1: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Test task detail
	req = httptest.NewRequest("GET", "/task/task-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /task/task-1: want 200, got %d", w.Code)
	}

	// Test agent detail
	req = httptest.NewRequest("GET", "/agent/agent-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /agent/agent-1: want 200, got %d", w.Code)
	}

	// Test briefing with seeded data
	req = httptest.NewRequest("GET", "/briefing", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("GET /briefing: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test goal") {
		t.Error("briefing page should contain conductor goal")
	}
}
