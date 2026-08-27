package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RalphRefineOpts configures a Ralph Wiggum spec refinement loop.
type RalphRefineOpts struct {
	SpecPath         string // path to spec YAML
	RequirementsPath string // path to requirements doc
	ProjectRoot      string // working directory for claude -p
	MaxIterations    int    // default 4
}

// RalphRefineResult holds the outcome of a Ralph refinement run.
type RalphRefineResult struct {
	Iterations    int
	InitialTasks  int
	FinalTasks    int
	Converged     bool
	FinalSpecPath string
}

// RalphRefineSpec runs the Ralph Wiggum file-based refinement loop:
// 1. Write a prompt that instructs claude to Read spec + requirements, find gaps, Edit to fix
// 2. Spawn claude -p — agent reads both files FROM DISK, uses Edit tool
// 3. Check task count convergence — if unchanged, it converged
// 4. Loop up to MaxIterations
//
// This is fundamentally different from SpecRefiner (additive, diverges).
// Ralph edits the existing spec in place (converges).
func RalphRefineSpec(ctx context.Context, opts RalphRefineOpts) (*RalphRefineResult, error) {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 4
	}

	specRel, err := filepath.Rel(opts.ProjectRoot, opts.SpecPath)
	if err != nil {
		specRel = opts.SpecPath
	}
	reqRel, err := filepath.Rel(opts.ProjectRoot, opts.RequirementsPath)
	if err != nil {
		reqRel = opts.RequirementsPath
	}

	prompt := fmt.Sprintf(`Read %s and %s.
Compare the spec against the requirements. Find gaps where requirements are not covered by spec tasks. Edit the spec to fix.
Rules:
- Each file path must appear in EXACTLY ONE task within each phase
- If two tasks in the same phase need the same file, consolidate them
- Cross-phase file overlap is OK (extending code across phases)
- Keep correct existing tasks, add missing tasks
- All code tasks must use role: implementer
- Research tasks must use role: deep-researcher
- Do not create duplicate tasks
- Dev tooling (linting, testing frameworks, pre-commit hooks) must be in Phase 1
- Every gate test_cmd must reference tools that exist in the project
- NEVER modify gate test_cmd values — these are authoritative and must remain unchanged
- NEVER modify gate mode values
- If a test_cmd seems wrong, add a comment but do NOT change the value
- Verify YAML indentation after every Edit
When no file conflicts AND all requirements covered, output: COMPLETE
`, specRel, reqRel)

	result := &RalphRefineResult{
		InitialTasks:  countSpecTitles(opts.SpecPath),
		FinalSpecPath: opts.SpecPath,
	}

	prevTasks := 0
	for iter := 1; iter <= opts.MaxIterations; iter++ {
		currTasks := countSpecTitles(opts.SpecPath)
		if currTasks == prevTasks && iter > 1 {
			result.Converged = true
			result.Iterations = iter - 1
			result.FinalTasks = currTasks
			return result, nil
		}
		prevTasks = currTasks

		cmd := exec.CommandContext(ctx, "claude",
			"-p", prompt,
			"--model", "claude-opus-4-6",
			"--permission-mode", "bypassPermissions",
			"--output-format", "stream-json", "--verbose")
		cmd.Dir = opts.ProjectRoot
		cmd.Stdin, _ = os.Open(os.DevNull)
		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			// Check if it's a context cancellation
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Log but continue — agent may have partially succeeded
			_ = out
		}
	}

	result.Iterations = opts.MaxIterations
	result.FinalTasks = countSpecTitles(opts.SpecPath)
	result.Converged = result.FinalTasks == prevTasks
	return result, nil
}

// countSpecTitles counts "title:" lines in a YAML spec file for convergence detection.
func countSpecTitles(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "title:") {
			count++
		}
	}
	return count
}
