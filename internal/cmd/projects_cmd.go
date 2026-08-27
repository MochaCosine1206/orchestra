package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/spf13/cobra"
)

// NewProjectsCmd creates the projects command group with all subcommands.
func NewProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Manage registered Orchestra projects",
		Long:  "List, add, remove, scan, and inspect Orchestra projects in the global registry.",
	}

	cmd.AddCommand(
		newProjectsListCmd(),
		newProjectsAddCmd(),
		newProjectsRemoveCmd(),
		newProjectsScanCmd(),
		newProjectsShowCmd(),
	)

	return cmd
}

func newProjectsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered projects",
		RunE:  runProjectsList,
	}
}

func runProjectsList(cmd *cobra.Command, args []string) error {
	reg, err := config.LoadProjects()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}

	if len(reg.Projects) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No projects registered. Use 'orchestra projects add' or 'orchestra projects scan' to register projects.")
		return nil
	}

	// Sort by name for stable output.
	entries := make([]*config.ProjectEntry, 0, len(reg.Projects))
	for _, e := range reg.Projects {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%-20s %-40s %-20s %s\n", "NAME", "PATH", "LAST ACTIVE", "TAGS")
	for _, e := range entries {
		lastActive := e.LastActive.Format("2006-01-02 15:04")
		tags := strings.Join(e.Tags, ", ")
		fmt.Fprintf(w, "%-20s %-40s %-20s %s\n",
			truncate(e.Name, 20),
			truncate(e.Path, 40),
			lastActive,
			tags,
		)
	}

	return nil
}

func newProjectsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a project by path",
		Args:  cobra.ExactArgs(1),
		RunE:  runProjectsAdd,
	}

	cmd.Flags().String("name", "", "Display name for the project (defaults to directory name)")
	cmd.Flags().StringSlice("tags", nil, "Comma-separated tags for the project")

	return cmd
}

func runProjectsAdd(cmd *cobra.Command, args []string) error {
	projectPath := args[0]
	name, _ := cmd.Flags().GetString("name")
	tags, _ := cmd.Flags().GetStringSlice("tags")

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	// Verify the directory exists.
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}

	isNew, err := config.RegisterProject(absPath, name, false)
	if err != nil {
		return fmt.Errorf("registering project: %w", err)
	}

	// If tags were provided, update the entry.
	if len(tags) > 0 {
		reg, err := config.LoadProjects()
		if err != nil {
			return fmt.Errorf("loading registry for tags: %w", err)
		}
		if entry, ok := reg.Projects[absPath]; ok {
			entry.Tags = tags
			if err := config.SaveProjects(reg); err != nil {
				return fmt.Errorf("saving tags: %w", err)
			}
		}
	}

	w := cmd.OutOrStdout()
	if isNew {
		fmt.Fprintf(w, "Registered project: %s (%s)\n", filepath.Base(absPath), absPath)
	} else {
		fmt.Fprintf(w, "Project already registered (updated): %s (%s)\n", filepath.Base(absPath), absPath)
	}

	return nil
}

func newProjectsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name-or-path>",
		Short: "Remove a project from the registry",
		Args:  cobra.ExactArgs(1),
		RunE:  runProjectsRemove,
	}
}

func runProjectsRemove(cmd *cobra.Command, args []string) error {
	query := args[0]

	reg, err := config.LoadProjects()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}

	// Try matching by absolute path first, then by name.
	var removeKey string
	absQuery, _ := filepath.Abs(query)
	if _, ok := reg.Projects[absQuery]; ok {
		removeKey = absQuery
	} else {
		for key, entry := range reg.Projects {
			if entry.Name == query {
				removeKey = key
				break
			}
		}
	}

	if removeKey == "" {
		return fmt.Errorf("project not found: %s", query)
	}

	removedName := reg.Projects[removeKey].Name
	delete(reg.Projects, removeKey)

	if err := config.SaveProjects(reg); err != nil {
		return fmt.Errorf("saving registry: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Removed project: %s\n", removedName)
	return nil
}

func newProjectsScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan <directory>",
		Short: "Discover and register projects under a directory",
		Args:  cobra.ExactArgs(1),
		RunE:  runProjectsScan,
	}

	cmd.Flags().Int("depth", 4, "Maximum directory depth to scan")

	return cmd
}

func runProjectsScan(cmd *cobra.Command, args []string) error {
	scanDir := args[0]
	maxDepth, _ := cmd.Flags().GetInt("depth")

	absDir, err := filepath.Abs(scanDir)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	discovered, err := config.ScanDirectory(absDir, maxDepth)
	if err != nil {
		return fmt.Errorf("scanning directory: %w", err)
	}

	w := cmd.OutOrStdout()
	if len(discovered) == 0 {
		fmt.Fprintf(w, "No Orchestra projects found under %s (depth %d)\n", absDir, maxDepth)
		return nil
	}

	registered := 0
	for _, path := range discovered {
		isNew, err := config.RegisterProject(path, "", true)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to register %s: %v\n", path, err)
			continue
		}
		status := "already registered"
		if isNew {
			status = "registered"
			registered++
		}
		fmt.Fprintf(w, "  %s — %s\n", path, status)
	}

	fmt.Fprintf(w, "\nDiscovered %d projects, %d newly registered\n", len(discovered), registered)
	return nil
}

func newProjectsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name-or-path>",
		Short: "Show detailed info for a project",
		Args:  cobra.ExactArgs(1),
		RunE:  runProjectsShow,
	}
}

func runProjectsShow(cmd *cobra.Command, args []string) error {
	query := args[0]

	reg, err := config.LoadProjects()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}

	// Find the project by path or name.
	var entry *config.ProjectEntry
	absQuery, _ := filepath.Abs(query)
	if e, ok := reg.Projects[absQuery]; ok {
		entry = e
	} else {
		for _, e := range reg.Projects {
			if e.Name == query {
				entry = e
				break
			}
		}
	}

	if entry == nil {
		return fmt.Errorf("project not found: %s", query)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Project: %s\n", entry.Name)
	fmt.Fprintf(w, "Path:    %s\n", entry.Path)
	fmt.Fprintf(w, "Active:  %s\n", entry.LastActive.Format(time.RFC3339))
	fmt.Fprintf(w, "Auto:    %v\n", entry.AutoRegistered)
	if len(entry.Tags) > 0 {
		fmt.Fprintf(w, "Tags:    %s\n", strings.Join(entry.Tags, ", "))
	}

	// Try to read DB stats if the project has an orchestra database.
	dbPath := filepath.Join(entry.Path, ".orchestra", "orchestrator.db")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		fmt.Fprintln(w, "\n--- Database Stats ---")
		printProjectDBStats(w, dbPath)
	} else {
		fmt.Fprintln(w, "\nNo orchestra database found at this project.")
	}

	return nil
}

func printProjectDBStats(w io.Writer, dbPath string) {
	d, err := db.OpenReadOnly(dbPath)
	if err != nil {
		fmt.Fprintf(w, "  (could not open database: %v)\n", err)
		return
	}
	defer d.Close()

	ctx := context.Background()
	summary, err := d.GetStatusSummary(ctx)
	if err != nil {
		fmt.Fprintf(w, "  (could not read status: %v)\n", err)
		return
	}

	fmt.Fprintf(w, "  Tasks:\n")
	for status, count := range summary.Tasks.ByStatus {
		fmt.Fprintf(w, "    %-12s %d\n", status, count)
	}

	fmt.Fprintf(w, "  Agents:      %d total\n", summary.Agents.Total)
	fmt.Fprintf(w, "  File locks:  %d\n", summary.LockCount)

	// Show last session info from blackboard.
	sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
	if sessionID != "" {
		fmt.Fprintf(w, "  Last session: %s\n", sessionID)
	}
}
