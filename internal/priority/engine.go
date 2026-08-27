package priority

import (
	"context"
	"sort"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// TaskPriority holds task data and computed priority fields for ranking.
type TaskPriority struct {
	ID              string
	Title           string
	Description     string
	Priority        int
	PriorityLabel   string
	CreatedAt       time.Time
	Urgency         float64
	ActivityCount   int
	PreemptionCount int

	// Computed fields
	GoalAlignment     float64
	EffectivePriority float64
}

// Engine orchestrates priority computation across tasks.
type Engine struct {
	scorer *AlignmentScorer
	goal   string
}

// NewEngine creates a priority Engine. If runner is nil, LLM alignment scoring
// is skipped and all tasks get 1.0 alignment (non-LLM operation).
func NewEngine(runner orchestrator.ClaudeRunner, projectGoal string) *Engine {
	return &Engine{
		scorer: NewAlignmentScorer(runner),
		goal:   projectGoal,
	}
}

// RankTasks computes effective priorities and returns tasks sorted descending.
// Original slice order is preserved for ties (stable sort).
func (e *Engine) RankTasks(ctx context.Context, tasks []TaskPriority) []TaskPriority {
	for i := range tasks {
		alignment := tasks[i].GoalAlignment
		if alignment == 0 {
			var err error
			alignment, err = e.scorer.ScoreGoalAlignment(ctx, tasks[i].Description, e.goal)
			if err != nil {
				alignment = 1.0
			}
			tasks[i].GoalAlignment = alignment
		}

		urgency := tasks[i].Urgency
		if urgency == 0 {
			urgency = 1.0
		}

		tasks[i].EffectivePriority = ComputeEffectivePriority(PriorityInput{
			BasePriority:    tasks[i].Priority,
			GoalAlignment:   alignment,
			Urgency:         urgency,
			CreatedAt:       tasks[i].CreatedAt,
			ActivityCount:   tasks[i].ActivityCount,
			PreemptionCount: tasks[i].PreemptionCount,
		})
	}

	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].EffectivePriority > tasks[j].EffectivePriority
	})

	return tasks
}
