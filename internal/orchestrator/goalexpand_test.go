package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandFileReferences_SingleFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello World"), 0644)

	result := expandFileReferences("implement changes in @README.md please", dir)
	if !strings.Contains(result, "<contents of README.md>") {
		t.Errorf("expected expanded contents tag, got: %s", result)
	}
	if !strings.Contains(result, "# Hello World") {
		t.Errorf("expected file contents, got: %s", result)
	}
	if !strings.Contains(result, "</contents of README.md>") {
		t.Errorf("expected closing tag, got: %s", result)
	}
}

func TestExpandFileReferences_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("file a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("file b"), 0644)

	result := expandFileReferences("see @a.md and @b.md", dir)
	if !strings.Contains(result, "file a") {
		t.Errorf("expected file a contents, got: %s", result)
	}
	if !strings.Contains(result, "file b") {
		t.Errorf("expected file b contents, got: %s", result)
	}
}

func TestExpandFileReferences_FileNotFound(t *testing.T) {
	dir := t.TempDir()

	result := expandFileReferences("see @nonexistent.md", dir)
	if !strings.Contains(result, "[WARNING: @nonexistent.md not found]") {
		t.Errorf("expected warning, got: %s", result)
	}
}

func TestExpandFileReferences_NoReferences(t *testing.T) {
	dir := t.TempDir()

	input := "just a plain goal with no file refs"
	result := expandFileReferences(input, dir)
	if result != input {
		t.Errorf("expected unchanged text, got: %s", result)
	}
}

func TestExpandFileReferences_EmailIgnored(t *testing.T) {
	dir := t.TempDir()

	result := expandFileReferences("email user@example.com about this", dir)
	if strings.Contains(result, "[WARNING") {
		t.Errorf("should not attempt to expand email address, got: %s", result)
	}
	if !strings.Contains(result, "user@example.com") {
		t.Errorf("email should be preserved, got: %s", result)
	}
}

func TestExpandFileReferences_Truncation(t *testing.T) {
	dir := t.TempDir()
	// Create a file larger than maxFileChars (10,000)
	big := strings.Repeat("x", 15000)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0644)

	result := expandFileReferences("see @big.txt", dir)
	if !strings.Contains(result, "[TRUNCATED]") {
		t.Errorf("expected TRUNCATED marker, got length: %d", len(result))
	}
	// Content should be capped at maxFileChars
	contentsStart := strings.Index(result, "<contents of big.txt>\n")
	contentsEnd := strings.Index(result, "\n[TRUNCATED]")
	if contentsStart >= 0 && contentsEnd >= 0 {
		inner := result[contentsStart+len("<contents of big.txt>\n") : contentsEnd]
		if len(inner) != maxFileChars {
			t.Errorf("expected %d chars, got %d", maxFileChars, len(inner))
		}
	}
}

func TestExpandFileReferencesUnlimited_NoTruncation(t *testing.T) {
	dir := t.TempDir()
	// Create a 50KB file — larger than both default limits
	big := strings.Repeat("x", 50_000)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0644)

	result := expandFileReferencesUnlimited("see @big.txt", dir)
	if strings.Contains(result, "[TRUNCATED]") {
		t.Error("unlimited expansion should not truncate")
	}
	// Full content should be present
	contentsStart := strings.Index(result, "<contents of big.txt>\n")
	contentsEnd := strings.Index(result, "\n</contents of big.txt>")
	if contentsStart >= 0 && contentsEnd >= 0 {
		inner := result[contentsStart+len("<contents of big.txt>\n") : contentsEnd]
		if len(inner) != 50_000 {
			t.Errorf("expected 50000 chars, got %d", len(inner))
		}
	}
}

func TestExpandFileReferences_AbsolutePaths(t *testing.T) {
	// Create a file in one directory, expand from a different repoRoot
	externalDir := t.TempDir()
	os.WriteFile(filepath.Join(externalDir, "plan.md"), []byte("# The Plan"), 0644)

	repoRoot := t.TempDir() // different directory — simulates orchestra new scenario

	absPath := filepath.Join(externalDir, "plan.md")
	result := expandFileReferences("build from @"+absPath, repoRoot)
	if !strings.Contains(result, "# The Plan") {
		t.Errorf("expected absolute path expansion, got: %s", result)
	}
	if strings.Contains(result, "[WARNING") {
		t.Errorf("should not produce warning for valid absolute path, got: %s", result)
	}
}

func TestExpandFileReferences_RelativePaths(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "internal", "db"), 0755)
	os.WriteFile(filepath.Join(dir, "internal", "db", "schema.sql"), []byte("CREATE TABLE t(id INT);"), 0644)

	result := expandFileReferences("follow schema in @internal/db/schema.sql", dir)
	if !strings.Contains(result, "CREATE TABLE t(id INT);") {
		t.Errorf("expected schema contents, got: %s", result)
	}
}
