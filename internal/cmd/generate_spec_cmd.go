package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewGenerateSpecCmd creates the generate-spec subcommand.
func NewGenerateSpecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-spec",
		Short: "Generate a phased YAML spec from a project idea",
		Long: `Generate a multi-phase orchestration spec from a high-level project idea.
The spec is produced by an LLM (architect agent) and written as YAML.

Review the generated spec, then execute with: orchestra exec --spec <path>`,
		RunE: runGenerateSpec,
	}

	cmd.Flags().String("idea", "", "High-level project idea (required)")
	cmd.Flags().StringP("output", "o", "spec.yaml", "Output path for the generated spec")
	cmd.Flags().String("tech-stack", "", `Tech stack as key=value pairs (e.g. "language=go,framework=gin,database=sqlite")`)
	cmd.Flags().String("constraints", "", "Comma-separated constraints")
	cmd.Flags().Bool("repo-context", false, "Include codebase context in the prompt")
	cmd.Flags().Bool("fail-closed", false, "Return error if spec fails validation with critical gaps (B-289)")
	_ = cmd.MarkFlagRequired("idea")

	return cmd
}

// runGenerateSpec is the RunE handler for the generate-spec command.
func runGenerateSpec(cmd *cobra.Command, args []string) error {
	idea, _ := cmd.Flags().GetString("idea")
	output, _ := cmd.Flags().GetString("output")
	techStackStr, _ := cmd.Flags().GetString("tech-stack")
	constraintsStr, _ := cmd.Flags().GetString("constraints")
	repoContext, _ := cmd.Flags().GetBool("repo-context")
	failClosed, _ := cmd.Flags().GetBool("fail-closed")

	opts := orchestrator.GenerateSpecOpts{
		Idea:        idea,
		OutputPath:  output,
		RepoContext: repoContext,
		FailClosed:  failClosed,
	}

	if techStackStr != "" {
		opts.TechStack = parseTechStack(techStackStr)
	}
	if constraintsStr != "" {
		for _, c := range strings.Split(constraintsStr, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				opts.Constraints = append(opts.Constraints, c)
			}
		}
	}

	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	repoRoot, _ := os.Getwd()
	runner := &orchestrator.ExecRunner{}
	c, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: repoRoot,
		Runner:   runner,
	})
	if err != nil {
		return fmt.Errorf("creating conductor: %w", err)
	}

	cmd.Printf("Generating spec from idea: %s\n", idea)

	result, valResult, err := c.GenerateSpecWithValidation(cmd.Context(), opts, 3)
	if err != nil {
		return fmt.Errorf("generating spec: %w", err)
	}

	// Print validation warnings/errors
	for _, w := range result.Validation.Warnings {
		cmd.PrintErrf("Warning: [%s] %s\n", w.Rule, w.Message)
	}
	if !result.Validation.IsValid() {
		for _, e := range result.Validation.Errors {
			cmd.PrintErrf("Error: [%s] %s\n", e.Rule, e.Message)
		}
		cmd.PrintErrf("\nSpec has validation errors — review and fix before executing.\n")
	}

	// Print plan validation results (if validation ran)
	if valResult != nil {
		cmd.Printf("\nPlan validation: %.0f%% coverage (%d/%d items)\n",
			valResult.CoverageScore*100, valResult.SpecItemsFound, valResult.PlanItemsFound)
		if len(valResult.MissingItems) > 0 {
			cmd.Printf("  Remaining gaps:\n")
			for _, gap := range valResult.MissingItems {
				cmd.Printf("    [%s] %s\n", gap.Severity, gap.Description)
			}
		}
		if !valResult.Pass {
			cmd.Printf("\n  WARNING: Spec may not fully cover the planning document.\n")
		}
	}

	// Print summary
	totalTasks := 0
	for _, p := range result.Spec.Phases {
		totalTasks += len(p.Tasks)
	}

	cmd.Printf("\nSpec generated: %s\n", result.Spec.Metadata.Title)
	cmd.Printf("  Phases: %d\n", len(result.Spec.Phases))
	cmd.Printf("  Tasks:  %d\n", totalTasks)
	cmd.Printf("  Output: %s\n", result.OutputPath)
	cmd.Printf("\nReview the spec, then execute with: orchestra exec --spec %s\n", result.OutputPath)

	return nil
}

// parseTechStack splits "k=v,k=v" format into a map.
func parseTechStack(s string) map[string]string {
	result := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}
