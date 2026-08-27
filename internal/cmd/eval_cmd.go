package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/eval"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/version"
	"github.com/spf13/cobra"
)

// NewEvalCmd creates the eval subcommand with sub-commands for the evaluation framework.
func NewEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluation framework for testing orchestrator versions",
		Long: `Run evaluations, compare versions, manage scenarios, and view reports.

Sub-commands:
  run        Execute scenarios against the current version
  compare    A/B test two versions
  scenarios  List, add, seed, or remove eval scenarios
  report     Show results for an eval run`,
	}

	cmd.AddCommand(
		newEvalRunCmd(),
		newEvalCompareCmd(),
		newEvalScenariosCmd(),
		newEvalReportCmd(),
	)

	return cmd
}

func newEvalRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute scenarios against the current version",
		Long: `Run all eval scenarios (or a filtered subset) against the current orchestrator version.
Creates eval_runs and eval_results entries in the database.
Each scenario is judged by an LLM against the role-specific rubric.

Examples:
  orchestra eval run --version v1
  orchestra eval run --version v1 --role implementer`,
		RunE: runEvalRun,
	}

	cmd.Flags().String("version", "", "Version ID to evaluate (required)")
	cmd.Flags().String("role", "", "Filter scenarios by role")
	cmd.MarkFlagRequired("version")

	return cmd
}

func runEvalRun(cmd *cobra.Command, args []string) error {
	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	ctx := context.Background()
	versionID, _ := cmd.Flags().GetString("version")
	role, _ := cmd.Flags().GetString("role")

	// Verify version exists
	ver, err := d.GetEvalVersion(ctx, versionID)
	if err != nil {
		return fmt.Errorf("looking up version: %w", err)
	}
	if ver == nil {
		return fmt.Errorf("version %q not found", versionID)
	}

	// Get scenarios
	var scenarios []db.EvalScenario
	if role != "" {
		scenarios, err = d.ListEvalScenariosForRole(ctx, role)
	} else {
		scenarios, err = d.ListEvalScenarios(ctx)
	}
	if err != nil {
		return fmt.Errorf("listing scenarios: %w", err)
	}

	if len(scenarios) == 0 {
		cmd.Println("No scenarios found.")
		return nil
	}

	cmd.Printf("Running %d scenario(s) against version %s...\n", len(scenarios), versionID)

	// Create the runner for LLM judge calls
	runner := &orchestrator.ExecRunner{}

	var passed, failed int
	for _, s := range scenarios {
		runID := fmt.Sprintf("run-%s-%s-%d", versionID, s.ID, time.Now().UnixNano())
		now := sql.NullTime{Time: time.Now(), Valid: true}
		run := db.EvalRun{
			ID:         runID,
			VersionID:  versionID,
			ScenarioID: s.ID,
			StartedAt:  now,
			Status:     "running",
		}
		if err := d.InsertEvalRun(ctx, run); err != nil {
			cmd.PrintErrf("Failed to create run for scenario %s: %v\n", s.ID, err)
			continue
		}

		// Convert db.EvalScenario to eval.Scenario
		scenario := dbScenarioToEval(s)

		// Get the role-specific rubric
		rubric := eval.RubricForRole(s.Role)

		// Read real transcript from JSONL log if scenario has a source task ID.
		// Fall back to synthetic transcript for seeded/synthetic scenarios.
		logsDir := version.LogsDir
		var transcript string
		if scenario.TaskID != "" {
			if t, err := eval.ReadTaskTranscript(logsDir, scenario.TaskID); err == nil {
				transcript = t
			}
		}
		if transcript == "" {
			transcript = eval.SyntheticTranscript(scenario, versionID)
		}

		// Judge the transcript
		result, err := eval.Judge(ctx, runner, scenario, transcript, rubric)
		if err != nil {
			cmd.PrintErrf("  Judge failed for scenario %s: %v\n", s.ID, err)
			if updateErr := d.UpdateEvalRunStatus(ctx, runID, "failed"); updateErr != nil {
				cmd.PrintErrf("  Failed to update run status: %v\n", updateErr)
			}
			failed++
			continue
		}

		// Store results in the database
		for _, w := range rubric.Rubric.Weights {
			score, ok := result.Scores[w.Metric]
			if !ok {
				continue
			}
			resultID := fmt.Sprintf("res-%s-%s-%d", runID, w.Metric, time.Now().UnixNano())
			evalResult := db.EvalResult{
				ID:      resultID,
				RunID:   runID,
				Metric:  w.Metric,
				Score:   score,
				Weight:  w.Weight,
				Details: sql.NullString{String: result.Reasoning, Valid: result.Reasoning != ""},
			}
			if err := d.InsertEvalResult(ctx, evalResult); err != nil {
				cmd.PrintErrf("  Failed to store result %s: %v\n", w.Metric, err)
			}
		}

		// Update run status
		status := "passed"
		if !result.Pass {
			status = "failed"
		}
		if err := d.UpdateEvalRunStatus(ctx, runID, status); err != nil {
			cmd.PrintErrf("  Failed to update run status: %v\n", err)
		}

		if result.Pass {
			passed++
		} else {
			failed++
		}

		cmd.Printf("  %s: composite=%.2f pass=%v (%s)\n", s.ID, result.Composite, result.Pass, status)
	}

	cmd.Printf("\nSummary: %d passed, %d failed out of %d scenario(s)\n", passed, failed, len(scenarios))
	return nil
}

func newEvalCompareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "A/B test two orchestrator versions",
		Long: `Run an A/B comparison between two versions using the eval judge and statistical tests.
Uses bootstrap CI and Wilcoxon signed-rank test for significance.

Examples:
  orchestra eval compare --baseline v1 --candidate v2`,
		RunE: runEvalCompare,
	}

	cmd.Flags().String("baseline", "", "Baseline version ID (required)")
	cmd.Flags().String("candidate", "", "Candidate version ID (required)")
	cmd.MarkFlagRequired("baseline")
	cmd.MarkFlagRequired("candidate")

	return cmd
}

func runEvalCompare(cmd *cobra.Command, args []string) error {
	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	ctx := context.Background()
	baselineID, _ := cmd.Flags().GetString("baseline")
	candidateID, _ := cmd.Flags().GetString("candidate")

	// Load scenarios for A/B test
	dbScenarios, err := d.ListEvalScenarios(ctx)
	if err != nil {
		return fmt.Errorf("listing scenarios: %w", err)
	}
	if len(dbScenarios) == 0 {
		cmd.Println("No scenarios found. Run 'orchestra eval scenarios seed' first.")
		return nil
	}

	// Convert to eval.Scenario
	var scenarios []eval.Scenario
	for _, s := range dbScenarios {
		scenarios = append(scenarios, dbScenarioToEval(s))
	}

	runner := &orchestrator.ExecRunner{}
	config := eval.ABTestConfig{
		BaselineVersion:  baselineID,
		CandidateVersion: candidateID,
		Scenarios:        scenarios,
		Runner:           runner,
	}

	cmd.Printf("Running A/B comparison: %s vs %s on %d scenario(s)...\n", baselineID, candidateID, len(scenarios))

	result, err := eval.RunABTest(ctx, config)
	if err != nil {
		return fmt.Errorf("A/B test failed: %w", err)
	}

	// Print results
	cmd.Printf("\n--- A/B Test Results ---\n")
	cmd.Printf("Baseline mean:  %.3f\n", result.BaselineMean)
	cmd.Printf("Candidate mean: %.3f\n", result.CandidateMean)
	cmd.Printf("Delta:          %.3f\n", result.Delta)
	cmd.Printf("Bootstrap CI:   [%.3f, %.3f]\n", result.BootstrapCI[0], result.BootstrapCI[1])
	cmd.Printf("Wilcoxon p:     %.4f\n", result.WilcoxonP)
	cmd.Printf("Verdict:        %s\n", result.Verdict)

	cmd.Printf("\n--- Per-Scenario Results ---\n")
	cmd.Printf("%-36s  %10s  %10s  %8s  %8s\n", "SCENARIO", "BASELINE", "CANDIDATE", "B-PASS", "C-PASS")
	for _, sr := range result.ScenarioResults {
		cmd.Printf("%-36s  %10.3f  %10.3f  %8v  %8v\n",
			sr.ScenarioID, sr.BaselineScore, sr.CandidateScore, sr.BaselinePass, sr.CandidatePass)
	}

	return nil
}

func newEvalScenariosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenarios",
		Short: "Manage eval scenarios",
		Long:  `List, add, seed, or remove eval scenarios from the database.`,
	}

	cmd.AddCommand(
		newEvalScenariosListCmd(),
		newEvalScenariosAddCmd(),
		newEvalScenariosRemoveCmd(),
		newEvalScenariosSeedCmd(),
		newEvalScenariosCurateCmd(),
	)

	return cmd
}

func newEvalScenariosListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all eval scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			scenarios, err := d.ListEvalScenarios(context.Background())
			if err != nil {
				return fmt.Errorf("listing scenarios: %w", err)
			}

			if len(scenarios) == 0 {
				cmd.Println("No eval scenarios found.")
				return nil
			}

			cmd.Printf("%-36s  %-12s  %-10s  %-10s  %s\n", "ID", "ROLE", "CATEGORY", "DIFFICULTY", "GOAL")
			for _, s := range scenarios {
				cat := ""
				if s.Category.Valid {
					cat = s.Category.String
				}
				diff := ""
				if s.Difficulty.Valid {
					diff = s.Difficulty.String
				}
				goal := s.Goal
				if len(goal) > 60 {
					goal = goal[:57] + "..."
				}
				cmd.Printf("%-36s  %-12s  %-10s  %-10s  %s\n", s.ID, s.Role, cat, diff, goal)
			}
			return nil
		},
	}
}

func newEvalScenariosAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new eval scenario",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			role, _ := cmd.Flags().GetString("role")
			goal, _ := cmd.Flags().GetString("goal")
			category, _ := cmd.Flags().GetString("category")
			difficulty, _ := cmd.Flags().GetString("difficulty")

			id := fmt.Sprintf("scenario-%d", time.Now().UnixNano())
			s := db.EvalScenario{
				ID:   id,
				Role: role,
				Goal: goal,
			}
			if category != "" {
				s.Category = sql.NullString{String: category, Valid: true}
			}
			if difficulty != "" {
				s.Difficulty = sql.NullString{String: difficulty, Valid: true}
			}

			if err := d.InsertEvalScenario(context.Background(), s); err != nil {
				return fmt.Errorf("inserting scenario: %w", err)
			}
			cmd.Printf("Created scenario %s\n", id)
			return nil
		},
	}

	cmd.Flags().String("role", "", "Agent role (required)")
	cmd.Flags().String("goal", "", "Scenario goal text (required)")
	cmd.Flags().String("category", "", "Scenario category")
	cmd.Flags().String("difficulty", "", "Difficulty level")
	cmd.MarkFlagRequired("role")
	cmd.MarkFlagRequired("goal")

	return cmd
}

func newEvalScenariosRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove [scenario-id]",
		Short: "Remove an eval scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			id := args[0]
			_, err = d.ExecContext(context.Background(),
				`DELETE FROM eval_scenarios WHERE id = ?`, id)
			if err != nil {
				return fmt.Errorf("deleting scenario: %w", err)
			}
			cmd.Printf("Removed scenario %s\n", id)
			return nil
		},
	}
	return cmd
}

func newEvalScenariosSeedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "Seed default eval scenarios",
		Long:  `Create a minimal holdout set of default scenarios covering common patterns per role.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			if err := eval.SeedDefaultScenarios(context.Background(), d); err != nil {
				return fmt.Errorf("seeding scenarios: %w", err)
			}
			cmd.Println("Default scenarios seeded successfully.")
			return nil
		},
	}
}

func newEvalScenariosCurateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "curate",
		Short: "Curate scenarios from failed tasks",
		Long:  `Extract failed tasks from the orchestrator database and create eval scenarios from them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			ctx := context.Background()
			scenarios, err := eval.CurateFromDB(ctx, d)
			if err != nil {
				return fmt.Errorf("curating scenarios: %w", err)
			}

			if len(scenarios) == 0 {
				cmd.Println("No failed tasks found to curate.")
				return nil
			}

			// Insert curated scenarios into the eval_scenarios table
			inserted := 0
			for _, s := range scenarios {
				dbScenario := db.EvalScenario{
					ID:              s.ID,
					Role:            s.Role,
					Category:        sql.NullString{String: s.Category, Valid: s.Category != ""},
					Goal:            s.Goal,
					ExpectedOutcome: sql.NullString{String: s.ExpectedOutcome, Valid: s.ExpectedOutcome != ""},
					Difficulty:      sql.NullString{String: s.Difficulty, Valid: s.Difficulty != ""},
				}
				if err := d.InsertEvalScenario(ctx, dbScenario); err != nil {
					cmd.PrintErrf("  Skipping duplicate or error for %s: %v\n", s.ID, err)
					continue
				}
				inserted++
			}

			cmd.Printf("Curated %d scenario(s) from %d failed task(s).\n", inserted, len(scenarios))
			return nil
		},
	}
}

func newEvalReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Show results for an eval run",
		Long: `Display detailed results for a specific eval run.

Examples:
  orchestra eval report --run-id run-v1-scenario1-123456`,
		RunE: runEvalReport,
	}

	cmd.Flags().String("run-id", "", "Eval run ID to report on (required)")
	cmd.MarkFlagRequired("run-id")

	return cmd
}

func runEvalReport(cmd *cobra.Command, args []string) error {
	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	ctx := context.Background()
	runID, _ := cmd.Flags().GetString("run-id")

	results, err := d.ListEvalResultsForRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("listing results: %w", err)
	}

	if len(results) == 0 {
		cmd.Printf("No results found for run %s\n", runID)
		return nil
	}

	cmd.Printf("Results for run %s:\n\n", runID)
	cmd.Printf("%-30s  %8s  %8s  %s\n", "METRIC", "SCORE", "WEIGHT", "DETAILS")
	totalScore := 0.0
	totalWeight := 0.0
	for _, r := range results {
		details := ""
		if r.Details.Valid {
			details = r.Details.String
		}
		cmd.Printf("%-30s  %8.2f  %8.1f  %s\n", r.Metric, r.Score, r.Weight, details)
		totalScore += r.Score * r.Weight
		totalWeight += r.Weight
	}
	if totalWeight > 0 {
		cmd.Printf("\nWeighted average: %.2f\n", totalScore/totalWeight)
	}

	return nil
}

// dbScenarioToEval converts a db.EvalScenario to an eval.Scenario domain model.
func dbScenarioToEval(s db.EvalScenario) eval.Scenario {
	category := ""
	if s.Category.Valid {
		category = s.Category.String
	}
	expectedOutcome := ""
	if s.ExpectedOutcome.Valid {
		expectedOutcome = s.ExpectedOutcome.String
	}
	difficulty := ""
	if s.Difficulty.Valid {
		difficulty = s.Difficulty.String
	}
	// Extract task ID for curated scenarios (format: "curated-<taskID>")
	taskID := ""
	if strings.HasPrefix(s.ID, "curated-") {
		taskID = strings.TrimPrefix(s.ID, "curated-")
	}
	return eval.Scenario{
		ID:              s.ID,
		Role:            s.Role,
		Category:        category,
		Goal:            s.Goal,
		ExpectedOutcome: expectedOutcome,
		Difficulty:      difficulty,
		TaskID:          taskID,
		CreatedAt:       s.CreatedAt,
	}
}
