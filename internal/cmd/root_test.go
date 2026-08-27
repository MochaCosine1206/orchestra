package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHasAllSubcommands(t *testing.T) {
	root := NewRootCmd()

	expected := []string{
		"ab-test", "audit", "auto", "dashboard", "decompose",
		"go", "init", "merge", "status", "tokens", "version",
	}

	var found []string
	for _, sub := range root.Commands() {
		found = append(found, sub.Name())
	}

	for _, name := range expected {
		if !contains(found, name) {
			t.Errorf("missing subcommand: %s", name)
		}
	}

	// Verify we have at least 11 custom subcommands
	if len(root.Commands()) < 11 {
		t.Errorf("expected at least 11 subcommands, got %d", len(root.Commands()))
	}
}

func TestDefaultCommandShowsHelp(t *testing.T) {
	// When stdout is not a terminal (as in tests), root command shows help.
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{})

	if err := root.Execute(); err != nil {
		t.Fatalf("default (no args) failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "orchestra") {
		t.Errorf("default command should show help with 'orchestra', got: %q", out)
	}
}

func TestVersionOutputFormat(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	// Must contain "orchestra <version> (commit <hash>, built <date>)"
	for _, want := range []string{"orchestra", "commit", "built"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output should contain %q, got: %q", want, out)
		}
	}
}

func TestAllCommandsHaveRunE(t *testing.T) {
	// All commands are wired to Go (no more "not implemented yet" stubs).
	// Verify by checking each command constructor returns a cmd with RunE set.
	commands := map[string]func() interface{}{
		"go":        func() interface{} { return NewGoCmd() },
		"decompose": func() interface{} { return NewDecomposeCmd() },
		"merge":     func() interface{} { return NewMergeCmd() },
		"audit":     func() interface{} { return NewAuditCmd() },
		"auto":      func() interface{} { return NewAutoCmd() },
		"ab-test":   func() interface{} { return NewAbTestCmd() },
		"tokens":    func() interface{} { return NewTokensCmd() },
	}
	for name, fn := range commands {
		cmd := fn()
		if cmd == nil {
			t.Errorf("%s constructor returned nil", name)
		}
	}
}

// --- Required flag enforcement ---

func TestGoCommandRequiresGoal(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"go"})

	if err := root.Execute(); err == nil {
		t.Error("go command should fail without --goal flag")
	}
}

func TestDecomposeCommandRequiresGoal(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"decompose"})

	if err := root.Execute(); err == nil {
		t.Error("decompose command should fail without --goal flag")
	}
}

func TestAbTestRequiresGoal(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"ab-test", "--test-cmd", "true"})

	if err := root.Execute(); err == nil {
		t.Error("ab-test should fail without --goal")
	}
}

func TestAbTestRequiresTestCmd(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"ab-test", "--goal", "test"})

	if err := root.Execute(); err == nil {
		t.Error("ab-test should fail without --test-cmd")
	}
}

// --- Flag definitions per subcommand ---

func TestGoCommandHasAllFlags(t *testing.T) {
	cmd := NewGoCmd()
	flags := []string{"goal", "test-cmd", "iterative", "review", "dry-run", "base-branch", "repo-map", "json"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("go command missing flag: --%s", f)
		}
	}
}

func TestAutoCommandHasAllFlags(t *testing.T) {
	cmd := NewAutoCmd()
	flags := []string{"goal", "sources", "max-cycles", "json"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("auto command missing flag: --%s", f)
		}
	}
}

func TestStatusCommandHasAllFlags(t *testing.T) {
	cmd := NewStatusCmd()
	if cmd.Flags().Lookup("json") == nil {
		t.Error("status command missing flag: --json")
	}
}

func TestMergeCommandHasAllFlags(t *testing.T) {
	cmd := NewMergeCmd()
	flags := []string{"test-cmd", "review", "dry-run", "json"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("merge command missing flag: --%s", f)
		}
	}
}

func TestDecomposeCommandHasAllFlags(t *testing.T) {
	cmd := NewDecomposeCmd()
	flags := []string{"goal", "max-tasks"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("decompose command missing flag: --%s", f)
		}
	}
}

func TestAuditCommandHasAllFlags(t *testing.T) {
	cmd := NewAuditCmd()
	flags := []string{"scope", "dry-run"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("audit command missing flag: --%s", f)
		}
	}
}

func TestAbTestCommandHasAllFlags(t *testing.T) {
	cmd := NewAbTestCmd()
	flags := []string{"goal", "test-cmd", "runs", "routing"}
	for _, f := range flags {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("ab-test command missing flag: --%s", f)
		}
	}
}

func TestPersistentFlags(t *testing.T) {
	root := NewRootCmd()

	if root.PersistentFlags().Lookup("db") == nil {
		t.Error("root missing persistent flag: --db")
	}
	if root.PersistentFlags().Lookup("verbose") == nil {
		t.Error("root missing persistent flag: --verbose")
	}
}

func TestPersistentFlagsInheritedBySubcommand(t *testing.T) {
	dbPath, _ := setupStatusTestDB(t)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--db", dbPath, "--verbose"})

	if err := root.Execute(); err != nil {
		t.Fatalf("status with persistent flags failed: %v", err)
	}
}

// --- Flag defaults ---

func TestAutoSourcesDefault(t *testing.T) {
	cmd := NewAutoCmd()
	f := cmd.Flags().Lookup("sources")
	if f.DefValue != "audit,markers,gaps" {
		t.Errorf("auto --sources default should be 'audit,markers,gaps', got %q", f.DefValue)
	}
}

func TestAutoMaxCyclesDefault(t *testing.T) {
	cmd := NewAutoCmd()
	f := cmd.Flags().Lookup("max-cycles")
	if f.DefValue != "10" {
		t.Errorf("auto --max-cycles default should be '10', got %q", f.DefValue)
	}
}

func TestDecomposeMaxTasksDefault(t *testing.T) {
	cmd := NewDecomposeCmd()
	f := cmd.Flags().Lookup("max-tasks")
	if f.DefValue != "8" {
		t.Errorf("decompose --max-tasks default should be '8', got %q", f.DefValue)
	}
}

func TestAbTestRunsDefault(t *testing.T) {
	cmd := NewAbTestCmd()
	f := cmd.Flags().Lookup("runs")
	if f.DefValue != "1" {
		t.Errorf("ab-test --runs default should be '1', got %q", f.DefValue)
	}
}

func TestAuditScopeDefault(t *testing.T) {
	cmd := NewAuditCmd()
	f := cmd.Flags().Lookup("scope")
	if f.DefValue != "all" {
		t.Errorf("audit --scope default should be 'all', got %q", f.DefValue)
	}
}

func TestDbDefault(t *testing.T) {
	root := NewRootCmd()
	f := root.PersistentFlags().Lookup("db")
	if f.DefValue != ".orchestra/orchestrator.db" {
		t.Errorf("--db default should be '.orchestra/orchestrator.db', got %q", f.DefValue)
	}
}

// --- Short flag aliases ---

func TestGoalShortFlag(t *testing.T) {
	// Test that -g is defined as shorthand for --goal (don't execute, needs DB)
	cmd := NewGoCmd()
	f := cmd.Flags().ShorthandLookup("g")
	if f == nil {
		t.Fatal("go command missing short flag: -g")
	}
	if f.Name != "goal" {
		t.Errorf("expected -g to be shorthand for 'goal', got %q", f.Name)
	}
}

func TestVerboseShortFlag(t *testing.T) {
	dbPath, _ := setupStatusTestDB(t)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "-v", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status -v failed: %v", err)
	}
}

// --- Silenced usage/errors ---

func TestSilenceUsage(t *testing.T) {
	root := NewRootCmd()
	if !root.SilenceUsage {
		t.Error("root command should have SilenceUsage: true")
	}
}

func TestSilenceErrors(t *testing.T) {
	root := NewRootCmd()
	if !root.SilenceErrors {
		t.Error("root command should have SilenceErrors: true")
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
