package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/scaffold"
)

// NewNewCmd creates the new subcommand (greenfield project creation).
func NewNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new project from an idea",
		Long: `Create a new project directory, initialize git and orchestra, generate a
phased spec from your idea via an architect agent, and optionally execute it.

Steps:
  1. Create project directory (from --name or derived from idea)
  2. git init + initial commit
  3. orchestra init (full scaffolding)
  4. Generate phased YAML spec from idea
  5. Print spec summary for review
  6. Execute spec (if --execute is set)

Example:
  orchestra new --idea "REST API for todo items in Go with SQLite"
  orchestra new --idea "billing microservice" --name billing-api --execute`,
		RunE: runNew,
	}

	cmd.Flags().String("idea", "", "High-level project idea (required)")
	cmd.Flags().String("name", "", "Project directory name (default: derived from idea)")
	cmd.Flags().String("tech-stack", "", `Tech stack as key=value pairs (e.g. "language=go,framework=gin")`)
	cmd.Flags().String("constraints", "", "Comma-separated constraints")
	cmd.Flags().Bool("execute", false, "Execute the generated spec after creation")
	cmd.Flags().String("parent-dir", ".", "Parent directory for the new project")
	cmd.Flags().StringSlice("include", nil, "Directories/files to copy into project before spec generation (repeatable)")
	cmd.Flags().Bool("require-version", false, "Abort if Claude CLI version is below minimum")
	_ = cmd.MarkFlagRequired("idea")

	return cmd
}

// runNew is the RunE handler for the new command.
func runNew(cmd *cobra.Command, args []string) error {
	requireVersion, _ := cmd.Flags().GetBool("require-version")
	if err := checkCLIVersion(cmd, requireVersion); err != nil {
		return err
	}

	ctx := cmd.Context()
	idea, _ := cmd.Flags().GetString("idea")
	name, _ := cmd.Flags().GetString("name")
	techStackStr, _ := cmd.Flags().GetString("tech-stack")
	constraintsStr, _ := cmd.Flags().GetString("constraints")
	execute, _ := cmd.Flags().GetBool("execute")
	parentDir, _ := cmd.Flags().GetString("parent-dir")

	// If --parent-dir not explicitly set, check global config
	if !cmd.Flags().Changed("parent-dir") {
		if cfg, err := config.Load(); err == nil && cfg.DefaultProjectDir != "" {
			parentDir = cfg.DefaultProjectDir
		}
	}

	// Resolve project name
	if name == "" {
		name = projectNameFromIdea(idea)
	}

	// Resolve absolute parent dir
	absParent, err := filepath.Abs(parentDir)
	if err != nil {
		return fmt.Errorf("resolving parent directory: %w", err)
	}

	projectRoot := filepath.Join(absParent, name)

	// Step 1: Create project directory
	cmd.Printf("Creating project: %s\n", projectRoot)
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}

	// Step 2: git init + initial commit
	cmd.Printf("Initializing git repository...\n")
	if err := gitInit(projectRoot); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Step 2.5: Copy included files/directories
	includePaths, _ := cmd.Flags().GetStringSlice("include")
	if len(includePaths) > 0 {
		cmd.Printf("Copying included files...\n")
		if err := copyIncludePaths(projectRoot, includePaths); err != nil {
			return fmt.Errorf("copying included files: %w", err)
		}
	}

	// Step 3: orchestra init (non-interactive, full scaffolding)
	cmd.Printf("Scaffolding orchestra...\n")
	scaffoldResult, err := scaffold.Scaffold(ctx, scaffold.Options{
		ProjectRoot: projectRoot,
		ProjectName: name,
		ProjectType: scaffold.ProjectOther, // will be detected after spec gen creates files
		WriteClaude: true,
		WriteMCP:    true,
		WriteLoops:  true,
		Force:       false,
	})
	if err != nil {
		return fmt.Errorf("scaffolding: %w", err)
	}

	// Init database
	dbPath := filepath.Join(projectRoot, ".orchestra", "orchestrator.db")
	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	if err := d.InitSchema(ctx); err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}

	// Update .gitignore
	scaffold.AppendGitignore(projectRoot, false)

	// Commit scaffolding
	if err := gitAddCommit(projectRoot, "chore: orchestra init scaffolding"); err != nil {
		cmd.PrintErrf("Warning: scaffold commit: %v\n", err)
	}

	// Print scaffold summary
	created := 0
	for _, f := range scaffoldResult.Files {
		if f.Action == "created" {
			created++
		}
	}
	cmd.Printf("  Scaffolded %d files\n", created)

	// Step 4: Generate spec
	cmd.Printf("Generating spec from idea...\n")

	// Pre-expand @file references relative to the invoking CWD, not the new
	// project root. When the user runs `orchestra new --idea "@notes/plan.md"`,
	// the @file lives in their current directory, not the freshly-created project.
	invokingDir, _ := os.Getwd()
	expandedIdea := orchestrator.ExpandFileReferencesUnlimited(idea, invokingDir)

	runner := &orchestrator.ExecRunner{}
	c, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: projectRoot,
		Runner:   runner,
	})
	if err != nil {
		return fmt.Errorf("creating conductor: %w", err)
	}

	specOpts := orchestrator.GenerateSpecOpts{
		Idea:       expandedIdea,
		OutputPath: filepath.Join(projectRoot, "spec.yaml"),
	}
	if techStackStr != "" {
		specOpts.TechStack = parseTechStack(techStackStr)
	}
	if constraintsStr != "" {
		for _, con := range strings.Split(constraintsStr, ",") {
			con = strings.TrimSpace(con)
			if con != "" {
				specOpts.Constraints = append(specOpts.Constraints, con)
			}
		}
	}

	specResult, specValResult, err := c.GenerateSpecWithValidation(ctx, specOpts, 3)
	if err != nil {
		return fmt.Errorf("generating spec: %w", err)
	}

	// Print validation warnings/errors
	for _, w := range specResult.Validation.Warnings {
		cmd.PrintErrf("Warning: [%s] %s\n", w.Rule, w.Message)
	}
	if !specResult.Validation.IsValid() {
		for _, e := range specResult.Validation.Errors {
			cmd.PrintErrf("Error: [%s] %s\n", e.Rule, e.Message)
		}
		cmd.PrintErrf("\nSpec has validation errors — review and fix before executing.\n")
	}

	// Print plan validation results (if validation ran)
	if specValResult != nil {
		cmd.Printf("\nPlan validation: %.0f%% coverage (%d/%d items)\n",
			specValResult.CoverageScore*100, specValResult.SpecItemsFound, specValResult.PlanItemsFound)
		if len(specValResult.MissingItems) > 0 {
			cmd.Printf("  Remaining gaps:\n")
			for _, gap := range specValResult.MissingItems {
				cmd.Printf("    [%s] %s\n", gap.Severity, gap.Description)
			}
		}
		if !specValResult.Pass {
			cmd.Printf("\n  WARNING: Spec may not fully cover the planning document.\n")
		}
	}

	// Ralph Wiggum refinement: if coverage < 100%, agent reads spec + requirements from disk and edits to fix gaps
	if specValResult != nil && specValResult.CoverageScore < 1.0 {
		reqPath := filepath.Join(projectRoot, "REQUIREMENTS.md")
		if _, err := os.Stat(reqPath); err == nil {
			cmd.Printf("\nRalph Wiggum refining spec to 100%% coverage...\n")
			ralphResult, ralphErr := orchestrator.RalphRefineSpec(ctx, orchestrator.RalphRefineOpts{
				SpecPath:         specResult.OutputPath,
				RequirementsPath: reqPath,
				ProjectRoot:      projectRoot,
				MaxIterations:    4,
			})
			if ralphErr != nil {
				cmd.PrintErrf("Warning: Ralph refinement failed: %v\n", ralphErr)
			} else {
				cmd.Printf("Ralph complete: %d → %d tasks (%d iterations, converged=%v)\n",
					ralphResult.InitialTasks, ralphResult.FinalTasks,
					ralphResult.Iterations, ralphResult.Converged)
			}
		}
	}

	// Print spec summary
	totalTasks := 0
	for _, p := range specResult.Spec.Phases {
		totalTasks += len(p.Tasks)
	}
	cmd.Printf("\nSpec: %s\n", specResult.Spec.Metadata.Title)
	cmd.Printf("  Phases: %d\n", len(specResult.Spec.Phases))
	cmd.Printf("  Tasks:  %d\n", totalTasks)
	cmd.Printf("  Output: %s\n", specResult.OutputPath)

	// Step 5: Execute or print next steps
	if execute && specResult.Validation.IsValid() {
		// Reload local runner config — it may have been injected after
		// scaffolding but before execution (e.g., by an --include or background script).
		c.ReloadLocalRunner()
		cmd.Printf("\nExecuting spec...\n")

		if err := c.SetupLogFile(); err != nil {
			cmd.PrintErrf("Warning: conductor log: %v\n", err)
		}
		defer c.CloseLogFile()

		execOpts := orchestrator.GoSpecOpts{
			MaxParallel: 8,
			Interval:    10,
			Reconcile:   true,
			RepoMap:     true,
		}

		execResult, err := c.GoSpec(ctx, specResult.Spec, execOpts)
		if err != nil {
			if execResult != nil {
				printExecSummary(cmd, execResult)
			}
			return fmt.Errorf("spec execution failed: %w", err)
		}
		printExecSummary(cmd, execResult)
	} else {
		cmd.Printf("\nProject created at: %s\n", projectRoot)
		cmd.Printf("Next steps:\n")
		cmd.Printf("  cd %s\n", projectRoot)
		cmd.Printf("  cat spec.yaml                    # review the generated spec\n")
		cmd.Printf("  orchestra exec --spec spec.yaml  # execute when ready\n")
	}

	return nil
}

// projectNameFromIdea derives a kebab-case directory name from an idea string.
func projectNameFromIdea(idea string) string {
	// Take first ~4 meaningful words, lowercase, kebab-case
	words := strings.Fields(strings.ToLower(idea))

	// Filter out common filler words
	skip := map[string]bool{
		"a": true, "an": true, "the": true, "for": true, "with": true,
		"in": true, "and": true, "or": true, "to": true, "of": true,
		"that": true, "is": true, "it": true, "on": true, "my": true,
	}

	var meaningful []string
	for _, w := range words {
		// Strip non-alphanumeric
		cleaned := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, w)
		if cleaned == "" || skip[cleaned] {
			continue
		}
		meaningful = append(meaningful, cleaned)
		if len(meaningful) >= 4 {
			break
		}
	}

	if len(meaningful) == 0 {
		return "new-project"
	}
	return strings.Join(meaningful, "-")
}

// gitInit runs git init and creates an initial empty commit.
func gitInit(dir string) error {
	cmds := []struct {
		args []string
	}{
		{[]string{"git", "init"}},
		{[]string{"git", "add", "-A"}},
		{[]string{"git", "commit", "--allow-empty", "-m", "Initial commit"}},
	}

	for _, c := range cmds {
		cmd := exec.Command(c.args[0], c.args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(c.args, " "), err)
		}
	}
	return nil
}

// gitAddCommit stages all changes and commits with the given message.
func gitAddCommit(dir, message string) error {
	for _, args := range [][]string{
		{"git", "add", "-A"},
		{"git", "commit", "-m", message},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// copyIncludePaths copies files and directories into the project root.
// Directories are copied preserving their basename (e.g. research/ stays research/).
// Files are copied directly into the project root.
func copyIncludePaths(projectRoot string, paths []string) error {
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("resolving %q: %w", p, err)
		}

		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("include path %q: %w", p, err)
		}

		if info.IsDir() {
			dest := filepath.Join(projectRoot, filepath.Base(abs))
			cmd := exec.Command("cp", "-r", abs, dest)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("copying directory %q: %s: %w", p, string(out), err)
			}
		} else {
			dest := filepath.Join(projectRoot, filepath.Base(abs))
			cmd := exec.Command("cp", abs, dest)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("copying file %q: %s: %w", p, string(out), err)
			}
		}
	}
	return nil
}

// initNewProject is a testable wrapper that runs the core new-project flow.
// It takes dependencies explicitly (for testing with mocks).
func initNewProject(ctx context.Context, opts newProjectOpts) (*newProjectResult, error) {
	// Create directory
	if err := os.MkdirAll(opts.ProjectRoot, 0o755); err != nil {
		return nil, fmt.Errorf("creating project directory: %w", err)
	}

	// Git init
	if opts.GitInitFn != nil {
		if err := opts.GitInitFn(opts.ProjectRoot); err != nil {
			return nil, fmt.Errorf("git init: %w", err)
		}
	}

	// Copy included files/directories
	if opts.CopyFn != nil && len(opts.IncludePaths) > 0 {
		if err := opts.CopyFn(opts.ProjectRoot, opts.IncludePaths); err != nil {
			return nil, fmt.Errorf("copying included files: %w", err)
		}
	}

	// Scaffold
	scaffoldResult, err := scaffold.Scaffold(ctx, scaffold.Options{
		ProjectRoot: opts.ProjectRoot,
		ProjectName: opts.ProjectName,
		ProjectType: scaffold.ProjectOther,
		WriteClaude: true,
		WriteMCP:    true,
		WriteLoops:  true,
		Force:       false,
	})
	if err != nil {
		return nil, fmt.Errorf("scaffolding: %w", err)
	}

	// Init database
	dbPath := filepath.Join(opts.ProjectRoot, ".orchestra", "orchestrator.db")
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := d.InitSchema(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	// Gitignore
	scaffold.AppendGitignore(opts.ProjectRoot, false)

	// Commit scaffolding
	if opts.GitCommitFn != nil {
		opts.GitCommitFn(opts.ProjectRoot, "chore: orchestra init scaffolding")
	}

	// Generate spec
	c, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: opts.ProjectRoot,
		Runner:   opts.Runner,
	})
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("creating conductor: %w", err)
	}

	specResult, specValResult, err := c.GenerateSpecWithValidation(ctx, opts.SpecOpts, 3)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("generating spec: %w", err)
	}

	return &newProjectResult{
		ProjectRoot:    opts.ProjectRoot,
		ScaffoldResult: scaffoldResult,
		SpecResult:     specResult,
		SpecValResult:  specValResult,
		DB:             d,
		Conductor:      c,
	}, nil
}

// newProjectOpts is the dependency-injected configuration for initNewProject.
type newProjectOpts struct {
	ProjectRoot  string
	ProjectName  string
	Runner       orchestrator.ClaudeRunner
	SpecOpts     orchestrator.GenerateSpecOpts
	GitInitFn    func(dir string) error                         // nil to skip
	GitCommitFn  func(dir, msg string) error                    // nil to skip
	IncludePaths []string                                       // files/dirs to copy before scaffold
	CopyFn       func(projectRoot string, paths []string) error // nil to skip
}

// newProjectResult holds the outcome of initNewProject.
type newProjectResult struct {
	ProjectRoot    string
	ScaffoldResult *scaffold.Result
	SpecResult     *orchestrator.GenerateSpecResult
	SpecValResult  *orchestrator.PlanValidationResult
	DB             *db.DB
	Conductor      *orchestrator.Conductor
}
