package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// DecomposeOpts configures a decompose invocation.
type DecomposeOpts struct {
	Goal                  string
	MaxTasks              int         // default: 8
	MaxFilesPerTask       int         // default: 25; 0 = unlimited
	Critique              string      // reviewer feedback for re-decomposition
	DryRun                bool        // show plan without creating tasks
	Clarify               bool        // enable goal clarification before decomposition
	ClarifyMode           ClarifyMode // how to resolve ambiguity questions
	DisableActionExpansion bool       // B-240d: skip expandVagueFileActions
	ReadOnlyFiles         []string    // B-240c: files excluded from task file lists
	Hierarchical          bool        // B-281: enable feature cluster decomposition
	AtomicTasks           []string    // G-CSS-6: task titles that must not be split
	SpecTasks             []SpecTask  // Spec-defined tasks with roles to enforce post-decomposition
	OriginalGoal          string      // Pre-expansion goal text for plan cache hashing (set by Go())
}

// DecomposeResult captures the outcome of decomposition.
type DecomposeResult struct {
	SessionID            string
	Tasks                []DecomposedTask
	GoalFiles            []string // files extracted from the goal text
	FilesCoverageMissing []string // goal files not assigned by the LLM (injected post-hoc)
	FilesCoveragePercent float64  // (assigned / total goal files) * 100; 0 if no goal files
}

// DecomposedTask represents a single task from decomposition output.
type DecomposedTask struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Role        string        `json:"role"`
	Priority      int           `json:"priority"`
	PriorityLabel string        `json:"priority_label"`
	DependsOn     FlexStringArr `json:"depends_on"`
	Files              []string      `json:"files"`
	AdditionalFiles    []string      `json:"additional_files,omitempty"` // G137: new files not in goal enum
	AcceptanceCriteria string        `json:"acceptance_criteria"`
	FeatureCluster     string        `json:"feature_cluster,omitempty"` // B-281: hierarchical decomposition
}

// FlexStringArr implements custom JSON unmarshaling to normalize mixed-type arrays
// into a consistent []string. LLMs sometimes return depends_on values as integers
// instead of strings (e.g. [0, "setup-db"] instead of ["0", "setup-db"]), so
// FlexStringArr accepts []string, []int, or []interface{} and coerces all elements
// to strings.
type FlexStringArr []string

func (f *FlexStringArr) UnmarshalJSON(data []byte) error {
	// Try []string first
	var strs []string
	if err := json.Unmarshal(data, &strs); err == nil {
		*f = strs
		return nil
	}
	// Fall back to []int (LLM sometimes returns integer indices)
	var ints []int
	if err := json.Unmarshal(data, &ints); err == nil {
		result := make([]string, len(ints))
		for i, v := range ints {
			result[i] = fmt.Sprintf("%d", v)
		}
		*f = result
		return nil
	}
	// Fall back to []interface{} for mixed arrays
	var mixed []interface{}
	if err := json.Unmarshal(data, &mixed); err == nil {
		result := make([]string, len(mixed))
		for i, v := range mixed {
			result[i] = fmt.Sprintf("%v", v)
		}
		*f = result
		return nil
	}
	return fmt.Errorf("depends_on: expected array of strings or ints, got %s", string(data))
}

// decomposeJSONSchema enforces all required fields in decomposition output via
// Claude's --json-schema constrained decoding. This makes it structurally
// impossible for the LLM to omit acceptance_criteria, files, etc.
const decomposeJSONSchema = `{
  "type": "object",
  "properties": {
    "tasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "title": { "type": "string", "description": "Short imperative title" },
          "description": { "type": "string", "description": "Detailed description of what to do" },
          "role": { "type": "string", "enum": ["implementer", "deep-researcher", "architect", "reviewer", "scout", "pi-implementer"] },
          "priority": { "type": "integer", "minimum": 1, "maximum": 5 },
          "priority_label": { "type": "string" },
          "depends_on": { "type": "array", "items": { "type": "string" } },
          "files": { "type": "array", "items": { "type": "string" }, "description": "File paths this task owns" },
          "acceptance_criteria": { "type": "string", "description": "Markdown checkboxes with testable criteria" }
        },
        "required": ["title", "description", "role", "priority", "files", "acceptance_criteria"]
      }
    }
  },
  "required": ["tasks"]
}`

// decomposeHierarchicalJSONSchema extends the base schema with feature_cluster (B-281).
const decomposeHierarchicalJSONSchema = `{
  "type": "object",
  "properties": {
    "tasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "title": { "type": "string", "description": "Short imperative title" },
          "description": { "type": "string", "description": "Detailed description of what to do" },
          "role": { "type": "string", "enum": ["implementer", "deep-researcher", "architect", "reviewer", "scout", "pi-implementer"] },
          "priority": { "type": "integer", "minimum": 1, "maximum": 5 },
          "priority_label": { "type": "string" },
          "depends_on": { "type": "array", "items": { "type": "string" } },
          "files": { "type": "array", "items": { "type": "string" }, "description": "File paths this task owns" },
          "acceptance_criteria": { "type": "string", "description": "Markdown checkboxes with testable criteria" },
          "feature_cluster": { "type": "string", "description": "Logical feature group this task belongs to (e.g. 'auth', 'api', 'ui')" }
        },
        "required": ["title", "description", "role", "priority", "files", "acceptance_criteria", "feature_cluster"]
      }
    }
  },
  "required": ["tasks"]
}`

// buildEnumConstrainedSchema builds a JSON schema that constrains file paths to an
// enum of resolved goal files. This is the primary defense against the decomposer
// renaming file paths (G137). The schema includes an optional additional_files field
// for genuinely new files not in the goal.
func buildEnumConstrainedSchema(goalFiles []string, hierarchical bool) string {
	// Build the files items object with enum constraint
	filesItems := map[string]interface{}{
		"type": "string",
		"enum": goalFiles,
	}

	// Build task properties
	taskProps := map[string]interface{}{
		"title":       map[string]interface{}{"type": "string", "description": "Short imperative title"},
		"description": map[string]interface{}{"type": "string", "description": "Detailed description of what to do"},
		"role":        map[string]interface{}{"type": "string", "enum": []string{"implementer", "deep-researcher", "architect", "reviewer", "scout", "pi-implementer"}},
		"priority":    map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 5},
		"priority_label": map[string]interface{}{"type": "string"},
		"depends_on":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"files":       map[string]interface{}{"type": "array", "items": filesItems, "description": "Existing file paths from the goal — use ONLY paths from this enum list"},
		"additional_files": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "New files this task creates that are not listed in the goal"},
		"acceptance_criteria": map[string]interface{}{"type": "string", "description": "Markdown checkboxes with testable criteria"},
	}

	required := []string{"title", "description", "role", "priority", "files", "acceptance_criteria"}

	if hierarchical {
		taskProps["feature_cluster"] = map[string]interface{}{
			"type":        "string",
			"description": "Logical feature group this task belongs to (e.g. 'auth', 'api', 'ui')",
		}
		required = append(required, "feature_cluster")
	}

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tasks": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type":       "object",
					"properties": taskProps,
					"required":   required,
				},
			},
		},
		"required": []string{"tasks"},
	}

	data, _ := json.Marshal(schema)
	return string(data)
}

// decomposeClusterRules is appended to the prompt when hierarchical decomposition is enabled.
const decomposeClusterRules = `

FEATURE CLUSTERING RULES (hierarchical mode):
- Group related tasks into logical feature clusters (e.g. "auth", "api", "database", "ui", "testing")
- Each task MUST include a "feature_cluster" field with a short, descriptive cluster name
- Use lowercase, hyphen-separated names (e.g. "error-handling", "user-auth", "data-layer")
- Tasks within the same cluster should modify related files and have tighter coupling
- Tasks in different clusters should be as independent as possible
- Aim for 2-4 clusters for typical goals; never put all tasks in one cluster
- Dependencies WITHIN a cluster are fine; dependencies ACROSS clusters should be minimized
- Think of clusters as "mini-features" that can be developed and merged independently`

const decomposePromptTemplate = `You are a task decomposition architect. Break the following goal into focused, independent tasks.

GOAL: %s

RULES:
- Create at most %d tasks
- %s
- Each task must have a clear, atomic objective
- Assign roles: implementer, researcher, architect, reviewer, scout
- Set priority 1-5 (5=highest)
- Specify file dependencies to prevent conflicts
- Each file MUST appear in at most one task's "files" list. If multiple tasks need to modify the same file, assign ALL modifications of that file to a single task
- Only create dependencies (depends_on) when task B genuinely needs task A's output
- Each task MUST include 2-5 specific, testable acceptance criteria as markdown checkboxes in the acceptance_criteria field
- Acceptance criteria describe concrete, verifiable outcomes — never use "verify", "confirm", or "ensure" without specifying what observable change to make
- BAD: "Verify the field flows through the system" / "Confirm no field is dropped" / "No direct changes expected"
- GOOD: "- [ ] spawner.go passes PriorityLabel to the agent spec via SpecGenOpts" / "- [ ] GoOpts struct includes PriorityLabel field with type string"
- If FILE CONTENTS are provided below, read them carefully before writing task descriptions. Base implementation guidance on what the code ACTUALLY does, not assumptions about API behavior
- Describe WHAT each task should change, not HOW to implement it — let the agent investigate the best approach. Say "modify the groupby method to support multi-column keys" not "remove the NotImplementedError and call set_index"
- Tasks marked [ATOMIC — DO NOT SPLIT OR MERGE WITH OTHER TASKS] MUST be emitted as exactly ONE task with the same title, description, files, and role. Do NOT decompose atomic tasks into sub-tasks or merge them with other tasks

Respond with a JSON object containing a "tasks" array. Each task must include all fields. The acceptance_criteria field must contain 2-5 specific, testable checkboxes.`

// Decompose breaks a goal into tasks using ClaudeRunner.
// If Critique is provided, appends reviewer feedback to the prompt for re-decomposition.
func (c *Conductor) Decompose(ctx context.Context, opts DecomposeOpts) (*DecomposeResult, error) {
	if opts.Goal == "" {
		return nil, fmt.Errorf("goal is required")
	}

	// Expand @file references in goal text
	opts.Goal = expandFileReferences(opts.Goal, c.RepoRoot)

	// Preprocess large goals (summarize if over token threshold)
	processedGoal, fullGoalPath, err := c.PreprocessLargeGoal(ctx, opts.Goal)
	if err != nil {
		c.log("Goal preprocessing failed: %v", err)
	} else if fullGoalPath != "" {
		c.setBlackboard(ctx, "full_goal_path", fullGoalPath)
		c.DB.LogEvent(ctx, "goal_preprocessed", "", "",
			fmt.Sprintf(`{"original_tokens":%d,"path":"%s"}`, len(opts.Goal)/4, fullGoalPath))
		opts.Goal = processedGoal
	}

	// Plan cache: use original (pre-expansion) goal for hashing to ensure consistency
	cacheGoal := opts.OriginalGoal
	if cacheGoal == "" {
		cacheGoal = opts.Goal // fallback if OriginalGoal not set
	}

	// Plan cache: invalidate stale entries, then check for a cache hit
	if err := c.InvalidateStaleEntries(ctx); err != nil {
		c.log("Plan cache invalidation failed (non-fatal): %v", err)
	}
	// Estimate tier for cache lookup (use simple heuristic based on goal file count)
	cacheGoalFiles := extractGoalFiles(opts.Goal)
	estimatedTier := 2 // default Tier2Medium
	if len(cacheGoalFiles) <= 3 {
		estimatedTier = 1
	} else if len(cacheGoalFiles) >= 8 {
		estimatedTier = 3
	}
	cacheHit, cacheErr := c.LookupCache(ctx, cacheGoal, estimatedTier)
	if cacheErr != nil {
		c.log("Plan cache lookup failed (non-fatal): %v", cacheErr)
	}
	if cacheHit != nil {
		c.log("Plan cache HIT (score=%.2f, tier=%s, cache_id=%d)", cacheHit.Score, cacheHit.Tier, cacheHit.CacheID)
		c.DB.LogEvent(ctx, "plan_cache_hit", "", "",
			fmt.Sprintf(`{"score":%.2f,"tier":"%s","cache_id":%d}`, cacheHit.Score, cacheHit.Tier, cacheHit.CacheID))
		// Use cached tasks but run them through the full post-decomposition pipeline
		tasks := cacheHit.Plan
		return c.postDecomposePipeline(ctx, opts, tasks)
	}
	c.DB.LogEvent(ctx, "plan_cache_miss", "", "",
		fmt.Sprintf(`{"goal_hash":"%s","estimated_tier":%d}`, NormalizeGoalHash(cacheGoal)[:16], estimatedTier))

	// MaxFilesPerTask: default 25, negative means unlimited (same as 0 from CLI --max-files-per-task=0)
	maxFilesPerTask := opts.MaxFilesPerTask
	if maxFilesPerTask == 0 {
		maxFilesPerTask = 25 // default
	} else if maxFilesPerTask < 0 {
		maxFilesPerTask = 0 // unlimited
	}

	maxTasks := opts.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 8
	}

	// Compute effective maxTasks: ensure enough room for the file cap
	goalFiles := extractGoalFiles(opts.Goal)
	maxTasks = computeEffectiveMaxTasks(maxTasks, maxFilesPerTask, len(goalFiles))

	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for decompose")
	}

	// Optional goal clarification phase
	goal := opts.Goal
	if opts.Clarify {
		clarifyResult, err := c.Clarify(ctx, ClarifyOpts{Goal: goal, Mode: opts.ClarifyMode, DryRun: opts.DryRun})
		if err != nil {
			c.log("Clarification failed, proceeding with original goal: %v", err)
		} else if !clarifyResult.Skipped {
			goal = clarifyResult.AugmentedGoal
			c.log("Goal augmented with %d clarifications", len(clarifyResult.Questions))
		}
	}

	// Build task count preference (conditional on goal size and maxTasks)
	var taskPref string
	if len(goalFiles) > 20 && maxFilesPerTask > 0 {
		taskPref = fmt.Sprintf("This is a large goal with %d files. Create enough tasks so each owns at most %d files", len(goalFiles), maxFilesPerTask)
	} else if maxTasks >= 5 {
		taskPref = fmt.Sprintf("Create up to %d tasks when work is genuinely parallelizable with no file overlap. Each task should own at most %d files", maxTasks, maxFilesPerTask)
	} else {
		taskPref = fmt.Sprintf("Prefer fewer tasks (1-2) unless genuinely parallel and independent work exists. Each task should own at most %d files", maxFilesPerTask)
	}

	// Build prompt, optionally including reviewer critique
	prompt := fmt.Sprintf(decomposePromptTemplate, goal, maxTasks, taskPref)

	// B-281: Append clustering rules for hierarchical decomposition
	if opts.Hierarchical {
		prompt += decomposeClusterRules
	}

	// Lift resolvedGoalFiles so it's available for both prompt hints and enum schema
	var resolvedGoalFiles []string
	if hintFiles := extractGoalFiles(goal); len(hintFiles) > 0 {
		resolvedGoalFiles = resolveGoalFilePaths(hintFiles, c.RepoRoot)
		prompt += fmt.Sprintf("\n\nFILES MENTIONED IN GOAL (use these paths EXACTLY — never rename, abbreviate, or expand them):\n- %s",
			strings.Join(resolvedGoalFiles, "\n- "))

		// G100: Read goal file contents so the decomposer understands actual APIs
		if fileContents := readGoalFileContents(resolvedGoalFiles, c.RepoRoot, 24000); fileContents != "" {
			prompt += fileContents
			c.log("Decompose: injected %d bytes of goal file contents", len(fileContents))
		}
	}

	if opts.Critique != "" {
		prompt += fmt.Sprintf("\n\nREVIEWER FEEDBACK (address these issues in your revised decomposition):\n%s", opts.Critique)
	}

	// Doc 021, Gap 2: Augment goal files with files from the staging/dev branch.
	// Cross-phase shared files (e.g., server.go created in phase 1) won't appear in
	// a later phase's goal list. Scanning the staging branch ensures they're in the enum
	// so the decomposer CAN assign them.
	if len(resolvedGoalFiles) > 0 && c.RepoRoot != "" {
		stagingFiles := discoverStagingBranchFiles(c, ctx, resolvedGoalFiles)
		if len(stagingFiles) > 0 {
			// Deduplicate: only add files not already in the goal list
			goalSet := make(map[string]bool, len(resolvedGoalFiles))
			for _, gf := range resolvedGoalFiles {
				goalSet[gf] = true
			}
			added := 0
			for _, sf := range stagingFiles {
				if !goalSet[sf] {
					resolvedGoalFiles = append(resolvedGoalFiles, sf)
					goalSet[sf] = true
					added++
				}
			}
			if added > 0 {
				c.log("Decompose: added %d staging branch files to enum (total: %d)", added, len(resolvedGoalFiles))
			}
		}
	}

	// G137 enhancement: enum-constrained schema when goal files are available
	jsonSchema := decomposeJSONSchema
	if len(resolvedGoalFiles) > 0 {
		jsonSchema = buildEnumConstrainedSchema(resolvedGoalFiles, opts.Hierarchical)
		c.log("Decompose: using enum-constrained schema with %d goal files", len(resolvedGoalFiles))
	} else if opts.Hierarchical {
		jsonSchema = decomposeHierarchicalJSONSchema
	}

	c.log("Decompose: prompt=%d bytes, schema=%d bytes, model=%s, hierarchical=%v", len(prompt), len(jsonSchema), agent.ModelOpus, opts.Hierarchical)
	c.log("Decompose prompt (first 1000 chars): %.1000s", prompt)

	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		Model:      agent.ModelOpus,
		WorkDir:    c.RepoRoot,
		JSONSchema: jsonSchema,
	})
	if err != nil {
		c.log("Decompose LLM call failed: %v", err)
		return nil, fmt.Errorf("decompose call failed: %w", err)
	}
	output := runResult.Output
	c.log("Decompose raw output (%d bytes): %.500s", len(output), output)
	if runResult.Reasoning != "" {
		c.log("Decompose reasoning (%d bytes): %.2000s", len(runResult.Reasoning), runResult.Reasoning)
		c.DB.LogEvent(ctx, "decompose_reasoning", "", "",
			fmt.Sprintf(`{"reasoning_len":%d,"reasoning":"%s"}`, len(runResult.Reasoning), escapeJSON(runResult.Reasoning)))
	}

	// Parse the output
	tasks, err := parseDecomposeOutput(output, maxTasks)
	if err != nil && goal != opts.Goal {
		// Fallback: augmented goal confused the model — retry with original goal
		c.log("Clarified decompose failed to parse, retrying with original goal: %v", err)
		fallbackPrompt := fmt.Sprintf(decomposePromptTemplate, opts.Goal, maxTasks, taskPref)
		if opts.Critique != "" {
			fallbackPrompt += fmt.Sprintf("\n\nREVIEWER FEEDBACK (address these issues in your revised decomposition):\n%s", opts.Critique)
		}
		runResult, err = c.Runner.Run(ctx, RunOpts{
			Prompt:     fallbackPrompt,
			Model:      agent.ModelOpus,
			WorkDir:    c.RepoRoot,
			JSONSchema: decomposeJSONSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("decompose fallback call failed: %w", err)
		}
		output = runResult.Output
		tasks, err = parseDecomposeOutput(output, maxTasks)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing decompose output: %w", err)
	}

	// Store successful decomposition in plan cache (use original goal for consistent hashing)
	if storeErr := c.StoreCache(ctx, cacheGoal, tasks, estimatedTier); storeErr != nil {
		c.log("Plan cache store failed (non-fatal): %v", storeErr)
	} else {
		c.log("Plan cache stored: hash=%s tasks=%d tier=%d goal=%q", NormalizeGoalHash(cacheGoal)[:16], len(tasks), estimatedTier, cacheGoal[:min(len(cacheGoal), 80)])
	}

	// G137/G138: Correct LLM-introduced file path renames BEFORE merging
	// additional_files. The correction must run only on enum-constrained files
	// (the "files" field). additional_files are new files the agent creates and
	// should NOT be corrected — they are not renames of goal files. G138 bug:
	// running correction after merge caused edit reports (e.g., ch01-dev-edit.md)
	// to be incorrectly mapped to chapter files (e.g., ch01.md).
	if gf := extractGoalFiles(opts.Goal); len(gf) > 0 {
		canonicalGoalFiles := resolveGoalFilePaths(gf, c.RepoRoot)
		canonicalGoalFiles = filterPathList(canonicalGoalFiles, opts.ReadOnlyFiles)
		var corrections []FileCorrection
		tasks, corrections = correctDecomposerFilePaths(tasks, canonicalGoalFiles)
		if len(corrections) > 0 {
			c.log("G137: corrected %d file path renames from decomposer:", len(corrections))
			for _, fc := range corrections {
				c.log("  %s → %s (method: %s, task: %s)", fc.Original, fc.Corrected, fc.Method, fc.TaskTitle)
			}
			c.DB.LogEvent(ctx, "decompose_file_path_correction", "", "",
				fmt.Sprintf(`{"corrections":%d}`, len(corrections)))
		}
	}

	// G137: Merge additional_files into files list AFTER correction (new files not in the enum)
	for i := range tasks {
		if len(tasks[i].AdditionalFiles) > 0 {
			tasks[i].Files = append(tasks[i].Files, tasks[i].AdditionalFiles...)
			c.log("Task %q: merged %d additional files into files list", tasks[i].Title, len(tasks[i].AdditionalFiles))
		}
	}

	// Log acceptance criteria quality for each task
	for _, t := range tasks {
		hasAC := t.AcceptanceCriteria != ""
		acLen := len(t.AcceptanceCriteria)
		checkboxCount := strings.Count(t.AcceptanceCriteria, "- [ ]")
		c.log("Task %s: AC=%v (len=%d, checkboxes=%d)", t.Title, hasAC, acLen, checkboxCount)

		if vague := containsVagueAC(t.AcceptanceCriteria); vague {
			c.log("WARNING: Task %q has vague acceptance criteria — may produce no code changes", t.Title)
		}

		c.DB.LogEvent(ctx, "decompose_ac_quality", "", t.ID,
			fmt.Sprintf(`{"has_ac":%v,"ac_len":%d,"checkboxes":%d,"vague":%v}`, hasAC, acLen, checkboxCount, containsVagueAC(t.AcceptanceCriteria)))
	}

	// B-240c: Filter read-only files before action expansion
	if len(opts.ReadOnlyFiles) > 0 {
		tasks = filterReadOnlyFiles(tasks, opts.ReadOnlyFiles)
	}

	// B-240d: Action expansion toggle
	if opts.DisableActionExpansion {
		c.log("Action expansion disabled (--disable-action-expansion)")
	} else {
		// B-207: Action expansion pass — convert vague per-file instructions into
		// concrete actions using an LLM expansion pass (Agentless/Copilot Workspace pattern).
		tasks = c.expandVagueFileActions(ctx, tasks)
	}

	// B-236: Enforce per-task file cap — split oversized tasks mechanically
	if maxFilesPerTask > 0 {
		tasks = enforceFileCap(tasks, maxFilesPerTask)
	}

	// G-CSS-6: Re-merge atomic task fragments that got split by enforceFileCap or decomposer
	if len(opts.AtomicTasks) > 0 {
		tasks = enforceAtomicTasks(tasks, opts.AtomicTasks)
	}

	// Post-decomposition file coverage validation (with path resolution)
	var coverageMissing []string
	var coveragePct float64
	if gf := extractGoalFiles(opts.Goal); len(gf) > 0 {
		goalFiles = resolveGoalFilePaths(gf, c.RepoRoot)
		goalFiles = filterPathList(goalFiles, opts.ReadOnlyFiles) // L-011: exclude read-only from coverage
		assigned := collectAssignedFiles(tasks)
		missing := fileDifference(goalFiles, assigned)
		coveragePct = float64(len(goalFiles)-len(missing)) / float64(len(goalFiles)) * 100
		if len(missing) > 0 {
			coverageMissing = missing
			c.log("Decompose: %d/%d goal files missing from tasks (%.0f%% coverage): %v",
				len(missing), len(gf), coveragePct, missing)
			if float64(len(missing)) > float64(len(gf))*0.5 {
				c.log("WARNING: >50%% of goal files missing — decomposition may have misunderstood the goal")
			}
			tasks = injectMissingFiles(tasks, missing)
			c.DB.LogEvent(ctx, "decompose_coverage_gap", "", "",
				fmt.Sprintf(`{"missing":%q,"total_goal_files":%d,"coverage_pct":%.1f}`, missing, len(gf), coveragePct))
		} else {
			c.log("Decompose: all %d goal files covered by tasks", len(gf))
		}
	}

	// Post-decomposition file exclusivity validation: reject overlapping file assignments
	if dupes := findDuplicateFileAssignments(tasks); len(dupes) > 0 {
		c.log("WARNING: file exclusivity violation — %d files assigned to multiple tasks", len(dupes))
		for file, taskTitles := range dupes {
			c.log("  %s → tasks: %v", file, taskTitles)
		}
		tasks = deduplicateFileAssignments(tasks)
		c.DB.LogEvent(ctx, "decompose_file_exclusivity_fix", "", "",
			fmt.Sprintf(`{"duplicated_files":%d}`, len(dupes)))
	}

	// Doc 021, Gap 1: Detect implicit shared file coupling.
	// If multiple tasks create handler files but none owns the routing file,
	// warn that this will cause merge conflicts.
	tasks = enforceSharedFileCoupling(tasks, c)

	// B-281: Validate feature cluster assignments in hierarchical mode
	if opts.Hierarchical {
		tasks = validateFeatureClusters(tasks, c)
	}

	// Enforce spec-defined roles: when the spec pre-defines task roles,
	// override whatever the decomposer LLM assigned. Matches by title similarity.
	if len(opts.SpecTasks) > 0 {
		tasks = enforceSpecRoles(tasks, opts.SpecTasks, c)
	}

	// Dry-run: return tasks without storing in DB
	if opts.DryRun {
		for i := range tasks {
			tasks[i].ID = fmt.Sprintf("dry-%d", i+1)
		}
		return &DecomposeResult{
			SessionID:            c.SessionID,
			Tasks:                tasks,
			GoalFiles:            goalFiles,
			FilesCoverageMissing: coverageMissing,
			FilesCoveragePercent: coveragePct,
		}, nil
	}

	// Generate IDs and create tasks in DB
	now := time.Now()
	var taskIDs []string
	idMap := make(map[int]string) // index -> generated ID

	for i := range tasks {
		genID := db.GenID("t")
		tasks[i].ID = genID
		idMap[i] = genID
		taskIDs = append(taskIDs, genID)
	}

	// Resolve depends_on references (convert index refs or title refs to actual IDs)
	for i := range tasks {
		tasks[i].DependsOn = resolveDependencies(tasks[i].DependsOn, tasks, idMap, tasks[i].ID)
	}

	// Validate DAG: detect cycles before DB insertion (Airflow-style parse-time validation)
	if cycleIDs := detectAndBreakCycles(tasks); len(cycleIDs) > 0 {
		c.log("Cycle detected in decomposition DAG — stripped deps from: %v", cycleIDs)
		c.DB.LogEvent(ctx, "decompose_cycle_detected", "", "",
			fmt.Sprintf(`{"cycle_participants":%q}`, cycleIDs))
	}

	// Store tasks in DB
	for _, t := range tasks {
		blockedBy := "[]"
		if len(t.DependsOn) > 0 {
			data, _ := json.Marshal(t.DependsOn)
			blockedBy = string(data)
		}

		err := c.DB.CreateTask(ctx, db.Task{
			ID:                 t.ID,
			Title:              t.Title,
			Description:        sql.NullString{String: t.Description, Valid: t.Description != ""},
			AcceptanceCriteria: sql.NullString{String: t.AcceptanceCriteria, Valid: t.AcceptanceCriteria != ""},
			Status:             "pending",
			Priority:           t.Priority,
			PriorityLabel:      sql.NullString{String: t.PriorityLabel, Valid: t.PriorityLabel != ""},
			Role:               t.Role,
			BlockedBy:          sql.NullString{String: blockedBy, Valid: true},
			ConductorID:        sql.NullString{String: c.ConductorID, Valid: c.ConductorID != ""},
			PhaseID:            sql.NullString{String: c.currentPhaseID, Valid: c.currentPhaseID != ""},
			FeatureCluster:     sql.NullString{String: t.FeatureCluster, Valid: t.FeatureCluster != ""},
			CreatedAt:          now,
		})
		if err != nil {
			return nil, fmt.Errorf("creating task %s: %w", t.ID, err)
		}

		// Create file locks for ownership enforcement
		for _, filePath := range t.Files {
			if filePath != "" {
				if err := c.DB.CreateFileLock(ctx, filePath, "", t.ID, time.Time{}); err != nil {
					c.log("WARN: file lock for %s (task %s): %v", filePath, t.ID, err)
				}
				// Check for cross-conductor file overlaps (warn-only, no blocking)
				if c.ConductorID != "" {
					existingTask, existingConductor, _ := c.DB.FindCrossConductorFileLock(ctx, filePath, t.ID, c.ConductorID)
					if existingTask != "" {
						c.log("WARN: file %s also locked by task %s (conductor %s) — cross-conductor overlap", filePath, existingTask, existingConductor)
						c.DB.LogEvent(ctx, "file_lock_cross_conductor_overlap", "", t.ID,
							fmt.Sprintf(`{"file":%s,"other_task":%s,"other_conductor":%s}`,
								mustJSON(filePath), mustJSON(existingTask), mustJSON(existingConductor)))
					}
				}
			}
		}

		// Log event
		c.DB.LogEvent(ctx, "task_decomposed", "", t.ID,
			fmt.Sprintf(`{"goal":"%s","role":"%s"}`, escapeJSON(opts.Goal), t.Role))
	}

	// Store session info in blackboard
	if err := c.storeSessionTaskIDs(ctx, taskIDs); err != nil {
		return nil, fmt.Errorf("storing session task IDs: %w", err)
	}

	return &DecomposeResult{
		SessionID:            c.SessionID,
		Tasks:                tasks,
		GoalFiles:            goalFiles,
		FilesCoverageMissing: coverageMissing,
		FilesCoveragePercent: coveragePct,
	}, nil
}

// postDecomposePipeline runs the shared post-decomposition pipeline on a set of tasks,
// whether they came from the LLM or from the plan cache. This includes file path correction,
// deduplication, enforcement, action expansion, coverage validation, and DB insertion.
func (c *Conductor) postDecomposePipeline(ctx context.Context, opts DecomposeOpts, tasks []DecomposedTask) (*DecomposeResult, error) {
	// Compute maxFilesPerTask
	maxFilesPerTask := opts.MaxFilesPerTask
	if maxFilesPerTask == 0 {
		maxFilesPerTask = 25
	} else if maxFilesPerTask < 0 {
		maxFilesPerTask = 0
	}

	goalFiles := extractGoalFiles(opts.Goal)

	// G137/G138: Correct LLM-introduced file path renames
	if gf := extractGoalFiles(opts.Goal); len(gf) > 0 {
		canonicalGoalFiles := resolveGoalFilePaths(gf, c.RepoRoot)
		canonicalGoalFiles = filterPathList(canonicalGoalFiles, opts.ReadOnlyFiles)
		var corrections []FileCorrection
		tasks, corrections = correctDecomposerFilePaths(tasks, canonicalGoalFiles)
		if len(corrections) > 0 {
			c.log("G137: corrected %d file path renames:", len(corrections))
			for _, fc := range corrections {
				c.log("  %s → %s (method: %s, task: %s)", fc.Original, fc.Corrected, fc.Method, fc.TaskTitle)
			}
		}
	}

	// G137: Merge additional_files into files list
	for i := range tasks {
		if len(tasks[i].AdditionalFiles) > 0 {
			tasks[i].Files = append(tasks[i].Files, tasks[i].AdditionalFiles...)
		}
	}

	// B-240c: Filter read-only files
	if len(opts.ReadOnlyFiles) > 0 {
		tasks = filterReadOnlyFiles(tasks, opts.ReadOnlyFiles)
	}

	// B-240d: Action expansion
	if !opts.DisableActionExpansion {
		tasks = c.expandVagueFileActions(ctx, tasks)
	}

	// B-236: Enforce per-task file cap
	if maxFilesPerTask > 0 {
		tasks = enforceFileCap(tasks, maxFilesPerTask)
	}

	// G-CSS-6: Re-merge atomic task fragments
	if len(opts.AtomicTasks) > 0 {
		tasks = enforceAtomicTasks(tasks, opts.AtomicTasks)
	}

	// Post-decomposition file coverage validation
	var coverageMissing []string
	var coveragePct float64
	if gf := extractGoalFiles(opts.Goal); len(gf) > 0 {
		goalFiles = resolveGoalFilePaths(gf, c.RepoRoot)
		goalFiles = filterPathList(goalFiles, opts.ReadOnlyFiles)
		assigned := collectAssignedFiles(tasks)
		missing := fileDifference(goalFiles, assigned)
		coveragePct = float64(len(goalFiles)-len(missing)) / float64(len(goalFiles)) * 100
		if len(missing) > 0 {
			coverageMissing = missing
			tasks = injectMissingFiles(tasks, missing)
		}
	}

	// File exclusivity validation
	if dupes := findDuplicateFileAssignments(tasks); len(dupes) > 0 {
		tasks = deduplicateFileAssignments(tasks)
	}

	// Shared file coupling
	tasks = enforceSharedFileCoupling(tasks, c)

	// B-281: Feature cluster validation
	if opts.Hierarchical {
		tasks = validateFeatureClusters(tasks, c)
	}

	// Enforce spec-defined roles
	if len(opts.SpecTasks) > 0 {
		tasks = enforceSpecRoles(tasks, opts.SpecTasks, c)
	}

	// Dry-run: return without DB storage
	if opts.DryRun {
		for i := range tasks {
			tasks[i].ID = fmt.Sprintf("dry-%d", i+1)
		}
		return &DecomposeResult{
			SessionID:            c.SessionID,
			Tasks:                tasks,
			GoalFiles:            goalFiles,
			FilesCoverageMissing: coverageMissing,
			FilesCoveragePercent: coveragePct,
		}, nil
	}

	// Generate IDs and create tasks in DB
	now := time.Now()
	var taskIDs []string
	idMap := make(map[int]string)

	for i := range tasks {
		genID := db.GenID("t")
		tasks[i].ID = genID
		idMap[i] = genID
		taskIDs = append(taskIDs, genID)
	}

	for i := range tasks {
		tasks[i].DependsOn = resolveDependencies(tasks[i].DependsOn, tasks, idMap, tasks[i].ID)
	}

	if cycleIDs := detectAndBreakCycles(tasks); len(cycleIDs) > 0 {
		c.log("Cycle detected in decomposition DAG — stripped deps from: %v", cycleIDs)
	}

	// Spec validation: check decomposed specs against requirements doc.
	// If a requirements doc exists in the project, validate specs align with it.
	// On critical mismatch, log the issues (future: re-decompose).
	if c.Runner != nil {
		reqPaths := []string{
			filepath.Join(c.RepoRoot, "REQUIREMENTS.md"),
		}
		// Also check ~/symphonies/research/ for matching requirements docs
		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			matches, _ := filepath.Glob(filepath.Join(homeDir, "symphonies", "research", "*requirements*.md"))
			reqPaths = append(reqPaths, matches...)
		}
		for _, reqPath := range reqPaths {
			if _, err := os.Stat(reqPath); err == nil {
				c.log("Spec validation: checking specs against %s", reqPath)
				validation, valErr := ValidateSpecsAgainstRequirements(ctx, c.Runner, tasks, reqPath)
				if valErr != nil {
					c.log("Spec validation error (fail-open): %v", valErr)
				} else if !validation.Passed {
					c.log("SPEC VALIDATION FAILED — %d mismatches:", len(validation.Mismatches))
					for _, mm := range validation.Mismatches {
						c.log("  [%s] %s: %s", mm.Severity, mm.TaskTitle, mm.Issue)
					}
					c.DB.LogEvent(ctx, "spec_validation_failed", "", c.SessionID,
						fmt.Sprintf(`{"mismatches":%d}`, len(validation.Mismatches)))
					// TODO: Re-decompose with correction hints (max 2 retries)
				} else {
					c.log("Spec validation passed")
				}
				break // only validate against the first found requirements doc
			}
		}
	}

	for _, t := range tasks {
		blockedBy := "[]"
		if len(t.DependsOn) > 0 {
			data, _ := json.Marshal(t.DependsOn)
			blockedBy = string(data)
		}

		err := c.DB.CreateTask(ctx, db.Task{
			ID:                 t.ID,
			Title:              t.Title,
			Description:        sql.NullString{String: t.Description, Valid: t.Description != ""},
			AcceptanceCriteria: sql.NullString{String: t.AcceptanceCriteria, Valid: t.AcceptanceCriteria != ""},
			Status:             "pending",
			Priority:           t.Priority,
			PriorityLabel:      sql.NullString{String: t.PriorityLabel, Valid: t.PriorityLabel != ""},
			Role:               t.Role,
			BlockedBy:          sql.NullString{String: blockedBy, Valid: true},
			ConductorID:        sql.NullString{String: c.ConductorID, Valid: c.ConductorID != ""},
			PhaseID:            sql.NullString{String: c.currentPhaseID, Valid: c.currentPhaseID != ""},
			FeatureCluster:     sql.NullString{String: t.FeatureCluster, Valid: t.FeatureCluster != ""},
			CreatedAt:          now,
		})
		if err != nil {
			return nil, fmt.Errorf("creating task %s: %w", t.ID, err)
		}

		for _, filePath := range t.Files {
			if filePath != "" {
				if err := c.DB.CreateFileLock(ctx, filePath, "", t.ID, time.Time{}); err != nil {
					c.log("WARN: file lock for %s (task %s): %v", filePath, t.ID, err)
				}
			}
		}

		c.DB.LogEvent(ctx, "task_decomposed", "", t.ID,
			fmt.Sprintf(`{"goal":"%s","role":"%s","source":"cache"}`, escapeJSON(opts.Goal), t.Role))
	}

	if err := c.storeSessionTaskIDs(ctx, taskIDs); err != nil {
		return nil, fmt.Errorf("storing session task IDs: %w", err)
	}

	return &DecomposeResult{
		SessionID:            c.SessionID,
		Tasks:                tasks,
		GoalFiles:            goalFiles,
		FilesCoverageMissing: coverageMissing,
		FilesCoveragePercent: coveragePct,
	}, nil
}

// parseDecomposeOutput extracts tasks from Claude's JSON output.
// Supports two formats:
//   - Wrapped object: {"tasks": [...]} (from --json-schema mode)
//   - Bare JSON array: [...] (legacy text mode)
func parseDecomposeOutput(output string, maxTasks int) ([]DecomposedTask, error) {
	output = strings.TrimSpace(output)

	// Try structured output format first (wrapping object with "tasks" array)
	var wrapper struct {
		Tasks []DecomposedTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(output), &wrapper); err == nil && len(wrapper.Tasks) > 0 {
		return validateTasks(wrapper.Tasks, maxTasks)
	}

	// Fall back to bare JSON array (legacy text mode)
	jsonStr := extractJSONArray(output)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON array found in output")
	}
	var tasks []DecomposedTask
	if err := json.Unmarshal([]byte(jsonStr), &tasks); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w: output=%q", err, jsonStr)
	}
	return validateTasks(tasks, maxTasks)
}

// validateTasks applies post-parse validation and defaults to decomposed tasks.
func validateTasks(tasks []DecomposedTask, maxTasks int) ([]DecomposedTask, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks in decomposition output")
	}
	if len(tasks) > maxTasks {
		tasks = tasks[:maxTasks]
	}
	for i := range tasks {
		if tasks[i].Title == "" {
			return nil, fmt.Errorf("task %d has empty title", i)
		}
		if tasks[i].Role == "" {
			tasks[i].Role = "implementer"
		}
		if tasks[i].Priority == 0 {
			tasks[i].Priority = 3
		}
	}
	return tasks, nil
}

// extractJSONArray finds the first JSON array in the output.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start < 0 {
		return ""
	}
	// Find matching closing bracket
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// resolveDependencies converts dependency references to actual task IDs.
// Handles: existing task IDs, title strings, and integer index strings ("0", "1").
// selfID is the current task's ID — self-references are stripped to prevent deadlocks.
func resolveDependencies(deps []string, allTasks []DecomposedTask, idMap map[int]string, selfID string) []string {
	if len(deps) == 0 {
		return nil
	}

	var resolved []string
	for _, dep := range deps {
		// Check if it's already a valid task ID
		found := false
		for _, t := range allTasks {
			if t.ID == dep {
				resolved = append(resolved, dep)
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Try integer index (LLM sometimes returns [0] meaning "task at index 0")
		if idx, err := strconv.Atoi(dep); err == nil && idx >= 0 && idx < len(allTasks) {
			if id, ok := idMap[idx]; ok {
				resolved = append(resolved, id)
				continue
			}
		}

		// Try to match by title
		for i, t := range allTasks {
			if strings.EqualFold(t.Title, dep) {
				if id, ok := idMap[i]; ok {
					resolved = append(resolved, id)
					found = true
					break
				}
			}
		}
	}

	// Strip self-references to prevent deadlocks (LLM sometimes emits depends_on: [own_title])
	filtered := resolved[:0]
	for _, id := range resolved {
		if id != selfID {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// detectAndBreakCycles implements cycle detection and resolution inspired by
// Apache Airflow's DAG validation pattern (see airflow.utils.dag_processing
// check_cycle). It walks the dependency graph using Kahn's algorithm to detect
// back-edges indicating cycles, then breaks those cycles by removing all
// dependency edges from the cycle-participating tasks. Returns the IDs of tasks
// whose deps were cleared, or nil if the DAG is already acyclic.
func detectAndBreakCycles(tasks []DecomposedTask) []string {
	// Build temporary db.Task slice for TopoSort
	tempTasks := make([]db.Task, len(tasks))
	for i, t := range tasks {
		blockedBy := "[]"
		if len(t.DependsOn) > 0 {
			data, _ := json.Marshal([]string(t.DependsOn))
			blockedBy = string(data)
		}
		tempTasks[i] = db.Task{
			ID:        t.ID,
			BlockedBy: sql.NullString{String: blockedBy, Valid: true},
		}
	}

	if _, err := TopoSort(tempTasks); err == nil {
		return nil // DAG is valid
	}

	// Identify cycle participants via Kahn's algorithm
	taskSet := make(map[string]bool, len(tasks))
	inDeg := make(map[string]int, len(tasks))
	adj := make(map[string][]string)
	for _, t := range tasks {
		taskSet[t.ID] = true
	}
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if taskSet[dep] {
				inDeg[t.ID]++
				adj[dep] = append(adj[dep], t.ID)
			}
		}
	}

	var queue []string
	for _, t := range tasks {
		if inDeg[t.ID] == 0 {
			queue = append(queue, t.ID)
		}
	}
	sorted := make(map[string]bool)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted[id] = true
		for _, next := range adj[id] {
			inDeg[next]--
			if inDeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	// Clear deps for unsortable (cycle) tasks
	var participants []string
	for i := range tasks {
		if !sorted[tasks[i].ID] {
			tasks[i].DependsOn = nil
			participants = append(participants, tasks[i].ID)
		}
	}
	return participants
}

func escapeJSON(s string) string {
	// Simple JSON string escape
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// vagueACPatterns detects vague acceptance criteria that produce no code changes.
// These patterns (from doc 098) correlate with 0% task pass rates.
var vagueACPatterns = regexp.MustCompile(`(?i)\b(no direct changes|verify .{0,30} flows?|confirm .{0,30} (is|are) not dropped|ensure .{0,30} works?|check that .{0,30} still)\b`)

// containsVagueAC returns true if the acceptance criteria text contains vague
// patterns like "verify it flows" or "no direct changes expected" that correlate
// with agents producing no code changes.
func containsVagueAC(ac string) bool {
	if ac == "" {
		return false
	}
	return vagueACPatterns.MatchString(ac)
}

// fileActionExpansionPrompt asks the LLM to convert vague per-file instructions
// into concrete modification actions, following the Agentless/Copilot Workspace
// pattern of hierarchical localization.
const fileActionExpansionPrompt = `You are analyzing a coding task to produce concrete file-level modification instructions.

TASK TITLE: %s
TASK DESCRIPTION:
%s

ACCEPTANCE CRITERIA:
%s

The following files have VAGUE instructions — the task description says to "verify", "ensure", "confirm", or otherwise doesn't specify a concrete code change for these files:

%s

For each vague file, analyze the task context and determine:
1. What CONCRETE modification should be made (e.g., "Add PriorityLabel parameter to the SpawnAgent function signature and pass it to specGenOpts")
2. A testable acceptance criterion for that specific change
3. If no modification is actually needed, mark skip=true with a justification

Respond with JSON only.`

// fileActionExpansionSchema enforces structured output for the expansion pass.
const fileActionExpansionSchema = `{
  "type": "object",
  "properties": {
    "expansions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "file": { "type": "string", "description": "File path being expanded" },
          "concrete_action": { "type": "string", "description": "Specific code change to make" },
          "acceptance_criterion": { "type": "string", "description": "Testable checkbox for this change" },
          "skip": { "type": "boolean", "description": "True if no modification is actually needed" },
          "skip_reason": { "type": "string", "description": "Why this file should be skipped (only if skip=true)" }
        },
        "required": ["file", "concrete_action", "acceptance_criterion", "skip"]
      }
    }
  },
  "required": ["expansions"]
}`

// fileActionExpansion represents one expanded file action from the LLM.
type fileActionExpansion struct {
	File                string `json:"file"`
	ConcreteAction      string `json:"concrete_action"`
	AcceptanceCriterion string `json:"acceptance_criterion"`
	Skip                bool   `json:"skip"`
	SkipReason          string `json:"skip_reason"`
}

// fileActionExpansionResult wraps the LLM response for action expansion.
type fileActionExpansionResult struct {
	Expansions []fileActionExpansion `json:"expansions"`
}

// filterPathList removes paths that match any entry in exclude.
// Matches by full path and basename, consistent with filterReadOnlyFiles.
func filterPathList(paths []string, exclude []string) []string {
	if len(exclude) == 0 {
		return paths
	}
	exFull := make(map[string]bool, len(exclude))
	exBase := make(map[string]bool, len(exclude))
	for _, f := range exclude {
		exFull[f] = true
		exBase[filepath.Base(f)] = true
	}
	var kept []string
	for _, p := range paths {
		if exFull[p] || exBase[filepath.Base(p)] {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// filterReadOnlyFiles removes read-only files from task file lists.
// Matches are checked by both full path and basename to handle cases where
// the goal uses bare names but tasks use full paths or vice versa.
func filterReadOnlyFiles(tasks []DecomposedTask, readOnly []string) []DecomposedTask {
	if len(readOnly) == 0 {
		return tasks
	}
	for i := range tasks {
		tasks[i].Files = filterPathList(tasks[i].Files, readOnly)
	}
	return tasks
}

// expandVagueFileActions converts vague per-file instructions into concrete
// actions using an LLM expansion pass. This follows the Agentless/Copilot
// Workspace pattern: decompose → localize → specify per-file actions.
// Fail-open: if any LLM call fails, the original task is kept unchanged.
func (c *Conductor) expandVagueFileActions(ctx context.Context, tasks []DecomposedTask) []DecomposedTask {
	for i := range tasks {
		vagueFiles := findVagueFiles(tasks[i])
		if len(vagueFiles) == 0 {
			continue
		}

		c.log("Action expansion: task %q has %d vague files: %v", tasks[i].Title, len(vagueFiles), vagueFiles)

		// Build file list string
		var fileList strings.Builder
		for _, f := range vagueFiles {
			fmt.Fprintf(&fileList, "- %s\n", f)
		}

		prompt := fmt.Sprintf(fileActionExpansionPrompt,
			tasks[i].Title, tasks[i].Description, tasks[i].AcceptanceCriteria, fileList.String())

		expResult, err := c.Runner.Run(ctx, RunOpts{
			Prompt:     prompt,
			Model:      agent.ModelOpus,
			WorkDir:    c.RepoRoot,
			JSONSchema: fileActionExpansionSchema,
		})
		if err != nil {
			c.log("Action expansion LLM call failed for task %q, keeping original: %v", tasks[i].Title, err)
			continue // fail-open
		}

		var result fileActionExpansionResult
		if err := json.Unmarshal([]byte(expResult.Output), &result); err != nil {
			c.log("Action expansion parse failed for task %q, keeping original: %v", tasks[i].Title, err)
			continue // fail-open
		}

		tasks[i] = mergeExpandedActions(tasks[i], result)
		c.DB.LogEvent(ctx, "decompose_action_expanded", "", tasks[i].ID,
			fmt.Sprintf(`{"vague_files":%d,"expansions":%d}`, len(vagueFiles), len(result.Expansions)))
	}
	return tasks
}

// findVagueFiles returns files in a task that have only vague instructions.
// A file is considered "vague" if the task description + AC don't contain a
// concrete action for that file — only patterns like "verify/ensure/confirm/flow".
func findVagueFiles(task DecomposedTask) []string {
	if len(task.Files) == 0 {
		return nil
	}

	combined := strings.ToLower(task.Description + "\n" + task.AcceptanceCriteria)
	var vague []string

	for _, f := range task.Files {
		baseName := strings.ToLower(filepath.Base(f))

		// Find all text around this file reference
		fileContext := extractFileContext(combined, baseName)
		if fileContext == "" {
			// File not mentioned in description at all — vague by default
			vague = append(vague, f)
			continue
		}

		// Check if the file context has only vague patterns
		if vagueACPatterns.MatchString(fileContext) && !hasConcreteAction(fileContext) {
			vague = append(vague, f)
		}
	}

	return vague
}

// extractFileContext returns text surrounding a filename mention in the combined text.
// Returns up to 60 chars before and 60 chars after the mention — tight enough to avoid
// matching concrete actions that apply to OTHER files nearby.
func extractFileContext(text, filename string) string {
	idx := strings.Index(text, filename)
	if idx < 0 {
		return ""
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(filename) + 60
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

// hasConcreteAction checks if text contains concrete modification keywords
// (add, create, implement, insert, update, change, modify, set, pass, include).
var concreteActionPattern = regexp.MustCompile(`(?i)\b(add|create|implement|insert|update|change|modify|set|pass|include|append|remove|delete|replace|rename|move)\b`)

func hasConcreteAction(text string) bool {
	return concreteActionPattern.MatchString(text)
}

// mergeExpandedActions applies expanded concrete actions to a task,
// updating its description and acceptance criteria. Skipped files are
// removed from the task's file list.
func mergeExpandedActions(task DecomposedTask, result fileActionExpansionResult) DecomposedTask {
	skipSet := make(map[string]bool)
	var addedActions []string
	var addedAC []string

	for _, exp := range result.Expansions {
		if exp.Skip {
			skipSet[exp.File] = true
			continue
		}
		if exp.ConcreteAction != "" {
			addedActions = append(addedActions, fmt.Sprintf("- **%s**: %s", filepath.Base(exp.File), exp.ConcreteAction))
		}
		if exp.AcceptanceCriterion != "" {
			addedAC = append(addedAC, fmt.Sprintf("- [ ] %s", exp.AcceptanceCriterion))
		}
	}

	// Append expanded actions to description
	if len(addedActions) > 0 {
		task.Description += "\n\n### Expanded File Actions (auto-generated)\n\n" + strings.Join(addedActions, "\n")
	}

	// Append expanded AC
	if len(addedAC) > 0 {
		task.AcceptanceCriteria += "\n" + strings.Join(addedAC, "\n")
	}

	// Remove skipped files
	if len(skipSet) > 0 {
		var kept []string
		for _, f := range task.Files {
			if !skipSet[f] {
				kept = append(kept, f)
			}
		}
		task.Files = kept
	}

	return task
}

// discoverStagingBranchFiles scans the staging branch (or dev branch) for files
// in the same packages/directories as the goal files. This ensures cross-phase
// shared files (e.g., server.go created in phase 1) appear in the enum so the
// decomposer can assign them (doc 021, Gap 2).
func discoverStagingBranchFiles(c *Conductor, ctx context.Context, goalFiles []string) []string {
	// Determine which directories to scan from the goal files
	targetDirs := make(map[string]bool)
	for _, gf := range goalFiles {
		dir := filepath.Dir(gf)
		if dir != "" {
			targetDirs[dir] = true
		}
	}
	if len(targetDirs) == 0 {
		return nil
	}

	// Find the staging branch: check current conductor's staging branch,
	// or look for a prior phase's conductor staging branch.
	var stagingBranch string
	if cond, err := c.DB.GetConductor(ctx, c.ConductorID); err == nil && cond != nil && cond.StagingBranch != "" {
		stagingBranch = cond.StagingBranch
	}
	if stagingBranch == "" {
		// Try dev branch as fallback
		if out, err := exec.Command("git", "-C", c.RepoRoot, "rev-parse", "--verify", "dev").CombinedOutput(); err == nil {
			_ = out
			stagingBranch = "dev"
		}
	}
	if stagingBranch == "" {
		return nil
	}

	// Use git ls-tree to list files on the staging branch
	out, err := exec.Command("git", "-C", c.RepoRoot, "ls-tree", "-r", "--name-only", stagingBranch).CombinedOutput()
	if err != nil {
		return nil
	}

	// Filter to files in target directories only
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		dir := filepath.Dir(line)
		if targetDirs[dir] {
			result = append(result, line)
		}
	}
	return result
}

// goalFilePattern matches file paths with common source code extensions.
var goalFilePattern = regexp.MustCompile(`[\w./\-]+\.(?:go|sql|md|ts|js|py|yaml|json|toml|rs|rb|sh|css|html|proto)`)

// extractGoalFiles regex-extracts file paths from goal text.
// Matches tokens ending in common extensions, deduplicates, and returns sorted.
func extractGoalFiles(goal string) []string {
	matches := goalFilePattern.FindAllString(goal, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var result []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			result = append(result, m)
		}
	}
	sort.Strings(result)
	return result
}

// skipDirs are directories excluded from repo tree walks during path resolution.
var skipDirs = map[string]bool{
	".git": true, ".worktree": true, "node_modules": true, "vendor": true,
	".orchestra": true, "logs": true, "pids": true,
}

// resolveGoalFilePaths resolves bare filenames to full repo-relative paths.
// Files already containing a "/" are kept as-is. Bare names like "go.go" are
// searched in the repo tree; if found, the full relative path is used instead.
// Multiple matches produce multiple entries (all are potentially relevant).
func resolveGoalFilePaths(files []string, repoRoot string) []string {
	if repoRoot == "" {
		return files
	}

	// Separate bare names from paths
	var needsResolve []string
	resolved := make([]string, 0, len(files))
	for _, f := range files {
		if strings.Contains(f, "/") {
			resolved = append(resolved, f)
		} else {
			needsResolve = append(needsResolve, f)
		}
	}
	if len(needsResolve) == 0 {
		return files
	}

	// Build lookup: basename → []relative paths
	index := make(map[string][]string)
	filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}
		base := filepath.Base(rel)
		index[base] = append(index[base], rel)
		return nil
	})

	// Resolve bare names
	seen := make(map[string]bool)
	for _, r := range resolved {
		seen[r] = true
	}
	for _, bare := range needsResolve {
		if matches, ok := index[bare]; ok && len(matches) > 0 {
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					resolved = append(resolved, m)
				}
			}
		} else {
			// No match found — keep bare name (file may be created)
			if !seen[bare] {
				seen[bare] = true
				resolved = append(resolved, bare)
			}
		}
	}

	sort.Strings(resolved)
	return resolved
}

// readGoalFileContents reads the resolved goal files and returns formatted
// markdown with their contents, suitable for injection into the decompose prompt.
// Files are truncated individually (>200 lines → first 50 + last 50) and the
// total output is capped at charBudget characters (~tokenBudget * 4).
// Unreadable, binary, or oversized (>500KB) files are silently skipped (fail-open).
func readGoalFileContents(files []string, repoRoot string, charBudget int) string {
	if len(files) == 0 || charBudget <= 0 {
		return ""
	}

	// File extension priorities for budget allocation (higher = keep more)
	extPriority := map[string]int{
		".py": 4, ".go": 4, ".js": 3, ".ts": 3, ".jsx": 3, ".tsx": 3,
		".rs": 3, ".java": 3, ".rb": 2, ".sql": 2, ".sh": 2,
		".md": 1, ".txt": 1, ".yaml": 1, ".yml": 1, ".json": 1, ".toml": 1,
	}

	// Skip extensions that are binary or unreadable
	skipExts := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
		".zip": true, ".tar": true, ".gz": true, ".bz2": true,
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".pdf": true, ".wasm": true, ".o": true, ".a": true,
	}

	// Also skip lockfiles and generated files
	skipNames := map[string]bool{
		".gitignore": true, "package-lock.json": true, "yarn.lock": true,
		"go.sum": true, "Cargo.lock": true, "pnpm-lock.yaml": true,
	}

	type fileContent struct {
		path     string
		content  string
		priority int
	}

	var contents []fileContent
	for _, f := range files {
		base := filepath.Base(f)
		ext := strings.ToLower(filepath.Ext(f))

		if skipExts[ext] || skipNames[base] {
			continue
		}

		fullPath := f
		if !filepath.IsAbs(f) {
			fullPath = filepath.Join(repoRoot, f)
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue // fail-open
		}

		// Skip files >500KB
		if len(data) > 500*1024 {
			continue
		}

		// Skip binary-looking files (check first 512 bytes for null bytes)
		checkLen := 512
		if len(data) < checkLen {
			checkLen = len(data)
		}
		isBinary := false
		for i := 0; i < checkLen; i++ {
			if data[i] == 0 {
				isBinary = true
				break
			}
		}
		if isBinary {
			continue
		}

		text := string(data)
		lines := strings.Split(text, "\n")

		// Truncate long files: first 50 + omission marker + last 50
		if len(lines) > 200 {
			omitted := len(lines) - 100
			truncated := make([]string, 0, 102)
			truncated = append(truncated, lines[:50]...)
			truncated = append(truncated, fmt.Sprintf("\n... (%d lines omitted) ...\n", omitted))
			truncated = append(truncated, lines[len(lines)-50:]...)
			text = strings.Join(truncated, "\n")
		}

		pri := extPriority[ext]
		if pri == 0 {
			pri = 1
		}
		contents = append(contents, fileContent{path: f, content: text, priority: pri})
	}

	if len(contents) == 0 {
		return ""
	}

	// Sort by priority descending so high-priority files get budget first
	sort.Slice(contents, func(i, j int) bool {
		if contents[i].priority != contents[j].priority {
			return contents[i].priority > contents[j].priority
		}
		return contents[i].path < contents[j].path
	})

	// Build output, respecting charBudget
	var b strings.Builder
	b.WriteString("\n\nFILE CONTENTS (read these to understand the codebase before decomposing):\n")
	remaining := charBudget

	for _, fc := range contents {
		ext := strings.ToLower(filepath.Ext(fc.path))
		lang := strings.TrimPrefix(ext, ".")

		entry := fmt.Sprintf("\n### %s\n```%s\n%s\n```\n", fc.path, lang, fc.content)

		if len(entry) > remaining {
			// If we can fit at least a meaningful snippet (500 chars), truncate to fit
			if remaining > 500 {
				// Truncate content to fit within remaining budget
				overhead := len(fmt.Sprintf("\n### %s\n```%s\n", fc.path, lang)) + len("\n```\n")
				availContent := remaining - overhead
				if availContent > 100 {
					truncContent := fc.content
					if len(truncContent) > availContent {
						truncContent = truncContent[:availContent] + "\n... (truncated to fit budget) ..."
					}
					entry = fmt.Sprintf("\n### %s\n```%s\n%s\n```\n", fc.path, lang, truncContent)
					b.WriteString(entry)
					remaining -= len(entry)
				}
			}
			break // Budget exhausted
		}

		b.WriteString(entry)
		remaining -= len(entry)
	}

	result := b.String()
	// Only return if we actually included file content (not just the header)
	if !strings.Contains(result, "```") {
		return ""
	}
	return result
}

// fileDiffKey normalizes a file path to its basename for fuzzy matching.
// This allows "go.go" in the goal to match "internal/orchestrator/go.go" in assignments.
func fileDiffKey(path string) string {
	return filepath.Base(path)
}

// collectAssignedFiles returns all files assigned across all decomposed tasks.
func collectAssignedFiles(tasks []DecomposedTask) map[string]bool {
	assigned := make(map[string]bool)
	for _, t := range tasks {
		for _, f := range t.Files {
			assigned[f] = true
		}
	}
	return assigned
}

// fileDifference returns elements in want that are not in have.
// Checks both exact match and basename match (e.g., "internal/orchestrator/go.go"
// matches assigned "go.go" and vice versa).
func fileDifference(want []string, have map[string]bool) []string {
	// Build basename index from assigned files for fuzzy matching
	baseIndex := make(map[string]bool)
	for f := range have {
		baseIndex[filepath.Base(f)] = true
	}

	var missing []string
	for _, f := range want {
		if have[f] {
			continue
		}
		// Check if basename matches any assigned file
		if baseIndex[filepath.Base(f)] {
			continue
		}
		missing = append(missing, f)
	}
	return missing
}

// injectMissingFiles distributes missing files into existing tasks.
// Each missing file goes to the task that already owns the most files in
// the same directory. Falls back to the first implementer task, or the
// first task overall.
func injectMissingFiles(tasks []DecomposedTask, missing []string) []DecomposedTask {
	if len(tasks) == 0 || len(missing) == 0 {
		return tasks
	}
	for _, mf := range missing {
		bestIdx := -1
		bestScore := -1
		mfDir := filepath.Dir(mf)

		for i, t := range tasks {
			score := 0
			for _, f := range t.Files {
				if filepath.Dir(f) == mfDir {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}

		// If no directory match (all scores 0), prefer first implementer
		if bestScore == 0 {
			bestIdx = 0
			for i, t := range tasks {
				if t.Role == "implementer" {
					bestIdx = i
					break
				}
			}
		}

		tasks[bestIdx].Files = append(tasks[bestIdx].Files, mf)
	}
	return tasks
}

// FileCorrection records a single file path correction made by
// correctDecomposerFilePaths. Used for logging and diagnostics.
type FileCorrection struct {
	TaskTitle string
	Original  string
	Corrected string
	Method    string // "ordinal", "levenshtein", or "basename"
}

// correctDecomposerFilePaths detects and fixes file path renames introduced by
// the decomposer LLM. G137: The LLM sometimes "improves" file paths (e.g.,
// ch01.md → chapter-01.md) which breaks gate checks and downstream expectations.
//
// For each file in each task, if it's not in the goal file set, try to find a
// canonical match. Matching strategies (in priority order):
//  1. Same directory + same extension + same numeric ordinal
//  2. Same directory + same extension + Levenshtein distance ≤ threshold
//  3. Same extension + same numeric ordinal (cross-directory, weaker)
//
// Files not in the goal set with no close match are kept as-is (the LLM may
// have legitimately introduced new files).
func correctDecomposerFilePaths(tasks []DecomposedTask, goalFiles []string) ([]DecomposedTask, []FileCorrection) {
	if len(goalFiles) == 0 {
		return tasks, nil
	}

	// Build goal file index for quick lookups
	goalSet := make(map[string]bool, len(goalFiles))
	for _, gf := range goalFiles {
		goalSet[gf] = true
	}

	// Build matching indexes from goal files
	type goalInfo struct {
		path    string
		dir     string
		base    string
		ext     string
		ordinal string
	}
	goalIndex := make([]goalInfo, 0, len(goalFiles))
	for _, gf := range goalFiles {
		goalIndex = append(goalIndex, goalInfo{
			path:    gf,
			dir:     filepath.Dir(gf),
			base:    filepath.Base(gf),
			ext:     filepath.Ext(gf),
			ordinal: extractOrdinal(filepath.Base(gf)),
		})
	}

	var corrections []FileCorrection
	// Track which goal files have already been claimed by corrections
	// to prevent multiple task files mapping to the same goal file
	claimed := make(map[string]bool)

	for i, task := range tasks {
		for j, file := range task.Files {
			if goalSet[file] {
				continue // already matches a goal file exactly
			}

			// Try to find canonical match
			fDir := filepath.Dir(file)
			fBase := filepath.Base(file)
			fExt := filepath.Ext(file)
			fOrd := extractOrdinal(fBase)

			var bestMatch string
			var bestMethod string
			bestScore := 0 // higher is better

			for _, gi := range goalIndex {
				if claimed[gi.path] {
					continue
				}

				// Strategy 1: Same dir + same ext + same ordinal (strongest)
				if fDir == gi.dir && fExt == gi.ext && fOrd != "" && fOrd == gi.ordinal {
					if bestScore < 3 {
						bestMatch = gi.path
						bestMethod = "ordinal"
						bestScore = 3
					}
					continue
				}

				// Strategy 2: Same dir + same ext + low Levenshtein distance
				if fDir == gi.dir && fExt == gi.ext {
					dist := levenshtein(fBase, gi.base)
					threshold := max(len(fBase), len(gi.base)) / 2
					if dist <= threshold && bestScore < 2 {
						bestMatch = gi.path
						bestMethod = "levenshtein"
						bestScore = 2
					}
					continue
				}

				// Strategy 3: Same ext + same ordinal, different dir (weaker)
				if fExt == gi.ext && fOrd != "" && fOrd == gi.ordinal && bestScore < 1 {
					bestMatch = gi.path
					bestMethod = "basename"
					bestScore = 1
				}
			}

			if bestMatch != "" {
				tasks[i].Files[j] = bestMatch
				claimed[bestMatch] = true
				corrections = append(corrections, FileCorrection{
					TaskTitle: task.Title,
					Original:  file,
					Corrected: bestMatch,
					Method:    bestMethod,
				})
			}
		}
	}

	return tasks, corrections
}

// extractOrdinal returns the first contiguous run of digits found in a filename
// stem (without extension). Returns "" if no digits found.
// Examples: "ch01.md" → "01", "chapter-15.md" → "15", "helpers.go" → ""
func extractOrdinal(filename string) string {
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	digits := regexp.MustCompile(`\d+`)
	match := digits.FindString(stem)
	return match
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Use single-row DP
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// findDuplicateFileAssignments returns files that appear in more than one task's
// file list. Each entry maps file path → list of task titles that claim it.
func findDuplicateFileAssignments(tasks []DecomposedTask) map[string][]string {
	fileOwners := make(map[string][]string) // file → task titles
	for _, t := range tasks {
		for _, f := range t.Files {
			fileOwners[f] = append(fileOwners[f], t.Title)
		}
	}
	dupes := make(map[string][]string)
	for file, owners := range fileOwners {
		if len(owners) > 1 {
			dupes[file] = owners
		}
	}
	return dupes
}

// deduplicateFileAssignments ensures each file appears in exactly one task.
// When a file is claimed by multiple tasks, it stays with the task that has the
// highest priority (lowest priority number = most important). Ties break by
// keeping it in the task with the most total files (likely the "main" task for
// that layer).
func deduplicateFileAssignments(tasks []DecomposedTask) []DecomposedTask {
	// Build ownership map: file → best task index
	bestOwner := make(map[string]int) // file → task index
	for i, t := range tasks {
		for _, f := range t.Files {
			prev, exists := bestOwner[f]
			if !exists {
				bestOwner[f] = i
				continue
			}
			// Prefer higher priority (lower number)
			if tasks[i].Priority > tasks[prev].Priority {
				continue // prev has higher priority, keep it
			}
			if tasks[i].Priority < tasks[prev].Priority {
				bestOwner[f] = i
				continue
			}
			// Same priority — prefer the task with more files (likely the "primary" owner)
			if len(tasks[i].Files) > len(tasks[prev].Files) {
				bestOwner[f] = i
			}
		}
	}

	// Rebuild file lists using the ownership map
	for i := range tasks {
		var kept []string
		for _, f := range tasks[i].Files {
			if bestOwner[f] == i {
				kept = append(kept, f)
			}
		}
		tasks[i].Files = kept
	}
	return tasks
}

// detectImplicitSharedFileCoupling checks for a common decomposition failure pattern:
// multiple tasks create handler files (e.g., *_handler.go, *_handlers.go) but none of
// them owns the central routing file (server.go, routes.go, main.go). This pattern
// guarantees merge conflicts because every handler registration modifies the same routing
// file. Warning-only — does not block decomposition (doc 021, Gap 1).
// enforceSpecRoles overrides decomposed task roles with spec-defined roles.
// When a spec pre-defines tasks with specific roles (e.g. deep-researcher),
// the decomposer LLM may reassign them. This function restores the intended roles
// by matching decomposed tasks to spec tasks using title containment.
func enforceSpecRoles(tasks []DecomposedTask, specTasks []SpecTask, c *Conductor) []DecomposedTask {
	overrideCount := 0
	for i := range tasks {
		bestMatch := ""
		for _, st := range specTasks {
			// Match by title containment (case-insensitive)
			if st.Role != "" && strings.Contains(
				strings.ToLower(tasks[i].Title),
				strings.ToLower(firstNWords(st.Title, 4)),
			) {
				bestMatch = st.Role
				break
			}
		}
		if bestMatch != "" && bestMatch != tasks[i].Role {
			// Don't override pi-implementer → implementer. The decomposer
			// intentionally promoted to pi-implementer for bounded tasks.
			// Also don't override deep-researcher → researcher for same reason.
			if (tasks[i].Role == "pi-implementer" && bestMatch == "implementer") ||
				(tasks[i].Role == "deep-researcher" && bestMatch == "researcher") {
				continue
			}
			c.log("Spec role enforcement: %q role %q → %q (spec-defined)", tasks[i].Title, tasks[i].Role, bestMatch)
			tasks[i].Role = bestMatch
			overrideCount++
		}
	}
	if overrideCount > 0 {
		c.log("Spec role enforcement: overrode %d/%d task roles", overrideCount, len(tasks))
	}
	return tasks
}

// firstNWords returns the first n whitespace-delimited words of s.
func firstNWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return s
	}
	return strings.Join(words[:n], " ")
}

func enforceSharedFileCoupling(tasks []DecomposedTask, c *Conductor) []DecomposedTask {
	if len(tasks) < 2 {
		return tasks
	}

	// Routing file basenames that are commonly shared
	routingFiles := map[string]bool{
		"server.go": true, "routes.go": true, "main.go": true,
		"router.go": true, "app.go": true,
		"index.ts": true, "index.js": true,
		"__init__.py": true,
	}

	// Track handler tasks (index into tasks slice) and check if any owns a routing file
	var handlerTaskIndices []int
	routingFileOwned := false

	for i, t := range tasks {
		hasHandler := false
		for _, f := range t.Files {
			base := filepath.Base(f)
			if strings.HasSuffix(base, "_handler.go") || strings.HasSuffix(base, "_handlers.go") {
				hasHandler = true
			}
			if routingFiles[base] {
				routingFileOwned = true
			}
		}
		// Also check additional_files
		for _, f := range t.AdditionalFiles {
			base := filepath.Base(f)
			if strings.HasSuffix(base, "_handler.go") || strings.HasSuffix(base, "_handlers.go") {
				hasHandler = true
			}
			if routingFiles[base] {
				routingFileOwned = true
			}
		}
		if hasHandler {
			handlerTaskIndices = append(handlerTaskIndices, i)
		}
	}

	if len(handlerTaskIndices) >= 2 && !routingFileOwned {
		// Infer routing file path from the first handler file's directory
		routingFilePath := "server.go"
		for _, f := range tasks[handlerTaskIndices[0]].Files {
			base := filepath.Base(f)
			if strings.HasSuffix(base, "_handler.go") || strings.HasSuffix(base, "_handlers.go") {
				routingFilePath = filepath.Join(filepath.Dir(f), "server.go")
				break
			}
		}

		// Find the highest-priority handler task (lowest Priority value = highest priority)
		primaryIdx := handlerTaskIndices[0]
		for _, idx := range handlerTaskIndices[1:] {
			if tasks[idx].Priority < tasks[primaryIdx].Priority {
				primaryIdx = idx
			}
		}

		// Assign routing file to the primary handler task
		tasks[primaryIdx].Files = append(tasks[primaryIdx].Files, routingFilePath)

		// Make all other handler tasks depend on the primary one
		primaryRef := tasks[primaryIdx].Title
		if tasks[primaryIdx].ID != "" {
			primaryRef = tasks[primaryIdx].ID
		}
		depCount := 0
		for _, idx := range handlerTaskIndices {
			if idx == primaryIdx {
				continue
			}
			tasks[idx].DependsOn = append(tasks[idx].DependsOn, primaryRef)
			depCount++
		}

		c.log("ENFORCED: %s assigned to task %s. %d other handler tasks now depend on it.",
			routingFilePath, primaryRef, depCount)
	}

	return tasks
}

// computeEffectiveMaxTasks returns the higher of maxTasks and the dynamic floor
// derived from goal file count and per-task file cap. This ensures enough tasks
// exist to fit all goal files within the file cap.
func computeEffectiveMaxTasks(maxTasks, maxFilesPerTask, goalFileCount int) int {
	if maxFilesPerTask <= 0 || goalFileCount <= 0 {
		return maxTasks
	}
	// Dynamic floor: ceil(goalFiles / maxFilesPerTask)
	floor := (goalFileCount + maxFilesPerTask - 1) / maxFilesPerTask
	if floor > maxTasks {
		return floor
	}
	return maxTasks
}

// enforceAtomicTasks detects when the decomposer or file-cap splitter broke an atomic
// task into fragments (identified by title substring match or "(1/N)" suffix patterns).
// It re-merges fragments back into the original atomic task by combining files and
// descriptions. This ensures critical tasks run as a single agent.
func enforceAtomicTasks(tasks []DecomposedTask, atomicTitles []string) []DecomposedTask {
	if len(atomicTitles) == 0 {
		return tasks
	}

	// fragmentPartRe matches titles like "CSS Design System (1/3)", "CSS Design System (2/3)"
	fragmentPartRe := regexp.MustCompile(`^(.+?)\s*\(\d+/\d+\)$`)

	// Build index: for each atomic title, collect matching task indices
	type atomicGroup struct {
		indices []int
	}
	groups := make(map[string]*atomicGroup) // normalized atomic title → group

	for i, t := range tasks {
		titleToCheck := t.Title
		// Strip "(N/M)" suffix if present
		if m := fragmentPartRe.FindStringSubmatch(t.Title); len(m) >= 2 {
			titleToCheck = strings.TrimSpace(m[1])
		}
		for _, at := range atomicTitles {
			if strings.Contains(strings.ToLower(titleToCheck), strings.ToLower(at)) ||
				strings.Contains(strings.ToLower(at), strings.ToLower(titleToCheck)) {
				if groups[at] == nil {
					groups[at] = &atomicGroup{}
				}
				groups[at].indices = append(groups[at].indices, i)
				break
			}
		}
	}

	// Re-merge fragments: keep the first task, absorb files and descriptions from the rest
	mergedIndices := make(map[int]bool)
	for _, group := range groups {
		if len(group.indices) <= 1 {
			continue // not split, nothing to merge
		}
		primary := group.indices[0]
		for _, idx := range group.indices[1:] {
			mergedIndices[idx] = true
			// Merge files (deduplicate)
			seen := make(map[string]bool)
			for _, f := range tasks[primary].Files {
				seen[f] = true
			}
			for _, f := range tasks[idx].Files {
				if !seen[f] {
					tasks[primary].Files = append(tasks[primary].Files, f)
					seen[f] = true
				}
			}
			// Append description from fragment
			if tasks[idx].Description != "" && tasks[idx].Description != tasks[primary].Description {
				tasks[primary].Description += "\n\n" + tasks[idx].Description
			}
		}
		// Clean up the title if it had a suffix
		if m := fragmentPartRe.FindStringSubmatch(tasks[primary].Title); len(m) >= 2 {
			tasks[primary].Title = strings.TrimSpace(m[1])
		}
	}

	// Rebuild task list without absorbed fragments
	var result []DecomposedTask
	for i, t := range tasks {
		if !mergedIndices[i] {
			result = append(result, t)
		}
	}
	return result
}

// enforceFileCap splits any task whose file list exceeds maxFilesPerTask.
// Split tasks inherit the parent's role, priority, and dependencies.
// This is a mechanical post-processing step — no LLM call required.
func enforceFileCap(tasks []DecomposedTask, maxFilesPerTask int) []DecomposedTask {
	var result []DecomposedTask
	for _, t := range tasks {
		if len(t.Files) <= maxFilesPerTask {
			result = append(result, t)
			continue
		}
		splits := splitTaskByFileCap(t, maxFilesPerTask)
		result = append(result, splits...)
	}
	return result
}

// splitTaskByFileCap splits a single task into multiple tasks, each with at most
// maxFiles files. Files are grouped by directory to maintain locality. Each
// resulting task inherits the parent's role, priority, and dependencies.
func splitTaskByFileCap(task DecomposedTask, maxFiles int) []DecomposedTask {
	if len(task.Files) <= maxFiles {
		return []DecomposedTask{task}
	}

	// Group files by directory
	dirGroups := make(map[string][]string)
	var dirOrder []string
	for _, f := range task.Files {
		dir := filepath.Dir(f)
		if _, exists := dirGroups[dir]; !exists {
			dirOrder = append(dirOrder, dir)
		}
		dirGroups[dir] = append(dirGroups[dir], f)
	}
	// Sort directories for deterministic output
	sort.Strings(dirOrder)

	// Greedy bin-pack: directories stay together when possible
	var bins [][]string
	var currentBin []string

	for _, dir := range dirOrder {
		files := dirGroups[dir]

		// If a single directory exceeds the cap, split it across bins
		if len(files) > maxFiles {
			// Flush current bin if non-empty
			if len(currentBin) > 0 {
				bins = append(bins, currentBin)
				currentBin = nil
			}
			for i := 0; i < len(files); i += maxFiles {
				end := i + maxFiles
				if end > len(files) {
					end = len(files)
				}
				bins = append(bins, files[i:end])
			}
			continue
		}

		// If adding this directory would exceed the cap, start a new bin
		if len(currentBin)+len(files) > maxFiles {
			if len(currentBin) > 0 {
				bins = append(bins, currentBin)
			}
			currentBin = make([]string, len(files))
			copy(currentBin, files)
		} else {
			currentBin = append(currentBin, files...)
		}
	}
	if len(currentBin) > 0 {
		bins = append(bins, currentBin)
	}

	// Create a task per bin
	totalParts := len(bins)
	result := make([]DecomposedTask, totalParts)
	for i, bin := range bins {
		suffix := ""
		if totalParts > 1 {
			suffix = fmt.Sprintf(" (part %d/%d)", i+1, totalParts)
		}
		result[i] = DecomposedTask{
			Title:              task.Title + suffix,
			Description:        task.Description,
			Role:               task.Role,
			Priority:           task.Priority,
			PriorityLabel:      task.PriorityLabel,
			DependsOn:          task.DependsOn,
			Files:              bin,
			AcceptanceCriteria: filterACForFiles(task.AcceptanceCriteria, bin),
		}
	}
	return result
}

// filterACForFiles returns acceptance criteria lines that reference any of the
// given files. If no lines match (or AC is empty), returns the full AC unchanged
// to avoid losing criteria.
func filterACForFiles(ac string, files []string) string {
	if ac == "" || len(files) == 0 {
		return ac
	}

	// Build a set of basenames for matching
	baseNames := make(map[string]bool, len(files))
	for _, f := range files {
		baseNames[strings.ToLower(filepath.Base(f))] = true
	}

	lines := strings.Split(ac, "\n")
	var matched []string
	for _, line := range lines {
		lineLower := strings.ToLower(line)
		// Keep the line if it references any of our files
		keep := false
		for base := range baseNames {
			if strings.Contains(lineLower, base) {
				keep = true
				break
			}
		}
		if keep {
			matched = append(matched, line)
		}
	}

	// If no lines matched, return full AC (don't lose criteria)
	if len(matched) == 0 {
		return ac
	}
	return strings.Join(matched, "\n")
}

// validateFeatureClusters ensures hierarchical decomposition produced valid clusters.
// Falls back to flat (clears all clusters) if clustering is degenerate:
// - All tasks in one cluster
// - No clusters assigned
// - Fewer than 2 distinct clusters for 4+ tasks
func validateFeatureClusters(tasks []DecomposedTask, c *Conductor) []DecomposedTask {
	clusters := make(map[string]int)
	unassigned := 0
	for _, t := range tasks {
		if t.FeatureCluster == "" {
			unassigned++
		} else {
			clusters[t.FeatureCluster]++
		}
	}

	// Fall back to flat if no clusters or degenerate grouping
	fallback := false
	reason := ""
	if len(clusters) == 0 {
		fallback = true
		reason = "no clusters assigned"
	} else if len(clusters) == 1 && len(tasks) > 1 {
		fallback = true
		reason = "all tasks in single cluster"
	} else if len(clusters) < 2 && len(tasks) >= 4 {
		fallback = true
		reason = fmt.Sprintf("only %d cluster(s) for %d tasks", len(clusters), len(tasks))
	}

	if fallback {
		c.log("Hierarchical decomposition fallback to flat: %s", reason)
		for i := range tasks {
			tasks[i].FeatureCluster = ""
		}
		return tasks
	}

	if unassigned > 0 {
		c.log("WARNING: %d/%d tasks missing feature_cluster — assigning to 'misc'", unassigned, len(tasks))
		for i := range tasks {
			if tasks[i].FeatureCluster == "" {
				tasks[i].FeatureCluster = "misc"
			}
		}
	}

	c.log("Hierarchical decomposition: %d tasks across %d clusters", len(tasks), len(clusters))
	for name, count := range clusters {
		c.log("  cluster %q: %d tasks", name, count)
	}
	return tasks
}

// distinctClusters returns the unique non-empty cluster names from a task list.
func distinctClusters(tasks []DecomposedTask) []string {
	seen := make(map[string]bool)
	var clusters []string
	for _, t := range tasks {
		if t.FeatureCluster != "" && !seen[t.FeatureCluster] {
			seen[t.FeatureCluster] = true
			clusters = append(clusters, t.FeatureCluster)
		}
	}
	sort.Strings(clusters)
	return clusters
}
