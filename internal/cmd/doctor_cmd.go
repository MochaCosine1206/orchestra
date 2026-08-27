package cmd

import (
	"context"
	"fmt"

	"github.com/MochaCosine1206/orchestra/internal/onboard"
	"github.com/spf13/cobra"
)

// scanRunner allows tests to inject a mock CommandRunner.
var scanRunner onboard.CommandRunner = onboard.ExecRunner{}

// NewDoctorCmd creates the doctor subcommand.
func NewDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment dependencies for Orchestra",
		Long:  `Scans the local environment for required and optional dependencies, displays a status table, and exits 1 if any required dependency is missing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd)
		},
	}
}

func runDoctor(cmd *cobra.Command) error {
	ctx := context.Background()
	result := onboard.ScanEnvironment(ctx, scanRunner)

	cmd.Println(onboard.FormatTable(result))

	if result.MissingRequired > 0 {
		cmd.Println("Missing required dependencies:")
		for _, d := range result.Dependencies {
			if d.Required && d.Status != onboard.StatusFound {
				cmd.Printf("  %s: %s\n", d.Name, d.InstallHint)
			}
		}
		cmd.Println()
		return fmt.Errorf("%d required dependency(ies) missing", result.MissingRequired)
	}

	cmd.Println("All required dependencies found.")
	return nil
}
