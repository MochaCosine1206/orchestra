package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"gopkg.in/yaml.v3"
)

// PlanValidationOpts configures plan-to-spec validation (B-289).
type PlanValidationOpts struct {
	PlanText   string         // raw planning document text
	Spec       *OrchestraSpec // generated spec to validate
	FailClosed bool           // if true, return error on critical gaps
}

// PlanValidationResult holds the coverage analysis.
type PlanValidationResult struct {
	CoverageScore  float64               `json:"coverage_score"` // 0.0-1.0
	PlanItemsFound int                   `json:"plan_items_found"`
	SpecItemsFound int                   `json:"spec_items_found"`
	MissingItems   []PlanValidationGap   `json:"missing_items"`
	CoveredItems   []PlanValidationMatch `json:"covered_items"`
	Pass           bool                  `json:"pass"` // true if coverage >= 0.8 and no critical gaps
	Summary        string                `json:"summary"`
}

// PlanValidationGap represents an item from the plan that is missing in the spec.
type PlanValidationGap struct {
	PlanReference string `json:"plan_reference"` // quote from plan
	Category      string `json:"category"`       // "phase", "deliverable", "constraint", "requirement"
	Severity      string `json:"severity"`       // "critical", "major", "minor"
	Description   string `json:"description"`
}

// PlanValidationMatch represents an item from the plan that is covered by the spec.
type PlanValidationMatch struct {
	PlanReference string `json:"plan_reference"`
	SpecMatch     string `json:"spec_match"`    // which phase/task covers it
	MatchQuality  string `json:"match_quality"` // "exact", "partial", "inferred"
}

// --- JSON Schemas for LLM calls ---

const checklistExtractionSchema = `{
  "type": "object",
  "properties": {
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "description": { "type": "string" },
          "category": {
            "type": "string",
            "enum": ["phase", "deliverable", "constraint", "requirement"]
          },
          "importance": {
            "type": "string",
            "enum": ["critical", "major", "minor"]
          }
        },
        "required": ["id", "description", "category", "importance"]
      }
    }
  },
  "required": ["items"]
}`

const coverageScoringSchema = `{
  "type": "object",
  "properties": {
    "coverage_score": { "type": "number" },
    "plan_items_found": { "type": "integer" },
    "spec_items_found": { "type": "integer" },
    "missing_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "plan_reference": { "type": "string" },
          "category": { "type": "string" },
          "severity": { "type": "string" },
          "description": { "type": "string" }
        },
        "required": ["plan_reference", "category", "severity", "description"]
      }
    },
    "covered_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "plan_reference": { "type": "string" },
          "spec_match": { "type": "string" },
          "match_quality": { "type": "string" }
        },
        "required": ["plan_reference", "spec_match", "match_quality"]
      }
    },
    "pass": { "type": "boolean" },
    "summary": { "type": "string" }
  },
  "required": ["coverage_score", "plan_items_found", "spec_items_found", "missing_items", "covered_items", "pass", "summary"]
}`

// --- Prompts ---

const checklistExtractionPrompt = `You are a requirements analyst. Given the planning document below, extract every distinct deliverable, phase, requirement, and constraint into a checklist.

## PLANNING DOCUMENT

%s

## INSTRUCTIONS

1. Read the planning document carefully
2. Extract each distinct item that the project should produce or satisfy
3. Categorize each as: phase (a named stage of work), deliverable (a concrete output), constraint (a limitation or rule), requirement (a feature or behavior)
4. Rate importance as: critical (project fails without it), major (significant gap), minor (nice-to-have)
5. Use short, specific descriptions — quote key terms from the plan
6. Do NOT infer items not mentioned — only extract what is explicitly stated`

const coverageScoringPrompt = `You are a spec reviewer. Given a checklist of planned items and a generated spec, score how well the spec covers the plan.

## CHECKLIST (from planning document)

%s

## GENERATED SPEC

%s

## INSTRUCTIONS

1. For each checklist item, determine if it is covered by any phase or task in the spec
2. Score coverage as: exact (directly addressed), partial (somewhat addressed), inferred (implicitly covered)
3. Items with no coverage are missing — classify severity as: critical, major, or minor
4. Calculate coverage_score as (covered items / total items), where partial counts as 0.5
5. Set pass=true if coverage_score >= 0.8 AND there are no critical missing items
6. Write a brief summary of coverage gaps`

// ValidateSpecAgainstPlan runs a 2-call LLM pipeline to check spec coverage
// against the source planning document (B-289).
//
// Call 1: Extract checklist from plan (spec NOT provided — prevents anchoring)
// Call 2: Score spec against checklist
func (c *Conductor) ValidateSpecAgainstPlan(ctx context.Context, opts PlanValidationOpts) (*PlanValidationResult, error) {
	if opts.PlanText == "" {
		return nil, fmt.Errorf("plan text is required")
	}
	if opts.Spec == nil {
		return nil, fmt.Errorf("spec is required")
	}

	// Call 1: Extract checklist from plan (blind to spec)
	extractPrompt := fmt.Sprintf(checklistExtractionPrompt, opts.PlanText)

	extractResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     extractPrompt,
		Model:      agent.ModelOpus,
		JSONSchema: checklistExtractionSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("checklist extraction: %w", err)
	}

	// Parse checklist
	var checklist struct {
		Items []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Category    string `json:"category"`
			Importance  string `json:"importance"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(extractResult.Output), &checklist); err != nil {
		return nil, fmt.Errorf("parsing checklist: %w", err)
	}

	if len(checklist.Items) == 0 {
		// No items extracted — return a pass-by-default result
		return &PlanValidationResult{
			CoverageScore:  1.0,
			PlanItemsFound: 0,
			Pass:           true,
			Summary:        "No distinct plan items extracted — validation skipped",
		}, nil
	}

	// Format checklist for Call 2
	var checklistLines []string
	for _, item := range checklist.Items {
		checklistLines = append(checklistLines,
			fmt.Sprintf("- [%s] %s (%s, %s)", item.ID, item.Description, item.Category, item.Importance))
	}
	checklistText := strings.Join(checklistLines, "\n")

	// Serialize spec to YAML for the scoring call
	specYAML, err := yaml.Marshal(opts.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshaling spec for validation: %w", err)
	}

	// Call 2: Score spec against checklist
	scorePrompt := fmt.Sprintf(coverageScoringPrompt, checklistText, string(specYAML))

	scoreResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     scorePrompt,
		Model:      agent.ModelOpus,
		JSONSchema: coverageScoringSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("coverage scoring: %w", err)
	}

	// Parse result
	var result PlanValidationResult
	if err := json.Unmarshal([]byte(scoreResult.Output), &result); err != nil {
		return nil, fmt.Errorf("parsing coverage result: %w", err)
	}

	return &result, nil
}
