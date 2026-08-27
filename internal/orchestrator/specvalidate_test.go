package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
)

func TestValidateSpecAgainstPlan_Basic(t *testing.T) {
	// Mock runner that returns canned checklist + coverage results
	checklistResponse := map[string]interface{}{
		"items": []map[string]string{
			{"id": "P-1", "description": "Research phase", "category": "phase", "importance": "critical"},
			{"id": "P-2", "description": "Content writing phase", "category": "phase", "importance": "critical"},
			{"id": "D-1", "description": "Chapter 1 markdown", "category": "deliverable", "importance": "major"},
		},
	}
	checklistJSON, _ := json.Marshal(checklistResponse)

	coverageResponse := PlanValidationResult{
		CoverageScore:  0.83,
		PlanItemsFound: 3,
		SpecItemsFound: 2,
		MissingItems: []PlanValidationGap{
			{PlanReference: "D-1", Category: "deliverable", Severity: "major", Description: "Chapter 1 not in spec"},
		},
		CoveredItems: []PlanValidationMatch{
			{PlanReference: "P-1", SpecMatch: "research phase", MatchQuality: "exact"},
			{PlanReference: "P-2", SpecMatch: "content phase", MatchQuality: "exact"},
		},
		Pass:    true,
		Summary: "2/3 items covered, 1 major gap",
	}
	coverageJSON, _ := json.Marshal(coverageResponse)

	mock := &MockRunner{
		Outputs: []string{string(checklistJSON), string(coverageJSON)},
	}
	c := newTestConductor(mock)

	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Test Book", Description: "test"},
		Phases: []Phase{
			{ID: "research", Name: "Research", Description: "Do research",
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{{Title: "Research task", Role: "researcher",
					Files: []string{"r.md"}, Description: "d", AcceptanceCriteria: []string{"ok"}}}},
			{ID: "content", Name: "Content", Description: "Write content", DependsOn: []string{"research"},
				Gate: PhaseGate{},
				Tasks: []SpecTask{{Title: "Write", Role: "implementer",
					Files: []string{"ch01.md"}, Description: "d", AcceptanceCriteria: []string{"ok"}}}},
		},
	}

	result, err := c.ValidateSpecAgainstPlan(context.Background(), PlanValidationOpts{
		PlanText: "Plan: research phase, content writing phase, produce Chapter 1 markdown",
		Spec:     spec,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PlanItemsFound != 3 {
		t.Errorf("PlanItemsFound = %d, want 3", result.PlanItemsFound)
	}
	if result.CoverageScore < 0.8 {
		t.Errorf("CoverageScore = %.2f, want >= 0.8", result.CoverageScore)
	}
	if !result.Pass {
		t.Error("expected Pass=true")
	}
	if len(result.MissingItems) != 1 {
		t.Errorf("MissingItems count = %d, want 1", len(result.MissingItems))
	}
	if len(result.CoveredItems) != 2 {
		t.Errorf("CoveredItems count = %d, want 2", len(result.CoveredItems))
	}
}

func TestValidateSpecAgainstPlan_EmptyPlan(t *testing.T) {
	mock := &MockRunner{}
	c := newTestConductor(mock)

	_, err := c.ValidateSpecAgainstPlan(context.Background(), PlanValidationOpts{
		PlanText: "",
		Spec:     &OrchestraSpec{},
	})
	if err == nil {
		t.Fatal("expected error for empty plan text")
	}
}

func TestValidateSpecAgainstPlan_NilSpec(t *testing.T) {
	mock := &MockRunner{}
	c := newTestConductor(mock)

	_, err := c.ValidateSpecAgainstPlan(context.Background(), PlanValidationOpts{
		PlanText: "Some plan",
		Spec:     nil,
	})
	if err == nil {
		t.Fatal("expected error for nil spec")
	}
}

func TestValidateSpecAgainstPlan_NoItemsExtracted(t *testing.T) {
	// LLM returns empty checklist
	emptyChecklist, _ := json.Marshal(map[string]interface{}{"items": []interface{}{}})
	mock := &MockRunner{Outputs: []string{string(emptyChecklist)}}
	c := newTestConductor(mock)

	result, err := c.ValidateSpecAgainstPlan(context.Background(), PlanValidationOpts{
		PlanText: "vague idea",
		Spec: &OrchestraSpec{
			Version:  "1",
			Metadata: SpecMetadata{Title: "Test", Description: "test"},
			Phases:   []Phase{{ID: "p1", Name: "P1", Description: "d"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Pass {
		t.Error("expected Pass=true when no items extracted")
	}
	if result.CoverageScore != 1.0 {
		t.Errorf("CoverageScore = %.2f, want 1.0", result.CoverageScore)
	}
}
