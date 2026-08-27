package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// validSpecJSON returns a well-formed OrchestraSpec as JSON for mock runner.
func validNewSpecJSON() string {
	spec := orchestrator.OrchestraSpec{
		Version: "1",
		Metadata: orchestrator.SpecMetadata{
			Title:       "Todo API",
			Description: "REST API for todo items",
			TechStack:   map[string]string{"language": "go"},
		},
		Phases: []orchestrator.Phase{
			{
				ID:          "foundation",
				Name:        "Foundation",
				Description: "Project setup",
				DependsOn:   []string{},
				Gate: orchestrator.PhaseGate{
					TestCmd:    "go build ./...",
					Acceptance: []string{"Project compiles"},
				},
				Tasks: []orchestrator.SpecTask{
					{
						Title:              "Setup",
						Role:               "implementer",
						Files:              []string{"main.go"},
						Description:        "Init project",
						AcceptanceCriteria: []string{"main.go exists"},
						DependsOn:          []string{},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(spec)
	return string(b)
}

func TestProjectNameFromIdea(t *testing.T) {
	tests := []struct {
		idea string
		want string
	}{
		{"REST API for todo items in Go", "rest-api-todo-items"},
		{"billing microservice", "billing-microservice"},
		{"a simple chat app with websockets", "simple-chat-app-websockets"},
		{"Build the ULTIMATE task manager!!!", "build-ultimate-task-manager"},
		{"", "new-project"},
		{"the a an", "new-project"},
		{"go", "go"},
	}
	for _, tt := range tests {
		t.Run(tt.idea, func(t *testing.T) {
			got := projectNameFromIdea(tt.idea)
			if got != tt.want {
				t.Errorf("projectNameFromIdea(%q) = %q, want %q", tt.idea, got, tt.want)
			}
		})
	}
}

func TestInitNewProject_CreatesDirectoryAndSpec(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "my-project")

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "my-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Simple REST API for todo items",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
		// Skip git operations in unit tests
		GitInitFn:   nil,
		GitCommitFn: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	// Project directory created
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("project directory not created: %v", err)
	}

	// Orchestra dirs created
	orchestraDir := filepath.Join(projectRoot, ".orchestra")
	if _, err := os.Stat(orchestraDir); err != nil {
		t.Fatalf(".orchestra dir not created: %v", err)
	}

	// Database file exists
	dbPath := filepath.Join(projectRoot, ".orchestra", "orchestrator.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file not created: %v", err)
	}

	// Spec file written
	specPath := filepath.Join(projectRoot, "spec.yaml")
	if _, err := os.Stat(specPath); err != nil {
		t.Fatalf("spec.yaml not created: %v", err)
	}

	// Spec result populated
	if result.SpecResult == nil {
		t.Fatal("expected non-nil spec result")
	}
	if result.SpecResult.Spec.Metadata.Title != "Todo API" {
		t.Errorf("spec title = %q, want %q", result.SpecResult.Spec.Metadata.Title, "Todo API")
	}

	// Spec round-trips through ParseSpec
	parsed, err := orchestrator.ParseSpec(specPath)
	if err != nil {
		t.Fatalf("spec round-trip parse failed: %v", err)
	}
	if len(parsed.Phases) != 1 {
		t.Errorf("parsed phases = %d, want 1", len(parsed.Phases))
	}
}

func TestInitNewProject_RunnerFailure(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "fail-project")

	mock := &orchestrator.MockRunner{
		Outputs: []string{""},
		Errors:  []error{fmt.Errorf("LLM is down")},
	}

	_, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "fail-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Build something",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
	})
	if err == nil {
		t.Fatal("expected error when runner fails")
	}

	// Project directory should still exist (partial setup)
	if _, err := os.Stat(projectRoot); err != nil {
		t.Errorf("project directory should exist even after failure: %v", err)
	}
}

func TestInitNewProject_DirectoryAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "existing-project")

	// Pre-create directory
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "existing-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Build something",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
	})
	if err != nil {
		t.Fatalf("should succeed even if directory exists: %v", err)
	}
	defer result.DB.Close()

	// Spec should still be generated
	specPath := filepath.Join(projectRoot, "spec.yaml")
	if _, err := os.Stat(specPath); err != nil {
		t.Fatalf("spec.yaml not created: %v", err)
	}
}

func TestInitNewProject_GitInitCalled(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "git-project")

	gitInitCalled := false
	gitCommitCalled := false

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "git-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Build something",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
		GitInitFn: func(dir string) error {
			gitInitCalled = true
			if dir != projectRoot {
				t.Errorf("git init dir = %q, want %q", dir, projectRoot)
			}
			return nil
		},
		GitCommitFn: func(dir, msg string) error {
			gitCommitCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	if !gitInitCalled {
		t.Error("expected git init to be called")
	}
	if !gitCommitCalled {
		t.Error("expected git commit to be called")
	}
}

func TestInitNewProject_SpecIncludesTechStack(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "tech-project")

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "tech-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Build an API",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
			TechStack:  map[string]string{"language": "go", "framework": "gin"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	// Verify the prompt included tech stack
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(mock.Calls))
	}
	prompt := mock.Calls[0].Prompt
	if !strings.Contains(prompt, "TECH STACK") {
		t.Error("prompt should contain TECH STACK section")
	}
}

func TestRunNew_UsesGlobalConfigParentDir(t *testing.T) {
	// Set up global config with a default project dir
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ORCHESTRA_CONFIG", cfgPath)

	wantParent := t.TempDir()
	if err := config.Save(&config.GlobalConfig{DefaultProjectDir: wantParent}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	// Use initNewProject with the resolved parent dir (simulates runNew logic)
	projectRoot := filepath.Join(wantParent, "test-project")
	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "test-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Test project",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	// Verify project landed in the config's default dir
	if filepath.Dir(result.ProjectRoot) != wantParent {
		t.Errorf("project parent = %q, want %q", filepath.Dir(result.ProjectRoot), wantParent)
	}
}

func TestCopyIncludePaths_Directory(t *testing.T) {
	srcDir := t.TempDir()
	// Create a source directory with files
	researchDir := filepath.Join(srcDir, "research")
	if err := os.MkdirAll(researchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(researchDir, "notes.md"), []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(researchDir, "data.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyIncludePaths(projectRoot, []string{researchDir}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify directory was copied preserving basename
	copiedNotes := filepath.Join(projectRoot, "research", "notes.md")
	if _, err := os.Stat(copiedNotes); err != nil {
		t.Errorf("expected %s to exist: %v", copiedNotes, err)
	}
	copiedData := filepath.Join(projectRoot, "research", "data.txt")
	if _, err := os.Stat(copiedData); err != nil {
		t.Errorf("expected %s to exist: %v", copiedData, err)
	}
}

func TestCopyIncludePaths_File(t *testing.T) {
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "requirements.md")
	if err := os.WriteFile(srcFile, []byte("# Requirements"), 0o644); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyIncludePaths(projectRoot, []string{srcFile}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	copiedFile := filepath.Join(projectRoot, "requirements.md")
	data, err := os.ReadFile(copiedFile)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if string(data) != "# Requirements" {
		t.Errorf("file content = %q, want %q", string(data), "# Requirements")
	}
}

func TestCopyIncludePaths_Multiple(t *testing.T) {
	srcDir := t.TempDir()

	// Create a directory
	researchDir := filepath.Join(srcDir, "research")
	if err := os.MkdirAll(researchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(researchDir, "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file
	specFile := filepath.Join(srcDir, "spec.md")
	if err := os.WriteFile(specFile, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyIncludePaths(projectRoot, []string{researchDir, specFile}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both should exist
	if _, err := os.Stat(filepath.Join(projectRoot, "research", "notes.md")); err != nil {
		t.Error("research/notes.md not copied")
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "spec.md")); err != nil {
		t.Error("spec.md not copied")
	}
}

func TestCopyIncludePaths_NonExistent(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	err := copyIncludePaths(projectRoot, []string{"/nonexistent/path"})
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
	if !strings.Contains(err.Error(), "include path") {
		t.Errorf("error should mention include path, got: %v", err)
	}
}

func TestInitNewProject_WithInclude(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "include-project")

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	var copiedRoot string
	var copiedPaths []string

	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot:  projectRoot,
		ProjectName:  "include-project",
		Runner:       mock,
		IncludePaths: []string{"/fake/research", "/fake/notes.md"},
		CopyFn: func(root string, paths []string) error {
			copiedRoot = root
			copiedPaths = paths
			return nil
		},
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Test with includes",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	if copiedRoot != projectRoot {
		t.Errorf("CopyFn root = %q, want %q", copiedRoot, projectRoot)
	}
	if len(copiedPaths) != 2 {
		t.Fatalf("CopyFn paths len = %d, want 2", len(copiedPaths))
	}
	if copiedPaths[0] != "/fake/research" || copiedPaths[1] != "/fake/notes.md" {
		t.Errorf("CopyFn paths = %v, want [/fake/research /fake/notes.md]", copiedPaths)
	}
}

func TestRunNew_FlagOverridesGlobalConfig(t *testing.T) {
	// Set up global config pointing to one directory
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ORCHESTRA_CONFIG", cfgPath)

	configDir := filepath.Join(t.TempDir(), "config-dir")
	if err := config.Save(&config.GlobalConfig{DefaultProjectDir: configDir}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Simulate --parent-dir flag explicitly set (overrides config)
	flagDir := t.TempDir()

	mock := &orchestrator.MockRunner{
		Outputs: []string{validNewSpecJSON()},
	}

	projectRoot := filepath.Join(flagDir, "override-project")
	result, err := initNewProject(context.Background(), newProjectOpts{
		ProjectRoot: projectRoot,
		ProjectName: "override-project",
		Runner:      mock,
		SpecOpts: orchestrator.GenerateSpecOpts{
			Idea:       "Override test",
			OutputPath: filepath.Join(projectRoot, "spec.yaml"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.DB.Close()

	// Verify project landed in the flag dir, not the config dir
	if filepath.Dir(result.ProjectRoot) != flagDir {
		t.Errorf("project parent = %q, want %q (flag should override config)", filepath.Dir(result.ProjectRoot), flagDir)
	}
}
