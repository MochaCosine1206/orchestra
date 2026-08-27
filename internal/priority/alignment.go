package priority

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// AlignmentScorer scores how well a task description aligns with a project goal.
type AlignmentScorer struct {
	runner orchestrator.ClaudeRunner
	cache  sync.Map // map[string]cacheEntry
}

type cacheEntry struct {
	score     float64
	expiresAt time.Time
}

const alignmentCacheTTL = 1 * time.Hour

const alignmentJSONSchema = `{
	"type": "object",
	"properties": {
		"alignment_score": {
			"type": "number",
			"minimum": 0,
			"maximum": 1,
			"description": "How well the task aligns with the project goal, from 0.0 (unrelated) to 1.0 (perfectly aligned)"
		}
	},
	"required": ["alignment_score"],
	"additionalProperties": false
}`

// NewAlignmentScorer creates an AlignmentScorer. If runner is nil, ScoreGoalAlignment
// always returns 1.0 (fail-open).
func NewAlignmentScorer(runner orchestrator.ClaudeRunner) *AlignmentScorer {
	return &AlignmentScorer{runner: runner}
}

func cacheKey(taskDesc, projectGoal string) string {
	h := sha256.Sum256([]byte(taskDesc + "\x00" + projectGoal))
	return fmt.Sprintf("%x", h)
}

// ScoreGoalAlignment returns a 0.0-1.0 score for how well taskDescription
// aligns with projectGoal. Fail-open: returns 1.0 on any error or timeout.
func (a *AlignmentScorer) ScoreGoalAlignment(ctx context.Context, taskDescription, projectGoal string) (float64, error) {
	if a.runner == nil {
		return 1.0, nil
	}

	key := cacheKey(taskDescription, projectGoal)
	if v, ok := a.cache.Load(key); ok {
		entry := v.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.score, nil
		}
		a.cache.Delete(key)
	}

	// Apply 5s deadline
	scoreCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Rate how well this task aligns with the project goal.\n\nProject Goal: %s\n\nTask Description: %s\n\nReturn a JSON object with alignment_score between 0.0 and 1.0.",
		projectGoal, taskDescription,
	)

	result, err := a.runner.Run(scoreCtx, orchestrator.RunOpts{
		Prompt:     prompt,
		JSONSchema: alignmentJSONSchema,
	})
	if err != nil {
		return 1.0, nil // fail-open
	}

	var resp struct {
		AlignmentScore float64 `json:"alignment_score"`
	}
	if err := json.Unmarshal([]byte(result.Output), &resp); err != nil {
		return 1.0, nil // fail-open
	}

	score := resp.AlignmentScore
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	a.cache.Store(key, cacheEntry{
		score:     score,
		expiresAt: time.Now().Add(alignmentCacheTTL),
	})

	return score, nil
}
