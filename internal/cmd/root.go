package cmd

import (
	"os"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// NewRootCmd creates the root orchestra command with all subcommands registered.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestra",
		Short: "Claude Orchestra — multi-agent code orchestration",
		Long: `Claude Orchestra coordinates parallel Claude Code agents to transform
ideas into deployed outcomes through autonomous, self-correcting loops.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			version.CheckStaleness(cmd.ErrOrStderr())
			// Auto-register project if we're inside one
			if globalFlag, _ := cmd.Flags().GetBool("global"); !globalFlag {
				config.AutoRegister()
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Terminal → launch TUI dashboard; pipe/redirect → show help.
			if term.IsTerminal(int(os.Stdout.Fd())) {
				return runDashboard(cmd, args)
			}
			return cmd.Help()
		},
	}

	// Persistent flags available to all subcommands.
	cmd.PersistentFlags().String("db", ".orchestra/orchestrator.db", "Path to SQLite database")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")
	cmd.PersistentFlags().Bool("global", false, "Operate in global mode (all projects)")
	cmd.PersistentFlags().String("project", "", "Target a specific registered project by name")

	// Register all subcommands.
	cmd.AddCommand(
		NewDashboardCmd(),
		NewGoCmd(),
		NewAutoCmd(),
		NewStatusCmd(),
		NewMergeCmd(),
		NewDecomposeCmd(),
		NewAuditCmd(),
		NewAbTestCmd(),
		NewTokensCmd(),
		NewInitCmd(),
		NewVersionCmd(),
		NewSpawnCmd(),
		NewMonitorCmd(),
		NewRecoverCmd(),
		NewResetCmd(),
		NewReconcileCmd(),
		NewConductorRunCmd(),
		NewDockerCmd(),
		NewExecCmd(),
		NewGenerateSpecCmd(),
		NewNewCmd(),
		NewCacheCmd(),
		NewProjectsCmd(),
		NewDaemonCmd(),
		NewQueueCmd(),
		NewEvalCmd(),
		NewHealCmd(),
		NewReleaseCmd(),
		NewDoctorCmd(),
	)

	return cmd
}
