package healing

import (
	"context"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Impact detects tasks affected by a failed task's file modifications.
type Impact struct {
	db *db.DB
}

// NewImpact creates an Impact checker.
func NewImpact(database *db.DB) *Impact {
	return &Impact{db: database}
}

// Check returns task IDs whose locked files overlap with the failed task's locked files.
// The failed task itself is excluded from the result.
func (imp *Impact) Check(ctx context.Context, failedTaskID string, candidateTaskIDs []string) ([]string, error) {
	failedLocks, err := imp.db.ListFileLocksForTask(ctx, failedTaskID)
	if err != nil {
		return nil, err
	}
	if len(failedLocks) == 0 {
		return nil, nil
	}

	failedFiles := make(map[string]bool, len(failedLocks))
	for _, l := range failedLocks {
		failedFiles[l.FilePath] = true
	}

	return imp.checkOverlap(ctx, failedTaskID, failedFiles, candidateTaskIDs)
}

// CheckWithFiles is like Check but accepts the failed task's files directly,
// useful when the failed task's locks have already been released.
func (imp *Impact) CheckWithFiles(ctx context.Context, failedTaskID string, failedFiles []string, candidateTaskIDs []string) ([]string, error) {
	if len(failedFiles) == 0 {
		return nil, nil
	}

	fileSet := make(map[string]bool, len(failedFiles))
	for _, f := range failedFiles {
		fileSet[f] = true
	}

	return imp.checkOverlap(ctx, failedTaskID, fileSet, candidateTaskIDs)
}

func (imp *Impact) checkOverlap(ctx context.Context, failedTaskID string, failedFiles map[string]bool, candidateTaskIDs []string) ([]string, error) {
	var affected []string
	for _, taskID := range candidateTaskIDs {
		if taskID == failedTaskID {
			continue
		}
		locks, err := imp.db.ListFileLocksForTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		for _, l := range locks {
			if failedFiles[l.FilePath] {
				affected = append(affected, taskID)
				break
			}
		}
	}
	return affected, nil
}
