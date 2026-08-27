package priority

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

func TestRankTasks_DescendingOrder(t *testing.T) {
	engine := NewEngine(nil, "")
	now := time.Now()

	tasks := []TaskPriority{
		{ID: "low", Priority: 1, Urgency: 1.0, CreatedAt: now},
		{ID: "high", Priority: 10, Urgency: 1.0, CreatedAt: now},
		{ID: "mid", Priority: 5, Urgency: 1.0, CreatedAt: now},
	}

	ranked := engine.RankTasks(context.Background(), tasks)
	if ranked[0].ID != "high" {
		t.Fatalf("expected 'high' first, got %s", ranked[0].ID)
	}
	if ranked[1].ID != "mid" {
		t.Fatalf("expected 'mid' second, got %s", ranked[1].ID)
	}
	if ranked[2].ID != "low" {
		t.Fatalf("expected 'low' third, got %s", ranked[2].ID)
	}
}

func TestRankTasks_EmptyInput(t *testing.T) {
	engine := NewEngine(nil, "")
	ranked := engine.RankTasks(context.Background(), nil)
	if len(ranked) != 0 {
		t.Fatalf("expected empty result, got %d", len(ranked))
	}

	ranked = engine.RankTasks(context.Background(), []TaskPriority{})
	if len(ranked) != 0 {
		t.Fatalf("expected empty result, got %d", len(ranked))
	}
}

func TestRankTasks_NilRunnerDefaultAlignment(t *testing.T) {
	engine := NewEngine(nil, "some goal")
	now := time.Now()

	tasks := []TaskPriority{
		{ID: "a", Priority: 5, Urgency: 1.0, CreatedAt: now},
	}

	ranked := engine.RankTasks(context.Background(), tasks)
	if ranked[0].GoalAlignment != 1.0 {
		t.Fatalf("expected 1.0 alignment with nil runner, got %f", ranked[0].GoalAlignment)
	}
}

func TestRankTasks_WithRunner(t *testing.T) {
	resp, _ := json.Marshal(map[string]float64{"alignment_score": 0.5})
	mock := &orchestrator.MockRunner{
		Outputs: []string{string(resp)},
	}
	engine := NewEngine(mock, "build a web app")
	now := time.Now()

	tasks := []TaskPriority{
		{ID: "a", Priority: 10, Urgency: 1.0, CreatedAt: now, Description: "implement login"},
	}

	ranked := engine.RankTasks(context.Background(), tasks)
	if ranked[0].GoalAlignment != 0.5 {
		t.Fatalf("expected 0.5 alignment, got %f", ranked[0].GoalAlignment)
	}
	// effective priority should use the 0.5 alignment
	if ranked[0].EffectivePriority == 0 {
		t.Fatal("expected non-zero effective priority")
	}
}

func TestRankTasks_TieBreakingStability(t *testing.T) {
	engine := NewEngine(nil, "")
	// Use a time far enough in the past that aging boost is capped at 50
	// for all tasks, eliminating nanosecond drift between evaluations.
	old := time.Now().Add(-100 * time.Hour)

	tasks := []TaskPriority{
		{ID: "first", Priority: 5, Urgency: 1.0, CreatedAt: old},
		{ID: "second", Priority: 5, Urgency: 1.0, CreatedAt: old},
		{ID: "third", Priority: 5, Urgency: 1.0, CreatedAt: old},
	}

	ranked := engine.RankTasks(context.Background(), tasks)
	// Stable sort: equal elements retain original order
	if ranked[0].ID != "first" || ranked[1].ID != "second" || ranked[2].ID != "third" {
		t.Fatalf("tie-breaking not stable: got %s, %s, %s", ranked[0].ID, ranked[1].ID, ranked[2].ID)
	}
}
