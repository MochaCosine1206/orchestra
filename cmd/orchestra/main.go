// orchestra — CLI entry point verified by scripts/test-go-install.sh
package main

import (
	"os"

	cmd "github.com/MochaCosine1206/orchestra/internal/cmd"
)

func main() {
	root := cmd.NewRootCmd()
	if err := root.Execute(); err != nil {
		verbose, _ := root.PersistentFlags().GetBool("verbose")
		cmd.PrintFormattedError(os.Stderr, err, verbose)
		os.Exit(cmd.ExitCodeFor(err))
	}
}
