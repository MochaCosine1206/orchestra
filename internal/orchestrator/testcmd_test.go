package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTestCommand_NPM(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0644)

	cmd := DetectTestCommand(dir)
	expected := "npm install && npm run build && npm test"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_Go(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "go build ./... && go test ./..."
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_Cargo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "cargo build && cargo test"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_Python_PyprojectToml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"test\"\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "pip install -e . && pytest"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_Python_SetupPy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "setup.py"), []byte("from setuptools import setup\nsetup()\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "pip install -e . && pytest"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_Makefile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo build\n\ntest:\n\techo test\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "make test"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestDetectTestCommand_MakefileNoTestTarget(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo build\n\nclean:\n\techo clean\n"), 0644)

	cmd := DetectTestCommand(dir)
	if cmd != "" {
		t.Errorf("expected empty string for Makefile without test target, got %q", cmd)
	}
}

func TestDetectTestCommand_Unknown(t *testing.T) {
	dir := t.TempDir()
	// Empty directory — no recognizable project files

	cmd := DetectTestCommand(dir)
	if cmd != "" {
		t.Errorf("expected empty string for unknown project, got %q", cmd)
	}
}

func TestDetectTestCommand_Priority(t *testing.T) {
	// When both package.json and go.mod exist, package.json wins (checked first)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0644)

	cmd := DetectTestCommand(dir)
	expected := "npm install && npm run build && npm test"
	if cmd != expected {
		t.Errorf("expected npm to win priority, got %q", cmd)
	}
}
