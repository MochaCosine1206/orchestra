package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/assets"
	"gopkg.in/yaml.v3"
)

// GenerateSpecOpts configures a spec generation invocation.
type GenerateSpecOpts struct {
	Idea                string              // required
	TechStack           map[string]string   // optional: language, framework, database, testing
	Constraints         []string            // optional
	OutputPath          string              // default: "spec.yaml" relative to RepoRoot
	RepoContext         bool                // include repo map in prompt
	FailClosed          bool                // B-289: return error if critical gaps remain after all iterations
	PriorGaps           []PlanValidationGap // gaps from prior validation (correction loop)
	PriorCoveredSummary string              // what was covered (prevent regression)
}

// GenerateSpecResult captures the outcome of spec generation.
type GenerateSpecResult struct {
	Spec           *OrchestraSpec
	Validation     SpecValidation
	OutputPath     string
	Reasoning      string
	SourcePlanText string // B-289: expanded idea text for plan-to-spec validation
}

// generateSpecJSONSchema enforces all required fields in spec generation output.
const generateSpecJSONSchema = `{
  "type": "object",
  "properties": {
    "version": { "type": "string" },
    "metadata": {
      "type": "object",
      "properties": {
        "title": { "type": "string" },
        "description": { "type": "string" },
        "tech_stack": {
          "type": "object",
          "additionalProperties": { "type": "string" }
        },
        "constraints": {
          "type": "array",
          "items": { "type": "string" }
        }
      },
      "required": ["title", "description"]
    },
    "phases": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "name": { "type": "string" },
          "description": { "type": "string" },
          "depends_on": {
            "type": "array",
            "items": { "type": "string" }
          },
          "gate": {
            "type": "object",
            "properties": {
              "mode": { "type": "string", "enum": ["test", "acceptance", "none"] },
              "test_cmd": { "type": "string" },
              "acceptance": {
                "type": "array",
                "items": { "type": "string" }
              }
            },
            "additionalProperties": false
          },
          "tasks": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "title": { "type": "string" },
                "role": {
                  "type": "string",
                  "enum": ["implementer", "researcher", "deep-researcher", "architect", "reviewer", "scout", "editor", "illustrator"]
                },
                "files": {
                  "type": "array",
                  "items": { "type": "string" }
                },
                "description": { "type": "string" },
                "acceptance_criteria": {
                  "type": "array",
                  "items": { "type": "string" }
                },
                "depends_on": {
                  "type": "array",
                  "items": { "type": "string" }
                }
              },
              "required": ["title", "role", "files", "description", "acceptance_criteria"]
            }
          }
        },
        "required": ["id", "name", "description", "depends_on", "gate", "tasks"]
      }
    }
  },
  "required": ["version", "metadata", "phases"]
}`

const generateSpecPrompt = `You are a project architect generating a phased build spec for Claude Orchestra.
Your output must be a JSON object conforming to the OrchestraSpec schema.

## IDEA

%s

%s%s%s%s%s%s
## RULES

### Universal Rules (all project types)

1. Set version to "1"
2. Phase IDs must be lowercase letters, numbers, and hyphens only (e.g. "foundation", "research", "content")
3. First phase should have depends_on: [] — subsequent phases depend on prior phases
4. Every phase SHOULD have a gate. Code phases MUST have a concrete test_cmd. Content phases may use empty test_cmd (gate auto-passes) but should still have 1-3 acceptance criteria for human review
5. Files must be exclusive within a phase — no file assigned to multiple tasks in the same phase
6. Each task must have 2-5 specific, testable acceptance_criteria
7. Acceptance criteria describe concrete, verifiable outcomes — never use vague words like "verify" or "ensure" without specifying what observable change to make
8. Task descriptions should say WHAT to produce, not HOW — let the agent investigate the best approach
9. Constraints from the CONSTRAINTS section (if any) must be respected in all tasks

### Code Project Rules (software, APIs, CLIs, libraries, tools)

Use these rules when the idea describes building software:

10. Create 2-5 phases, each with 1-4 tasks (3-12 total tasks)
11. Assign roles: implementer (code changes), researcher (investigation), architect (design), reviewer (quality), scout (exploration), pi-implementer (bounded local-LLM tasks)
12. Gates should use build/test commands (e.g. "go test ./...", "npm test", "make build")
13. Typical phase pattern: foundation → core-logic → api/integration → testing/polish
14. The FINAL phase gate MUST include integration/E2E tests (Playwright, Cypress, or equivalent) — not just unit tests. Unit tests verify functions in isolation; integration tests verify layers communicate correctly. Also set metadata.final_e2e_cmd to this same command
15. Every task description must use concrete action verbs: "add", "create", "modify", "replace". Never "ensure", "verify", or "confirm" as the primary verb — agents interpret these as "read and move on" rather than "implement"
16. Every data structure that crosses a boundary between two tasks (e.g., backend API response consumed by frontend) must have the EXACT type definition written inline in BOTH tasks as a contract. Not a reference to a doc — the literal interface/struct definition. This prevents field name mismatches
17. No stubs: every route, view, endpoint, and command declared in any task must have a working implementation. No placeholder text ("Coming soon"), no TODO comments. If a feature cannot be fully implemented, remove it from declarations entirely
18. Error path acceptance criteria: every task involving network calls, LLM inference, or external services must include at least one acceptance criterion for the failure case (e.g., "When service is unreachable, display error toast within 2 seconds — no infinite spinner")
19. Fallback values must reference resources guaranteed to exist at runtime. If a default model or config may not exist, the feature must show an error message, not fail silently
20. If the PRE-BUILT RESOURCES section lists files, task descriptions must say "Read and use resources/X" — not "Create X". Agents must use existing assets, not regenerate them
21. Persistence round-trip: any value saved during setup/onboarding must have acceptance criteria verifying it can be read back by downstream features, using the SAME key names
22. Every view/route declared in the routing system must have a corresponding task that creates the real implementation component. After generating the spec, verify: no declared route is missing an implementing task
23. Acceptance criteria must be machine-verifiable in under 30 seconds. Bad: "User can manage settings." Good: "Settings page displays a dropdown populated with installed models; selecting one persists to settings.selected_model; reloading shows the selection"
24. Maximum 150 lines of task-specific context per task — inject only the relevant contracts, file listings, and acceptance criteria, not the entire project spec. Longer context reduces agent success rates

### Content Project Rules (books, textbooks, courses, curricula, documentation sets)

Use these rules when the idea describes producing written content (books, chapters, lessons, courses, educational materials):

25. Create 4-8 phases with 20-50 total tasks
26. First phase MUST be "research" using deep-researcher role — all source material and frameworks gathered before any writing. Deep-researcher searches recursively until information saturation (not just one pass)
27. Include a "specification" phase (architect role) after research — outlines, chapter plans, learning objectives
28. Content phases use implementer role — one task per chapter/lesson, max 3 files per task
29. Include at least one editorial phase (editor role) — developmental editing, consistency checks, polish
30. If the idea mentions figures, diagrams, or illustrations, include an illustrator phase (illustrator role)
31. All 8 roles available: implementer, researcher, deep-researcher, architect, reviewer, scout, editor, illustrator. DEFAULT to deep-researcher for all research tasks in content projects — it searches recursively until saturation. Only use researcher for quick, targeted lookups within other phases (e.g., an architect checking a single fact)
32. Gates for content phases use file-existence checks: bash -c '[ $(ls chapters/*.md 2>/dev/null | wc -l) -ge N ]'
33. Research tasks should specify output files (e.g. "research/topic-name.md") and acceptance criteria requiring specific findings
34. Content tasks should include word count constraints (e.g. "Chapter is 4000-6000 words") and citation/reference requirements where applicable
35. Typical phase pattern: research → specification → content → figures (if needed) → developmental-edit → polish-edit
36. If the planning document explicitly names phases, branches, or sections that map to distinct work streams, generate a phase for EACH one. Do not merge or skip any. The REQUIRED PHASES section (if present) is authoritative
37. Every content task description MUST begin with "FIRST: Read specs/style-guide.md (or equivalent style/template file) and [relevant spec files]." Agents that skip reading specifications produce generic, inconsistent output
38. Every edit task description MUST begin with "FIRST: Read all chapters sequentially for full arc context, then edit the assigned chapters." Editors need the full picture before making changes
39. File paths must be flat: "chapters/ch-01.md", NOT "chapters/ch01/content.md" or "chapters/01-chapter-name.md". Consistent kebab-case with zero-padded numbers. The gate test_cmd glob pattern must match the actual file naming convention
40. Edit phases MUST include word count buffers in acceptance criteria: "Each chapter maintains or increases its word count (tolerance: -50 words max)." Without this, editors silently reduce content
41. Build phases MUST include the actual build command in the gate test_cmd (e.g. "make book && make epub"), not just file-existence checks. The agent should set up the pipeline AND run it
42. If the project uses Plyne Paged Design (pagedjs-cli), the build task description must specify: pagedjs-cli for PDF rendering, Pandoc for EPUB, and the exact CSS stack (theme → override → variant)
43. Shell scripts generated by agents must pass syntax validation: "bash -n script.sh" should be in acceptance criteria for any task that produces shell scripts
`

// GenerateSpec calls an LLM to produce an OrchestraSpec from a high-level idea.
func (c *Conductor) GenerateSpec(ctx context.Context, opts GenerateSpecOpts) (*GenerateSpecResult, error) {
	if opts.Idea == "" {
		return nil, fmt.Errorf("idea is required")
	}

	// Expand @file references with no size limits — spec generation needs full file fidelity
	idea := expandFileReferencesUnlimited(opts.Idea, c.RepoRoot)

	// Resolve output path
	outPath := opts.OutputPath
	if outPath == "" {
		outPath = filepath.Join(c.RepoRoot, "spec.yaml")
	} else if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(c.RepoRoot, outPath)
	}

	// Build optional prompt sections
	var techStackSection, repoContextSection, constraintsSection string

	if len(opts.TechStack) > 0 {
		var lines []string
		// Sort keys for deterministic output
		keys := make([]string, 0, len(opts.TechStack))
		for k := range opts.TechStack {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("- %s: %s", k, opts.TechStack[k]))
		}
		techStackSection = fmt.Sprintf("## TECH STACK\n\n%s\n\n", strings.Join(lines, "\n"))
	}

	if opts.RepoContext {
		repoMap, err := agent.GenerateRepoMap(c.RepoRoot, 4000)
		if err != nil {
			c.log("repo map generation failed (continuing without): %v", err)
		} else if repoMap != "" {
			repoContextSection = fmt.Sprintf("## CODEBASE CONTEXT\n\n```\n%s```\n\n", repoMap)
		}
	}

	if len(opts.Constraints) > 0 {
		var lines []string
		for _, con := range opts.Constraints {
			lines = append(lines, fmt.Sprintf("- %s", con))
		}
		constraintsSection = fmt.Sprintf("## CONSTRAINTS\n\n%s\n\n", strings.Join(lines, "\n"))
	}

	// Inject REQUIREMENTS.md as a constraint document if it exists in the project root.
	// The requirements doc is authoritative — every section should map to at least one task.
	var requirementsSection string
	reqPath := filepath.Join(c.RepoRoot, "REQUIREMENTS.md")
	if reqData, err := os.ReadFile(reqPath); err == nil && len(reqData) > 0 {
		// Truncate to 30K to fit context
		reqText := string(reqData)
		if len(reqText) > 30000 {
			reqText = reqText[:30000] + "\n\n[... truncated ...]"
		}
		requirementsSection = fmt.Sprintf("## REQUIREMENTS DOCUMENT (authoritative — every section must map to at least one task)\n\n"+
			"The following requirements document was provided. Treat it as the PRIMARY source of truth for:\n"+
			"- What features to build (map each to a task)\n"+
			"- Acceptance criteria (pull directly from the doc)\n"+
			"- Dev tooling setup (linting, testing, pre-commit hooks — these MUST be Phase 1 tasks)\n"+
			"- Tech stack details (specific versions, libraries, configurations)\n"+
			"- Constraints and compliance requirements\n\n"+
			"```\n%s\n```\n\n", reqText)
	}

	// Inject pre-built resources directory listing if it exists.
	// Tells agents to USE existing files instead of generating equivalents.
	var resourcesSection string
	resourcesDir := filepath.Join(c.RepoRoot, "resources")
	if info, statErr := os.Stat(resourcesDir); statErr == nil && info.IsDir() {
		if entries, readErr := os.ReadDir(resourcesDir); readErr == nil && len(entries) > 0 {
			var resourceLines []string
			totalFiles := 0
			for _, entry := range entries {
				if entry.IsDir() {
					subEntries, _ := os.ReadDir(filepath.Join(resourcesDir, entry.Name()))
					resourceLines = append(resourceLines, fmt.Sprintf("- resources/%s/ (%d files)", entry.Name(), len(subEntries)))
					totalFiles += len(subEntries)
				} else {
					resourceLines = append(resourceLines, fmt.Sprintf("- resources/%s", entry.Name()))
					totalFiles++
				}
			}
			resourcesSection = fmt.Sprintf("## PRE-BUILT RESOURCES (%d files — tasks MUST use these, do NOT regenerate)\n\n"+
				"The project includes pre-built resource files. Task descriptions should say "+
				"\"Read and use resources/X\" not \"Create X\".\n\n%s\n\n",
				totalFiles, strings.Join(resourceLines, "\n"))
		}
	}

	// B-289: Extract phase hints from the planning document and inject them
	var phaseHintsSection string
	if hints := extractPhaseHints(idea); len(hints) > 0 {
		var lines []string
		for i, h := range hints {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, h))
		}
		phaseHintsSection = fmt.Sprintf("## REQUIRED PHASES (extracted from planning document)\n\n"+
			"You MUST generate a phase for each of these. Do not skip or merge any.\n\n"+
			"%s\n\n", strings.Join(lines, "\n"))
	}

	// Load example specs
	codeExample, err := assets.ExampleSpec("billing-api-spec.yaml")
	if err != nil {
		c.log("code example spec load failed (continuing without): %v", err)
	}
	contentExample, err := assets.ExampleSpec("content-book-spec.yaml")
	if err != nil {
		c.log("content example spec load failed (continuing without): %v", err)
	}

	// Build the example spec section if available
	var exampleSection string
	var exampleParts []string
	if len(codeExample) > 0 {
		exampleParts = append(exampleParts, fmt.Sprintf("### Code Project Example\n\n```yaml\n%s```", string(codeExample)))
	}
	if len(contentExample) > 0 {
		exampleParts = append(exampleParts, fmt.Sprintf("### Content/Book Project Example\n\n```yaml\n%s```", string(contentExample)))
	}
	if len(exampleParts) > 0 {
		exampleSection = fmt.Sprintf("## EXAMPLE SPECS\n\nBelow are examples of well-formed specs (YAML representation of the JSON you should produce):\n\n%s\n\n", strings.Join(exampleParts, "\n\n"))
	}

	prompt := fmt.Sprintf(generateSpecPrompt,
		idea,
		techStackSection,
		constraintsSection,
		repoContextSection,
		phaseHintsSection,
		requirementsSection,
		resourcesSection,
	)

	// Insert gap feedback from prior validation (correction loop)
	if len(opts.PriorGaps) > 0 {
		var gapLines []string
		for _, gap := range opts.PriorGaps {
			gapLines = append(gapLines, fmt.Sprintf("- [%s/%s] %s: %s",
				gap.Category, gap.Severity, gap.PlanReference, gap.Description))
		}
		gapSection := fmt.Sprintf("## PRIOR VALIDATION GAPS\n\n"+
			"A previous spec generation was validated against the planning document and found gaps.\n"+
			"You MUST address ALL of these in your output:\n\n%s\n\n"+
			"Previous attempt summary: %s\n\n",
			strings.Join(gapLines, "\n"), opts.PriorCoveredSummary)
		prompt = strings.Replace(prompt, "## RULES\n", gapSection+"## RULES\n", 1)
	}

	// Insert example section before ## RULES
	if exampleSection != "" {
		prompt = strings.Replace(prompt, "## RULES\n", exampleSection+"## RULES\n", 1)
	}

	// Call LLM
	result, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		Model:      agent.ModelOpus,
		JSONSchema: generateSpecJSONSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("spec generation: %w", err)
	}

	// Parse JSON output into OrchestraSpec
	var spec OrchestraSpec
	if err := json.Unmarshal([]byte(result.Output), &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec output: %w", err)
	}

	// Validate
	validation := ValidateSpec(&spec)

	// Write YAML regardless of validation (user can fix)
	yamlData, err := yaml.Marshal(&spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec to YAML: %w", err)
	}

	// Ensure output directory exists
	if dir := filepath.Dir(outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}
	}

	if err := os.WriteFile(outPath, yamlData, 0o644); err != nil {
		return nil, fmt.Errorf("writing spec file: %w", err)
	}

	return &GenerateSpecResult{
		Spec:           &spec,
		Validation:     validation,
		OutputPath:     outPath,
		Reasoning:      result.Reasoning,
		SourcePlanText: idea, // B-289: store expanded idea for plan-to-spec validation
	}, nil
}

// GenerateSpecWithValidation wraps GenerateSpec + ValidateSpecAgainstPlan in a
// self-correcting loop. On validation failure, gaps are fed back into the next
// generation attempt. Circuit breaker at maxIterations. Returns the best result
// by coverage score. Fail-open: validation errors don't block spec return.
func (c *Conductor) GenerateSpecWithValidation(ctx context.Context, opts GenerateSpecOpts, maxIterations int) (*GenerateSpecResult, *PlanValidationResult, error) {
	if maxIterations <= 0 {
		maxIterations = 3
	}

	var bestResult *GenerateSpecResult
	var bestValResult *PlanValidationResult
	bestCoverage := -1.0

	for i := 0; i < maxIterations; i++ {
		// Generate spec
		result, err := c.GenerateSpec(ctx, opts)
		if err != nil {
			// If we have a prior best, return it instead of failing
			if bestResult != nil {
				c.log("iteration %d/%d generation failed (returning best prior): %v", i+1, maxIterations, err)
				return bestResult, bestValResult, nil
			}
			return nil, nil, fmt.Errorf("iteration %d/%d: %w", i+1, maxIterations, err)
		}

		// Skip validation if source plan text is too short
		if result.SourcePlanText == "" || len(result.SourcePlanText) <= 200 {
			return result, nil, nil
		}

		// Validate against plan
		valResult, valErr := c.ValidateSpecAgainstPlan(ctx, PlanValidationOpts{
			PlanText: result.SourcePlanText,
			Spec:     result.Spec,
		})
		if valErr != nil {
			// Fail-open: return spec without blocking on validation error
			c.log("iteration %d/%d validation failed (non-blocking): %v", i+1, maxIterations, valErr)
			if bestResult == nil || (result != nil) {
				// Return current result if no prior best, or if we at least generated something
				return result, nil, nil
			}
			return bestResult, bestValResult, nil
		}

		// Track best result by coverage score
		if valResult.CoverageScore > bestCoverage {
			bestCoverage = valResult.CoverageScore
			bestResult = result
			bestValResult = valResult
		}

		// Success — pass threshold met
		if valResult.Pass {
			c.log("iteration %d/%d: %.0f%% coverage — PASS", i+1, maxIterations, valResult.CoverageScore*100)
			return result, valResult, nil
		}

		c.log("iteration %d/%d: %.0f%% coverage, %d gaps",
			i+1, maxIterations, valResult.CoverageScore*100, len(valResult.MissingItems))

		// Feed gaps into next iteration (unless this is the last)
		if i < maxIterations-1 {
			opts.PriorGaps = valResult.MissingItems
			opts.PriorCoveredSummary = valResult.Summary
		}
	}

	// Return best result after exhausting iterations
	c.log("max iterations reached — returning best result (%.0f%% coverage)", bestCoverage*100)

	// B-289: If FailClosed is set, return error when critical gaps remain
	if opts.FailClosed && bestValResult != nil && !bestValResult.Pass {
		criticalCount := 0
		for _, gap := range bestValResult.MissingItems {
			if gap.Severity == "critical" || gap.Severity == "high" {
				criticalCount++
			}
		}
		if criticalCount > 0 {
			return bestResult, bestValResult, fmt.Errorf(
				"B-289: spec generation failed validation with %d critical/high gaps after %d iterations (%.0f%% coverage)",
				criticalCount, maxIterations, bestCoverage*100)
		}
	}

	return bestResult, bestValResult, nil
}

// phaseHintPatterns matches common patterns for phase/branch/section headings in planning documents.
var phaseHintPatterns = []*regexp.Regexp{
	// "### Branch A: Topic Name" or "## Branch A — Topic"
	regexp.MustCompile(`(?m)^#{2,4}\s+Branch\s+([A-Z])\s*[:—–-]\s*(.+)$`),
	// "Phase 1: Name" or "Phase 1 — Name"
	regexp.MustCompile(`(?m)^#{0,4}\s*Phase\s+(\d+)\s*[:—–-]\s*(.+)$`),
	// "- phase: research" (YAML-like)
	regexp.MustCompile(`(?m)^\s*-\s*phase:\s*(.+)$`),
	// "| 1. **research** |" (table row)
	regexp.MustCompile(`(?m)\|\s*\d+\.\s*\*{0,2}(\w[\w\s-]*\w)\*{0,2}\s*\|`),
	// "Step 1: Name" or "Step 1 — Name"
	regexp.MustCompile(`(?m)^#{0,4}\s*Step\s+(\d+)\s*[:—–-]\s*(.+)$`),
}

// extractPhaseHints scans the planning document for phase-like headings and returns
// a deduplicated list of phase descriptions for injection into the spec generation prompt.
func extractPhaseHints(idea string) []string {
	if len(idea) < 100 {
		return nil // too short to contain structured phases
	}

	seen := make(map[string]bool)
	var hints []string

	for _, pat := range phaseHintPatterns {
		matches := pat.FindAllStringSubmatch(idea, -1)
		for _, m := range matches {
			// Use the full match text minus markdown formatting as the hint
			var hint string
			if len(m) >= 3 && m[2] != "" {
				// Pattern with label + description (e.g., "Branch A: Topic")
				hint = strings.TrimSpace(m[0])
			} else if len(m) >= 2 {
				hint = strings.TrimSpace(m[1])
			} else {
				continue
			}

			// Strip leading markdown hashes
			hint = strings.TrimLeft(hint, "# ")

			// Deduplicate
			key := strings.ToLower(hint)
			if seen[key] {
				continue
			}
			seen[key] = true
			hints = append(hints, hint)
		}
	}

	return hints
}
