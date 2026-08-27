package quality

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE ship_decisions (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			project_path TEXT,
			branch TEXT,
			decision TEXT,
			reason TEXT,
			quality_report TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_ship_decisions_session ON ship_decisions(session_id);
		CREATE INDEX idx_ship_decisions_project ON ship_decisions(project_path);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// mockGate implements Gate for testing.
type mockGate struct {
	name   string
	result *LayerResult
	err    error
}

func (g *mockGate) Name() string { return g.name }
func (g *mockGate) Check(_ context.Context, _ string, _ string) (*LayerResult, error) {
	return g.result, g.err
}

func TestGateStack_AllPass(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	stack := &GateStack{
		Gates: []Gate{
			&mockGate{"build", &LayerResult{Layer: "build", Passed: true, Score: 1.0}, nil},
			&mockGate{"lint", &LayerResult{Layer: "lint", Passed: true, Score: 1.0}, nil},
			&mockGate{"security", &LayerResult{Layer: "security", Passed: true, Score: 1.0}, nil},
			&mockGate{"test", &LayerResult{Layer: "test", Passed: true, Score: 0.85}, nil},
			&mockGate{"coverage-ratchet", &LayerResult{Layer: "coverage-ratchet", Passed: true, Score: 0.85}, nil},
			&mockGate{"mutation", &LayerResult{Layer: "mutation", Passed: true, Score: 0.85}, nil},
			&mockGate{"review", &LayerResult{Layer: "review", Passed: true, Score: 1.0}, nil},
			&mockGate{"eval-judge", &LayerResult{Layer: "eval-judge", Passed: true, Score: 0.8}, nil},
		},
		DB:     db,
		Logger: testLogger(),
	}

	report, err := stack.Run(context.Background(), "/tmp/test", "feature/x", "test-session")
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != PROMOTE {
		t.Errorf("expected PROMOTE, got %s: %s", report.Decision, report.Reason)
	}
}

func TestGateStack_SecurityFailRollback(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	stack := &GateStack{
		Gates: []Gate{
			&mockGate{"build", &LayerResult{Layer: "build", Passed: true, Score: 1.0}, nil},
			&mockGate{"security", &LayerResult{Layer: "security", Passed: false, Score: 0.0, Details: "CRITICAL vuln"}, nil},
			&mockGate{"test", &LayerResult{Layer: "test", Passed: true, Score: 0.9}, nil},
		},
		DB:     db,
		Logger: testLogger(),
	}

	report, err := stack.Run(context.Background(), "/tmp/test", "feature/x", "test-session")
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != ROLLBACK {
		t.Errorf("expected ROLLBACK, got %s", report.Decision)
	}
	// Should short-circuit — test gate should not have run.
	if len(report.Results) != 2 {
		t.Errorf("expected 2 results (short-circuit), got %d", len(report.Results))
	}
}

func TestGateStack_LowMutationRollback(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	stack := &GateStack{
		Gates: []Gate{
			&mockGate{"build", &LayerResult{Layer: "build", Passed: true, Score: 1.0}, nil},
			&mockGate{"security", &LayerResult{Layer: "security", Passed: true, Score: 1.0}, nil},
			&mockGate{"mutation", &LayerResult{Layer: "mutation", Passed: true, Score: 0.40}, nil},
			&mockGate{"eval-judge", &LayerResult{Layer: "eval-judge", Passed: true, Score: 0.8}, nil},
		},
		DB:     db,
		Logger: testLogger(),
	}

	report, err := stack.Run(context.Background(), "/tmp/test", "feature/x", "test-session")
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != ROLLBACK {
		t.Errorf("expected ROLLBACK (mutation < 0.50), got %s: %s", report.Decision, report.Reason)
	}
}

func TestGateStack_JudgeHold(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	stack := &GateStack{
		Gates: []Gate{
			&mockGate{"build", &LayerResult{Layer: "build", Passed: true, Score: 1.0}, nil},
			&mockGate{"security", &LayerResult{Layer: "security", Passed: true, Score: 1.0}, nil},
			&mockGate{"mutation", &LayerResult{Layer: "mutation", Passed: true, Score: 0.85}, nil},
			&mockGate{"review", &LayerResult{Layer: "review", Passed: true, Score: 1.0}, nil},
			&mockGate{"eval-judge", &LayerResult{Layer: "eval-judge", Passed: true, Score: 0.55}, nil},
		},
		DB:     db,
		Logger: testLogger(),
	}

	report, err := stack.Run(context.Background(), "/tmp/test", "feature/x", "test-session")
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != HOLD {
		t.Errorf("expected HOLD (judge 0.55), got %s: %s", report.Decision, report.Reason)
	}
}

func TestGateStack_StoresDecision(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	stack := &GateStack{
		Gates: []Gate{
			&mockGate{"build", &LayerResult{Layer: "build", Passed: true, Score: 1.0}, nil},
		},
		DB:     db,
		Logger: testLogger(),
	}

	_, err := stack.Run(context.Background(), "/tmp/test", "feature/x", "session-abc")
	if err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM ship_decisions WHERE session_id='session-abc'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 stored decision, got %d", count)
	}
}

func TestDecide_PromoteAllConditions(t *testing.T) {
	t.Parallel()
	s := &GateStack{Logger: testLogger()}
	decision, _ := s.decide(true, 0.85, RiskLow, PROMOTE)
	if decision != PROMOTE {
		t.Errorf("expected PROMOTE, got %s", decision)
	}
}

func TestDecide_HoldMediumMutation(t *testing.T) {
	t.Parallel()
	s := &GateStack{Logger: testLogger()}
	decision, _ := s.decide(true, 0.65, RiskLow, PROMOTE)
	if decision != HOLD {
		t.Errorf("expected HOLD (mutation between 0.50-0.80), got %s", decision)
	}
}

func TestClassifyReviewRisk(t *testing.T) {
	tests := []struct {
		score float64
		want  RiskScore
	}{
		{1.0, RiskLow},
		{0.7, RiskMedium},
		{0.3, RiskHigh},
		{0.0, RiskCritical},
	}
	for _, tt := range tests {
		got := classifyReviewRisk(tt.score)
		if got != tt.want {
			t.Errorf("classifyReviewRisk(%v) = %s, want %s", tt.score, got, tt.want)
		}
	}
}
