package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// DriftResult captures the outcome of a goal drift check.
type DriftResult struct {
	Score       float64
	Explanation string
	Action      string
}

// CheckDrift evaluates whether completed work has drifted from the immutable goal.
// It reads the immutable goal and completed task summaries from the blackboard,
// calls the LLM to score alignment, and applies graduated containment.
// Fails open: returns score=1.0 (no drift) if the LLM call fails.
func (c *Conductor) CheckDrift(ctx context.Context, cycleNumber int) (*DriftResult, error) {
	// Read immutable goal
	goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
	goal := c.getBlackboard(ctx, goalKey)
	if goal == "" {
		c.log("Drift check: no immutable goal found (key=%s), skipping", goalKey)
		return &DriftResult{Score: 1.0, Explanation: "no immutable goal set", Action: "none"}, nil
	}

	// Collect completed task summaries from result_summary blackboard keys
	entries, err := c.DB.ListBlackboardByPrefix(ctx, "result_summary:")
	if err != nil {
		c.log("Drift check: failed to read result summaries: %v", err)
		return &DriftResult{Score: 1.0, Explanation: "failed to read summaries", Action: "none"}, nil
	}

	var summaries []string
	for _, e := range entries {
		summaries = append(summaries, fmt.Sprintf("[%s]: %s", e.Key, e.Value))
	}

	if len(summaries) == 0 {
		c.log("Drift check: no completed task summaries, skipping")
		return &DriftResult{Score: 1.0, Explanation: "no completed summaries", Action: "none"}, nil
	}

	// Build LLM prompt
	prompt := fmt.Sprintf(`You are a goal alignment evaluator. Given the original goal and completed task summaries, score how well the work aligns with the goal.

Original Goal:
%s

Completed Task Summaries:
%s

Respond with ONLY a JSON object (no markdown, no explanation outside the JSON):
{"score": <float 0.0-1.0>, "explanation": "<brief explanation>"}

Score guide:
- 1.0: Perfect alignment, all work directly serves the goal
- 0.8: Good alignment, minor tangential work
- 0.6: Moderate drift, some work is off-target
- 0.4: Significant drift, substantial work doesn't serve the goal
- 0.2: Severe drift, most work is unrelated to the goal
- 0.0: Complete drift, work contradicts or ignores the goal`, goal, strings.Join(summaries, "\n"))

	// Call LLM with JSON schema to ensure structured response
	driftSchema := `{"type":"object","properties":{"score":{"type":"number"},"explanation":{"type":"string"}},"required":["score","explanation"]}`
	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		Model:      agent.NormalizeModelName("opus"),
		JSONSchema: driftSchema,
	})
	if err != nil {
		// Fail open: LLM error means no drift signal
		c.log("Drift check: LLM call failed (fail-open): %v", err)
		result := &DriftResult{Score: 1.0, Explanation: "LLM call failed, fail-open", Action: "none"}
		c.recordDrift(ctx, cycleNumber, result)
		return result, nil
	}

	// Parse LLM response
	var llmResp struct {
		Score       float64 `json:"score"`
		Explanation string  `json:"explanation"`
	}
	output := strings.TrimSpace(runResult.Output)
	if err := json.Unmarshal([]byte(output), &llmResp); err != nil {
		// Try to extract JSON from response (LLM might wrap in markdown)
		if start := strings.Index(output, "{"); start >= 0 {
			if end := strings.LastIndex(output, "}"); end > start {
				if err2 := json.Unmarshal([]byte(output[start:end+1]), &llmResp); err2 != nil {
					c.log("Drift check: failed to parse LLM response (fail-open): %v", err2)
					result := &DriftResult{Score: 1.0, Explanation: "parse error, fail-open", Action: "none"}
					c.recordDrift(ctx, cycleNumber, result)
					return result, nil
				}
			}
		} else {
			c.log("Drift check: failed to parse LLM response (fail-open): %v", err)
			result := &DriftResult{Score: 1.0, Explanation: "parse error, fail-open", Action: "none"}
			c.recordDrift(ctx, cycleNumber, result)
			return result, nil
		}
	}

	// Apply graduated containment
	result := &DriftResult{
		Score:       llmResp.Score,
		Explanation: llmResp.Explanation,
	}

	switch {
	case llmResp.Score < 0.4:
		// Critical drift: stop auto mode
		result.Action = "stop"
		c.log("Drift check: CRITICAL drift (score=%.2f) — stopping auto mode: %s", llmResp.Score, llmResp.Explanation)
		c.setBlackboard(ctx, "auto:stop", "drift_critical")
		c.DB.LogEvent(ctx, "goal_drift_critical", "", "",
			fmt.Sprintf(`{"score":%.2f,"cycle":%d,"explanation":%s}`,
				llmResp.Score, cycleNumber, mustJSON(truncate(llmResp.Explanation, 500))))

	case llmResp.Score < 0.6:
		// Significant drift: pause auto mode
		result.Action = "pause"
		c.log("Drift check: significant drift (score=%.2f) — pausing auto mode: %s", llmResp.Score, llmResp.Explanation)
		c.setBlackboard(ctx, "auto:pause", "drift_detected")
		c.DB.LogEvent(ctx, "goal_drift_pause", "", "",
			fmt.Sprintf(`{"score":%.2f,"cycle":%d,"explanation":%s}`,
				llmResp.Score, cycleNumber, mustJSON(truncate(llmResp.Explanation, 500))))

	case llmResp.Score < 0.8:
		// Mild drift: log alert, continue
		result.Action = "alert"
		c.log("Drift check: mild drift (score=%.2f): %s", llmResp.Score, llmResp.Explanation)
		c.DB.LogEvent(ctx, "goal_drift_alert", "", "",
			fmt.Sprintf(`{"score":%.2f,"cycle":%d,"explanation":%s}`,
				llmResp.Score, cycleNumber, mustJSON(truncate(llmResp.Explanation, 500))))

	default:
		// Good alignment
		result.Action = "none"
		c.log("Drift check: good alignment (score=%.2f)", llmResp.Score)
	}

	c.recordDrift(ctx, cycleNumber, result)
	return result, nil
}

// recordDrift persists the drift score to the database.
func (c *Conductor) recordDrift(ctx context.Context, cycleNumber int, result *DriftResult) {
	if err := c.DB.RecordDriftScore(ctx, db.DriftScore{
		SessionID:   c.SessionID,
		CycleNumber: cycleNumber,
		Score:       result.Score,
		Explanation: sql.NullString{Valid: result.Explanation != "", String: result.Explanation},
		ActionTaken: sql.NullString{Valid: result.Action != "", String: result.Action},
	}); err != nil {
		c.log("Drift check: failed to record score: %v", err)
	}
}
