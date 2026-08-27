package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CheckLogResult scans the JSONL log for a result line and determines success.
// Extracted from monitor.checkLogResult for shared use by spawner and monitor.
func CheckLogResult(logsDir, taskID string) (hasResult bool, isSuccess bool) {
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return false, false
	}

	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, `"type":"result"`) || strings.Contains(line, `"type": "result"`) {
			isError := strings.Contains(line, `"is_error":true`) || strings.Contains(line, `"is_error": true`)
			isSuccess = !isError && (strings.Contains(line, `"subtype":"success"`) || strings.Contains(line, `"success"`))
			return true, isSuccess
		}
	}
	return false, false
}

// handleAgentExit is called after cmd.Wait() returns in a spawner goroutine.
// It is the primary completion path for agent exit; the monitor serves as backup.
// This method is idempotent: if the task is already in a terminal state, it returns immediately.
func (s *Spawner) handleAgentExit(ctx context.Context, taskID, agentID string) {
	// Guard: if task already terminal, this is a no-op (monitor may have handled it first).
	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.Status == "done" || task.Status == "failed" {
		// Still clean up agent status and PID file.
		s.DB.SetAgentDead(ctx, agentID)
		RemovePID(s.PidsDir, taskID)
		return
	}

	// Check log for result.
	hasResult, isSuccess := CheckLogResult(s.LogsDir, taskID)

	// For iterative roles (deep-researcher, visual-reviewer, functional-tester):
	// The loop runs in-process, not via a background agent process, so there's no
	// JSONL log file. Check the blackboard for convergence instead.
	if !hasResult {
		if iterResult, _ := s.DB.GetBlackboardValue(ctx, "iterative_result:"+taskID); iterResult != "" {
			hasResult = true
			isSuccess = strings.Contains(iterResult, `"converged":true`)
		}
	}

	if hasResult && isSuccess {
		// Salvage any uncommitted changes before validation.
		// Researchers may write files but forget to commit; this ensures
		// the validation check (which requires commits) can find them.
		if task, tErr := s.DB.GetTaskByID(ctx, taskID); tErr == nil && task != nil && task.Worktree.Valid {
			SalvageWorktreeChanges(ctx, task.Worktree.String, taskID)
		}

		// Validate output.
		result, err := ValidateTaskOutput(ctx, s.DB, taskID, s.RepoRoot, s.LogsDir)
		if err != nil {
			s.DB.FailTask(ctx, taskID, "validation_error: "+err.Error())
			s.DB.SetAgentDead(ctx, agentID)
			RemovePID(s.PidsDir, taskID)
			s.DB.LogEvent(ctx, "spawner_exit_failed", agentID, taskID, err.Error())
			return
		}

		if result.OK {
			s.DB.CompleteTask(ctx, taskID, "completed")
			s.DB.SetBlackboard(ctx, "validation:"+taskID, "passed", "spawner")
			if result.ResultSummary != "" {
				s.DB.SetBlackboard(ctx, "result_summary:"+taskID, result.ResultSummary, "spawner")
			}
			s.DB.LogEvent(ctx, "spawner_completed", agentID, taskID, "")

			// Immediately trigger spawning of any newly-unblocked dependent tasks.
			if s.OnTaskCompleted != nil {
				go s.OnTaskCompleted(ctx, taskID)
			}
		} else {
			s.DB.FailTask(ctx, taskID, "validation_failed: "+result.Reason)
			s.DB.SetBlackboard(ctx, "failure_type:"+taskID, "validation_failure", "spawner")
			s.DB.SetBlackboard(ctx, "last_failure:"+taskID, result.Reason, "spawner")
			validationPayload := result.Reason
			if result.ResultSummary != "" {
				validationPayload = fmt.Sprintf("%s\n---\n%s", result.Reason, result.ResultSummary)
			}
			s.DB.LogEvent(ctx, "spawner_validation_failed", agentID, taskID, validationPayload)

			// B-207: Log and store skip reports for refinement context
			if len(result.SkipReports) > 0 {
				skipJSON, _ := json.Marshal(result.SkipReports)
				s.DB.SetBlackboard(ctx, "skip_reports:"+taskID, string(skipJSON), "spawner")
				for _, sr := range result.SkipReports {
					s.DB.LogEvent(ctx, "agent_file_skipped", agentID, taskID,
						fmt.Sprintf(`{"file":"%s","reason":"%s"}`, sr.FilePath, sr.Reason))
				}
			}

			// Check if failure qualifies for refinement loop
			if IsRefinableFailure(result.Reason) {
				refinementStr, _ := s.DB.GetBlackboardValue(ctx, "refinement:"+taskID)
				refinementCount, _ := strconv.Atoi(refinementStr)
				if refinementCount < MaxRefinements {
					s.DB.LogEvent(ctx, "spawner_refinement_triggered", agentID, taskID,
						fmt.Sprintf(`{"reason":"%s","refinement":%d}`, result.Reason, refinementCount))
					go func() {
						if _, err := s.Refine(context.Background(), taskID); err != nil {
							s.DB.LogEvent(context.Background(), "spawner_refinement_failed", "", taskID, err.Error())
						}
					}()
				}
			}
		}
	} else {
		// Agent crashed or produced no result.
		stderrFile := filepath.Join(s.LogsDir, taskID+".stderr")
		jsonlFile := filepath.Join(s.LogsDir, taskID+".jsonl")
		ft := ClassifyFailure(stderrFile, jsonlFile)
		s.DB.SetBlackboard(ctx, "failure_type:"+taskID, ft.Kind, "spawner")
		s.DB.FailTask(ctx, taskID, fmt.Sprintf("agent_exit: %s", ft.Kind))
		s.DB.LogEvent(ctx, "spawner_exit_failed", agentID, taskID, ft.Kind)

		// G139: Set global rate limit cooldown so other agents don't burn sessions.
		if ft.Kind == "session_limit" && ft.ResetEpoch > 0 {
			s.DB.SetBlackboard(ctx, "conductor:rate_limit_cooldown_until",
				strconv.FormatInt(ft.ResetEpoch, 10), "spawner")
		}
	}

	// Always clean up agent status and PID file.
	// NOTE: auto-respawn is NOT done here — that responsibility stays in the monitor.
	s.DB.SetAgentDead(ctx, agentID)
	RemovePID(s.PidsDir, taskID)
}
