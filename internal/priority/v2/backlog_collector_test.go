package priority

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const testBacklogContent = `# BACKLOG.md — Test Repo

## P0: Blockers

### B-001: Fix critical database bug
**Status:** DONE
**Category:** Infrastructure

Fixed the database connection pooling issue.

### B-002: Implement session recovery
**Status:** Open
**Category:** Resilience

Session recovery mechanism needed.

---

## P1: Core Functionality

### B-010: Add monitoring dashboard
**Status:** Open
**Category:** Feature

### B-011: Completed feature
**Status:** Done
**Category:** Feature

---

## P2: Important Features

### B-020: Improve error messages
**Status:** Open
**Category:** UX

---

## P3: Polish

### B-030: Clean up logging
**Status:** Open
**Category:** Infrastructure

---

## P4: Ideas

### B-040: AI-powered code review
**Status:** Open
**Category:** Research

---

## P5: Exploratory

### I-001: Investigate new LLM providers
**Status:** Open
**Category:** Research
`

func writeTestBacklog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BACKLOG.md")
	if err := os.WriteFile(path, []byte(testBacklogContent), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBacklogCollector_ParseItems(t *testing.T) {
	t.Parallel()

	repo := writeTestBacklog(t)
	collector := NewBacklogCollector([]string{repo})

	// Verify interface compliance
	var _ Collector = collector

	if collector.Name() != SourceBacklog {
		t.Errorf("Name() = %q, want %q", collector.Name(), SourceBacklog)
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}

	// B-001 (DONE) and B-011 (Done) should be excluded
	// Remaining: B-002, B-010, B-020, B-030, B-040, I-001 = 6 items
	if len(items) != 6 {
		t.Fatalf("got %d items, want 6 (2 DONE excluded)", len(items))
	}

	// Verify B-002 (P0, not done) is present with correct mapping
	b002 := findItem(items, "B-002")
	if b002 == nil {
		t.Fatal("B-002 not found in results")
	}
	if b002.BasePriority != 100 {
		t.Errorf("B-002 BasePriority = %d, want 100 (P0)", b002.BasePriority)
	}
	if b002.Tier != TierGoalImpl {
		t.Errorf("B-002 Tier = %d, want TierGoalImpl (%d)", b002.Tier, TierGoalImpl)
	}

	// Verify B-010 (P1) mapping
	b010 := findItem(items, "B-010")
	if b010 == nil {
		t.Fatal("B-010 not found")
	}
	if b010.BasePriority != 80 {
		t.Errorf("B-010 BasePriority = %d, want 80 (P1)", b010.BasePriority)
	}
	if b010.Tier != TierGoalResearch {
		t.Errorf("B-010 Tier = %d, want TierGoalResearch (%d)", b010.Tier, TierGoalResearch)
	}
}

func TestBacklogCollector_PriorityMapping(t *testing.T) {
	t.Parallel()

	repo := writeTestBacklog(t)
	collector := NewBacklogCollector([]string{repo})
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}

	expected := []struct {
		id           string
		basePriority int
		tier         Tier
	}{
		{"B-002", 100, TierGoalImpl},       // P0
		{"B-010", 80, TierGoalResearch},    // P1
		{"B-020", 60, TierGoalResearch},    // P2
		{"B-030", 40, TierTechDebt},        // P3
		{"B-040", 20, TierTechDebt},        // P4
		{"I-001", 10, TierExploratory},     // P5
	}

	for _, e := range expected {
		item := findItem(items, e.id)
		if item == nil {
			t.Errorf("item %s not found", e.id)
			continue
		}
		if item.BasePriority != e.basePriority {
			t.Errorf("%s BasePriority = %d, want %d", e.id, item.BasePriority, e.basePriority)
		}
		if item.Tier != e.tier {
			t.Errorf("%s Tier = %d, want %d", e.id, item.Tier, e.tier)
		}
	}
}

func TestBacklogCollector_SourceID(t *testing.T) {
	t.Parallel()

	repo := writeTestBacklog(t)
	collector := NewBacklogCollector([]string{repo})
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}

	expectedIDs := []string{"B-002", "B-010", "B-020", "B-030", "B-040", "I-001"}
	for _, id := range expectedIDs {
		item := findItem(items, id)
		if item == nil {
			t.Errorf("item with SourceID %q not found", id)
			continue
		}
		if item.SourceID != id {
			t.Errorf("item SourceID = %q, want %q", item.SourceID, id)
		}
		if item.Source != SourceBacklog {
			t.Errorf("item %s Source = %q, want %q", id, item.Source, SourceBacklog)
		}
	}
}

func TestBacklogCollector_MissingFile(t *testing.T) {
	t.Parallel()

	collector := NewBacklogCollector([]string{"/nonexistent/repo/path"})
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for missing repo, got: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for missing repo, got %d", len(items))
	}
}

func findItem(items []WorkItem, sourceID string) *WorkItem {
	for i := range items {
		if items[i].SourceID == sourceID {
			return &items[i]
		}
	}
	return nil
}
