package orchestra

import (
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

func TestGeneratePRDescription_Full(t *testing.T) {
	t.Parallel()
	result := &orchestrator.GoResult{
		SessionID:     "s-test-123",
		TaskCount:     5,
		DoneCount:     4,
		FailedCount:   1,
		Duration:      3*time.Minute + 42*time.Second,
		StagingBranch: "conductor/s-test-123",
		MergeResult: &orchestrator.MergeResult{
			Merged:       []string{"feature/task-1", "feature/task-2", "feature/task-3"},
			Failed:       []string{"feature/task-4"},
			AutoResolved: []string{"feature/task-2"},
			TestsFailed:  false,
		},
		ReconcileResult: &orchestrator.ReconcileResult{
			GoalAlignmentScore: 0.85,
		},
	}

	desc := GeneratePRDescription(result)

	checks := []struct {
		name     string
		contains string
	}{
		{"session ID", "s-test-123"},
		{"duration", "3m42s"},
		{"task counts", "5 total, 4 done, 1 failed"},
		{"staging branch", "conductor/s-test-123"},
		{"merged count", "Merged branches (3)"},
		{"failed count", "Failed branches (1)"},
		{"auto-resolved", "**Auto-resolved conflicts:** 1 branches"},
		{"tests passed", "**PASSED**"},
		{"details tag", "<details>"},
		{"alignment", "85%"},
		{"footer", "Claude Orchestra"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(desc, tc.contains) {
				t.Errorf("expected %q in description, got:\n%s", tc.contains, desc)
			}
		})
	}
}

func TestGeneratePRDescription_Minimal(t *testing.T) {
	t.Parallel()
	result := &orchestrator.GoResult{
		SessionID: "s-minimal",
		TaskCount: 1,
		DoneCount: 1,
		Duration:  10 * time.Second,
	}

	desc := GeneratePRDescription(result)

	if !strings.Contains(desc, "s-minimal") {
		t.Error("missing session ID")
	}
	if !strings.Contains(desc, "**PASSED**") {
		t.Error("expected PASSED for nil MergeResult")
	}
	// Should not contain merge sections
	if strings.Contains(desc, "Merged branches") {
		t.Error("should not have merged branches section with nil MergeResult")
	}
}

func TestGeneratePRDescription_TestsFailed(t *testing.T) {
	t.Parallel()
	result := &orchestrator.GoResult{
		SessionID:   "s-fail",
		TaskCount:   2,
		DoneCount:   1,
		FailedCount: 1,
		Duration:    30 * time.Second,
		MergeResult: &orchestrator.MergeResult{
			TestsFailed: true,
		},
	}

	desc := GeneratePRDescription(result)

	if !strings.Contains(desc, "**FAILED**") {
		t.Error("expected FAILED in test results")
	}
}

func TestGeneratePRDescription_RefiningAndAbandoned(t *testing.T) {
	t.Parallel()
	result := &orchestrator.GoResult{
		SessionID:      "s-complex",
		TaskCount:      10,
		DoneCount:      6,
		FailedCount:    2,
		RefiningCount:  1,
		AbandonedCount: 1,
		Duration:       5 * time.Minute,
	}

	desc := GeneratePRDescription(result)

	if !strings.Contains(desc, "Refining") {
		t.Error("expected Refining row in stats")
	}
	if !strings.Contains(desc, "Abandoned") {
		t.Error("expected Abandoned row in stats")
	}
}

func TestGeneratePRTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		result   *orchestrator.GoResult
		contains string
		maxLen   int
	}{
		{
			name:     "all_done",
			result:   &orchestrator.GoResult{TaskCount: 3, DoneCount: 3},
			contains: "3/3 tasks done",
			maxLen:   70,
		},
		{
			name:     "with_failures",
			result:   &orchestrator.GoResult{TaskCount: 5, DoneCount: 3, FailedCount: 2},
			contains: "(2 failed)",
			maxLen:   70,
		},
		{
			name:     "orchestra_prefix",
			result:   &orchestrator.GoResult{TaskCount: 1, DoneCount: 1},
			contains: "[Orchestra]",
			maxLen:   70,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title := GeneratePRTitle(tc.result)
			if !strings.Contains(title, tc.contains) {
				t.Errorf("expected %q in title %q", tc.contains, title)
			}
			if len(title) > tc.maxLen {
				t.Errorf("title too long: %d chars (max %d): %q", len(title), tc.maxLen, title)
			}
		})
	}
}

func TestGeneratePRTitle_LengthLimit(t *testing.T) {
	t.Parallel()
	result := &orchestrator.GoResult{
		TaskCount:   999999,
		DoneCount:   888888,
		FailedCount: 111111,
	}
	title := GeneratePRTitle(result)
	if len(title) > 70 {
		t.Errorf("title exceeds 70 chars: %d chars: %q", len(title), title)
	}
}
