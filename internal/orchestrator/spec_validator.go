package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RequirementsSpecValidation holds the result of validating decomposed task specs
// against a requirements document.
type RequirementsSpecValidation struct {
	Passed      bool
	Mismatches  []SpecMismatch
	Suggestions []string
}

// SpecMismatch represents a single mismatch between a task spec and a requirement.
type SpecMismatch struct {
	TaskTitle   string
	Issue       string // e.g., "Task uses React but requirements specify Tauri"
	Severity    string // "critical", "warning", "info"
	Requirement string // the requirement that was violated
}

// specValidationLLMResult is the JSON schema for the LLM validation response.
type specValidationLLMResult struct {
	Mismatches []struct {
		TaskTitle   string `json:"task_title"`
		Issue       string `json:"issue"`
		Severity    string `json:"severity"`
		Requirement string `json:"requirement"`
	} `json:"mismatches"`
	Suggestions []string `json:"suggestions"`
}

const specValidationSchema = `{
  "type": "object",
  "properties": {
    "mismatches": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "task_title": { "type": "string" },
          "issue": { "type": "string" },
          "severity": { "type": "string", "enum": ["critical", "warning", "info"] },
          "requirement": { "type": "string" }
        },
        "required": ["task_title", "issue", "severity", "requirement"]
      }
    },
    "suggestions": {
      "type": "array",
      "items": { "type": "string" }
    }
  },
  "required": ["mismatches", "suggestions"]
}`

const specValidationPrompt = `You are a requirements validator. Compare these decomposed task specs against the requirements document and list any mismatches.

## REQUIREMENTS DOCUMENT

%s

## DECOMPOSED TASK SPECS

%s

## INSTRUCTIONS

Focus on sections 2-5 of the requirements document (Technical Architecture, Tech Stack Decision, Code Standards, Deployment Model) to extract key constraints: language, framework, database, deployment model.

For each task, check if its description and file list align with those constraints. Report mismatches with severity:
- "critical": wrong language, wrong framework, wrong database — would require full rewrite
- "warning": suboptimal choice but not fundamentally wrong
- "info": minor style or naming differences

Also provide suggestions for improving alignment.`

// ValidateSpecsAgainstRequirements checks decomposed tasks against a requirements doc.
// Returns a list of mismatches and a bool indicating if specs should be re-generated.
func ValidateSpecsAgainstRequirements(ctx context.Context, runner ClaudeRunner, tasks []DecomposedTask, requirementsPath string) (*RequirementsSpecValidation, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner is required")
	}

	// Read the requirements file
	reqBytes, err := os.ReadFile(requirementsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing requirements file — pass with a warning
			return &RequirementsSpecValidation{
				Passed:      true,
				Suggestions: []string{"Requirements file not found at " + requirementsPath + "; skipping validation"},
			}, nil
		}
		return nil, fmt.Errorf("reading requirements file: %w", err)
	}
	reqText := string(reqBytes)

	if len(tasks) == 0 {
		return &RequirementsSpecValidation{
			Passed:      true,
			Suggestions: []string{"No tasks to validate"},
		}, nil
	}

	// Format tasks for the prompt
	var taskLines []string
	for _, t := range tasks {
		files := strings.Join(t.Files, ", ")
		taskLines = append(taskLines, fmt.Sprintf("- **%s** (role: %s): %s\n  Files: [%s]",
			t.Title, t.Role, t.Description, files))
	}
	tasksText := strings.Join(taskLines, "\n")

	prompt := fmt.Sprintf(specValidationPrompt, reqText, tasksText)

	result, err := runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		JSONSchema: specValidationSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM validation call: %w", err)
	}

	var llmResult specValidationLLMResult
	if err := json.Unmarshal([]byte(result.Output), &llmResult); err != nil {
		return nil, fmt.Errorf("parsing LLM validation response: %w", err)
	}

	validation := &RequirementsSpecValidation{
		Passed:      true,
		Suggestions: llmResult.Suggestions,
	}

	for _, m := range llmResult.Mismatches {
		mismatch := SpecMismatch{
			TaskTitle:   m.TaskTitle,
			Issue:       m.Issue,
			Severity:    m.Severity,
			Requirement: m.Requirement,
		}
		validation.Mismatches = append(validation.Mismatches, mismatch)
		if m.Severity == "critical" {
			validation.Passed = false
		}
	}

	return validation, nil
}
