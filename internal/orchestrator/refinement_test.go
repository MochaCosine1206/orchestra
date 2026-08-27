package orchestrator

import (
	"strings"
	"testing"
)

func TestRefinementSpec_Heal(t *testing.T) {
	original := "# Task: Build the widget\n\nImplement a widget that does X."
	errors := []ErrorEnvelope{
		{
			ErrorType:     "missing_import",
			FilesAffected: []string{"widget.go"},
			LineNumbers:   []int{5},
			FixHint:       "Add missing import for fmt package",
		},
		{
			ErrorType:     "type_error",
			FilesAffected: []string{"widget.go"},
			LineNumbers:   []int{12},
			FixHint:       "Cannot use int as string",
		},
	}

	result := BuildRefinementSpec(original, errors, TriageHeal)

	if !strings.Contains(result, original) {
		t.Error("result should contain original spec")
	}
	if !strings.Contains(result, "## Fix Required") {
		t.Error("HEAL spec should contain '## Fix Required' section")
	}
	if !strings.Contains(result, "missing_import") {
		t.Error("HEAL spec should mention error types")
	}
	if !strings.Contains(result, "widget.go") {
		t.Error("HEAL spec should mention affected files")
	}
	if !strings.Contains(result, "line 5") {
		t.Error("HEAL spec should mention line numbers")
	}
}

func TestRefinementSpec_Refine(t *testing.T) {
	original := "# Task: Implement auth\n\nBuild authentication middleware."
	errors := []ErrorEnvelope{
		{
			ErrorType: "test_failure",
			RawOutput: "--- FAIL: TestAuth (0.01s)\n    auth_test.go:42: expected 200, got 401",
			FixHint:   "Test TestAuth failed - fix the auth handler to return 200",
		},
	}

	result := BuildRefinementSpec(original, errors, TriageRefine)

	if !strings.Contains(result, original) {
		t.Error("result should contain original spec")
	}
	if !strings.Contains(result, "## Previous Attempt Failed") {
		t.Error("REFINE spec should contain '## Previous Attempt Failed' section")
	}
	if !strings.Contains(result, "keeping the working parts") {
		t.Error("REFINE spec should instruct to keep working parts")
	}
	if !strings.Contains(result, "FAIL: TestAuth") {
		t.Error("REFINE spec should include raw error output")
	}
	if !strings.Contains(result, "### Fix Hints") {
		t.Error("REFINE spec should include fix hints section")
	}
}

func TestRefinementSpec_Redo(t *testing.T) {
	original := "# Task: Design the API\n\nCreate REST endpoints."
	errors := []ErrorEnvelope{
		{
			ErrorType: "build_error",
			RawOutput: "tons of errors",
		},
	}

	result := BuildRefinementSpec(original, errors, TriageRedo)

	if !strings.Contains(result, original) {
		t.Error("result should contain original spec")
	}
	if !strings.Contains(result, "## IMPORTANT: Previous attempt was fundamentally wrong. Start fresh.") {
		t.Error("REDO spec should contain fresh start instruction")
	}
	if !strings.Contains(result, "implement from scratch") {
		t.Error("REDO spec should instruct to implement from scratch")
	}
	// REDO should NOT contain error details (fresh start)
	if strings.Contains(result, "tons of errors") {
		t.Error("REDO spec should NOT include raw error output")
	}
}

func TestRefinementSpec_PreservesOriginal(t *testing.T) {
	original := "Keep this exact content unchanged."
	for _, triage := range []TriageOutcome{TriageHeal, TriageRefine, TriageRedo} {
		result := BuildRefinementSpec(original, nil, triage)
		if !strings.HasPrefix(result, original) {
			t.Errorf("triage %s: result should start with original spec", triage)
		}
	}
}

func TestRefinementSpec_ErrorTruncation(t *testing.T) {
	original := "# Task"
	// Create 10 errors — only 5 should appear in output
	var errors []ErrorEnvelope
	for i := 0; i < 10; i++ {
		errors = append(errors, ErrorEnvelope{
			ErrorType:     "build_error",
			FilesAffected: []string{"file.go"},
			FixHint:       "fix something",
			RawOutput:     "some error",
		})
	}

	healResult := BuildRefinementSpec(original, errors, TriageHeal)
	if !strings.Contains(healResult, "5 more errors") {
		t.Error("HEAL should truncate at 5 errors and show count of remaining")
	}

	refineResult := BuildRefinementSpec(original, errors, TriageRefine)
	if !strings.Contains(refineResult, "5 more errors") {
		t.Error("REFINE should truncate at 5 errors and show count of remaining")
	}
}

func TestRefinementSpec_LongHintTruncation(t *testing.T) {
	original := "# Task"
	longHint := strings.Repeat("x", 300)
	errors := []ErrorEnvelope{
		{
			ErrorType: "build_error",
			FixHint:   longHint,
			RawOutput: longHint,
		},
	}

	result := BuildRefinementSpec(original, errors, TriageHeal)
	// The hint should be truncated to 200 chars
	if strings.Contains(result, longHint) {
		t.Error("long hints should be truncated")
	}
	if !strings.Contains(result, "...") {
		t.Error("truncated content should end with ...")
	}
}
