package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// RequirementsCheckResult holds the result of checking built code against
// acceptance criteria from a requirements document.
type RequirementsCheckResult struct {
	CriteriaMet     int
	CriteriaTotal   int
	CriteriaResults []CriterionResult
	PassRate        float64
}

// CriterionResult holds the evaluation of a single acceptance criterion.
type CriterionResult struct {
	Criterion string
	Met       bool
	Evidence  string // file/line that proves it, or "not found"
}

// requirementsCheckLLMResult is the JSON schema target for the LLM evaluation.
type requirementsCheckLLMResult struct {
	Results []struct {
		Criterion string `json:"criterion"`
		Met       bool   `json:"met"`
		Evidence  string `json:"evidence"`
	} `json:"results"`
}

const requirementsCheckSchema = `{
  "type": "object",
  "properties": {
    "results": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "criterion": { "type": "string" },
          "met": { "type": "boolean" },
          "evidence": { "type": "string" }
        },
        "required": ["criterion", "met", "evidence"]
      }
    }
  },
  "required": ["results"]
}`

const requirementsCheckPrompt = `You are a QA engineer. Given the acceptance criteria below and a summary of files in the project directory, determine which criteria are met.

## ACCEPTANCE CRITERIA

%s

## PROJECT FILE LISTING

%s

## INSTRUCTIONS

For each criterion:
1. Check if any file in the project provides evidence that the criterion is met
2. If met, cite the file path and what about it satisfies the criterion
3. If not met, set evidence to "not found"
4. Be conservative — only mark as met if there is concrete evidence`

// CheckRequirementsAlignment runs after a build completes and checks if the
// output meets the acceptance criteria from the requirements document.
func CheckRequirementsAlignment(ctx context.Context, runner ClaudeRunner, projectPath string, requirementsPath string) (*RequirementsCheckResult, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner is required")
	}

	// Read the requirements doc
	reqBytes, err := os.ReadFile(requirementsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &RequirementsCheckResult{
				CriteriaMet:   0,
				CriteriaTotal: 0,
				PassRate:      1.0, // no criteria = vacuous pass
			}, nil
		}
		return nil, fmt.Errorf("reading requirements file: %w", err)
	}

	// Extract acceptance criteria from "## 8. Acceptance Criteria for MVP" section
	criteria := extractAcceptanceCriteria(string(reqBytes))
	if len(criteria) == 0 {
		return &RequirementsCheckResult{
			CriteriaMet:   0,
			CriteriaTotal: 0,
			PassRate:      1.0,
		}, nil
	}

	// Build a file listing of the project
	fileListing, err := buildFileListing(projectPath)
	if err != nil {
		return nil, fmt.Errorf("listing project files: %w", err)
	}

	criteriaText := strings.Join(criteria, "\n")
	prompt := fmt.Sprintf(requirementsCheckPrompt, criteriaText, fileListing)

	result, err := runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		WorkDir:    projectPath,
		JSONSchema: requirementsCheckSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM requirements check: %w", err)
	}

	var llmResult requirementsCheckLLMResult
	if err := json.Unmarshal([]byte(result.Output), &llmResult); err != nil {
		return nil, fmt.Errorf("parsing LLM requirements check response: %w", err)
	}

	checkResult := &RequirementsCheckResult{
		CriteriaTotal: len(criteria),
	}

	for _, r := range llmResult.Results {
		cr := CriterionResult{
			Criterion: r.Criterion,
			Met:       r.Met,
			Evidence:  r.Evidence,
		}
		checkResult.CriteriaResults = append(checkResult.CriteriaResults, cr)
		if r.Met {
			checkResult.CriteriaMet++
		}
	}

	if checkResult.CriteriaTotal > 0 {
		checkResult.PassRate = float64(checkResult.CriteriaMet) / float64(checkResult.CriteriaTotal)
	}

	return checkResult, nil
}

// extractAcceptanceCriteria extracts bullet points from the
// "## 8. Acceptance Criteria for MVP" section of a requirements document.
func extractAcceptanceCriteria(doc string) []string {
	// Find the acceptance criteria section
	sectionRe := regexp.MustCompile(`(?i)##\s*8\.\s*Acceptance\s+Criteria`)
	loc := sectionRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}

	// Extract from section start to the next ## heading or end of doc
	rest := doc[loc[1]:]
	nextSection := regexp.MustCompile(`\n##\s`)
	nextLoc := nextSection.FindStringIndex(rest)
	if nextLoc != nil {
		rest = rest[:nextLoc[0]]
	}

	// Extract bullet points (lines starting with - or *)
	var criteria []string
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			criterion := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			if criterion != "" {
				criteria = append(criteria, criterion)
			}
		}
	}

	return criteria
}

// buildFileListing walks the project directory and returns a newline-separated
// list of file paths (relative to projectPath), excluding hidden dirs and common
// non-source directories.
func buildFileListing(projectPath string) (string, error) {
	var files []string
	err := walkDir(projectPath, "", &files, 0)
	if err != nil {
		return "", err
	}
	if len(files) > 500 {
		files = files[:500] // cap for prompt size
	}
	return strings.Join(files, "\n"), nil
}

// walkDir recursively lists files, skipping hidden and vendor directories.
func walkDir(base, rel string, files *[]string, depth int) error {
	if depth > 10 {
		return nil // prevent excessive recursion
	}

	dir := base
	if rel != "" {
		dir = base + "/" + rel
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	skipDirs := map[string]bool{
		"node_modules": true, "vendor": true, ".git": true,
		".orchestra": true, "__pycache__": true, "target": true,
	}

	for _, e := range entries {
		name := e.Name()
		relPath := name
		if rel != "" {
			relPath = rel + "/" + name
		}

		if e.IsDir() {
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			if err := walkDir(base, relPath, files, depth+1); err != nil {
				continue // skip unreadable dirs
			}
		} else {
			*files = append(*files, relPath)
		}
	}
	return nil
}
