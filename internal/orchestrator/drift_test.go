package orchestrator

import (
	"context"
	"fmt"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupDriftTest(t *testing.T) (*Conductor, *MockRunner, context.Context) {
	t.Helper()
	testDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := testDB.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { testDB.Close() })

	runner := &MockRunner{}
	c := &Conductor{
		DB:            testDB,
		Runner:        runner,
		RepoRoot:      t.TempDir(),
		SessionID:     "test-session",
		DisableNotify: true,
		Log:           func(s string) { t.Logf("conductor: %s", s) },
	}
	return c, runner, context.Background()
}

func TestCheckDrift_NoImmutableGoal(t *testing.T) {
	c, _, ctx := setupDriftTest(t)

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 (fail-open), got %.2f", result.Score)
	}
	if result.Action != "none" {
		t.Errorf("expected action 'none', got %q", result.Action)
	}
}

func TestCheckDrift_NoSummaries(t *testing.T) {
	c, _, ctx := setupDriftTest(t)

	// Set immutable goal
	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 (no summaries), got %.2f", result.Score)
	}
}

func TestCheckDrift_LLMError_FailOpen(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	// Set goal and summary
	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Implemented GET /users endpoint", "agent-1")

	// Make LLM fail
	runner.Errors = []error{fmt.Errorf("rate limit exceeded")}
	runner.Outputs = []string{""}

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 (fail-open on LLM error), got %.2f", result.Score)
	}

	// Verify score was recorded in DB
	history, err := c.DB.GetDriftHistory(ctx, c.SessionID)
	if err != nil {
		t.Fatalf("getting drift history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 drift score recorded, got %d", len(history))
	}
	if history[0].Score != 1.0 {
		t.Errorf("recorded score should be 1.0, got %.2f", history[0].Score)
	}
}

func TestCheckDrift_GoodAlignment(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Implemented GET /users endpoint", "agent-1")

	runner.Outputs = []string{`{"score": 0.95, "explanation": "Work directly serves the API goal"}`}

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 0.95 {
		t.Errorf("expected score 0.95, got %.2f", result.Score)
	}
	if result.Action != "none" {
		t.Errorf("expected action 'none', got %q", result.Action)
	}
}

func TestCheckDrift_MildDrift_LogsAlert(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Refactored logging module", "agent-1")

	runner.Outputs = []string{`{"score": 0.7, "explanation": "Some tangential refactoring"}`}

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 0.7 {
		t.Errorf("expected score 0.7, got %.2f", result.Score)
	}
	if result.Action != "alert" {
		t.Errorf("expected action 'alert', got %q", result.Action)
	}
}

func TestCheckDrift_SignificantDrift_Pauses(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Rewrote the CSS framework", "agent-1")

	runner.Outputs = []string{`{"score": 0.5, "explanation": "Work diverged into unrelated CSS changes"}`}

	result, err := c.CheckDrift(ctx, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 0.5 {
		t.Errorf("expected score 0.5, got %.2f", result.Score)
	}
	if result.Action != "pause" {
		t.Errorf("expected action 'pause', got %q", result.Action)
	}

	// Verify blackboard was set to pause
	pause := c.getBlackboard(ctx, "auto:pause")
	if pause != "drift_detected" {
		t.Errorf("expected auto:pause=drift_detected, got %q", pause)
	}
}

func TestCheckDrift_CriticalDrift_Stops(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Deleted the database", "agent-1")

	runner.Outputs = []string{`{"score": 0.2, "explanation": "Work contradicts the goal entirely"}`}

	result, err := c.CheckDrift(ctx, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 0.2 {
		t.Errorf("expected score 0.2, got %.2f", result.Score)
	}
	if result.Action != "stop" {
		t.Errorf("expected action 'stop', got %q", result.Action)
	}

	// Verify blackboard was set to stop
	stop := c.getBlackboard(ctx, "auto:stop")
	if stop != "drift_critical" {
		t.Errorf("expected auto:stop=drift_critical, got %q", stop)
	}
}

func TestCheckDrift_ParseError_FailOpen(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Did some work", "agent-1")

	runner.Outputs = []string{"This is not valid JSON at all"}

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 (fail-open on parse error), got %.2f", result.Score)
	}
}

func TestCheckDrift_JSONInMarkdown(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Implemented endpoint", "agent-1")

	// LLM wraps JSON in markdown code block
	runner.Outputs = []string{"```json\n{\"score\": 0.85, \"explanation\": \"Mostly aligned\"}\n```"}

	result, err := c.CheckDrift(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Score != 0.85 {
		t.Errorf("expected score 0.85, got %.2f", result.Score)
	}
}

func TestCheckDrift_RecordsToDB(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Implemented endpoint", "agent-1")

	runner.Outputs = []string{`{"score": 0.9, "explanation": "Good alignment"}`}

	_, err := c.CheckDrift(ctx, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history, err := c.DB.GetDriftHistory(ctx, c.SessionID)
	if err != nil {
		t.Fatalf("getting drift history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 drift score, got %d", len(history))
	}
	if history[0].CycleNumber != 5 {
		t.Errorf("expected cycle 5, got %d", history[0].CycleNumber)
	}
	if history[0].Score != 0.9 {
		t.Errorf("expected score 0.9, got %.2f", history[0].Score)
	}
	if !history[0].Explanation.Valid || history[0].Explanation.String != "Good alignment" {
		t.Errorf("expected explanation 'Good alignment', got %v", history[0].Explanation)
	}
}

func TestCheckDrift_UsesNormalizedModel(t *testing.T) {
	c, runner, ctx := setupDriftTest(t)

	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	c.DB.SetBlackboard(ctx, goalKey, "Build a REST API", "conductor")
	c.DB.SetBlackboard(ctx, "result_summary:task-1", "Did work", "agent-1")

	runner.Outputs = []string{`{"score": 0.9, "explanation": "aligned"}`}

	c.CheckDrift(ctx, 1)

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(runner.Calls))
	}
	// NormalizeModelName("opus") should produce the full model ID
	expectedModel := "claude-opus-4-6[1m]"
	if runner.Calls[0].Model != expectedModel {
		t.Errorf("expected model %q, got %q", expectedModel, runner.Calls[0].Model)
	}
}

func TestSetBlackboardOnce_ImmutableGoal(t *testing.T) {
	testDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := testDB.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { testDB.Close() })
	ctx := context.Background()

	// First write should succeed
	inserted, err := testDB.SetBlackboardOnce(ctx, "immutable_goal:test-session", "original goal", "conductor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inserted {
		t.Error("expected first write to insert")
	}

	// Second write should be ignored
	inserted, err = testDB.SetBlackboardOnce(ctx, "immutable_goal:test-session", "different goal", "conductor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inserted {
		t.Error("expected second write to be ignored")
	}

	// Verify original value is preserved
	val, err := testDB.GetBlackboardValue(ctx, "immutable_goal:test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "original goal" {
		t.Errorf("expected 'original goal', got %q", val)
	}
}

