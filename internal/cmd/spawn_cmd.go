package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// NewSpawnCmd creates the spawn subcommand tree.
func NewSpawnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "Manage agent spawning",
		Long:  "Spawn, kill, respawn, and batch-launch Claude agents for tasks.",
	}

	cmd.AddCommand(
		newSpawnRunCmd(),
		newSpawnKillCmd(),
		newSpawnRespawnCmd(),
		newSpawnBatchCmd(),
	)

	return cmd
}

// parseClarifyMode converts a string flag value to a ClarifyMode enum.
func parseClarifyMode(s string) orchestrator.ClarifyMode {
	switch s {
	case "cli":
		return orchestrator.ClarifyCLI
	case "tui":
		return orchestrator.ClarifyTUI
	default:
		return orchestrator.ClarifyAuto
	}
}

func openDB(cmd *cobra.Command) (*db.DB, error) {
	dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
	// --project flag overrides: resolve project name to its DB path
	if projectName, _ := cmd.Root().PersistentFlags().GetString("project"); projectName != "" {
		if _, resolved := config.ResolveProjectByName(projectName); resolved != "" {
			dbPath = resolved
		}
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	// Ensure schema exists (idempotent CREATE IF NOT EXISTS).
	if err := d.InitSchema(cmd.Context()); err != nil {
		d.Close()
		return nil, fmt.Errorf("ensuring schema: %w", err)
	}
	return d, nil
}

func newSpawner(cmd *cobra.Command, d *db.DB) *agent.Spawner {
	repoRoot, _ := os.Getwd()
	return &agent.Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  filepath.Join(repoRoot, ".orchestra", "logs"),
		PidsDir:  filepath.Join(repoRoot, ".orchestra", "pids"),
	}
}

func newSpawnRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Spawn an agent for a pending task",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("task")
			role, _ := cmd.Flags().GetString("role")
			model, _ := cmd.Flags().GetString("model")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			s := newSpawner(cmd, d)
			result, err := s.Run(context.Background(), agent.SpawnOpts{
				TaskID: taskID,
				Role:   agent.Role(role),
				Model:  model,
			})
			if err != nil {
				return fmt.Errorf("spawn run: %w", err)
			}

			cmd.Printf("Spawned agent %s (PID=%d, model=%s, worktree=%s)\n",
				result.AgentID, result.PID, result.Model, result.Worktree)
			return nil
		},
	}

	cmd.Flags().StringP("task", "t", "", "Task ID to spawn (required)")
	cmd.Flags().StringP("role", "r", "", "Agent role (default: from task)")
	cmd.Flags().StringP("model", "m", "", "Model override")
	_ = cmd.MarkFlagRequired("task")

	return cmd
}

func newSpawnKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kill",
		Short: "Kill a running agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("task")
			force, _ := cmd.Flags().GetBool("force")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			repoRoot, _ := os.Getwd()
			pidsDir := filepath.Join(repoRoot, ".orchestra", "pids")

			err = agent.KillTaskProcess(context.Background(), d, taskID, pidsDir, force)
			if err != nil {
				return fmt.Errorf("kill: %w", err)
			}

			cmd.Printf("Killed agent for task %s\n", taskID)
			return nil
		},
	}

	cmd.Flags().StringP("task", "t", "", "Task ID to kill (required)")
	cmd.Flags().Bool("force", false, "Force kill (SIGKILL immediately)")
	_ = cmd.MarkFlagRequired("task")

	return cmd
}

func newSpawnRespawnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "respawn",
		Short: "Respawn a failed task with circuit breaker",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("task")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			s := newSpawner(cmd, d)
			result, err := s.Respawn(context.Background(), taskID)
			if err != nil {
				return fmt.Errorf("respawn: %w", err)
			}

			cmd.Printf("Respawned agent %s (PID=%d, model=%s)\n",
				result.AgentID, result.PID, result.Model)
			return nil
		},
	}

	cmd.Flags().StringP("task", "t", "", "Task ID to respawn (required)")
	_ = cmd.MarkFlagRequired("task")

	return cmd
}

func newSpawnBatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Batch-spawn unblocked pending tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent")
			staggerSec, _ := cmd.Flags().GetInt("stagger")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			s := newSpawner(cmd, d)
			results, err := s.Batch(context.Background(), maxConcurrent, time.Duration(staggerSec)*time.Second, nil)
			if err != nil {
				return fmt.Errorf("batch: %w", err)
			}

			cmd.Printf("Batch spawned %d agents\n", len(results))
			for _, r := range results {
				cmd.Printf("  %s: PID=%d model=%s\n", r.AgentID, r.PID, r.Model)
			}
			return nil
		},
	}

	cmd.Flags().Int("max-concurrent", 5, "Maximum concurrent agents")
	cmd.Flags().Int("stagger", 0, "Stagger delay between spawns (seconds)")

	return cmd
}
