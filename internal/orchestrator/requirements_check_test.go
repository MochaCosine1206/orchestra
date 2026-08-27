package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRequirementsAlignment_CriteriaMet(t *testing.T) {
	// LLM says all criteria are met
	llmResponse := requirementsCheckLLMResult{
		Results: []struct {
			Criterion string `json:"criterion"`
			Met       bool   `json:"met"`
			Evidence  string `json:"evidence"`
		}{
			{Criterion: "User can create an account", Met: true, Evidence: "auth.go:42 — CreateUser function"},
			{Criterion: "Dashboard displays stats", Met: true, Evidence: "dashboard.go:18 — RenderStats handler"},
		},
	}
	responseJSON, _ := json.Marshal(llmResponse)

	// Create a project dir with some files
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "auth.go"), []byte("package main\nfunc CreateUser() {}"), 0644)
	os.WriteFile(filepath.Join(projectDir, "dashboard.go"), []byte("package main\nfunc RenderStats() {}"), 0644)

	// Create requirements doc
	reqDir := t.TempDir()
	reqPath := filepath.Join(reqDir, "requirements.md")
	os.WriteFile(reqPath, []byte(`# Requirements
## 8. Acceptance Criteria for MVP
- User can create an account
- Dashboard displays stats
`), 0644)

	mock := &MockRunner{Outputs: []string{string(responseJSON)}}

	result, err := CheckRequirementsAlignment(context.Background(), mock, projectDir, reqPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CriteriaTotal != 2 {
		t.Errorf("expected CriteriaTotal=2, got %d", result.CriteriaTotal)
	}
	if result.CriteriaMet != 2 {
		t.Errorf("expected CriteriaMet=2, got %d", result.CriteriaMet)
	}
	if result.PassRate != 1.0 {
		t.Errorf("expected PassRate=1.0, got %f", result.PassRate)
	}
}

func TestCheckRequirementsAlignment_CriteriaNotMet(t *testing.T) {
	// LLM says 1 of 3 criteria is met
	llmResponse := requirementsCheckLLMResult{
		Results: []struct {
			Criterion string `json:"criterion"`
			Met       bool   `json:"met"`
			Evidence  string `json:"evidence"`
		}{
			{Criterion: "API returns JSON", Met: true, Evidence: "handler.go — JSON response"},
			{Criterion: "Auth with JWT", Met: false, Evidence: "not found"},
			{Criterion: "Rate limiting", Met: false, Evidence: "not found"},
		},
	}
	responseJSON, _ := json.Marshal(llmResponse)

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "handler.go"), []byte("package main"), 0644)

	reqDir := t.TempDir()
	reqPath := filepath.Join(reqDir, "requirements.md")
	os.WriteFile(reqPath, []byte(`# Requirements
## 8. Acceptance Criteria for MVP
- API returns JSON
- Auth with JWT
- Rate limiting
`), 0644)

	mock := &MockRunner{Outputs: []string{string(responseJSON)}}

	result, err := CheckRequirementsAlignment(context.Background(), mock, projectDir, reqPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CriteriaTotal != 3 {
		t.Errorf("expected CriteriaTotal=3, got %d", result.CriteriaTotal)
	}
	if result.CriteriaMet != 1 {
		t.Errorf("expected CriteriaMet=1, got %d", result.CriteriaMet)
	}
	expectedRate := 1.0 / 3.0
	if result.PassRate < expectedRate-0.01 || result.PassRate > expectedRate+0.01 {
		t.Errorf("expected PassRate ~0.333, got %f", result.PassRate)
	}
}

func TestCheckRequirementsAlignment_MissingRequirementsFile(t *testing.T) {
	mock := &MockRunner{}
	projectDir := t.TempDir()

	result, err := CheckRequirementsAlignment(context.Background(), mock, projectDir, "/nonexistent/requirements.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CriteriaTotal != 0 {
		t.Errorf("expected CriteriaTotal=0, got %d", result.CriteriaTotal)
	}
	if result.PassRate != 1.0 {
		t.Errorf("expected PassRate=1.0 (vacuous pass), got %f", result.PassRate)
	}
	// Should not have called the LLM
	if len(mock.Calls) != 0 {
		t.Errorf("expected 0 LLM calls, got %d", len(mock.Calls))
	}
}

func TestCheckRequirementsAlignment_NilRunner(t *testing.T) {
	_, err := CheckRequirementsAlignment(context.Background(), nil, "", "")
	if err == nil {
		t.Error("expected error for nil runner")
	}
}

func TestExtractAcceptanceCriteria(t *testing.T) {
	doc := `# Project Requirements
## 7. Ethical Classification
- Some ethical stuff

## 8. Acceptance Criteria for MVP
- User can log in
- Dashboard shows real-time data
* API responds within 200ms

## 9. Future Work
- Phase 2 features
`
	criteria := extractAcceptanceCriteria(doc)
	if len(criteria) != 3 {
		t.Fatalf("expected 3 criteria, got %d: %v", len(criteria), criteria)
	}
	if criteria[0] != "User can log in" {
		t.Errorf("expected first criterion 'User can log in', got %q", criteria[0])
	}
	if criteria[2] != "API responds within 200ms" {
		t.Errorf("expected third criterion 'API responds within 200ms', got %q", criteria[2])
	}
}

func TestExtractAcceptanceCriteria_NoSection(t *testing.T) {
	doc := `# Project
## 1. Overview
Some text
`
	criteria := extractAcceptanceCriteria(doc)
	if len(criteria) != 0 {
		t.Errorf("expected 0 criteria when section missing, got %d", len(criteria))
	}
}
