package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// gitRunner executes git commands. Override in tests to avoid real repos.
var gitRunner func(args ...string) (string, error) = defaultGitCmd

// semverRe matches a valid semver version with optional leading 'v'.
var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// goreleaserConfig is the path to the GoReleaser config file, checked in gate 5.
const goreleaserConfig = ".goreleaser.yaml"

// NewReleaseCmd creates the release command for gated version tagging.
func NewReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release [version]",
		Short: "Run release gate checks and tag a new version",
		Long: `Validates the codebase is release-ready, then creates and pushes a git tag.

Gate checks:
  1. No uncommitted changes
  2. On dev or main branch
  3. All tests pass (go test ./...)
  4. Tag doesn't already exist
  5. GoReleaser config valid (if goreleaser installed)

The tag push triggers the GitHub Action (.github/workflows/release.yml)
which runs GoReleaser to produce Homebrew formula, Docker image, and binaries.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			skipTests, _ := cmd.Flags().GetBool("skip-tests")

			// Determine version
			ver := ""
			if len(args) > 0 {
				ver = args[0]
			}
			if ver == "" {
				return fmt.Errorf("version required (e.g., orchestra release v1.0.0)")
			}
			if !strings.HasPrefix(ver, "v") {
				ver = "v" + ver
			}
			if !semverRe.MatchString(ver) {
				return fmt.Errorf("invalid semver version %q (expected format: vMAJOR.MINOR.PATCH)", ver)
			}

			cmd.Printf("Release gate checks for %s\n", ver)
			cmd.Println("=" + strings.Repeat("=", len(ver)+27))

			// Gate 1: Clean working tree
			cmd.Print("  [1/5] Clean working tree... ")
			statusOut, err := gitRunner("status", "--porcelain")
			if err != nil {
				cmd.Println("FAIL")
				return fmt.Errorf("git status: %w", err)
			}
			if strings.TrimSpace(statusOut) != "" {
				cmd.Println("FAIL")
				return fmt.Errorf("uncommitted changes — commit or stash before releasing")
			}
			cmd.Println("PASS")

			// Gate 2: On release-eligible branch
			cmd.Print("  [2/5] Release branch... ")
			branch, err := gitRunner("branch", "--show-current")
			if err != nil {
				cmd.Println("FAIL")
				return fmt.Errorf("git branch: %w", err)
			}
			branch = strings.TrimSpace(branch)
			if branch != "dev" && branch != "main" && branch != "master" {
				cmd.Println("FAIL")
				return fmt.Errorf("must be on dev or main (currently on %s)", branch)
			}
			cmd.Printf("PASS (%s)\n", branch)

			// Gate 3: Tests pass
			if !skipTests {
				cmd.Print("  [3/5] Test suite... ")
				testCmd := exec.Command("go", "test", "./...", "-count=1", "-short", "-timeout=300s")
				testOut, err := testCmd.CombinedOutput()
				if err != nil {
					cmd.Println("FAIL")
					// Show last 10 lines of test output
					lines := strings.Split(string(testOut), "\n")
					start := len(lines) - 10
					if start < 0 {
						start = 0
					}
					for _, l := range lines[start:] {
						cmd.Printf("    %s\n", l)
					}
					return fmt.Errorf("tests failed — fix before releasing")
				}
				cmd.Println("PASS")
			} else {
				cmd.Println("  [3/5] Test suite... SKIPPED (--skip-tests)")
			}

			// Gate 4: Tag doesn't already exist
			cmd.Print("  [4/5] Tag available... ")
			if _, err := gitRunner("rev-parse", ver); err == nil {
				cmd.Println("FAIL")
				return fmt.Errorf("tag %s already exists", ver)
			}
			cmd.Println("PASS")

			// Gate 5: GoReleaser config valid (skip if not installed)
			cmd.Print("  [5/5] GoReleaser config... ")
			if _, err := exec.LookPath("goreleaser"); err != nil {
				cmd.Println("SKIP (goreleaser not installed)")
			} else {
				grCmd := exec.Command("goreleaser", "check")
				if grOut, err := grCmd.CombinedOutput(); err != nil {
					cmd.Println("FAIL")
					cmd.Printf("    %s\n", strings.TrimSpace(string(grOut)))
					return fmt.Errorf("goreleaser check failed — fix .goreleaser.yaml before releasing")
				}
				cmd.Println("PASS")
			}

			cmd.Println()
			cmd.Printf("All gates passed for %s\n", ver)

			if dryRun {
				cmd.Println("Dry run — skipping tag creation")
				return nil
			}

			// Create and push tag
			cmd.Printf("Creating tag %s...\n", ver)
			if _, err := gitRunner("tag", "-a", ver, "-m", fmt.Sprintf("Release %s", ver)); err != nil {
				return fmt.Errorf("creating tag: %w", err)
			}

			cmd.Printf("Pushing tag %s to origin...\n", ver)
			if _, err := gitRunner("push", "origin", ver); err != nil {
				return fmt.Errorf("pushing tag: %w", err)
			}

			cmd.Println()
			cmd.Printf("Released %s\n", ver)
			cmd.Println("GitHub Action will build and publish artifacts.")
			cmd.Printf("Monitor: https://github.com/MochaCosine1206/orchestra/actions\n")

			// Update LatestVersion in version.go
			cmd.Printf("\nNote: update internal/version/version.go LatestVersion to %q for staleness checks.\n", ver)

			return nil
		},
	}

	cmd.Flags().Bool("dry-run", false, "Run gate checks without creating tag")
	cmd.Flags().Bool("skip-tests", false, "Skip test suite (not recommended)")

	return cmd
}

func defaultGitCmd(args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir, _ = os.Getwd()
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
