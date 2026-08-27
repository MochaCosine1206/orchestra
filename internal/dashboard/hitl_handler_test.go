package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// --- Plan Approval ---

func TestHITLPlanApprovalGET(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Seed blackboard with plan data
	plan := `[{"title":"Implement feature X","role":"implementer"},{"title":"Review feature X","role":"reviewer"}]`
	s.DB.SetBlackboard(ctx, "hitl:plan_approval:cond-1", plan, "test")

	req := httptest.NewRequest("GET", "/api/hitl/plan/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/hitl/plan/cond-1: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Plan Approval") {
		t.Error("response should contain Plan Approval heading")
	}
	if !strings.Contains(body, "Implement feature X") {
		t.Error("response should contain task title from plan")
	}
}

func TestHITLPlanApprovalPOSTApprove(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:plan_approval:cond-1", `[{"title":"task1"}]`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/plan/cond-1", strings.NewReader(`{"action":"approve"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST approve: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["action"] != "approved" {
		t.Errorf("expected action=approved, got %s", resp["action"])
	}

	// Verify blackboard state: plan_approval deleted, plan_approved set
	val, _ := s.DB.GetBlackboardValue(ctx, "hitl:plan_approval:cond-1")
	if val != "" {
		t.Error("plan_approval key should be deleted after approve")
	}
	val, _ = s.DB.GetBlackboardValue(ctx, "hitl:plan_approved:cond-1")
	if val != "approved" {
		t.Errorf("plan_approved should be 'approved', got %q", val)
	}
}

func TestHITLPlanApprovalPOSTReject(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:plan_approval:cond-1", `[{"title":"task1"}]`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/plan/cond-1", strings.NewReader(`{"action":"reject","notes":"too broad"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST reject: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "hitl:plan_rejected:cond-1")
	if val != "too broad" {
		t.Errorf("plan_rejected should contain notes, got %q", val)
	}
}

// --- Clarification ---

func TestHITLClarifyGET(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	clarify := `{"question":"Which database backend?","options":["PostgreSQL","SQLite","MySQL"]}`
	s.DB.SetBlackboard(ctx, "hitl:clarify:cond-2", clarify, "test")

	req := httptest.NewRequest("GET", "/api/hitl/clarify/cond-2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET clarify: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Which database backend?") {
		t.Error("response should contain question text")
	}
	if !strings.Contains(body, "SQLite") {
		t.Error("response should contain option buttons")
	}
}

func TestHITLClarifyPOST(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:clarify:cond-2", `{"question":"pick one","options":["A","B"]}`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/clarify/cond-2", strings.NewReader(`{"response":"SQLite"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST clarify: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "hitl:clarify_response:cond-2")
	if val != "SQLite" {
		t.Errorf("clarify_response should be 'SQLite', got %q", val)
	}

	// Original clarify key should be cleaned up
	val, _ = s.DB.GetBlackboardValue(ctx, "hitl:clarify:cond-2")
	if val != "" {
		t.Error("clarify key should be deleted after response")
	}
}

// --- Merge Approval ---

func TestHITLMergeGET(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	merge := `{"summary":"Added 3 files, modified 2","diff":"+ new line\n- old line"}`
	s.DB.SetBlackboard(ctx, "hitl:merge_approval:task-1", merge, "test")

	req := httptest.NewRequest("GET", "/api/hitl/merge/task-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET merge: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Merge Approval") {
		t.Error("response should contain Merge Approval heading")
	}
	if !strings.Contains(body, "Added 3 files") {
		t.Error("response should contain merge summary")
	}
}

func TestHITLMergePOSTApprove(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:merge_approval:task-1", `{"summary":"ok"}`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/merge/task-1", strings.NewReader(`{"action":"approve"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST merge approve: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "hitl:merge_approved:task-1")
	if val != "approved" {
		t.Errorf("merge_approved should be 'approved', got %q", val)
	}
}

// --- Failure Triage ---

func TestHITLFailureGET(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Create a failed task in DB
	s.DB.CreateTask(ctx, taskForTest("fail-task-1", "Build widget", "failed"))
	s.DB.SetBlackboard(ctx, "failure_type:fail-task-1", "normal_failure", "test")
	s.DB.SetBlackboard(ctx, "retry:fail-task-1", "2", "test")

	req := httptest.NewRequest("GET", "/api/hitl/failure/fail-task-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET failure: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Failure Triage") {
		t.Error("response should contain Failure Triage heading")
	}
	if !strings.Contains(body, "normal_failure") {
		t.Error("response should contain classification")
	}
}

func TestHITLFailurePOSTRetry(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.CreateTask(ctx, taskForTest("fail-task-2", "Build widget", "failed"))

	req := httptest.NewRequest("POST", "/api/hitl/failure/fail-task-2", strings.NewReader(`{"action":"retry"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST failure retry: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:retry:fail-task-2")
	if val != "requested" {
		t.Errorf("auto:retry should be 'requested', got %q", val)
	}
}

// --- Priority Override ---

func TestHITLPriorityPOST(t *testing.T) {
	_, mux := setupTestServer(t)

	payload := `{"task_order":["task-3","task-1","task-2"]}`
	req := httptest.NewRequest("POST", "/api/hitl/priority/cond-3", strings.NewReader(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST priority: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != "3" {
		t.Errorf("expected count=3, got %s", resp["count"])
	}
}

// --- Emergency Stop ---

func TestEmergencyStopPOST(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/factory/emergency-stop", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST emergency-stop: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["action"] != "emergency-stop" {
		t.Errorf("expected action=emergency-stop, got %s", resp["action"])
	}
}

func TestEmergencyStopGETNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/factory/emergency-stop", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET emergency-stop: want 405, got %d", w.Code)
	}
}

// --- Missing ID tests ---

func TestHITLPlanNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/plan/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("GET /api/hitl/plan/ with no ID: want 404, got %d", w.Code)
	}
}

// --- Failure Triage: skip and replan actions ---

func TestHITLFailurePOSTSkip(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.CreateTask(ctx, taskForTest("fail-task-skip", "Broken widget", "failed"))

	req := httptest.NewRequest("POST", "/api/hitl/failure/fail-task-skip", strings.NewReader(`{"action":"skip"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST failure skip: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:skip:fail-task-skip")
	if val != "requested" {
		t.Errorf("auto:skip should be 'requested', got %q", val)
	}
}

func TestHITLFailurePOSTReplan(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Insert conductor first to satisfy FK
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES ('cond-replan', 123, 'test', 'active', 'staging/x', 'main', 3, 'all-opus', 'claude-code', 'none', 'local', 'fifo', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("seed conductor: %v", err)
	}
	// Insert task with conductor_id
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, conductor_id, created_at)
		 VALUES ('fail-task-replan', 'Broken widget', 'failed', 'implementer', 'cond-replan', datetime('now'))`)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/hitl/failure/fail-task-replan", strings.NewReader(`{"action":"replan"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST failure replan: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "auto:replan")
	if val != "cond-replan" {
		t.Errorf("auto:replan should be 'cond-replan', got %q", val)
	}
}

func TestHITLFailurePOSTInvalidAction(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.CreateTask(ctx, taskForTest("fail-task-bad", "Widget", "failed"))

	req := httptest.NewRequest("POST", "/api/hitl/failure/fail-task-bad", strings.NewReader(`{"action":"invalid"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("POST failure invalid action: want 400, got %d", w.Code)
	}
}

func TestHITLFailurePOSTBadJSON(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/hitl/failure/fail-task-x", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("POST failure bad JSON: want 400, got %d", w.Code)
	}
}

func TestHITLFailureMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("PUT", "/api/hitl/failure/fail-task-x", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("PUT failure: want 405, got %d", w.Code)
	}
}

func TestHITLFailureNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/failure/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("GET /api/hitl/failure/ no ID: want 404, got %d", w.Code)
	}
}

// --- Merge Approval: reject ---

func TestHITLMergePOSTReject(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:merge_approval:task-reject", `{"summary":"test"}`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/merge/task-reject", strings.NewReader(`{"action":"reject","notes":"bad code"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("POST merge reject: want 200, got %d", w.Code)
	}

	val, _ := s.DB.GetBlackboardValue(ctx, "hitl:merge_rejected:task-reject")
	if val != "bad code" {
		t.Errorf("merge_rejected should contain notes, got %q", val)
	}
}

func TestHITLMergeMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("PUT", "/api/hitl/merge/task-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("PUT merge: want 405, got %d", w.Code)
	}
}

func TestHITLMergeNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/merge/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("GET /api/hitl/merge/ no ID: want 404, got %d", w.Code)
	}
}

// --- Clarification edge cases ---

func TestHITLClarifyNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/clarify/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("GET /api/hitl/clarify/ no ID: want 404, got %d", w.Code)
	}
}

func TestHITLClarifyMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("PUT", "/api/hitl/clarify/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("PUT clarify: want 405, got %d", w.Code)
	}
}

// --- Priority edge cases ---

func TestHITLPriorityNoID(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/hitl/priority/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("POST /api/hitl/priority/ no ID: want 404, got %d", w.Code)
	}
}

func TestHITLPriorityMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/priority/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("GET priority: want 405, got %d", w.Code)
	}
}

func TestHITLPriorityBadJSON(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/hitl/priority/cond-1", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("POST priority bad JSON: want 400, got %d", w.Code)
	}
}

// --- Plan Approval edge cases ---

func TestHITLPlanApprovalGETNoPlan(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/hitl/plan/cond-no-plan", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET plan no plan: want 200, got %d", w.Code)
	}
	// Should render the "no plan" state
	body := w.Body.String()
	if strings.Contains(body, "Approve") {
		t.Error("should not show Approve button when no plan exists")
	}
}

func TestHITLPlanApprovalMethodNotAllowed(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("PUT", "/api/hitl/plan/cond-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("PUT plan: want 405, got %d", w.Code)
	}
}

func TestHITLPlanApprovalPOSTBadJSON(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	s.DB.SetBlackboard(ctx, "hitl:plan_approval:cond-bad", `[{"title":"task1"}]`, "test")

	req := httptest.NewRequest("POST", "/api/hitl/plan/cond-bad", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("POST plan bad JSON: want 400, got %d", w.Code)
	}
}

// --- extractFilesOwned tests ---

func TestExtractFilesOwned(t *testing.T) {
	// Array of strings
	task1 := map[string]interface{}{
		"files_owned": []interface{}{"file1.go", "file2.go", "file3.go"},
	}
	got := extractFilesOwned(task1)
	if got != "file1.go, file2.go, file3.go" {
		t.Errorf("extractFilesOwned(array) = %q, want %q", got, "file1.go, file2.go, file3.go")
	}

	// Single string
	task2 := map[string]interface{}{
		"files_owned": "single.go",
	}
	got = extractFilesOwned(task2)
	if got != "single.go" {
		t.Errorf("extractFilesOwned(string) = %q, want %q", got, "single.go")
	}

	// Missing key
	task3 := map[string]interface{}{"title": "test"}
	got = extractFilesOwned(task3)
	if got != "" {
		t.Errorf("extractFilesOwned(missing) = %q, want empty", got)
	}

	// Empty map
	got = extractFilesOwned(map[string]interface{}{})
	if got != "" {
		t.Errorf("extractFilesOwned(empty) = %q, want empty", got)
	}
}

// --- Helper ---

func taskForTest(id, title, status string) db.Task {
	return db.Task{
		ID:     id,
		Title:  title,
		Status: status,
		Role:   "implementer",
	}
}

// Ensure all responses are readable
func readBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	b, _ := io.ReadAll(w.Result().Body)
	return string(b)
}
