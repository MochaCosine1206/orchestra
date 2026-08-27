package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// NewResetCmd creates the reset subcommand for full cleanup.
func NewResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Remove all worktrees and reset the database",
		Long: `Full cleanup: removes all orchestra git worktrees, prunes stale worktree
references, deletes feature branches created by orchestra, and truncates
all database tables.

This is the "start fresh" command — use it after a failed run or when you
want to clean up completely.

Examples:
  orchestra reset              # Clean up everything
  orchestra reset --keep-db    # Remove worktrees but keep task history
  orchestra reset --dry-run    # Show what would be removed`,
		RunE: func(cmd *cobra.Command, args []string) error {
			keepDB, _ := cmd.Flags().GetBool("keep-db")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			repoRoot, _ := os.Getwd()

			// Step 0: Kill conductor and all agent processes
			pidsDir := filepath.Join(repoRoot, ".orchestra", "pids")
			killCount := killAllAgentPIDs(cmd, pidsDir, dryRun)

			// Step 1: Remove git worktrees
			worktreeDir := filepath.Join(repoRoot, ".worktree")
			removed, err := removeWorktrees(cmd, repoRoot, worktreeDir, dryRun)
			if err != nil {
				cmd.PrintErrln(fmt.Sprintf("Warning: worktree cleanup had errors: %v", err))
			}
			if removed > 0 {
				cmd.Printf("Removed %d worktree(s)\n", removed)
			} else {
				cmd.Println("No worktrees to remove")
			}

			// Step 2: Prune stale worktree references
			if !dryRun {
				if out, err := exec.Command("git", "-C", repoRoot, "worktree", "prune").CombinedOutput(); err != nil {
					cmd.PrintErrln(fmt.Sprintf("Warning: git worktree prune: %s", strings.TrimSpace(string(out))))
				}
			}

			// Step 3: Clean up orphan feature branches
			branchesRemoved := cleanupFeatureBranches(cmd, repoRoot, dryRun)
			if branchesRemoved > 0 {
				cmd.Printf("Removed %d feature branch(es)\n", branchesRemoved)
			}

			// Step 4: Reset database
			if !keepDB {
				if dryRun {
					cmd.Println("Would truncate all database tables")
				} else {
					d, err := openDB(cmd)
					if err != nil {
						return fmt.Errorf("opening database: %w", err)
					}
					defer d.Close()

					if err := d.TruncateAll(cmd.Context()); err != nil {
						return fmt.Errorf("truncating database: %w", err)
					}
					cmd.Println("Database reset")
				}
			} else {
				cmd.Println("Database kept (--keep-db)")
			}

			// Step 5: Clean up runtime dirs (logs, pids)
			cleanupRuntimeDirs(cmd, repoRoot, dryRun)

			// Step 6: Restore git state if behind upstream
			// A/B tests use resetToCommit() which can leave HEAD at old base commits.
			// If the session is interrupted, git stays at that old state.
			restoreGitState(cmd, repoRoot, dryRun)

			if dryRun {
				cmd.Println("\nDry run — no changes made")
			} else {
				if killCount > 0 {
					cmd.Printf("Killed %d process(es)\n", killCount)
				}
				cmd.Println("\nReset complete. Run 'orchestra go' to start fresh.")
			}
			return nil
		},
	}

	cmd.Flags().Bool("keep-db", false, "Keep database (only remove worktrees and branches)")
	cmd.Flags().Bool("dry-run", false, "Show what would be removed without doing it")

	return cmd
}

// removeWorktrees removes all worktrees under the .worktree directory.
func removeWorktrees(cmd *cobra.Command, repoRoot, worktreeDir string, dryRun bool) (int, error) {
	// First check git worktree list for orchestra-managed worktrees
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("listing worktrees: %w", err)
	}

	removed := 0
	var lastErr error

	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimPrefix(line, "worktree ")

		// Skip the main worktree
		if path == repoRoot {
			continue
		}

		// Only remove worktrees under .worktree/
		if !strings.Contains(path, ".worktree") {
			continue
		}

		if dryRun {
			cmd.Printf("Would remove worktree: %s\n", path)
			removed++
			continue
		}

		if o, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
			cmd.PrintErrln(fmt.Sprintf("Warning: removing %s: %s", path, strings.TrimSpace(string(o))))
			lastErr = err
		} else {
			removed++
		}
	}

	// Also remove the .worktree directory itself if it exists and is empty
	if !dryRun {
		if entries, err := os.ReadDir(worktreeDir); err == nil && len(entries) == 0 {
			os.Remove(worktreeDir)
		}
	}

	return removed, lastErr
}

// cleanupFeatureBranches removes local feature/ branches created by orchestra.
func cleanupFeatureBranches(cmd *cobra.Command, repoRoot string, dryRun bool) int {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", "feature/*").CombinedOutput()
	if err != nil {
		return 0
	}

	removed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		branch := strings.TrimSpace(line)
		// Skip current branch (marked with *)
		branch = strings.TrimPrefix(branch, "* ")
		if branch == "" {
			continue
		}

		// Only remove feature/t-* branches (orchestra-created)
		if !strings.HasPrefix(branch, "feature/t-") && !strings.HasPrefix(branch, "feature/T-") {
			continue
		}

		if dryRun {
			cmd.Printf("Would delete branch: %s\n", branch)
			removed++
			continue
		}

		if _, err := exec.Command("git", "-C", repoRoot, "branch", "-D", branch).CombinedOutput(); err == nil {
			removed++
		}
	}
	return removed
}

// killAllAgentPIDs reads every PID file in the pids directory and kills the process.
// This handles both the conductor and all spawned agents (claude -p processes).
func killAllAgentPIDs(cmd *cobra.Command, pidsDir string, dryRun bool) int {
	entries, err := os.ReadDir(pidsDir)
	if err != nil {
		return 0
	}

	killed := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		taskID := strings.TrimSuffix(e.Name(), ".pid")
		pid, err := agent.ReadPID(pidsDir, taskID)
		if err != nil || pid <= 0 {
			continue
		}
		if !agent.PidAlive(pid) {
			if !dryRun {
				agent.RemovePID(pidsDir, taskID)
			}
			continue
		}

		if dryRun {
			cmd.Printf("Would kill %s (PID %d)\n", taskID, pid)
			killed++
			continue
		}

		if err := agent.KillProcess(pid, false, 10*time.Second); err != nil {
			cmd.PrintErrln(fmt.Sprintf("Warning: killing %s PID %d: %v", taskID, pid, err))
		} else {
			killed++
		}
		agent.RemovePID(pidsDir, taskID)
	}
	return killed
}

// restoreGitState fast-forwards to the upstream tip if HEAD is behind.
// This recovers from A/B test interruptions where resetToCommit left HEAD
// at an old base commit.
func restoreGitState(cmd *cobra.Command, repoRoot string, dryRun bool) {
	// Check if current branch has an upstream
	upstream, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "@{u}").CombinedOutput()
	if err != nil {
		return // no upstream, nothing to restore
	}
	upstreamRef := strings.TrimSpace(string(upstream))
	if upstreamRef == "" {
		return
	}

	// Fetch latest from remote
	exec.Command("git", "-C", repoRoot, "fetch").CombinedOutput()

	// Check if we're behind
	behindOut, err := exec.Command("git", "-C", repoRoot, "rev-list", "--count", "HEAD..@{u}").CombinedOutput()
	if err != nil {
		return
	}
	behind := strings.TrimSpace(string(behindOut))
	if behind == "0" {
		return
	}

	if dryRun {
		cmd.Printf("Would fast-forward %s commits to %s\n", behind, upstreamRef)
		return
	}

	// Fast-forward to upstream (safe — only moves forward, never discards local commits)
	if out, err := exec.Command("git", "-C", repoRoot, "merge", "--ff-only", "@{u}").CombinedOutput(); err != nil {
		// ff-only failed — upstream diverged, likely from AB test contamination
		cmd.PrintErrln(fmt.Sprintf("Warning: fast-forward failed, resetting to %s: %s", upstreamRef, strings.TrimSpace(string(out))))
		exec.Command("git", "-C", repoRoot, "reset", "--hard", "@{u}").CombinedOutput()
	}
	cmd.Printf("Restored git state (%s commits from %s)\n", behind, upstreamRef)
}

// cleanupRuntimeDirs removes logs and pids directories.
func cleanupRuntimeDirs(cmd *cobra.Command, repoRoot string, dryRun bool) {
	dirs := []string{
		filepath.Join(repoRoot, ".orchestra", "logs"),
		filepath.Join(repoRoot, ".orchestra", "pids"),
		filepath.Join(repoRoot, ".orchestra", "goals"),
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			continue
		}

		if dryRun {
			cmd.Printf("Would clean %d file(s) from %s\n", len(entries), dir)
			continue
		}

		for _, e := range entries {
			os.Remove(filepath.Join(dir, e.Name()))
		}
		cmd.Printf("Cleaned %s (%d files)\n", filepath.Base(dir), len(entries))
	}
}
