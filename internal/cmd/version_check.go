package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/version"
)

const minClaudeVersion = "2.1.83"

// checkCLIVersion checks whether the installed Claude CLI meets the minimum
// version requirement. When require is false (default), a below-minimum or
// undetectable version prints a warning to stderr but does not block execution.
// When require is true, a below-minimum version returns an error.
func checkCLIVersion(cmd *cobra.Command, require bool) error {
	result := version.CheckClaudeVersion(minClaudeVersion)

	if result.Err != nil {
		cmd.PrintErrf("Warning: could not check Claude CLI version: %v\n", result.Err)
		return nil
	}

	if !result.OK {
		msg := fmt.Sprintf("Claude CLI version %s is below minimum %s", result.Detected, minClaudeVersion)
		if require {
			return fmt.Errorf("%s (use without --require-version to continue anyway)", msg)
		}
		cmd.PrintErrf("Warning: %s\n", msg)
	}

	return nil
}
