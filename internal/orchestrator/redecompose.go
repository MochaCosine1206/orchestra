package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

const redecomposePromptTemplate = `You are a task decomposition architect. A task was partially completed by an agent that ran out of context window.

ORIGINAL TASK: %s

CHECKPOINT (what has been done):
- Commits made: %d
- Files modified: %s
- Estimated progress: %s
- Uncommitted files: %d
%s

RULES:
- Create at most %d tasks for the REMAINING work only
- Do NOT include work that is already completed (see checkpoint above)
- Each task must have a clear, atomic objective
- Assign roles: implementer, researcher, architect, reviewer, scout
- Set priority 1-5 (5=highest)
- Specify file dependencies to prevent conflicts

OUTPUT FORMAT (JSON array):
[
  {
    "title": "Short imperative title",
    "description": "Detailed description of what to do",
    "role": "implementer",
    "priority": 5,
    "depends_on": [],
    "files": ["path/to/file.go"]
  }
]

Respond with ONLY the JSON array, no other text.`

// ReDecompose splits a context-exhausted task into smaller subtasks based on checkpoint data.
// Called by the monitor via ReDecomposeFunc callback to avoid circular imports.
func (c *Conductor) ReDecompose(ctx context.Context, taskID string, checkpointJSON string) ([]string, error) {
	// 1. Look up original task
	task, err := c.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	// 2. Parse checkpoint
	var cp agent.Checkpoint
	if err := json.Unmarshal([]byte(checkpointJSON), &cp); err != nil {
		return nil, fmt.Errorf("parsing checkpoint: %w", err)
	}

	// 3. Build re-decomposition prompt
	desc := task.Title
	if task.Description.Valid && task.Description.String != "" {
		desc = task.Description.String
		if len(desc) > 2000 {
			desc = desc[:2000] + "..."
		}
	}

	filesModified := "none"
	if len(cp.FilesModified) > 0 {
		filesModified = strings.Join(cp.FilesModified, ", ")
	}

	logTail := ""
	if cp.LogTailSummary != "" {
		logTail = fmt.Sprintf("- Last actions:\n%s", cp.LogTailSummary)
	}

	maxSubtasks := 3
	prompt := fmt.Sprintf(redecomposePromptTemplate,
		desc, len(cp.CommitHashes), filesModified, cp.EstimatedProgress,
		cp.UncommittedFiles, logTail, maxSubtasks)

	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for re-decomposition")
	}

	// 4. Call Claude to decompose remaining work
	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelOpus,
		WorkDir: c.RepoRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("re-decompose call failed: %w", err)
	}

	// 5. Parse output into subtasks
	tasks, err := parseDecomposeOutput(runResult.Output, maxSubtasks)
	if err != nil {
		return nil, fmt.Errorf("parsing re-decompose output: %w", err)
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("re-decomposition produced no tasks")
	}

	// 6. Create tasks in DB (no dependencies — predecessor work already committed)
	now := time.Now()
	var newIDs []string
	idMap := make(map[int]string)

	for i := range tasks {
		genID := db.GenID("t")
		tasks[i].ID = genID
		idMap[i] = genID
		newIDs = append(newIDs, genID)
	}

	// Resolve internal dependencies between the new subtasks
	for i := range tasks {
		tasks[i].DependsOn = resolveDependencies(tasks[i].DependsOn, tasks, idMap, tasks[i].ID)
	}

	// Validate DAG: detect cycles before DB insertion
	if cycleIDs := detectAndBreakCycles(tasks); len(cycleIDs) > 0 {
		c.log("Cycle detected in re-decomposition DAG — stripped deps from: %v", cycleIDs)
		c.DB.LogEvent(ctx, "redecompose_cycle_detected", "", taskID,
			fmt.Sprintf(`{"cycle_participants":%q}`, cycleIDs))
	}

	for _, t := range tasks {
		blockedBy := "[]"
		if len(t.DependsOn) > 0 {
			data, _ := json.Marshal(t.DependsOn)
			blockedBy = string(data)
		}

		err := c.DB.CreateTask(ctx, db.Task{
			ID:          t.ID,
			Title:       t.Title,
			Description: sql.NullString{String: t.Description, Valid: t.Description != ""},
			Status:      "pending",
			Priority:    t.Priority,
			Role:        t.Role,
			BlockedBy:   sql.NullString{String: blockedBy, Valid: true},
			ConductorID: sql.NullString{String: c.ConductorID, Valid: c.ConductorID != ""},
			CreatedAt:   now,
		})
		if err != nil {
			return nil, fmt.Errorf("creating subtask %s: %w", t.ID, err)
		}

		c.DB.LogEvent(ctx, "task_redecomposed", "", t.ID,
			fmt.Sprintf(`{"parent":"%s","role":"%s"}`, taskID, t.Role))
	}

	// 7. Add new task IDs to session
	existing := c.sessionTaskIDs(ctx)
	combined := append(existing, newIDs...)
	if err := c.storeSessionTaskIDs(ctx, combined); err != nil {
		c.log("Failed to update session task IDs: %v", err)
	}

	c.log("Re-decomposed %s into %d subtasks: %v", taskID, len(newIDs), newIDs)
	return newIDs, nil
}
