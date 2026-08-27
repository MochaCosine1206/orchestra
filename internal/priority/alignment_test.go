package priority

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

func TestScoreGoalAlignment_Success(t *testing.T) {
	resp, _ := json.Marshal(map[string]float64{"alignment_score": 0.85})
	mock := &orchestrator.MockRunner{
		Outputs: []string{string(resp)},
	}
	scorer := NewAlignmentScorer(mock)

	score, err := scorer.ScoreGoalAlignment(context.Background(), "implement auth", "build secure app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.85 {
		t.Fatalf("expected 0.85, got %f", score)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
}

func TestScoreGoalAlignment_FailOpenOnError(t *testing.T) {
	mock := &orchestrator.MockRunner{
		Outputs: []string{""},
		Errors:  []error{errors.New("connection refused")},
	}
	scorer := NewAlignmentScorer(mock)

	score, err := scorer.ScoreGoalAlignment(context.Background(), "task", "goal")
	if err != nil {
		t.Fatalf("expected nil error (fail-open), got: %v", err)
	}
	if score != 1.0 {
		t.Fatalf("expected 1.0 (fail-open), got %f", score)
	}
}

func TestScoreGoalAlignment_FailOpenOnContextDeadline(t *testing.T) {
	// Create a runner that blocks longer than the 5s deadline
	slow := &slowRunner{delay: 10 * time.Second}
	scorer := NewAlignmentScorer(slow)

	// Use a very short parent context to trigger deadline quickly in tests
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	score, err := scorer.ScoreGoalAlignment(ctx, "task", "goal")
	if err != nil {
		t.Fatalf("expected nil error (fail-open), got: %v", err)
	}
	if score != 1.0 {
		t.Fatalf("expected 1.0 (fail-open), got %f", score)
	}
}

func TestScoreGoalAlignment_CacheHit(t *testing.T) {
	resp, _ := json.Marshal(map[string]float64{"alignment_score": 0.7})
	mock := &orchestrator.MockRunner{
		Outputs: []string{string(resp)},
	}
	scorer := NewAlignmentScorer(mock)

	ctx := context.Background()
	score1, _ := scorer.ScoreGoalAlignment(ctx, "task A", "goal X")
	score2, _ := scorer.ScoreGoalAlignment(ctx, "task A", "goal X")

	if score1 != score2 {
		t.Fatalf("scores differ: %f vs %f", score1, score2)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call (cached), got %d", len(mock.Calls))
	}
}

func TestScoreGoalAlignment_CacheExpiry(t *testing.T) {
	resp, _ := json.Marshal(map[string]float64{"alignment_score": 0.6})
	mock := &orchestrator.MockRunner{
		Outputs: []string{string(resp)},
	}
	scorer := NewAlignmentScorer(mock)

	ctx := context.Background()
	scorer.ScoreGoalAlignment(ctx, "task", "goal")

	// Manually expire the cache entry
	key := cacheKey("task", "goal")
	scorer.cache.Store(key, cacheEntry{
		score:     0.6,
		expiresAt: time.Now().Add(-1 * time.Second), // expired
	})

	scorer.ScoreGoalAlignment(ctx, "task", "goal")
	if len(mock.Calls) != 2 {
		t.Fatalf("expected 2 calls (cache expired), got %d", len(mock.Calls))
	}
}

func TestScoreGoalAlignment_CacheMissDifferentInputs(t *testing.T) {
	resp, _ := json.Marshal(map[string]float64{"alignment_score": 0.5})
	mock := &orchestrator.MockRunner{
		Outputs: []string{string(resp)},
	}
	scorer := NewAlignmentScorer(mock)

	ctx := context.Background()
	scorer.ScoreGoalAlignment(ctx, "task A", "goal 1")
	scorer.ScoreGoalAlignment(ctx, "task B", "goal 2")

	if len(mock.Calls) != 2 {
		t.Fatalf("expected 2 calls (different inputs), got %d", len(mock.Calls))
	}
}

// slowRunner is a test double that simulates a slow LLM call.
type slowRunner struct {
	delay time.Duration
}

func (s *slowRunner) Run(ctx context.Context, _ orchestrator.RunOpts) (orchestrator.RunResult, error) {
	select {
	case <-ctx.Done():
		return orchestrator.RunResult{}, ctx.Err()
	case <-time.After(s.delay):
		return orchestrator.RunResult{Output: `{"alignment_score": 0.5}`}, nil
	}
}
