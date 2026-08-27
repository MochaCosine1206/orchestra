package cmd

import (
	"github.com/MochaCosine1206/orchestra/internal/version"
	"github.com/spf13/cobra"
)

// NewVersionCmd creates the version subcommand.
func NewVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println(version.FullVersion())
			if v := version.InstalledVersion(); v != "" {
				cmd.Printf("installed: %s\n", v)
			}
			return nil
		},
	}

	return cmd
}
