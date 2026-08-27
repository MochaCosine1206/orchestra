package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRepoMapGoFiles(t *testing.T) {
	// Use the Orchestra codebase itself
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Navigate up to repo root from internal/agent
	repoRoot = filepath.Dir(filepath.Dir(repoRoot))

	// Check if we're in the right place
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); os.IsNotExist(err) {
		t.Skip("not in orchestra repo, skipping")
	}

	repoMap, err := GenerateRepoMap(repoRoot, 5000)
	if err != nil {
		t.Fatalf("GenerateRepoMap: %v", err)
	}

	if repoMap == "" {
		t.Fatal("expected non-empty repo map")
	}

	// Should contain known functions from the codebase
	if !strings.Contains(repoMap, "func") {
		t.Error("repo map should contain function signatures")
	}
	if !strings.Contains(repoMap, "pkg") {
		t.Error("repo map should contain package names")
	}
}

func TestGenerateRepoMapWithTempDir(t *testing.T) {
	root := t.TempDir()

	// Create some Go files
	os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "pkg", "main.go"), []byte(`package pkg

func Hello(name string) string {
	return "hello " + name
}

type Config struct {
	Name string
	Port int
}

type Service interface {
	Start()
	Stop()
}
`), 0o644)

	// Create a Python file
	os.WriteFile(filepath.Join(root, "script.py"), []byte(`
def hello(name):
    return f"hello {name}"

class MyClass:
    pass

async def fetch_data():
    pass
`), 0o644)

	// Create a SQL file
	os.WriteFile(filepath.Join(root, "schema.sql"), []byte(`
CREATE TABLE users (id TEXT PRIMARY KEY);
CREATE VIEW active_users AS SELECT * FROM users;
`), 0o644)

	repoMap, err := GenerateRepoMap(root, 5000)
	if err != nil {
		t.Fatalf("GenerateRepoMap: %v", err)
	}

	// Check Go signatures
	if !strings.Contains(repoMap, "func Hello") {
		t.Error("missing exported Go function")
	}
	if !strings.Contains(repoMap, "type Config struct") {
		t.Error("missing Go struct type")
	}
	if !strings.Contains(repoMap, "type Service interface") {
		t.Error("missing Go interface type")
	}

	// Check Python signatures
	if !strings.Contains(repoMap, "hello") {
		t.Error("missing Python function")
	}
	if !strings.Contains(repoMap, "MyClass") {
		t.Error("missing Python class")
	}

	// Check SQL signatures
	if !strings.Contains(repoMap, "users") {
		t.Error("missing SQL table")
	}
}

func TestRepoMapBudgetEnforcement(t *testing.T) {
	root := t.TempDir()

	// Create many Go files to exceed a small budget
	for i := 0; i < 20; i++ {
		dir := filepath.Join(root, "pkg"+string(rune('a'+i)))
		os.MkdirAll(dir, 0o755)
		content := "package " + string(rune('a'+i)) + "\n\n"
		for j := 0; j < 10; j++ {
			content += "func F" + string(rune('A'+j)) + "() {}\n"
		}
		os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644)
	}

	// Very small budget: 100 tokens = ~400 chars
	repoMap, err := GenerateRepoMap(root, 100)
	if err != nil {
		t.Fatalf("GenerateRepoMap: %v", err)
	}

	if len(repoMap) > 400 {
		t.Errorf("repo map exceeds budget: %d > 400 chars", len(repoMap))
	}
}

func TestRepoMapSkipsVendor(t *testing.T) {
	root := t.TempDir()

	// Create vendor directory with Go files
	os.MkdirAll(filepath.Join(root, "vendor", "lib"), 0o755)
	os.WriteFile(filepath.Join(root, "vendor", "lib", "lib.go"),
		[]byte("package lib\nfunc VendorFunc() {}\n"), 0o644)

	// Create node_modules directory
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"),
		[]byte("export function nodeFunc() {}\n"), 0o644)

	// Create .git directory
	os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"),
		[]byte("[core]\n"), 0o644)

	// Create a real source file
	os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\nfunc Main() {}\n"), 0o644)

	repoMap, err := GenerateRepoMap(root, 5000)
	if err != nil {
		t.Fatalf("GenerateRepoMap: %v", err)
	}

	if strings.Contains(repoMap, "VendorFunc") {
		t.Error("should not include vendor files")
	}
	if strings.Contains(repoMap, "nodeFunc") {
		t.Error("should not include node_modules files")
	}
	if !strings.Contains(repoMap, "func Main") {
		t.Error("should include source files")
	}
}

func TestGenerateRepoMapEmptyDir(t *testing.T) {
	root := t.TempDir()

	repoMap, err := GenerateRepoMap(root, 5000)
	if err != nil {
		t.Fatalf("GenerateRepoMap: %v", err)
	}

	if repoMap != "" {
		t.Error("expected empty repo map for empty directory")
	}
}

func TestGenerateRepoMapEmptyRoot(t *testing.T) {
	_, err := GenerateRepoMap("", 5000)
	if err == nil {
		t.Error("expected error for empty root")
	}
}

func TestExprString(t *testing.T) {
	// Test with nil (should return "?")
	result := exprString(nil)
	if result != "?" {
		t.Errorf("expected '?' for nil, got '%s'", result)
	}
}

func TestExtractRegexSignaturesMarkdown(t *testing.T) {
	root := t.TempDir()
	mdFile := filepath.Join(root, "test.md")
	os.WriteFile(mdFile, []byte("# Title\n## Subtitle\n### Deep\nText\n#### Too deep\n"), 0o644)

	sigs := extractRegexSignatures(mdFile, markdownPatterns)
	if len(sigs) != 3 {
		t.Errorf("expected 3 headings, got %d: %v", len(sigs), sigs)
	}
}
