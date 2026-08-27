package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func createTestTask(t *testing.T, d *db.DB, id, title string) {
	t.Helper()
	ctx := context.Background()
	d.CreateTask(ctx, db.Task{
		ID:          id,
		Title:       title,
		Description: sql.NullString{String: "test description", Valid: true},
		Status:      "done",
		Role:        "implementer",
		Branch:      sql.NullString{String: "feature/" + id, Valid: true},
		Worktree:    sql.NullString{String: "/tmp/wt/" + id, Valid: true},
	})
}

func TestReview_Approved(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	runner := &MockRunner{
		Outputs: []string{`{"approved": true, "feedback": "Looks good"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})
	if !result.Approved {
		t.Error("expected approved")
	}
	if result.Skipped {
		t.Error("should not be skipped")
	}
	if result.Feedback != "Looks good" {
		t.Errorf("feedback = %q, want %q", result.Feedback, "Looks good")
	}
}

func TestReview_Rejected(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	runner := &MockRunner{
		Outputs: []string{`{"approved": false, "feedback": "Missing error handling"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})
	if result.Approved {
		t.Error("expected not approved")
	}
	if result.Feedback != "Missing error handling" {
		t.Errorf("feedback = %q", result.Feedback)
	}
}

func TestReview_Disabled(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: false})
	if !result.Approved {
		t.Error("disabled review should approve")
	}
	if !result.Skipped {
		t.Error("disabled review should be skipped")
	}
	if result.Reason != "disabled" {
		t.Errorf("reason = %q, want %q", result.Reason, "disabled")
	}
}

func TestReview_RunnerError_FailOpen(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	runner := &MockRunner{
		Errors: []error{errors.New("runner crashed")},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})
	if !result.Approved {
		t.Error("fail-open: runner error should approve")
	}
	if !result.Skipped {
		t.Error("runner error should be skipped")
	}
	if result.Reason != "error" {
		t.Errorf("reason = %q, want %q", result.Reason, "error")
	}
}

func TestReview_Timeout_FailOpen(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")

	// Runner that blocks longer than timeout
	slowRunner := &slowMockRunner{delay: 2 * time.Second}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: slowRunner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{
		TaskID:  "T-001",
		Enabled: true,
		Timeout: 100 * time.Millisecond,
	})
	if !result.Approved {
		t.Error("fail-open: timeout should approve")
	}
	if !result.Skipped {
		t.Error("timeout should be skipped")
	}
	if result.Reason != "timeout" {
		t.Errorf("reason = %q, want %q", result.Reason, "timeout")
	}
}

func TestReview_NoRunner_FailOpen(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})
	if !result.Approved {
		t.Error("fail-open: no runner should approve")
	}
	if !result.Skipped {
		t.Error("no runner should be skipped")
	}
	if result.Reason != "no_runner" {
		t.Errorf("reason = %q, want %q", result.Reason, "no_runner")
	}
}

func TestReview_TaskNotFound_FailOpen(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{Outputs: []string{`{"approved": true}`}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{TaskID: "T-nonexistent", Enabled: true})
	if !result.Approved {
		t.Error("fail-open: missing task should approve")
	}
	if !result.Skipped {
		t.Error("missing task should be skipped")
	}
}

func TestReview_StoresInBlackboard(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	runner := &MockRunner{
		Outputs: []string{`{"approved": true, "feedback": "LGTM"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})

	val, _ := d.GetBlackboardValue(ctx, "review:T-001")
	if val != "approved" {
		t.Errorf("blackboard review = %q, want %q", val, "approved")
	}
}

func TestReview_MultipleReviews(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "First task")
	createTestTask(t, d, "T-002", "Second task")
	runner := &MockRunner{
		Outputs: []string{
			`{"approved": true, "feedback": "Good"}`,
			`{"approved": false, "feedback": "Needs work"}`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	r1 := c.Review(ctx, ReviewOpts{TaskID: "T-001", Enabled: true})
	r2 := c.Review(ctx, ReviewOpts{TaskID: "T-002", Enabled: true})

	if !r1.Approved {
		t.Error("first review should be approved")
	}
	if r2.Approved {
		t.Error("second review should be rejected")
	}
}

func TestParseReviewOutput(t *testing.T) {
	tests := []struct {
		input    string
		approved bool
	}{
		{`{"approved": true, "feedback": "ok"}`, true},
		{`{"approved": false, "feedback": "bad"}`, false},
		{`{"approved":true}`, true},
		{`{"approved":false}`, false},
		{"LGTM, approved!", true},
		{"Changes needed, please fix", false},
		{"Rejected due to bugs", false},
	}
	for _, tt := range tests {
		result := parseReviewOutput(tt.input)
		if result.Approved != tt.approved {
			t.Errorf("parseReviewOutput(%q).Approved = %v, want %v", tt.input, result.Approved, tt.approved)
		}
	}
}

func TestReview_SpecDiffMode(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Add feature")
	ctx := context.Background()

	// Store a spec in blackboard
	d.SetBlackboard(ctx, "spec:T-001", "## Acceptance Criteria\n1. Must add --verbose flag\n2. Must pass tests", "test")

	runner := &MockRunner{
		Outputs: []string{`{"approved": true, "feedback": "All criteria met"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})

	result := c.Review(ctx, ReviewOpts{
		TaskID:  "T-001",
		Enabled: true,
		Mode:    ReviewModeSpecDiff,
	})
	if !result.Approved {
		t.Error("expected approved")
	}

	// Verify the prompt contains the spec content
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.Calls))
	}
	prompt := runner.Calls[0].Prompt
	if !strings.Contains(prompt, "spec-anchored") {
		t.Errorf("spec-diff mode should use spec-anchored prompt, got: %s", prompt[:100])
	}
	if !strings.Contains(prompt, "Must add --verbose flag") {
		t.Error("spec-diff mode should include spec content in prompt")
	}
}

func TestReview_SpecDiffMode_NoSpec_FallsBack(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Add feature")
	runner := &MockRunner{
		Outputs: []string{`{"approved": true, "feedback": "LGTM"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// No spec stored — should fall back to default prompt
	result := c.Review(ctx, ReviewOpts{
		TaskID:  "T-001",
		Enabled: true,
		Mode:    ReviewModeSpecDiff,
	})
	if !result.Approved {
		t.Error("expected approved")
	}

	// Should use default prompt (not spec-anchored)
	prompt := runner.Calls[0].Prompt
	if !strings.Contains(prompt, "code reviewer") {
		t.Errorf("fallback should use default prompt, got: %s", prompt[:100])
	}
}

func TestReview_StructuredMode(t *testing.T) {
	d := setupTestDB(t)
	createTestTask(t, d, "T-001", "Fix bug")
	runner := &MockRunner{
		Outputs: []string{`{
			"approved": false,
			"findings": [
				{"severity": "high", "confidence": 0.9, "file": "main.go", "line_start": 10, "line_end": 15, "category": "bug", "description": "Null pointer", "suggestion": "Add nil check"}
			],
			"feedback": "Found a bug"
		}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result := c.Review(ctx, ReviewOpts{
		TaskID:  "T-001",
		Enabled: true,
		Mode:    ReviewModeStructured,
	})
	if result.Approved {
		t.Error("expected not approved")
	}
	if result.Feedback != "Found a bug" {
		t.Errorf("feedback = %q, want %q", result.Feedback, "Found a bug")
	}

	// Verify structured prompt was used
	prompt := runner.Calls[0].Prompt
	if !strings.Contains(prompt, "structured code reviewer") {
		t.Errorf("structured mode should use structured prompt, got: %s", prompt[:100])
	}
}

func TestParseStructuredReviewOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		approved bool
		hasFB    bool
	}{
		{
			name:     "valid JSON approved",
			input:    `{"approved": true, "findings": [], "feedback": "Clean code"}`,
			approved: true,
			hasFB:    true,
		},
		{
			name:     "valid JSON rejected with findings",
			input:    `{"approved": false, "findings": [{"severity": "critical", "confidence": 0.95, "category": "security", "description": "SQL injection"}], "feedback": ""}`,
			approved: false,
			hasFB:    true, // generates from findings
		},
		{
			name:     "malformed JSON falls back",
			input:    `not valid json at all`,
			approved: true, // heuristic: no rejection keywords
			hasFB:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseStructuredReviewOutput(tt.input)
			if result.Approved != tt.approved {
				t.Errorf("approved = %v, want %v", result.Approved, tt.approved)
			}
			if tt.hasFB && result.Feedback == "" {
				t.Error("expected non-empty feedback")
			}
		})
	}
}

// slowMockRunner blocks for a specified duration.
type slowMockRunner struct {
	delay time.Duration
}

func (s *slowMockRunner) Run(ctx context.Context, opts RunOpts) (RunResult, error) {
	select {
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	case <-time.After(s.delay):
		return RunResult{Output: `{"approved": true}`}, nil
	}
}
