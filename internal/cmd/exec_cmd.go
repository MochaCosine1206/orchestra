package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewExecCmd creates the exec subcommand (execute a multi-phase YAML spec).
func NewExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec",
		Short: "Execute a multi-phase YAML spec",
		Long: `Execute a multi-phase orchestration spec. Phases run in topological order,
each gated by a test command. Tasks within each phase are orchestrated via Go().

Use --dry-run to preview the execution plan without running anything.
Use --start-phase to resume from a specific phase (skipping prior phases).`,
		RunE: runExec,
	}

	cmd.Flags().String("spec", "", "Path to YAML spec file (required)")
	cmd.Flags().String("start-phase", "", "Resume from this phase ID (skip prior phases)")
	cmd.Flags().Bool("dry-run", false, "Show execution plan without running")
	cmd.Flags().Int("max-parallel", 8, "Maximum concurrent agents per phase")
	cmd.Flags().Int("interval", 10, "Monitor poll interval in seconds")
	cmd.Flags().Bool("review", false, "Enable review gates")
	cmd.Flags().Bool("reconcile", true, "Run reconciliation after final phase")
	cmd.Flags().Bool("repo-map", true, "Include repo map in task specs")
	cmd.Flags().String("base-branch", "", "Base branch (auto-detect if empty)")
	cmd.Flags().String("runtime", "local", "Execution runtime: local, docker")
	cmd.Flags().String("merge-mode", "local", "Merge mode: local, pr")
	cmd.Flags().Bool("json", false, "Machine-readable JSON output")
	cmd.Flags().Bool("force", false, "Skip start-phase gate validation for prior phases")
	cmd.Flags().Bool("require-version", false, "Abort if Claude CLI version is below minimum")
	_ = cmd.MarkFlagRequired("spec")

	return cmd
}

// runExec is the RunE handler for the exec command.
func runExec(cmd *cobra.Command, args []string) error {
	requireVersion, _ := cmd.Flags().GetBool("require-version")
	if err := checkCLIVersion(cmd, requireVersion); err != nil {
		return err
	}

	specPath, _ := cmd.Flags().GetString("spec")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// 1. Parse spec
	spec, err := orchestrator.ParseSpec(specPath)
	if err != nil {
		return fmt.Errorf("parsing spec: %w", err)
	}

	// 2. Validate spec
	validation := orchestrator.ValidateSpec(spec)
	for _, w := range validation.Warnings {
		cmd.PrintErrf("Warning: [%s] %s\n", w.Rule, w.Message)
	}
	if !validation.IsValid() {
		for _, e := range validation.Errors {
			cmd.PrintErrf("Error: [%s] %s\n", e.Rule, e.Message)
		}
		return fmt.Errorf("spec validation failed: %s", validation.Errors[0].Message)
	}

	// 3. Dry run — print execution plan and return
	if dryRun {
		return printExecPlan(cmd, spec)
	}

	// 4. Full execution
	startPhase, _ := cmd.Flags().GetString("start-phase")
	maxParallel, _ := cmd.Flags().GetInt("max-parallel")
	interval, _ := cmd.Flags().GetInt("interval")
	review, _ := cmd.Flags().GetBool("review")
	reconcile, _ := cmd.Flags().GetBool("reconcile")
	repoMap, _ := cmd.Flags().GetBool("repo-map")
	baseBranch, _ := cmd.Flags().GetString("base-branch")
	runtime, _ := cmd.Flags().GetString("runtime")
	mergeMode, _ := cmd.Flags().GetString("merge-mode")

	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	// Derive repo root from the spec file's directory, not CWD.
	// This ensures gate commands, worktrees, and local-runner.json
	// are found in the project directory regardless of where the
	// user runs the command from.
	repoRoot, _ := os.Getwd()
	if specPath != "" {
		specAbs, absErr := filepath.Abs(specPath)
		if absErr == nil {
			repoRoot = filepath.Dir(specAbs)
		}
	}
	runner := &orchestrator.ExecRunner{}
	c, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: repoRoot,
		Runner:   runner,
	})
	if err != nil {
		return fmt.Errorf("creating conductor: %w", err)
	}

	if err := c.SetupLogFile(); err != nil {
		cmd.PrintErrf("Warning: conductor log: %v\n", err)
	}
	defer c.CloseLogFile()

	force, _ := cmd.Flags().GetBool("force")

	opts := orchestrator.GoSpecOpts{
		StartPhase:  startPhase,
		MaxParallel: maxParallel,
		Interval:    interval,
		Review:      review,
		Reconcile:   reconcile,
		RepoMap:     repoMap,
		BaseBranch:  baseBranch,
		Runtime:     runtime,
		MergeMode:   mergeMode,
		Force:       force,
	}

	jsonMode, _ := cmd.Flags().GetBool("json")

	result, err := c.GoSpec(cmd.Context(), spec, opts)
	if err != nil {
		// Print partial results even on failure.
		if result != nil {
			printExecSummary(cmd, result)
		}
		oe := wrapExecError(err, result)
		if jsonMode {
			PrintJSONError(cmd.ErrOrStderr(), oe)
		}
		return oe
	}

	printExecSummary(cmd, result)

	// JSON success output.
	if jsonMode {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
	}

	// Detect task-level failures even when GoSpec() returns nil error.
	if result.TotalFailed > 0 {
		oe := buildExecTaskFailureError(result)
		if jsonMode {
			PrintJSONError(cmd.ErrOrStderr(), oe)
		}
		return oe
	}

	return nil
}

// printExecPlan shows the execution plan without running anything.
func printExecPlan(cmd *cobra.Command, spec *orchestrator.OrchestraSpec) error {
	order, err := orchestrator.TopoSortPhases(spec.Phases)
	if err != nil {
		return fmt.Errorf("topological sort: %w", err)
	}

	phaseMap := make(map[string]orchestrator.Phase, len(spec.Phases))
	for _, p := range spec.Phases {
		phaseMap[p.ID] = p
	}

	cmd.Printf("Spec: %s (%d phases)\n\n", spec.Metadata.Title, len(spec.Phases))

	for i, id := range order {
		phase := phaseMap[id]

		// Count roles
		roleCounts := make(map[string]int)
		for _, t := range phase.Tasks {
			roleCounts[t.Role]++
		}
		var roleParts []string
		for role, count := range roleCounts {
			roleParts = append(roleParts, fmt.Sprintf("%s\u00d7%d", role, count))
		}

		cmd.Printf("Phase %d: %s [%s]", i+1, phase.Name, phase.ID)
		if len(phase.DependsOn) > 0 {
			cmd.Printf(" (depends: %s)", strings.Join(phase.DependsOn, ", "))
		}
		cmd.Printf("\n")

		cmd.Printf("  Tasks: %d (%s)\n", len(phase.Tasks), strings.Join(roleParts, ", "))

		if phase.Gate.TestCmd != "" {
			cmd.Printf("  Gate:  %s\n", phase.Gate.TestCmd)
		}

		cmd.Printf("\n")
	}

	return nil
}

// wrapExecError wraps a GoSpec error into a categorized OrchestraError
// with phase-level detail and resume suggestions.
func wrapExecError(err error, result *orchestrator.GoSpecResult) *OrchestraError {
	exitCode := exitCodeHeuristic(err)
	category := CategoryFatal
	suggestions := suggestFromRawError(err)

	var failedPhaseIDs []string
	var lastPassedPhase string
	var failedTasks []TaskFailure

	if result != nil {
		for _, pr := range result.PhaseResults {
			if pr.Skipped {
				continue
			}
			if pr.GatePassed {
				lastPassedPhase = pr.PhaseID
			} else {
				failedPhaseIDs = append(failedPhaseIDs, pr.PhaseID)
				// Collect per-task failures from the phase's GoResult.
				if pr.GoResult != nil && pr.GoResult.ReconcileResult != nil {
					for _, ft := range pr.GoResult.ReconcileResult.FailedTasks {
						failedTasks = append(failedTasks, TaskFailure{
							TaskID:      ft.TaskID,
							Title:       ft.Title,
							FailureType: ft.FailureType,
							Suggestion:  TaskRecoverySuggestion(ft.FailureType),
						})
					}
				}
			}
		}
		if result.TotalFailed > 0 && result.TotalDone > 0 {
			exitCode = ExitPartialFailure
			category = CategoryPartial
		} else if result.TotalFailed > 0 {
			exitCode = ExitTotalFailure
		}
	}

	detail := fmt.Sprintf("Failed phases: %s", strings.Join(failedPhaseIDs, ", "))
	if lastPassedPhase != "" {
		suggestions = append(suggestions,
			fmt.Sprintf("orchestra exec --spec <spec> --start-phase %s  # resume after last passed phase", lastPassedPhase))
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions,
			"orchestra status --json  # inspect failure details",
			"orchestra reset  # clean slate and retry")
	}

	return &OrchestraError{
		ExitCode:    exitCode,
		Category:    category,
		Summary:     fmt.Sprintf("spec execution failed: %s", err.Error()),
		Detail:      detail,
		FailedTasks: failedTasks,
		Suggestions: suggestions,
		Cause:       err,
	}
}

// buildExecTaskFailureError builds an OrchestraError from a GoSpecResult with failed tasks.
func buildExecTaskFailureError(result *orchestrator.GoSpecResult) *OrchestraError {
	exitCode := ExitPartialFailure
	if result.TotalDone == 0 {
		exitCode = ExitTotalFailure
	}

	var failedTasks []TaskFailure
	var failedPhaseIDs []string
	var lastPassedPhase string

	for _, pr := range result.PhaseResults {
		if pr.Skipped {
			continue
		}
		if pr.GatePassed {
			lastPassedPhase = pr.PhaseID
		} else {
			failedPhaseIDs = append(failedPhaseIDs, pr.PhaseID)
		}
		if pr.GoResult != nil && pr.GoResult.ReconcileResult != nil {
			for _, ft := range pr.GoResult.ReconcileResult.FailedTasks {
				failedTasks = append(failedTasks, TaskFailure{
					TaskID:      ft.TaskID,
					Title:       ft.Title,
					FailureType: ft.FailureType,
					Suggestion:  TaskRecoverySuggestion(ft.FailureType),
				})
			}
		}
	}

	suggestions := []string{
		"orchestra status --json  # inspect failed task details",
		"orchestra reset  # clean slate and retry",
	}
	if lastPassedPhase != "" {
		suggestions = append(suggestions,
			fmt.Sprintf("orchestra exec --spec <spec> --start-phase %s  # resume after last passed phase", lastPassedPhase))
	}

	detail := fmt.Sprintf("Duration: %s", result.Duration.Round(1000000000))
	if len(failedPhaseIDs) > 0 {
		detail += fmt.Sprintf(" | Failed phases: %s", strings.Join(failedPhaseIDs, ", "))
	}

	return &OrchestraError{
		ExitCode:    exitCode,
		Category:    CategoryPartial,
		Summary:     fmt.Sprintf("%d of %d tasks failed", result.TotalFailed, result.TotalDone+result.TotalFailed),
		Detail:      detail,
		FailedTasks: failedTasks,
		Suggestions: suggestions,
	}
}

// printExecSummary prints the final summary after spec execution.
func printExecSummary(cmd *cobra.Command, result *orchestrator.GoSpecResult) {
	passed := 0
	failed := 0
	skipped := 0
	for _, pr := range result.PhaseResults {
		if pr.Skipped {
			skipped++
		} else if pr.GatePassed {
			passed++
		} else {
			failed++
		}
	}

	cmd.Printf("\n--- Spec Execution Summary ---\n")
	cmd.Printf("Spec:     %s\n", result.SpecTitle)
	cmd.Printf("Phases:   %d passed, %d failed, %d skipped\n", passed, failed, skipped)
	cmd.Printf("Tasks:    %d done, %d failed\n", result.TotalDone, result.TotalFailed)
	cmd.Printf("Duration: %s\n", result.Duration.Round(1000000000))
}
