package orchestrator

import (
	"database/sql"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func makeTask(id string, blockedBy string) db.Task {
	return db.Task{
		ID:        id,
		Title:     "task " + id,
		Status:    "pending",
		BlockedBy: sql.NullString{String: blockedBy, Valid: blockedBy != ""},
	}
}

func TestTopoSort_Empty(t *testing.T) {
	sorted, err := TopoSort(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 0 {
		t.Fatalf("expected empty, got %v", sorted)
	}
}

func TestTopoSort_SingleTask(t *testing.T) {
	tasks := []db.Task{makeTask("T-001", "")}
	sorted, err := TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 1 || sorted[0] != "T-001" {
		t.Fatalf("expected [T-001], got %v", sorted)
	}
}

func TestTopoSort_LinearChain(t *testing.T) {
	// T-001 -> T-002 -> T-003
	tasks := []db.Task{
		makeTask("T-001", ""),
		makeTask("T-002", `["T-001"]`),
		makeTask("T-003", `["T-002"]`),
	}
	sorted, err := TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %v", len(sorted), sorted)
	}
	// T-001 must come before T-002, T-002 before T-003
	indexOf := func(id string) int {
		for i, s := range sorted {
			if s == id {
				return i
			}
		}
		return -1
	}
	if indexOf("T-001") >= indexOf("T-002") {
		t.Fatalf("T-001 should come before T-002: %v", sorted)
	}
	if indexOf("T-002") >= indexOf("T-003") {
		t.Fatalf("T-002 should come before T-003: %v", sorted)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	// T-001 -> T-002, T-001 -> T-003, T-002+T-003 -> T-004
	tasks := []db.Task{
		makeTask("T-001", ""),
		makeTask("T-002", `["T-001"]`),
		makeTask("T-003", `["T-001"]`),
		makeTask("T-004", `["T-002","T-003"]`),
	}
	sorted, err := TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 4 {
		t.Fatalf("expected 4 tasks, got %d: %v", len(sorted), sorted)
	}
	indexOf := func(id string) int {
		for i, s := range sorted {
			if s == id {
				return i
			}
		}
		return -1
	}
	if indexOf("T-001") >= indexOf("T-002") || indexOf("T-001") >= indexOf("T-003") {
		t.Fatalf("T-001 should come before T-002 and T-003: %v", sorted)
	}
	if indexOf("T-002") >= indexOf("T-004") || indexOf("T-003") >= indexOf("T-004") {
		t.Fatalf("T-002 and T-003 should come before T-004: %v", sorted)
	}
}

func TestTopoSort_CycleDetection(t *testing.T) {
	// T-001 -> T-002 -> T-001 (cycle)
	tasks := []db.Task{
		makeTask("T-001", `["T-002"]`),
		makeTask("T-002", `["T-001"]`),
	}
	_, err := TopoSort(tasks)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestTopoSort_IndependentTasks(t *testing.T) {
	tasks := []db.Task{
		makeTask("T-001", ""),
		makeTask("T-002", ""),
		makeTask("T-003", ""),
	}
	sorted, err := TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %v", len(sorted), sorted)
	}
}

func TestTopoSort_ExternalBlockerIgnored(t *testing.T) {
	// T-002 depends on T-999 which is not in the set — should be ignored
	tasks := []db.Task{
		makeTask("T-001", ""),
		makeTask("T-002", `["T-999"]`),
	}
	sorted, err := TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %v", len(sorted), sorted)
	}
}

func TestParseBlockedBy(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"[]", 0},
		{`["T-001"]`, 1},
		{`["T-001","T-002","T-003"]`, 3},
		{"invalid json", 0},
	}
	for _, tt := range tests {
		got := parseBlockedBy(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseBlockedBy(%q) = %v (len=%d), want len=%d", tt.input, got, len(got), tt.want)
		}
	}
}
