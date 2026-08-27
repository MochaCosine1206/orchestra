package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSpecsAgainstRequirements_Matching(t *testing.T) {
	// LLM returns no mismatches — specs align with requirements
	llmResponse := specValidationLLMResult{
		Mismatches: nil,
		Suggestions: []string{"Consider adding error handling tests"},
	}
	responseJSON, _ := json.Marshal(llmResponse)

	reqDir := t.TempDir()
	reqPath := filepath.Join(reqDir, "requirements.md")
	os.WriteFile(reqPath, []byte(`# Requirements
## 2. Technical Architecture
- Language: Go
- Framework: Cobra CLI
## 3. Tech Stack
- Database: SQLite
## 4. Code Standards
- Tests required
## 5. Deployment
- Local binary
`), 0644)

	mock := &MockRunner{Outputs: []string{string(responseJSON)}}

	tasks := []DecomposedTask{
		{Title: "Setup Go project", Role: "implementer", Description: "Initialize Go module with Cobra CLI", Files: []string{"main.go", "go.mod"}},
		{Title: "Add SQLite layer", Role: "implementer", Description: "Add SQLite database layer", Files: []string{"db.go"}},
	}

	result, err := ValidateSpecsAgainstRequirements(context.Background(), mock, tasks, reqPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true for matching specs")
	}
	if len(result.Mismatches) != 0 {
		t.Errorf("expected 0 mismatches, got %d", len(result.Mismatches))
	}
	if len(result.Suggestions) != 1 {
		t.Errorf("expected 1 suggestion, got %d", len(result.Suggestions))
	}
}

func TestValidateSpecsAgainstRequirements_MismatchedTechStack(t *testing.T) {
	// LLM returns a critical mismatch — task uses React but requirements say Tauri
	llmResponse := specValidationLLMResult{
		Mismatches: []struct {
			TaskTitle   string `json:"task_title"`
			Issue       string `json:"issue"`
			Severity    string `json:"severity"`
			Requirement string `json:"requirement"`
		}{
			{
				TaskTitle:   "Build frontend",
				Issue:       "Task uses React web app but requirements specify Tauri desktop app",
				Severity:    "critical",
				Requirement: "Framework: Tauri for desktop",
			},
		},
		Suggestions: []string{"Switch from React to Tauri"},
	}
	responseJSON, _ := json.Marshal(llmResponse)

	reqDir := t.TempDir()
	reqPath := filepath.Join(reqDir, "requirements.md")
	os.WriteFile(reqPath, []byte(`# Requirements
## 2. Technical Architecture
- Language: Rust + TypeScript
## 3. Tech Stack
- Framework: Tauri for desktop
`), 0644)

	mock := &MockRunner{Outputs: []string{string(responseJSON)}}

	tasks := []DecomposedTask{
		{Title: "Build frontend", Role: "implementer", Description: "Build React web frontend", Files: []string{"src/App.tsx"}},
	}

	result, err := ValidateSpecsAgainstRequirements(context.Background(), mock, tasks, reqPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for critical mismatch")
	}
	if len(result.Mismatches) != 1 {
		t.Errorf("expected 1 mismatch, got %d", len(result.Mismatches))
	}
	if result.Mismatches[0].Severity != "critical" {
		t.Errorf("expected severity=critical, got %s", result.Mismatches[0].Severity)
	}
}

func TestValidateSpecsAgainstRequirements_MissingRequirementsFile(t *testing.T) {
	mock := &MockRunner{}

	tasks := []DecomposedTask{
		{Title: "Some task", Role: "implementer", Description: "Do work", Files: []string{"foo.go"}},
	}

	result, err := ValidateSpecsAgainstRequirements(context.Background(), mock, tasks, "/nonexistent/requirements.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true when requirements file is missing")
	}
	if len(result.Suggestions) == 0 {
		t.Error("expected a warning suggestion about missing file")
	}
	// Should not have called the LLM
	if len(mock.Calls) != 0 {
		t.Errorf("expected 0 LLM calls, got %d", len(mock.Calls))
	}
}

func TestValidateSpecsAgainstRequirements_NilRunner(t *testing.T) {
	_, err := ValidateSpecsAgainstRequirements(context.Background(), nil, nil, "")
	if err == nil {
		t.Error("expected error for nil runner")
	}
}
