package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/onboard"
	"github.com/MochaCosine1206/orchestra/internal/scaffold"
)

// NewInitCmd creates the init subcommand.
func NewInitCmd() *cobra.Command {
	var (
		clean          bool
		claudeCode     bool
		mcp            bool
		loops          bool
		all            bool
		force          bool
		nonInteractive bool
		projName       string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize orchestra in a project",
		Long: `Set up the orchestra directory structure, database, and scaffolding.

Default (no flags): creates runtime dirs + database (backward compatible).
With --all: full scaffolding including Claude Code, MCP servers, and loop dirs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, initFlags{
				clean:          clean,
				claudeCode:     claudeCode,
				mcp:            mcp,
				loops:          loops,
				all:            all,
				force:          force,
				nonInteractive: nonInteractive,
				projName:       projName,
			})
		},
	}

	cmd.Flags().BoolVar(&clean, "clean", false, "Drop and recreate database")
	cmd.Flags().BoolVar(&claudeCode, "claude-code", false, "Scaffold .claude/ files for Claude Code integration")
	cmd.Flags().BoolVar(&mcp, "mcp", false, "Install MCP servers via claude mcp add")
	cmd.Flags().BoolVar(&loops, "loops", false, "Create .orchestra/ loop scaffolding")
	cmd.Flags().BoolVar(&all, "all", false, "Enable all scaffolding (--claude-code --mcp --loops)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing scaffolding files")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Skip feature picker; enable all features with satisfied deps")
	cmd.Flags().StringVar(&projName, "project-name", "", "Project name (default: directory name)")

	return cmd
}

type initFlags struct {
	clean          bool
	claudeCode     bool
	mcp            bool
	loops          bool
	all            bool
	force          bool
	nonInteractive bool
	projName       string
}

func runInit(cmd *cobra.Command, flags initFlags) error {
	ctx := context.Background()
	dbPath, _ := cmd.Root().PersistentFlags().GetString("db")

	// Resolve project root and database path:
	// - If --db is absolute, derive project root from its parent directory
	// - If --db is relative, use cwd as project root and resolve db against it
	var repoRoot string
	if filepath.IsAbs(dbPath) {
		repoRoot = filepath.Dir(dbPath)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			repoRoot = "."
		} else {
			repoRoot = cwd
		}
		dbPath = filepath.Join(repoRoot, dbPath)
	}

	// Determine project name
	projectName := flags.projName
	if projectName == "" {
		projectName = filepath.Base(repoRoot)
	}

	// Detect project type
	projectType := scaffold.DetectProjectType(repoRoot)

	// Check if any scaffolding flags were set
	anyFlagSet := flags.claudeCode || flags.mcp || flags.loops || flags.all

	var opts scaffold.Options

	if anyFlagSet {
		// Legacy flag path — backward compatible
		writeClaude := flags.claudeCode || flags.all
		writeMCP := flags.mcp || flags.all
		writeLoops := flags.loops || flags.all

		opts = scaffold.Options{
			ProjectRoot: repoRoot,
			ProjectName: projectName,
			ProjectType: projectType,
			WriteClaude: writeClaude,
			WriteMCP:    writeMCP,
			WriteLoops:  writeLoops,
			Force:       flags.force,
		}
	} else if flags.nonInteractive {
		// Non-interactive: scan environment, enable all features with satisfied deps
		scan := onboard.ScanEnvironment(ctx, onboard.ExecRunner{})
		features := onboard.DefaultFeatures()
		selected := onboard.SelectNonInteractive(features, scan)

		if err := onboard.RunFeatureSetup(ctx, repoRoot, selected); err != nil {
			return fmt.Errorf("feature setup: %w", err)
		}

		wc, wm, wl := onboard.FeaturesToScaffoldFlags(selected)
		opts = scaffold.Options{
			ProjectRoot: repoRoot,
			ProjectName: projectName,
			ProjectType: projectType,
			WriteClaude: wc,
			WriteMCP:    wm,
			WriteLoops:  wl,
			Force:       flags.force,
		}
	} else if isatty.IsTerminal(os.Stdin.Fd()) {
		// Interactive TTY: run onboarding questionnaire, then feature picker
		existingDir := ""
		if gcfg, err := config.Load(); err == nil {
			existingDir = gcfg.DefaultProjectDir
		}

		result, err := onboard.Run(repoRoot, projectName, projectType, existingDir)
		if err != nil {
			return fmt.Errorf("onboarding: %w", err)
		}

		// Persist default project dir to global config if set
		if result.DefaultProjectDir != "" {
			if gcfg, err := config.Load(); err == nil {
				gcfg.DefaultProjectDir = result.DefaultProjectDir
				if saveErr := config.Save(gcfg); saveErr != nil {
					cmd.PrintErrf("Warning: saving global config: %v\n", saveErr)
				}
			}
		}

		// Scan environment and present feature picker
		scan := onboard.ScanEnvironment(ctx, onboard.ExecRunner{})
		features := onboard.DefaultFeatures()
		selected, err := onboard.FeaturePicker(features, scan)
		if err != nil {
			return fmt.Errorf("feature picker: %w", err)
		}

		if err := onboard.RunFeatureSetup(ctx, repoRoot, selected); err != nil {
			return fmt.Errorf("feature setup: %w", err)
		}

		wc, wm, wl := onboard.FeaturesToScaffoldFlags(selected)
		opts = result.ToScaffoldOptions(repoRoot)
		opts.WriteClaude = opts.WriteClaude || wc
		opts.WriteMCP = opts.WriteMCP || wm
		opts.WriteLoops = opts.WriteLoops || wl
		opts.Force = flags.force
	} else {
		// Non-TTY, no flags: minimal defaults
		opts = scaffold.Options{
			ProjectRoot: repoRoot,
			ProjectName: projectName,
			ProjectType: projectType,
			Force:       flags.force,
		}
	}

	result, err := scaffold.Scaffold(ctx, opts)
	if err != nil {
		return fmt.Errorf("scaffolding: %w", err)
	}

	// Open and initialize database
	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	if err := d.InitSchema(ctx); err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}

	if flags.clean {
		if err := d.TruncateAll(ctx); err != nil {
			return fmt.Errorf("truncating tables: %w", err)
		}
	}

	// Update .gitignore if any scaffolding was done
	if opts.WriteClaude || opts.WriteLoops {
		scaffold.AppendGitignore(repoRoot, false)
	}

	// Warn if not a git repository
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); os.IsNotExist(err) {
		cmd.PrintErrln()
		cmd.PrintErrln("⚠  No git repository detected.")
		cmd.PrintErrln("   Orchestra requires a git repo for agent worktrees.")
		cmd.PrintErrln("   Run: git init && git add -A && git commit -m \"Initial commit\"")
		cmd.PrintErrln()
	}

	// Print summary
	printInitSummary(cmd, repoRoot, dbPath, result, opts)

	return nil
}

func printInitSummary(cmd *cobra.Command, repoRoot, dbPath string, result *scaffold.Result, opts scaffold.Options) {
	cmd.Printf("Orchestra initialized in %s\n", repoRoot)
	cmd.Printf("  Database:     %s\n", filepath.Base(dbPath))

	if opts.WriteClaude {
		agentCount := 0
		profileCount := 0
		for _, f := range result.Files {
			if strings.Contains(f.Path, "/agents/") && f.Action != "skipped" {
				agentCount++
			}
			if strings.Contains(f.Path, "/profiles/") && f.Action != "skipped" {
				profileCount++
			}
		}
		cmd.Printf("  Agent defs:   .orchestra/agents/ (%d files)\n", agentCount)
		cmd.Printf("  Profiles:     .orchestra/profiles/ (%d files)\n", profileCount)
	}

	if opts.WriteMCP {
		if len(result.MCPErrors) == 0 {
			cmd.Println("  MCP servers:  6 installed (sqlite, memory, git-worktree, pm, filesystem, playwright)")
		} else {
			cmd.Printf("  MCP servers:  %d warnings:\n", len(result.MCPErrors))
			for _, e := range result.MCPErrors {
				cmd.Printf("    - %s\n", e)
			}
			if !result.ClaudeCheck.Installed {
				cmd.Println("    Install Claude Code: https://claude.com/download")
				cmd.Println("    Then run: claude login && orchestra init --mcp")
			} else if !result.ClaudeCheck.Authenticated {
				cmd.Println("    Run: claude login")
				cmd.Println("    Then: orchestra init --mcp")
			}
		}
	}

	if opts.WriteLoops {
		cmd.Println("  Loop dirs:    .orchestra/ (4 directories)")
	}

	cmd.Println()
	cmd.Println("Run 'orchestra dashboard' to get started.")
}
