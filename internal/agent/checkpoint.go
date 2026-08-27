package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Checkpoint captures structured state from a partially completed task.
// Used for continuation context when an agent is respawned after context exhaustion.
type Checkpoint struct {
	TaskID            string   `json:"task_id"`
	CommitHashes      []string `json:"commits"`
	FilesModified     []string `json:"files_modified"`
	TestsPassing      string   `json:"tests_passing"` // "unknown", "yes", "no"
	UncommittedFiles  int      `json:"uncommitted_files"`
	EstimatedProgress string   `json:"estimated_progress"` // "early", "mid", "late"
	LogTailSummary    string   `json:"log_tail_summary"`   // last meaningful actions
}

// ExtractCheckpoint gathers structured state from a task's worktree and logs.
// It returns nil if insufficient data is available.
func ExtractCheckpoint(ctx context.Context, d *db.DB, taskID, worktreePath, logsDir string) *Checkpoint {
	if worktreePath == "" {
		return nil
	}
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil
	}

	cp := &Checkpoint{
		TaskID:       taskID,
		TestsPassing: "unknown",
	}

	// 1. Parse commits ahead of base branch
	baseBranch := "dev"
	if bb, _ := d.GetBlackboardValue(ctx, "base_branch"); bb != "" {
		baseBranch = bb
	}
	logOutput, err := GitCmd(worktreePath, "log", baseBranch+"..HEAD", "--oneline")
	if err == nil && logOutput != "" {
		lines := strings.Split(logOutput, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 2)
			if len(parts) > 0 {
				cp.CommitHashes = append(cp.CommitHashes, parts[0])
			}
		}
	}

	// 2. Extract modified files from diff stat
	diffStat, err := GitCmd(worktreePath, "diff", "--stat", "--name-only", baseBranch+"..HEAD")
	if err == nil && diffStat != "" {
		for _, line := range strings.Split(diffStat, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				cp.FilesModified = append(cp.FilesModified, line)
			}
		}
	}

	// 3. Count uncommitted files
	statusOutput, err := GitCmd(worktreePath, "status", "--porcelain")
	if err == nil && statusOutput != "" {
		for _, line := range strings.Split(statusOutput, "\n") {
			if strings.TrimSpace(line) != "" {
				cp.UncommittedFiles++
			}
		}
	}

	// 4. Read last 30 lines of JSONL log and extract tool summaries
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	cp.LogTailSummary = extractLogSummary(logFile)

	// 5. Estimate progress based on commit count
	switch {
	case len(cp.CommitHashes) == 0:
		cp.EstimatedProgress = "early"
	case len(cp.CommitHashes) <= 2:
		cp.EstimatedProgress = "mid"
	default:
		cp.EstimatedProgress = "late"
	}

	// 6. Check for agent-written checkpoint file
	selfCPFile := filepath.Join(worktreePath, ".checkpoint.json")
	if data, err := os.ReadFile(selfCPFile); err == nil {
		var selfCP map[string]interface{}
		if json.Unmarshal(data, &selfCP) == nil {
			if completed, ok := selfCP["completed"].([]interface{}); ok {
				cp.LogTailSummary += fmt.Sprintf("\nSelf-reported completed: %d items", len(completed))
			}
			if remaining, ok := selfCP["remaining"].([]interface{}); ok {
				cp.LogTailSummary += fmt.Sprintf("\nSelf-reported remaining: %d items", len(remaining))
			}
			if notes, ok := selfCP["notes"].(string); ok && notes != "" {
				cp.LogTailSummary += fmt.Sprintf("\nNotes: %s", notes)
			}
		}
	}

	// 7. Marshal and enforce 3000 char limit
	data, err := json.Marshal(cp)
	if err != nil {
		return cp
	}
	if len(data) > 3000 {
		// Truncate log tail to fit
		cp.LogTailSummary = truncateToFit(cp.LogTailSummary, len(data)-3000)
	}

	return cp
}

// extractLogSummary reads the last 30 lines of a JSONL log and extracts
// the last 3 tool result summaries (tool name + first 100 chars of result).
func extractLogSummary(logFile string) string {
	tail := readFileTail(logFile, 30)
	if tail == "" {
		return ""
	}

	var summaries []string
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		// Look for tool_result messages
		msgType, _ := msg["type"].(string)
		if msgType != "assistant" {
			continue
		}

		messageObj, ok := msg["message"].(map[string]interface{})
		if !ok {
			continue
		}

		contentArr, ok := messageObj["content"].([]interface{})
		if !ok {
			continue
		}

		for _, block := range contentArr {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "tool_use" {
				toolName, _ := blockMap["name"].(string)
				if toolName != "" {
					summaries = append(summaries, fmt.Sprintf("- Used tool: %s", toolName))
				}
			}
		}
	}

	// Keep last 3 summaries
	if len(summaries) > 3 {
		summaries = summaries[len(summaries)-3:]
	}

	return strings.Join(summaries, "\n")
}

// truncateToFit shortens a string by removing `excess` characters from the end.
func truncateToFit(s string, excess int) string {
	if excess <= 0 || len(s) == 0 {
		return s
	}
	cutTo := len(s) - excess - 20 // extra margin
	if cutTo < 0 {
		return ""
	}
	return s[:cutTo] + "..."
}
