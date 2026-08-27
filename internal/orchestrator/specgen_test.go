package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validSpecJSON is a well-formed OrchestraSpec as JSON (matches the schema the LLM returns).
var validSpecJSON = mustMarshal(OrchestraSpec{
	Version: "1",
	Metadata: SpecMetadata{
		Title:       "Todo REST API",
		Description: "Simple REST API for todo items",
		TechStack:   map[string]string{"language": "go", "database": "sqlite"},
		Constraints: []string{"No external dependencies"},
	},
	Phases: []Phase{
		{
			ID:          "foundation",
			Name:        "Foundation",
			Description: "Project scaffolding and database",
			DependsOn:   []string{},
			Gate: PhaseGate{
				TestCmd:    "go build ./...",
				Acceptance: []string{"Project compiles"},
			},
			Tasks: []SpecTask{
				{
					Title:              "Project setup",
					Role:               "implementer",
					Files:              []string{"go.mod", "main.go"},
					Description:        "Initialize Go module and entry point",
					AcceptanceCriteria: []string{"go.mod exists", "main.go compiles"},
					DependsOn:          []string{},
				},
			},
		},
		{
			ID:          "api",
			Name:        "API Layer",
			Description: "HTTP handlers",
			DependsOn:   []string{"foundation"},
			Gate: PhaseGate{
				TestCmd:    "go test ./...",
				Acceptance: []string{"All tests pass"},
			},
			Tasks: []SpecTask{
				{
					Title:              "Todo handlers",
					Role:               "implementer",
					Files:              []string{"handlers/todo.go", "handlers/todo_test.go"},
					Description:        "Implement CRUD handlers for todos",
					AcceptanceCriteria: []string{"GET /todos returns 200", "POST /todos returns 201"},
					DependsOn:          []string{},
				},
			},
		},
	},
})

// emptyGateSpecJSON has no gate — now a warning (B-288), not an error.
var emptyGateSpecJSON = mustMarshal(OrchestraSpec{
	Version: "1",
	Metadata: SpecMetadata{
		Title:       "Content Spec",
		Description: "No gates (content project)",
	},
	Phases: []Phase{
		{
			ID:          "only-phase",
			Name:        "Only Phase",
			Description: "Has no gate",
			DependsOn:   []string{},
			Gate:        PhaseGate{}, // empty — warning, not error
			Tasks: []SpecTask{
				{
					Title:              "Some task",
					Role:               "implementer",
					Files:              []string{"file.go"},
					Description:        "Do something",
					AcceptanceCriteria: []string{"It works"},
					DependsOn:          []string{},
				},
			},
		},
	},
})

// invalidSpecJSON has missing required task fields (fails validation).
var invalidSpecJSON = mustMarshal(OrchestraSpec{
	Version: "1",
	Metadata: SpecMetadata{
		Title:       "Bad Spec",
		Description: "Missing task fields",
	},
	Phases: []Phase{
		{
			ID:          "only-phase",
			Name:        "Only Phase",
			Description: "Has incomplete task",
			DependsOn:   []string{},
			Gate:        PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
			Tasks: []SpecTask{
				{
					Title: "Incomplete task",
					// missing: Role, Files, Description, AcceptanceCriteria
				},
			},
		},
	},
})

func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// newTestConductor creates a Conductor with a MockRunner for specgen tests.
func newTestConductor(mock *MockRunner) *Conductor {
	return &Conductor{
		Runner:   mock,
		RepoRoot: ".",
		Log:      func(string) {},
	}
}

func TestGenerateSpec_ValidOutput(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := newTestConductor(mock)

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "spec.yaml")

	result, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Simple REST API for todo items in Go",
		OutputPath: outPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Spec parsed correctly
	if result.Spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if result.Spec.Metadata.Title != "Todo REST API" {
		t.Errorf("title = %q, want %q", result.Spec.Metadata.Title, "Todo REST API")
	}
	if len(result.Spec.Phases) != 2 {
		t.Errorf("phases = %d, want 2", len(result.Spec.Phases))
	}

	// Validation passes
	if !result.Validation.IsValid() {
		t.Errorf("expected valid spec, got errors: %v", result.Validation.Errors)
	}

	// YAML file written
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file not written: %v", err)
	}

	// Round-trip: parse the written YAML and verify it matches
	parsed, err := ParseSpec(outPath)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}
	if parsed.Metadata.Title != "Todo REST API" {
		t.Errorf("round-trip title = %q, want %q", parsed.Metadata.Title, "Todo REST API")
	}
	if len(parsed.Phases) != 2 {
		t.Errorf("round-trip phases = %d, want 2", len(parsed.Phases))
	}
}

func TestGenerateSpec_EmptyIdea(t *testing.T) {
	mock := &MockRunner{}
	c := newTestConductor(mock)

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea: "",
	})
	if err == nil {
		t.Fatal("expected error for empty idea")
	}
	if !strings.Contains(err.Error(), "idea is required") {
		t.Errorf("error = %q, want 'idea is required'", err.Error())
	}

	// Mock should not have been called
	if len(mock.Calls) != 0 {
		t.Errorf("expected 0 runner calls, got %d", len(mock.Calls))
	}
}

func TestGenerateSpec_RunnerFailure(t *testing.T) {
	mock := &MockRunner{
		Outputs: []string{""},
		Errors:  []error{fmt.Errorf("claude crashed")},
	}
	c := newTestConductor(mock)

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea: "Build a chat app",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "claude crashed") {
		t.Errorf("error = %q, want to contain 'claude crashed'", err.Error())
	}
}

func TestGenerateSpec_InvalidSpecOutput(t *testing.T) {
	mock := &MockRunner{Outputs: []string{invalidSpecJSON}}
	c := newTestConductor(mock)

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "spec.yaml")

	result, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build something",
		OutputPath: outPath,
	})
	if err != nil {
		t.Fatalf("should not error on invalid spec (returns result with validation): %v", err)
	}

	// Validation should fail (missing task fields)
	if result.Validation.IsValid() {
		t.Error("expected validation to fail (missing task fields)")
	}

	// YAML still written for user to fix
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file should still be written: %v", err)
	}
}

func TestGenerateSpec_EmptyGateIsWarning(t *testing.T) {
	mock := &MockRunner{Outputs: []string{emptyGateSpecJSON}}
	c := newTestConductor(mock)

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "spec.yaml")

	result, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Write a book",
		OutputPath: outPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B-288: empty gate should be warning, not error — spec is valid
	if !result.Validation.IsValid() {
		t.Errorf("expected valid spec (empty gate is warning), got errors: %v", result.Validation.Errors)
	}
	if len(result.Validation.Warnings) == 0 {
		t.Error("expected at least one warning for empty gate")
	}
}

func TestGenerateSpec_PromptContainsIdea(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := newTestConductor(mock)

	tmpDir := t.TempDir()

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build a billing microservice with Stripe integration",
		OutputPath: filepath.Join(tmpDir, "spec.yaml"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}

	prompt := mock.Calls[0].Prompt
	if !strings.Contains(prompt, "billing microservice with Stripe integration") {
		t.Error("prompt should contain the idea text")
	}
	// Should contain the example spec reference
	if !strings.Contains(prompt, "EXAMPLE SPEC") {
		t.Error("prompt should contain EXAMPLE SPEC section")
	}
}

func TestGenerateSpec_PromptContainsTechStack(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := newTestConductor(mock)

	tmpDir := t.TempDir()

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build a web app",
		OutputPath: filepath.Join(tmpDir, "spec.yaml"),
		TechStack:  map[string]string{"language": "go", "framework": "gin", "database": "postgres"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := mock.Calls[0].Prompt
	if !strings.Contains(prompt, "TECH STACK") {
		t.Error("prompt should contain TECH STACK section when tech stack provided")
	}
	if !strings.Contains(prompt, "go") {
		t.Error("prompt should contain language from tech stack")
	}
	if !strings.Contains(prompt, "gin") {
		t.Error("prompt should contain framework from tech stack")
	}
}

func TestGenerateSpec_DefaultOutputPath(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	// Use a temp dir as RepoRoot so the default "spec.yaml" writes there
	tmpDir := t.TempDir()
	c := &Conductor{
		Runner:   mock,
		RepoRoot: tmpDir,
		Log:      func(string) {},
	}

	result, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea: "Build something cool",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Default output path should be "spec.yaml" relative to RepoRoot
	expected := filepath.Join(tmpDir, "spec.yaml")
	if result.OutputPath != expected {
		t.Errorf("output path = %q, want %q", result.OutputPath, expected)
	}

	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("default output file not written: %v", err)
	}
}

func TestGenerateSpec_MalformedJSON(t *testing.T) {
	mock := &MockRunner{Outputs: []string{"this is not json at all {{{}"}}
	c := newTestConductor(mock)

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea: "Build something",
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "unmarshal") && !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error = %q, want JSON parsing error", err.Error())
	}
}

// --- Content-Aware Spec Generation Tests ---

func TestGenerateSpec_ContentRolesInSchema(t *testing.T) {
	// A spec with editor role should parse without error through GenerateSpec
	contentSpec := mustMarshal(OrchestraSpec{
		Version: "1",
		Metadata: SpecMetadata{
			Title:       "Developer Psychology Book",
			Description: "A book about developer psychology",
		},
		Phases: []Phase{
			{
				ID:          "research",
				Name:        "Research",
				Description: "Gather source material",
				DependsOn:   []string{},
				Gate: PhaseGate{
					TestCmd:    "bash -c '[ $(ls research/*.md 2>/dev/null | wc -l) -ge 2 ]'",
					Acceptance: []string{"Research docs produced"},
				},
				Tasks: []SpecTask{
					{
						Title:              "Literature survey",
						Role:               "researcher",
						Files:              []string{"research/cognitive-load.md"},
						Description:        "Survey cognitive load literature",
						AcceptanceCriteria: []string{"5+ studies cited"},
					},
				},
			},
			{
				ID:          "developmental-edit",
				Name:        "Developmental Edit",
				Description: "Review all chapters for coherence",
				DependsOn:   []string{"research"},
				Gate: PhaseGate{
					TestCmd:    "bash -c '[ -f chapters/01.md ]'",
					Acceptance: []string{"Chapters reviewed"},
				},
				Tasks: []SpecTask{
					{
						Title:              "Edit chapters 1-3",
						Role:               "editor",
						Files:              []string{"chapters/01.md", "chapters/02.md", "chapters/03.md"},
						Description:        "Developmental edit of first three chapters",
						AcceptanceCriteria: []string{"Arguments follow logical progression", "Citations verified"},
					},
				},
			},
		},
	})

	mock := &MockRunner{Outputs: []string{contentSpec}}
	c := newTestConductor(mock)
	tmpDir := t.TempDir()

	result, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Write a book about developer psychology",
		OutputPath: filepath.Join(tmpDir, "spec.yaml"),
	})
	if err != nil {
		t.Fatalf("unexpected error parsing content spec with editor role: %v", err)
	}

	// Should validate without errors (editor is a valid role)
	if !result.Validation.IsValid() {
		t.Errorf("content spec with editor role should be valid, got errors: %v", result.Validation.Errors)
	}
}

func TestGenerateSpec_PromptContainsBothExamples(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := newTestConductor(mock)
	tmpDir := t.TempDir()

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build a REST API",
		OutputPath: filepath.Join(tmpDir, "spec.yaml"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := mock.Calls[0].Prompt
	if !strings.Contains(prompt, "Code Project Example") {
		t.Error("prompt should contain Code Project Example section")
	}
	if !strings.Contains(prompt, "Content/Book Project Example") {
		t.Error("prompt should contain Content/Book Project Example section")
	}
}

func TestGenerateSpec_PromptContainsContentRules(t *testing.T) {
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := newTestConductor(mock)
	tmpDir := t.TempDir()

	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build a web app",
		OutputPath: filepath.Join(tmpDir, "spec.yaml"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := mock.Calls[0].Prompt
	// Universal rules
	if !strings.Contains(prompt, "Universal Rules") {
		t.Error("prompt should contain Universal Rules section")
	}
	// Code project rules
	if !strings.Contains(prompt, "Code Project Rules") {
		t.Error("prompt should contain Code Project Rules section")
	}
	// Content project rules
	if !strings.Contains(prompt, "Content Project Rules") {
		t.Error("prompt should contain Content Project Rules section")
	}
	// Content-specific directives
	if !strings.Contains(prompt, "research") {
		t.Error("prompt should mention research phase requirement for content projects")
	}
	if !strings.Contains(prompt, "editor") {
		t.Error("prompt should mention editor role for content projects")
	}
	if !strings.Contains(prompt, "illustrator") {
		t.Error("prompt should mention illustrator role for content projects")
	}
}

func TestGenerateSpec_FileRefExpansion(t *testing.T) {
	// Create a temp file to reference — path is relative to RepoRoot
	tmpDir := t.TempDir()
	refFile := filepath.Join(tmpDir, "notes.md")
	if err := os.WriteFile(refFile, []byte("Build a 12-chapter textbook about HITL patterns"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := &Conductor{
		Runner:   mock,
		RepoRoot: tmpDir,
		Log:      func(string) {},
	}

	outPath := filepath.Join(tmpDir, "spec.yaml")
	// Use relative path (expandFileReferences joins repoRoot + relPath)
	_, err := c.GenerateSpec(context.Background(), GenerateSpecOpts{
		Idea:       "Build textbook based on @notes.md",
		OutputPath: outPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := mock.Calls[0].Prompt
	// The @file reference should have been expanded to include file contents
	if !strings.Contains(prompt, "12-chapter textbook about HITL patterns") {
		t.Error("prompt should contain expanded @file contents")
	}
}

// --- GenerateSpecWithValidation (correction loop) tests ---

// mockChecklist returns a valid checklist extraction JSON with N items.
func mockChecklist(n int) string {
	type item struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Importance  string `json:"importance"`
	}
	var items []item
	for i := 1; i <= n; i++ {
		items = append(items, item{
			ID:          fmt.Sprintf("item-%d", i),
			Description: fmt.Sprintf("Plan item %d", i),
			Category:    "deliverable",
			Importance:  "critical",
		})
	}
	b, _ := json.Marshal(map[string]interface{}{"items": items})
	return string(b)
}

// mockCoverage returns a valid coverage scoring JSON.
func mockCoverage(score float64, pass bool, planItems, specItems int, missingItems []PlanValidationGap) string {
	type coverageMatch struct {
		PlanReference string `json:"plan_reference"`
		SpecMatch     string `json:"spec_match"`
		MatchQuality  string `json:"match_quality"`
	}

	type coverageMissing struct {
		PlanReference string `json:"plan_reference"`
		Category      string `json:"category"`
		Severity      string `json:"severity"`
		Description   string `json:"description"`
	}

	var missing []coverageMissing
	for _, g := range missingItems {
		missing = append(missing, coverageMissing{
			PlanReference: g.PlanReference,
			Category:      g.Category,
			Severity:      g.Severity,
			Description:   g.Description,
		})
	}
	if missing == nil {
		missing = []coverageMissing{}
	}

	result := map[string]interface{}{
		"coverage_score":  score,
		"plan_items_found": planItems,
		"spec_items_found": specItems,
		"missing_items":   missing,
		"covered_items":   []coverageMatch{},
		"pass":            pass,
		"summary":         fmt.Sprintf("Coverage: %.0f%%", score*100),
	}
	b, _ := json.Marshal(result)
	return string(b)
}

func TestGenerateSpecWithValidation_PassesFirstTry(t *testing.T) {
	// Sequence: gen(1) → checklist(2) → coverage passing(3) = 3 calls total
	mock := &MockRunner{Outputs: []string{
		validSpecJSON,                                   // gen call
		mockChecklist(5),                                // checklist extraction
		mockCoverage(0.95, true, 5, 5, nil),             // coverage scoring — pass
	}}
	c := &Conductor{
		Runner:   mock,
		RepoRoot: t.TempDir(),
		Log:      func(string) {},
	}

	result, valResult, err := c.GenerateSpecWithValidation(context.Background(), GenerateSpecOpts{
		Idea:       strings.Repeat("Long planning document content. ", 20), // >200 chars
		OutputPath: filepath.Join(c.RepoRoot, "spec.yaml"),
	}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if valResult == nil {
		t.Fatal("expected non-nil valResult")
	}
	if !valResult.Pass {
		t.Error("expected valResult.Pass = true")
	}
	// Should be exactly 3 calls: 1 gen + 2 validation
	if len(mock.Calls) != 3 {
		t.Errorf("expected 3 runner calls, got %d", len(mock.Calls))
	}
}

func TestGenerateSpecWithValidation_CorrectOnSecondAttempt(t *testing.T) {
	gaps := []PlanValidationGap{
		{PlanReference: "Chapter 3", Category: "deliverable", Severity: "critical", Description: "Missing chapter 3"},
	}

	// Sequence: gen1(1) → checklist1(2) → coverage1 fail(3) → gen2(4) → checklist2(5) → coverage2 pass(6)
	mock := &MockRunner{Outputs: []string{
		validSpecJSON,                                       // gen1
		mockChecklist(5),                                    // checklist1
		mockCoverage(0.60, false, 5, 3, gaps),               // coverage1 — fail
		validSpecJSON,                                       // gen2
		mockChecklist(5),                                    // checklist2
		mockCoverage(0.95, true, 5, 5, nil),                 // coverage2 — pass
	}}
	c := &Conductor{
		Runner:   mock,
		RepoRoot: t.TempDir(),
		Log:      func(string) {},
	}

	result, valResult, err := c.GenerateSpecWithValidation(context.Background(), GenerateSpecOpts{
		Idea:       strings.Repeat("Long planning document content. ", 20),
		OutputPath: filepath.Join(c.RepoRoot, "spec.yaml"),
	}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || valResult == nil {
		t.Fatal("expected non-nil result and valResult")
	}
	if !valResult.Pass {
		t.Error("expected pass on second attempt")
	}
	// Should be exactly 6 calls: 2 iterations × 3 calls each
	if len(mock.Calls) != 6 {
		t.Errorf("expected 6 runner calls, got %d", len(mock.Calls))
	}
	// Verify gap feedback was injected into second generation prompt
	gen2Prompt := mock.Calls[3].Prompt
	if !strings.Contains(gen2Prompt, "PRIOR VALIDATION GAPS") {
		t.Error("second generation prompt should contain PRIOR VALIDATION GAPS section")
	}
	if !strings.Contains(gen2Prompt, "Missing chapter 3") {
		t.Error("second generation prompt should contain the gap description")
	}
}

func TestGenerateSpecWithValidation_MaxIterations(t *testing.T) {
	gaps := []PlanValidationGap{
		{PlanReference: "Auth system", Category: "phase", Severity: "critical", Description: "No auth phase"},
	}

	// All 3 iterations fail — 9 calls total
	mock := &MockRunner{Outputs: []string{
		validSpecJSON,
		mockChecklist(5),
		mockCoverage(0.50, false, 5, 2, gaps),
		validSpecJSON,
		mockChecklist(5),
		mockCoverage(0.65, false, 5, 3, gaps),
		validSpecJSON,
		mockChecklist(5),
		mockCoverage(0.70, false, 5, 3, gaps), // best score = 0.70
	}}
	c := &Conductor{
		Runner:   mock,
		RepoRoot: t.TempDir(),
		Log:      func(string) {},
	}

	result, valResult, err := c.GenerateSpecWithValidation(context.Background(), GenerateSpecOpts{
		Idea:       strings.Repeat("Long planning document content. ", 20),
		OutputPath: filepath.Join(c.RepoRoot, "spec.yaml"),
	}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (best-effort)")
	}
	if valResult == nil {
		t.Fatal("expected non-nil valResult")
	}
	// Should return the best result (0.70 coverage)
	if valResult.CoverageScore != 0.70 {
		t.Errorf("expected best coverage 0.70, got %.2f", valResult.CoverageScore)
	}
	// Should NOT pass
	if valResult.Pass {
		t.Error("expected valResult.Pass = false (never passed)")
	}
	// Exactly 9 calls: 3 iterations × 3 calls each
	if len(mock.Calls) != 9 {
		t.Errorf("expected 9 runner calls, got %d", len(mock.Calls))
	}
}

func TestGenerateSpecWithValidation_NoSourcePlanText(t *testing.T) {
	// Short idea — validation should be skipped entirely
	mock := &MockRunner{Outputs: []string{validSpecJSON}}
	c := &Conductor{
		Runner:   mock,
		RepoRoot: t.TempDir(),
		Log:      func(string) {},
	}

	result, valResult, err := c.GenerateSpecWithValidation(context.Background(), GenerateSpecOpts{
		Idea:       "Build a REST API", // short, <200 chars
		OutputPath: filepath.Join(c.RepoRoot, "spec.yaml"),
	}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if valResult != nil {
		t.Error("expected nil valResult when idea is short")
	}
	// Only 1 call: the generation call (no validation)
	if len(mock.Calls) != 1 {
		t.Errorf("expected 1 runner call, got %d", len(mock.Calls))
	}
}

func TestValidateSpec_EditorIllustratorRoles(t *testing.T) {
	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Content Project", Description: "test"},
		Phases: []Phase{
			{
				ID: "edit", Name: "Edit", DependsOn: []string{},
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{
					{
						Title: "Dev edit", Role: "editor",
						Files: []string{"ch1.md"}, Description: "edit chapter 1",
						AcceptanceCriteria: []string{"chapter revised"},
					},
					{
						Title: "Illustrations", Role: "illustrator",
						Files: []string{"figures/fig1.svg"}, Description: "create figure 1",
						AcceptanceCriteria: []string{"figure created"},
					},
				},
			},
		},
	}
	v := ValidateSpec(spec)
	if !v.IsValid() {
		t.Errorf("spec with editor and illustrator roles should be valid, got errors: %v", v.Errors)
	}
}
