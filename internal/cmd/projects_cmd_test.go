package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/config"
)

func setupTestRegistry(t *testing.T) (cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmpDir)
	return func() {}
}

func TestProjectsListEmpty(t *testing.T) {
	setupTestRegistry(t)

	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("No projects registered")) {
		t.Errorf("expected empty message, got: %s", buf.String())
	}
}

func TestProjectsAddAndList(t *testing.T) {
	setupTestRegistry(t)

	// Create a temp directory to register as a project.
	projDir := t.TempDir()

	// Add the project.
	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"add", projDir, "--name", "testproj", "--tags", "foo,bar"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("add error: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("Registered project")) {
		t.Errorf("expected registered message, got: %s", buf.String())
	}

	// List and verify.
	cmd2 := NewProjectsCmd()
	buf2 := new(bytes.Buffer)
	cmd2.SetOut(buf2)
	cmd2.SetArgs([]string{"list"})

	if err := cmd2.Execute(); err != nil {
		t.Fatalf("list error: %v", err)
	}

	output := buf2.String()
	if !bytes.Contains([]byte(output), []byte("testproj")) {
		t.Errorf("expected testproj in list, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("foo, bar")) {
		t.Errorf("expected tags in list, got: %s", output)
	}
}

func TestProjectsRemove(t *testing.T) {
	setupTestRegistry(t)

	projDir := t.TempDir()

	// Register directly via config API.
	_, err := config.RegisterProject(projDir, "removeme", false)
	if err != nil {
		t.Fatalf("register error: %v", err)
	}

	// Remove by name.
	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"remove", "removeme"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("remove error: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("Removed project: removeme")) {
		t.Errorf("expected removed message, got: %s", buf.String())
	}

	// Verify it's gone.
	reg, err := config.LoadProjects()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(reg.Projects) != 0 {
		t.Errorf("expected empty registry, got %d projects", len(reg.Projects))
	}
}

func TestProjectsScan(t *testing.T) {
	setupTestRegistry(t)

	// Create a fake project structure.
	root := t.TempDir()
	projDir := filepath.Join(root, "myproject", ".orchestra")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	dbFile := filepath.Join(projDir, "orchestrator.db")
	if err := os.WriteFile(dbFile, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write error: %v", err)
	}

	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"scan", root})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan error: %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("myproject")) {
		t.Errorf("expected myproject in scan output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("registered")) {
		t.Errorf("expected 'registered' in output, got: %s", output)
	}
}

func TestProjectsShow(t *testing.T) {
	setupTestRegistry(t)

	projDir := t.TempDir()

	_, err := config.RegisterProject(projDir, "showme", false)
	if err != nil {
		t.Fatalf("register error: %v", err)
	}

	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"show", "showme"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("show error: %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("Project: showme")) {
		t.Errorf("expected project name, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("No orchestra database")) {
		t.Errorf("expected no-db message, got: %s", output)
	}
}

func TestProjectsRemoveNotFound(t *testing.T) {
	setupTestRegistry(t)

	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"remove", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}

func TestProjectsHelpShowsSubcommands(t *testing.T) {
	cmd := NewProjectsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("help error: %v", err)
	}

	output := buf.String()
	for _, sub := range []string{"list", "add", "remove", "scan", "show"} {
		if !bytes.Contains([]byte(output), []byte(sub)) {
			t.Errorf("expected subcommand %q in help output", sub)
		}
	}
}
