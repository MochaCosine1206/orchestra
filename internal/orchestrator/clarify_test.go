package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClarify_NoAmbiguity(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[]`},
	}
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Fix the typo in README", Mode: ClarifyAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Skipped {
		t.Error("expected Skipped=true for empty questions")
	}
	if result.AugmentedGoal != "Fix the typo in README" {
		t.Errorf("goal should be unchanged, got %q", result.AugmentedGoal)
	}
	if len(result.Questions) != 0 {
		t.Errorf("expected 0 questions, got %d", len(result.Questions))
	}
}

func TestClarify_SingleQuestion(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which shaders?","default":"text shaders only","context":"There are two types"}]`},
	}
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Remove ASCII shaders", Mode: ClarifyAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Skipped {
		t.Error("expected Skipped=false")
	}
	if !result.AutoAnswered {
		t.Error("expected AutoAnswered=true")
	}
	if len(result.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(result.Questions))
	}
	if result.Questions[0].Answer != "text shaders only" {
		t.Errorf("answer = %q, want %q", result.Questions[0].Answer, "text shaders only")
	}
}

func TestClarify_MultipleQuestions(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[
			{"id":1,"question":"Q1?","default":"A1","context":"C1"},
			{"id":2,"question":"Q2?","default":"A2","context":"C2"},
			{"id":3,"question":"Q3?","default":"A3","context":"C3"}
		]`},
	}
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Refactor auth", Mode: ClarifyAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(result.Questions))
	}
	for i, q := range result.Questions {
		expected := fmt.Sprintf("A%d", i+1)
		if q.Answer != expected {
			t.Errorf("question %d: answer=%q, want %q", i, q.Answer, expected)
		}
	}
}

func TestClarify_AugmentedGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which files?","default":"only Go files","context":"ambiguous scope"}]`},
	}
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Add logging", Mode: ClarifyAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.AugmentedGoal, "Add logging") {
		t.Error("augmented goal should contain original goal")
	}
	if !strings.Contains(result.AugmentedGoal, "CLARIFICATIONS:") {
		t.Error("augmented goal should contain CLARIFICATIONS section")
	}
	if !strings.Contains(result.AugmentedGoal, "Q: Which files?") {
		t.Error("augmented goal should contain question")
	}
	if !strings.Contains(result.AugmentedGoal, "A: only Go files") {
		t.Error("augmented goal should contain answer")
	}
}

func TestClarify_EmptyGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Clarify(ctx, ClarifyOpts{Goal: "", Mode: ClarifyAuto})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
	if !strings.Contains(err.Error(), "goal is required") {
		t.Errorf("error = %q, want 'goal is required'", err.Error())
	}
}

func TestClarify_MalformedOutput(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`This is not JSON at all`},
	}
	ctx := context.Background()

	_, err := c.Clarify(ctx, ClarifyOpts{Goal: "Do something", Mode: ClarifyAuto})
	if err == nil {
		t.Fatal("expected error for malformed output")
	}
	if !strings.Contains(err.Error(), "no JSON array found") {
		t.Errorf("error = %q, want 'no JSON array found'", err.Error())
	}
}

func TestClarify_RunnerError(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Errors: []error{fmt.Errorf("runner exploded")},
	}
	ctx := context.Background()

	_, err := c.Clarify(ctx, ClarifyOpts{Goal: "Do something", Mode: ClarifyAuto})
	if err == nil {
		t.Fatal("expected error from runner")
	}
	if !strings.Contains(err.Error(), "clarify call failed") {
		t.Errorf("error = %q, want 'clarify call failed'", err.Error())
	}
}

func TestParseClarifyOutput_Valid(t *testing.T) {
	input := `[{"id":1,"question":"Q?","default":"D","context":"C"}]`
	questions, err := parseClarifyOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if questions[0].Question != "Q?" {
		t.Errorf("question = %q, want %q", questions[0].Question, "Q?")
	}
	if questions[0].Default != "D" {
		t.Errorf("default = %q, want %q", questions[0].Default, "D")
	}
}

func TestParseClarifyOutput_Empty(t *testing.T) {
	input := `[]`
	questions, err := parseClarifyOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 0 {
		t.Errorf("expected 0 questions, got %d", len(questions))
	}
}

func TestParseClarifyOutput_ExtraText(t *testing.T) {
	input := `Here are the questions: [{"id":1,"question":"Q?","default":"D","context":"C"}] end`
	questions, err := parseClarifyOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
}

func TestBuildAugmentedGoal(t *testing.T) {
	questions := []ClarifyQuestion{
		{ID: 1, Question: "Which files?", Default: "all Go", Answer: "all Go"},
		{ID: 2, Question: "Include tests?", Default: "yes", Answer: "yes"},
	}

	result := buildAugmentedGoal("Add logging", questions)

	if !strings.HasPrefix(result, "Add logging") {
		t.Error("should start with original goal")
	}
	if !strings.Contains(result, "CLARIFICATIONS:") {
		t.Error("should contain CLARIFICATIONS header")
	}
	if !strings.Contains(result, "Q: Which files?") {
		t.Error("should contain Q1")
	}
	if !strings.Contains(result, "A: all Go") {
		t.Error("should contain A1")
	}
	if !strings.Contains(result, "Q: Include tests?") {
		t.Error("should contain Q2")
	}
	if !strings.Contains(result, "A: yes") {
		t.Error("should contain A2")
	}
}

func TestBuildAugmentedGoal_Empty(t *testing.T) {
	result := buildAugmentedGoal("Fix bug", nil)
	if result != "Fix bug" {
		t.Errorf("expected unchanged goal, got %q", result)
	}
}

func TestFileTreeSummary(t *testing.T) {
	// Create a temp directory structure
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("package src"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(""), 0o644)

	result := fileTreeSummary(dir, 2)

	if !strings.Contains(result, "src/") {
		t.Error("should contain src/ directory")
	}
	if !strings.Contains(result, "main.go") {
		t.Error("should contain main.go file")
	}
	// Hidden dirs should be skipped
	if strings.Contains(result, ".git") {
		t.Error("should not contain .git directory")
	}
}

func TestFileTreeSummary_EmptyRoot(t *testing.T) {
	result := fileTreeSummary("", 2)
	if result != "(no codebase root)" {
		t.Errorf("expected '(no codebase root)', got %q", result)
	}
}

func TestGoalIsObviouslySpecific(t *testing.T) {
	tests := []struct {
		name     string
		goal     string
		expected bool
	}{
		// File path patterns — should skip
		{"file path with dir", "Fix the typo in internal/orchestrator/clarify.go", true},
		{"file path src style", "Add logging to src/app.ts", true},
		{"bare filename", "CLAUDE.md needs updating", true},
		{"nested path", "Refactor cmd/orchestra/main.go", true},

		// Line number references — should skip
		{"line keyword", "Fix the bug on line 42", true},
		{"colon line ref", "Error at :142 in the handler", true},

		// CLI flag references — should skip
		{"double dash flag", "Add --verbose flag to the CLI", true},
		{"flag with hyphens", "Remove --dry-run from merge", true},

		// Vague goals — should NOT skip
		{"vague fix", "Fix the bugs", false},
		{"vague cleanup", "Clean up", false},
		{"single char", "x", false},
		{"vague improve", "Make it faster", false},
		{"vague refactor", "Refactor the TUI panels", false},
		{"vague add", "Add error handling", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goalIsObviouslySpecific(tt.goal)
			if got != tt.expected {
				t.Errorf("goalIsObviouslySpecific(%q) = %v, want %v", tt.goal, got, tt.expected)
			}
		})
	}
}

func TestClarify_PreFilterSkipsAPICall(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	// Runner should NOT be called — if it is, the test fails
	c.Runner = &MockRunner{
		Errors: []error{fmt.Errorf("runner should not be called")},
	}
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Fix typo in internal/cmd/go_cmd.go", Mode: ClarifyAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Skipped {
		t.Error("expected Skipped=true for specific goal")
	}
	if result.AugmentedGoal != "Fix typo in internal/cmd/go_cmd.go" {
		t.Errorf("goal should be unchanged, got %q", result.AugmentedGoal)
	}
}

func TestClarify_DryRunSkipsBlackboard(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which files?","default":"all Go","context":"scope"}]`},
	}
	ctx := context.Background()

	// Run with DryRun=true
	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Add logging", Mode: ClarifyAuto, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Skipped {
		t.Error("expected Skipped=false")
	}

	// Blackboard should NOT have been written
	val := c.getBlackboard(ctx, "conductor:clarifications")
	if val != "" {
		t.Errorf("blackboard should be empty in dry-run, got %q", val)
	}
}

func TestClarify_NonDryRunWritesBlackboard(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which files?","default":"all Go","context":"scope"}]`},
	}
	ctx := context.Background()

	// Run with DryRun=false (default)
	_, err := c.Clarify(ctx, ClarifyOpts{Goal: "Add logging", Mode: ClarifyAuto, DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Blackboard SHOULD have been written
	val := c.getBlackboard(ctx, "conductor:clarifications")
	if val == "" {
		t.Error("blackboard should contain clarifications")
	}
}

func TestClarify_CLIMode_UserAnswers(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[
			{"id":1,"question":"Which files?","default":"all Go","context":"scope"},
			{"id":2,"question":"Include tests?","default":"yes","context":"coverage"}
		]`},
	}
	ctx := context.Background()

	// Simulate user typing "only src files" then "no"
	input := strings.NewReader("only src files\nno\n")

	result, err := c.Clarify(ctx, ClarifyOpts{
		Goal:  "Add logging",
		Mode:  ClarifyCLI,
		Stdin: input,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AutoAnswered {
		t.Error("expected AutoAnswered=false for CLI mode")
	}
	if result.Questions[0].Answer != "only src files" {
		t.Errorf("Q1 answer = %q, want %q", result.Questions[0].Answer, "only src files")
	}
	if result.Questions[1].Answer != "no" {
		t.Errorf("Q2 answer = %q, want %q", result.Questions[1].Answer, "no")
	}
	// Augmented goal should contain user answers
	if !strings.Contains(result.AugmentedGoal, "A: only src files") {
		t.Error("augmented goal should contain user answer")
	}
}

func TestClarify_CLIMode_EmptyUsesDefault(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which files?","default":"all Go","context":"scope"}]`},
	}
	ctx := context.Background()

	// Simulate user pressing Enter (empty line)
	input := strings.NewReader("\n")

	result, err := c.Clarify(ctx, ClarifyOpts{
		Goal:  "Add logging",
		Mode:  ClarifyCLI,
		Stdin: input,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Questions[0].Answer != "all Go" {
		t.Errorf("empty input should use default, got %q", result.Questions[0].Answer)
	}
}

func TestClarify_TUIMode_ChannelHandshake(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Which scope?","default":"all","context":"ambiguous"}]`},
	}

	// Create channels to simulate TUI
	questionCh := make(chan []ClarifyQuestion, 1)
	answerCh := make(chan []ClarifyQuestion, 1)
	c.ClarifyQuestionCh = questionCh
	c.ClarifyAnswerCh = answerCh

	ctx := context.Background()

	// Run Clarify in a goroutine (it will block waiting for answers)
	var result *ClarifyResult
	var clarifyErr error
	done := make(chan struct{})
	go func() {
		result, clarifyErr = c.Clarify(ctx, ClarifyOpts{Goal: "Do something", Mode: ClarifyTUI})
		close(done)
	}()

	// Simulate TUI: receive questions, send back answers
	questions := <-questionCh
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	questions[0].Answer = "only tests"
	answerCh <- questions

	// Wait for Clarify to complete
	<-done
	if clarifyErr != nil {
		t.Fatalf("unexpected error: %v", clarifyErr)
	}
	if result.Questions[0].Answer != "only tests" {
		t.Errorf("answer = %q, want %q", result.Questions[0].Answer, "only tests")
	}
	if result.AutoAnswered {
		t.Error("should not be auto-answered in TUI mode")
	}
}

func TestClarify_TUIMode_NilChannelsFallsBack(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[{"id":1,"question":"Q?","default":"D","context":"C"}]`},
	}
	// No channels wired — should fall back to auto
	ctx := context.Background()

	result, err := c.Clarify(ctx, ClarifyOpts{Goal: "Do something", Mode: ClarifyTUI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.AutoAnswered {
		t.Error("expected AutoAnswered=true when channels not wired")
	}
	if result.Questions[0].Answer != "D" {
		t.Errorf("answer = %q, want default %q", result.Questions[0].Answer, "D")
	}
}

func TestClarify_CLIMode_EOFFallsBack(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Runner = &MockRunner{
		Outputs: []string{`[
			{"id":1,"question":"Q1?","default":"D1","context":"C1"},
			{"id":2,"question":"Q2?","default":"D2","context":"C2"}
		]`},
	}
	ctx := context.Background()

	// Only one line of input for 2 questions — second gets default
	input := strings.NewReader("custom answer\n")

	result, err := c.Clarify(ctx, ClarifyOpts{
		Goal:  "Something vague",
		Mode:  ClarifyCLI,
		Stdin: input,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Questions[0].Answer != "custom answer" {
		t.Errorf("Q1 answer = %q, want %q", result.Questions[0].Answer, "custom answer")
	}
	if result.Questions[1].Answer != "D2" {
		t.Errorf("Q2 should fall back to default, got %q", result.Questions[1].Answer)
	}
}

func TestFileTreeSummary_Truncation(t *testing.T) {
	// Create a directory with many files to exceed 1000 chars
	dir := t.TempDir()
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("very_long_filename_number_%03d_with_extra_padding.go", i)
		os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644)
	}

	result := fileTreeSummary(dir, 2)
	if len(result) > 1100 { // 1000 + "... (truncated)" + small margin
		t.Errorf("result too long: %d chars", len(result))
	}
	if !strings.Contains(result, "... (truncated)") {
		t.Error("should contain truncation marker")
	}
}
