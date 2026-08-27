package governor

import (
	"context"
	"testing"
)

func TestRecordUsage(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	ctx := context.Background()
	if err := gov.RecordUsage(ctx, "task-1", "agent-a", 1000, 500); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if err := gov.RecordUsage(ctx, "task-1", "agent-a", 2000, 1000); err != nil {
		t.Fatalf("RecordUsage second call: %v", err)
	}
}

func TestGetTaskUsage(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	ctx := context.Background()
	_ = gov.RecordUsage(ctx, "task-1", "agent-a", 1000, 500)
	_ = gov.RecordUsage(ctx, "task-1", "agent-b", 2000, 1000)
	_ = gov.RecordUsage(ctx, "task-2", "agent-a", 500, 200)

	status, err := gov.GetTaskUsage(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTaskUsage: %v", err)
	}
	if status.TokensUsed != 4500 {
		t.Errorf("expected 4500 tokens used, got %d", status.TokensUsed)
	}
	if status.Remaining != -1 {
		t.Errorf("expected -1 remaining (unlimited), got %d", status.Remaining)
	}
}

func TestGetDailyStatus(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	ctx := context.Background()
	_ = gov.RecordUsage(ctx, "task-1", "agent-a", 5000, 3000)

	status, err := gov.GetDailyStatus(ctx)
	if err != nil {
		t.Fatalf("GetDailyStatus: %v", err)
	}
	if status.TokensUsed != 8000 {
		t.Errorf("expected 8000 tokens used, got %d", status.TokensUsed)
	}
}

func TestCanSpawnTask_Unlimited(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	ctx := context.Background()
	_ = gov.RecordUsage(ctx, "task-1", "agent-a", 999999, 999999)

	can, err := gov.CanSpawnTask(ctx)
	if err != nil {
		t.Fatalf("CanSpawnTask: %v", err)
	}
	if !can {
		t.Error("expected CanSpawnTask to return true for unlimited budget")
	}
}

func TestCanSpawnTask_OverBudget(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	gov.Config.DailyTokenLimit = 10000

	ctx := context.Background()
	_ = gov.RecordUsage(ctx, "task-1", "agent-a", 8000, 3000)

	can, err := gov.CanSpawnTask(ctx)
	if err != nil {
		t.Fatalf("CanSpawnTask: %v", err)
	}
	if can {
		t.Error("expected CanSpawnTask to return false when over budget")
	}
}

func TestBuildStatus_WithLimit(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	status := gov.buildStatus(7500, 10000)
	if status.Remaining != 2500 {
		t.Errorf("expected 2500 remaining, got %d", status.Remaining)
	}
	if status.OverBudget {
		t.Error("expected not over budget")
	}
	expectedPct := 0.75
	if status.UtilizationPct != expectedPct {
		t.Errorf("expected utilization %.2f, got %.2f", expectedPct, status.UtilizationPct)
	}
}

func TestGetTaskUsage_NoRecords(t *testing.T) {
	t.Parallel()
	gov, err := newTestGovernor()
	if err != nil {
		t.Fatal(err)
	}
	defer gov.DB.Close()

	ctx := context.Background()
	status, err := gov.GetTaskUsage(ctx, "nonexistent-task")
	if err != nil {
		t.Fatalf("GetTaskUsage: %v", err)
	}
	if status.TokensUsed != 0 {
		t.Errorf("expected 0 tokens used for nonexistent task, got %d", status.TokensUsed)
	}
}
