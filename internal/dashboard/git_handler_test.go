package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleGitNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/git/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /git/ with no ID: want 404, got %d", w.Code)
	}
}

func TestHandleGitNotFound(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/git/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("GET /git/nonexistent: want 404, got %d", w.Code)
	}
}

func TestHandleGitWithConductor(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Insert a conductor
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-1', 123, 'test goal', 'active', 'staging/cond-1', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert conductor: %v", err)
	}

	// Insert tasks for this conductor
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, role, conductor_id, branch, feature_cluster, created_at)
		 VALUES ('t-1', 'Task One', 'in_progress', 3, 'implementer', 'cond-1', 'feature/t-1', 'backend', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, role, conductor_id, branch, created_at)
		 VALUES ('t-2', 'Task Two', 'done', 2, 'reviewer', 'cond-1', 'feature/t-2', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert task 2: %v", err)
	}

	req := httptest.NewRequest("GET", "/git/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /git/cond-1: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()

	// Verify key elements are present
	checks := []string{
		"cond-1",
		"staging/cond-1",
		"main",
		"Branch Flow",
		"File Locks",
		"Merge Queue",
		"t-1",
		"t-2",
		"backend", // cluster name
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("GET /git/cond-1: body missing %q", check)
		}
	}

	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type: want text/html, got %s", w.Header().Get("Content-Type"))
	}
}

func TestHandleGitWithMergeQueue(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Insert conductor
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, merge_status, started_at, heartbeat_at)
		 VALUES ('cond-2', 456, 'merge test', 'active', 'staging/cond-2', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', 'merging', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert conductor: %v", err)
	}

	// Insert a task
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, role, conductor_id, branch, created_at)
		 VALUES ('t-mq', 'MQ Task', 'done', 3, 'implementer', 'cond-2', 'feature/t-mq', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	// Insert merge queue entry
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO merge_queue_entries (id, conductor_id, task_id, branch, position, status, resolution_tier, conflict_files, refinement_count, enqueued_at)
		 VALUES ('mq-1', 'cond-2', 't-mq', 'feature/t-mq', 1, 'merging', '2', 'main.go', 0, datetime('now'))`)
	if err != nil {
		t.Fatalf("insert merge queue entry: %v", err)
	}

	req := httptest.NewRequest("GET", "/git/cond-2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /git/cond-2: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()

	checks := []string{
		"merging",      // staging status + mq status
		"feature/t-mq", // branch in merge queue
		"#1",           // position
		"tier 2",       // resolution tier
		"conflicts",    // conflict indicator
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("GET /git/cond-2: body missing %q", check)
		}
	}
}

func TestHandleGitWithFileLocks(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Insert conductor
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-3', 789, 'lock test', 'active', 'staging/cond-3', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert conductor: %v", err)
	}

	// Insert agent and task to satisfy FK constraints on file_locks
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, created_at) VALUES ('agent-1', 'implementer', 'active', datetime('now'))`)
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, role, conductor_id, created_at) VALUES ('t-lock', 'Lock Task', 'in_progress', 3, 'implementer', 'cond-3', datetime('now'))`)

	// Insert file lock
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO file_locks (file_path, locked_by, task_id, locked_at)
		 VALUES ('internal/main.go', 'agent-1', 't-lock', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert file lock: %v", err)
	}

	req := httptest.NewRequest("GET", "/git/cond-3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /git/cond-3: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "internal/main.go") {
		t.Error("body missing file lock path")
	}
	if !strings.Contains(body, "agent-1") {
		t.Error("body missing lock agent")
	}
}

func TestResolutionTierClass(t *testing.T) {
	tests := []struct {
		tier string
		want string
	}{
		{"1", "auto"},
		{"2", "auto"},
		{"3", "auto"},
		{"import", "auto"},
		{"non-overlapping", "auto"},
		{"trivial", "auto"},
		{"4", "llm"},
		{"llm", "llm"},
		{"", "failed"},
		{"unknown", "failed"},
	}
	for _, tc := range tests {
		got := resolutionTierClass(tc.tier)
		if got != tc.want {
			t.Errorf("resolutionTierClass(%q) = %q, want %q", tc.tier, got, tc.want)
		}
	}
}

func TestHandleGitHTMXLinks(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-4', 111, 'htmx test', 'active', 'staging/cond-4', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert conductor: %v", err)
	}

	// Insert agent to satisfy FK on assigned_to
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, created_at) VALUES ('agent-42', 'implementer', 'active', datetime('now'))`)
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, role, conductor_id, branch, assigned_to, created_at)
		 VALUES ('t-htmx', 'HTMX Task', 'in_progress', 3, 'implementer', 'cond-4', 'feature/t-htmx', 'agent-42', datetime('now'))`)

	req := httptest.NewRequest("GET", "/git/cond-4", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Branch lane should have hx-get for agent detail (check both quote styles)
	if !strings.Contains(body, `/agent/agent-42`) {
		t.Errorf("missing HTMX link to agent detail on branch lane; body: %s", body[:min(len(body), 2000)])
	}
}
