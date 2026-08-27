package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestDecompose_SingleTask(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Fix the bug","description":"Fix the login bug","role":"implementer","priority":5}]`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Fix login bug", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].Title != "Fix the bug" {
		t.Errorf("title = %q, want %q", result.Tasks[0].Title, "Fix the bug")
	}
	if result.Tasks[0].Role != "implementer" {
		t.Errorf("role = %q, want %q", result.Tasks[0].Role, "implementer")
	}
	if result.Tasks[0].ID == "" {
		t.Error("task ID should be generated")
	}
}

func TestDecompose_MultipleTasks(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"Research patterns","description":"Research error handling","role":"researcher","priority":4},
		{"title":"Implement handler","description":"Add error handler","role":"implementer","priority":5,"depends_on":["Research patterns"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add error handling", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}
}

func TestDecompose_MaxTasksRespected(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"Task 1","description":"d","role":"implementer","priority":5},
		{"title":"Task 2","description":"d","role":"implementer","priority":4},
		{"title":"Task 3","description":"d","role":"implementer","priority":3}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Big goal", MaxTasks: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Errorf("expected 2 tasks (max), got %d", len(result.Tasks))
	}
}

func TestDecompose_TasksCreatedInDB(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Implement feature","description":"Do the thing","role":"implementer","priority":5}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add feature"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify task exists in DB
	task, err := d.GetTaskByID(ctx, result.Tasks[0].ID)
	if err != nil {
		t.Fatalf("error getting task: %v", err)
	}
	if task == nil {
		t.Fatal("task not found in DB")
	}
	if task.Title != "Implement feature" {
		t.Errorf("DB title = %q, want %q", task.Title, "Implement feature")
	}
	if task.Status != "pending" {
		t.Errorf("DB status = %q, want %q", task.Status, "pending")
	}
	if task.Role != "implementer" {
		t.Errorf("DB role = %q, want %q", task.Role, "implementer")
	}
}

func TestDecompose_DependencyChain(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"Task A","description":"first","role":"architect","priority":5},
		{"title":"Task B","description":"second","role":"implementer","priority":4,"depends_on":["Task A"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Chained work"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task B should have Task A's ID in its blocked_by
	taskB, _ := d.GetTaskByID(ctx, result.Tasks[1].ID)
	if taskB == nil {
		t.Fatal("task B not found")
	}

	var blockers []string
	json.Unmarshal([]byte(taskB.BlockedBy.String), &blockers)
	if len(blockers) != 1 || blockers[0] != result.Tasks[0].ID {
		t.Errorf("task B blockers = %v, want [%s]", blockers, result.Tasks[0].ID)
	}
}

func TestDecompose_SessionIDStored(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Test goal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check blackboard has task IDs
	taskIDsJSON, _ := d.GetBlackboardValue(ctx, "conductor:task_ids")
	if taskIDsJSON == "" {
		t.Fatal("conductor:task_ids not set in blackboard")
	}

	var storedIDs []string
	json.Unmarshal([]byte(taskIDsJSON), &storedIDs)
	if len(storedIDs) != 1 || storedIDs[0] != result.Tasks[0].ID {
		t.Errorf("stored IDs = %v, want [%s]", storedIDs, result.Tasks[0].ID)
	}
}

func TestDecompose_EmptyGoal(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: ""})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
}

func TestDecompose_NilRunner(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for nil runner")
	}
}

func TestDecompose_MalformedOutput(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{Outputs: []string{"This is not JSON at all"}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for malformed output")
	}
}

func TestDecompose_JSONWithSurroundingText(t *testing.T) {
	d := setupTestDB(t)
	// Claude sometimes wraps JSON in markdown code blocks
	output := "Here is my decomposition:\n```json\n" +
		`[{"title":"Do stuff","description":"details","role":"implementer","priority":5}]` +
		"\n```\nDone!"
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(result.Tasks))
	}
}

func TestDecompose_GoalPassedToPrompt(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Decompose(ctx, DecomposeOpts{Goal: "My specific goal"})

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.Calls))
	}
	if !containsSubstring(runner.Calls[0].Prompt, "My specific goal") {
		t.Error("prompt should contain the goal")
	}
}

func TestDecompose_DefaultRole(t *testing.T) {
	d := setupTestDB(t)
	// Task with no role should default to implementer
	output := `[{"title":"Test","description":"d","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tasks[0].Role != "implementer" {
		t.Errorf("default role = %q, want %q", result.Tasks[0].Role, "implementer")
	}
}

func TestDecompose_EmptyTasksArray(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{Outputs: []string{`[]`}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for empty tasks array")
	}
}

func TestExtractJSONArray(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`[{"a":1}]`, `[{"a":1}]`},
		{`text before [{"a":1}] text after`, `[{"a":1}]`},
		{`no array here`, ""},
		{`[nested [inner] outer]`, `[nested [inner] outer]`},
		{``, ""},
	}
	for _, tt := range tests {
		got := extractJSONArray(tt.input)
		if got != tt.want {
			t.Errorf("extractJSONArray(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFlexStringArr_StringArray(t *testing.T) {
	input := `[{"title":"A","description":"","role":"implementer","priority":5,"depends_on":[],"files":[]},{"title":"B","description":"","role":"implementer","priority":4,"depends_on":["A"],"files":[]}]`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "A" {
		t.Errorf("depends_on = %v, want [A]", tasks[1].DependsOn)
	}
}

func TestFlexStringArr_IntArray(t *testing.T) {
	// LLM returns depends_on: [0] (integer index instead of string title)
	input := `[{"title":"A","description":"","role":"implementer","priority":5,"depends_on":[],"files":[]},{"title":"B","description":"","role":"implementer","priority":4,"depends_on":[0],"files":[]}]`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "0" {
		t.Errorf("depends_on = %v, want [0]", tasks[1].DependsOn)
	}
}

func TestFlexStringArr_EmptyArray(t *testing.T) {
	input := `[{"title":"A","description":"","role":"implementer","priority":5,"depends_on":[],"files":[]}]`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks[0].DependsOn) != 0 {
		t.Errorf("depends_on = %v, want []", tasks[0].DependsOn)
	}
}

func TestResolveDependencies_IntIndex(t *testing.T) {
	tasks := []DecomposedTask{
		{ID: "t-001", Title: "First task"},
		{ID: "t-002", Title: "Second task"},
	}
	idMap := map[int]string{0: "t-001", 1: "t-002"}

	// "0" should resolve to t-001 (index-based reference)
	resolved := resolveDependencies([]string{"0"}, tasks, idMap, "t-002")
	if len(resolved) != 1 || resolved[0] != "t-001" {
		t.Errorf("resolved = %v, want [t-001]", resolved)
	}
}

func TestResolveDependencies_StripsSelfReference(t *testing.T) {
	tasks := []DecomposedTask{
		{ID: "t-001", Title: "Research phase"},
		{ID: "t-002", Title: "Implementation phase"},
	}
	idMap := map[int]string{0: "t-001", 1: "t-002"}

	// Task t-002 depends on t-001 AND itself (via title match) — self-ref should be stripped
	resolved := resolveDependencies([]string{"0", "Implementation phase"}, tasks, idMap, "t-002")
	if len(resolved) != 1 || resolved[0] != "t-001" {
		t.Errorf("resolved = %v, want [t-001] (self-ref stripped)", resolved)
	}

	// Task t-001 depends on itself by ID — should be stripped entirely
	resolved2 := resolveDependencies([]string{"t-001"}, tasks, idMap, "t-001")
	if len(resolved2) != 0 {
		t.Errorf("resolved = %v, want [] (self-ref stripped)", resolved2)
	}
}

func TestDecompose_CycleDetected_DepsCleared(t *testing.T) {
	d := setupTestDB(t)
	// Two tasks with mutual dependency: A depends on B, B depends on A
	output := `[
		{"title":"Task A","description":"first","role":"implementer","priority":5,"depends_on":["Task B"]},
		{"title":"Task B","description":"second","role":"implementer","priority":4,"depends_on":["Task A"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Mutual cycle test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}

	// Both tasks should have their deps cleared (cycle broken)
	for _, task := range result.Tasks {
		dbTask, _ := d.GetTaskByID(ctx, task.ID)
		if dbTask == nil {
			t.Fatalf("task %s not found in DB", task.ID)
		}
		var blockers []string
		json.Unmarshal([]byte(dbTask.BlockedBy.String), &blockers)
		if len(blockers) != 0 {
			t.Errorf("task %s should have empty deps after cycle break, got %v", task.ID, blockers)
		}
	}

	// Verify cycle_detected event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "decompose_cycle_detected" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected decompose_cycle_detected event to be logged")
	}
}

func TestDetectAndBreakCycles_NoCycle(t *testing.T) {
	// A → B → C — valid acyclic DAG; all deps should be preserved unchanged.
	tasks := []DecomposedTask{
		{ID: "A", Title: "Task A", DependsOn: nil},
		{ID: "B", Title: "Task B", DependsOn: FlexStringArr{"A"}},
		{ID: "C", Title: "Task C", DependsOn: FlexStringArr{"B"}},
	}

	cycleIDs := detectAndBreakCycles(tasks)
	if len(cycleIDs) != 0 {
		t.Fatalf("expected no cycles, got participants: %v", cycleIDs)
	}

	// Verify all dependency edges are preserved.
	if len(tasks[0].DependsOn) != 0 {
		t.Errorf("task A deps = %v, want []", tasks[0].DependsOn)
	}
	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "A" {
		t.Errorf("task B deps = %v, want [A]", tasks[1].DependsOn)
	}
	if len(tasks[2].DependsOn) != 1 || tasks[2].DependsOn[0] != "B" {
		t.Errorf("task C deps = %v, want [B]", tasks[2].DependsOn)
	}
}

func TestDecompose_CreatesFileLocks(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"Add models","description":"Add models to models.go","role":"implementer","priority":5,"files":["internal/db/models.go","internal/db/schema.sql"]},
		{"title":"Add queries","description":"Add query functions","role":"implementer","priority":4,"files":["internal/db/queries.go"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add new DB feature", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}

	// Verify file locks were created
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		t.Fatalf("error listing locks: %v", err)
	}
	if len(locks) != 3 {
		t.Fatalf("expected 3 file locks, got %d", len(locks))
	}

	// Verify each task owns its files
	task1Locks, _ := d.ListFileLocksForTask(ctx, result.Tasks[0].ID)
	if len(task1Locks) != 2 {
		t.Errorf("task 1 should own 2 files, got %d", len(task1Locks))
	}
	task2Locks, _ := d.ListFileLocksForTask(ctx, result.Tasks[1].ID)
	if len(task2Locks) != 1 {
		t.Errorf("task 2 should own 1 file, got %d", len(task2Locks))
	}
}

func TestDecompose_DuplicateFileWarning(t *testing.T) {
	d := setupTestDB(t)
	// Both tasks claim the same file — second lock should fail gracefully
	output := `[
		{"title":"Task A","description":"Modify decompose","role":"implementer","priority":5,"files":["internal/orchestrator/decompose.go"]},
		{"title":"Task B","description":"Also modify decompose","role":"implementer","priority":4,"files":["internal/orchestrator/decompose.go"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// Should not error — duplicate lock is non-fatal
	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Overlapping files", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}

	// Only one lock should exist (highest priority wins)
	locks, _ := d.ListFileLocks(ctx)
	if len(locks) != 1 {
		t.Errorf("expected 1 file lock (highest priority wins), got %d", len(locks))
	}

	// The lock should belong to the second task (priority 4 beats priority 5)
	if len(locks) > 0 && locks[0].TaskID.String != result.Tasks[1].ID {
		t.Errorf("lock owner = %s, want %s (highest priority task)", locks[0].TaskID.String, result.Tasks[1].ID)
	}
}

func TestDecompose_PromptIncludesFileExclusivity(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Decompose(ctx, DecomposeOpts{Goal: "test goal", MaxTasks: 3})

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.Calls))
	}
	if !containsSubstring(runner.Calls[0].Prompt, "Each file MUST appear in at most one task") {
		t.Error("prompt should contain file exclusivity rule")
	}
	if !containsSubstring(runner.Calls[0].Prompt, "Prefer fewer tasks") {
		t.Error("prompt should prefer fewer tasks for maxTasks < 5")
	}
}

func TestDecompose_HighMaxTasksPrompt(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Decompose(ctx, DecomposeOpts{Goal: "big goal", MaxTasks: 5})

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.Calls))
	}
	if !containsSubstring(runner.Calls[0].Prompt, "Create up to 5 tasks") {
		t.Error("prompt should scale task preference for maxTasks >= 5")
	}
}

func TestExtractGoalFiles(t *testing.T) {
	tests := []struct {
		name string
		goal string
		want []string
	}{
		{
			name: "files in narrative chain",
			goal: "Add priority_label column: schema/001_initial.sql -> models.go -> queries.go -> mutations.go -> spawner.go -> go.go -> iterative.go -> TUI display.",
			want: []string{"go.go", "iterative.go", "models.go", "mutations.go", "queries.go", "schema/001_initial.sql", "spawner.go"},
		},
		{
			name: "full paths",
			goal: "Modify internal/orchestrator/decompose.go and internal/db/schema.sql to add validation",
			want: []string{"internal/db/schema.sql", "internal/orchestrator/decompose.go"},
		},
		{
			name: "mixed extensions",
			goal: "Update config.yaml, README.md, and src/index.ts for the new feature",
			want: []string{"README.md", "config.yaml", "src/index.ts"},
		},
		{
			name: "duplicates removed",
			goal: "Fix models.go then test models.go again",
			want: []string{"models.go"},
		},
		{
			name: "file with dashes",
			goal: "Edit my-component.ts and some-config.yaml",
			want: []string{"my-component.ts", "some-config.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGoalFiles(tt.goal)
			if len(got) != len(tt.want) {
				t.Fatalf("extractGoalFiles() = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractGoalFiles()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractGoalFiles_NoFiles(t *testing.T) {
	tests := []struct {
		name string
		goal string
	}{
		{"plain text", "Fix the login bug and add error handling"},
		{"no extensions", "Update the database schema and add models"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGoalFiles(tt.goal)
			if len(got) != 0 {
				t.Errorf("extractGoalFiles(%q) = %v, want nil/empty", tt.goal, got)
			}
		})
	}
}

func TestDecomposeCoverageValidation(t *testing.T) {
	d := setupTestDB(t)
	// LLM output only assigns models.go — missing queries.go and go.go from the goal
	output := `[
		{"title":"Add models","description":"Add the model","role":"implementer","priority":5,"files":["internal/db/models.go"]},
		{"title":"Add queries","description":"Add queries","role":"implementer","priority":4,"files":["internal/db/schema.sql"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// Goal explicitly mentions 4 files; LLM only assigned 2
	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:     "Update internal/db/models.go, internal/db/queries.go, internal/db/schema.sql, and internal/orchestrator/go.go",
		MaxTasks: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all assigned files from result
	allFiles := make(map[string]bool)
	for _, task := range result.Tasks {
		for _, f := range task.Files {
			allFiles[f] = true
		}
	}

	// The missing files (queries.go and go.go) should have been injected
	wantFiles := []string{
		"internal/db/models.go",
		"internal/db/queries.go",
		"internal/db/schema.sql",
		"internal/orchestrator/go.go",
	}
	for _, wf := range wantFiles {
		if !allFiles[wf] {
			t.Errorf("expected file %q to be assigned to a task, but it was not", wf)
		}
	}

	// queries.go should land in a task that owns other internal/db/ files
	for _, task := range result.Tasks {
		for _, f := range task.Files {
			if f == "internal/db/queries.go" {
				// Verify it's in a task with other internal/db/ files
				hasDbFile := false
				for _, of := range task.Files {
					if of != "internal/db/queries.go" && contains(of, "internal/db/") {
						hasDbFile = true
						break
					}
				}
				if !hasDbFile {
					t.Error("internal/db/queries.go should be injected into a task with other internal/db/ files")
				}
			}
		}
	}

	// Verify coverage gap event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "decompose_coverage_gap" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected decompose_coverage_gap event to be logged")
	}
}

func TestDecompose_PromptIncludesGoalFiles(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3,"files":["models.go","queries.go"]}]`
	// Second output: action expansion response (files have vague "d" description)
	expansionOutput := `{"expansions":[]}`
	runner := &MockRunner{Outputs: []string{output, expansionOutput}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Decompose(ctx, DecomposeOpts{Goal: "Update models.go and queries.go", MaxTasks: 3})

	// First call is decomposition, second is action expansion (B-207)
	if len(runner.Calls) < 1 {
		t.Fatalf("expected at least 1 runner call, got %d", len(runner.Calls))
	}
	if !containsSubstring(runner.Calls[0].Prompt, "FILES MENTIONED IN GOAL") {
		t.Error("prompt should contain FILES MENTIONED IN GOAL section")
	}
	if !containsSubstring(runner.Calls[0].Prompt, "models.go") {
		t.Error("prompt should list models.go as a goal file")
	}
	if !containsSubstring(runner.Calls[0].Prompt, "queries.go") {
		t.Error("prompt should list queries.go as a goal file")
	}
}

func TestExtractGoalFiles_ExtendedExtensions(t *testing.T) {
	tests := []struct {
		name string
		goal string
		want []string
	}{
		{
			name: "rust and ruby",
			goal: "Update lib.rs and app.rb for the new parser",
			want: []string{"app.rb", "lib.rs"},
		},
		{
			name: "shell and css",
			goal: "Fix deploy.sh and styles.css",
			want: []string{"deploy.sh", "styles.css"},
		},
		{
			name: "html and proto",
			goal: "Edit index.html and service.proto definitions",
			want: []string{"index.html", "service.proto"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGoalFiles(tt.goal)
			if len(got) != len(tt.want) {
				t.Fatalf("extractGoalFiles() = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDecomposeCoverageResult_FullCoverage(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Update models","description":"d","role":"implementer","priority":5,"files":["models.go","queries.go"]}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Update models.go and queries.go", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GoalFiles) != 2 {
		t.Errorf("GoalFiles = %v, want 2 files", result.GoalFiles)
	}
	if len(result.FilesCoverageMissing) != 0 {
		t.Errorf("FilesCoverageMissing = %v, want empty", result.FilesCoverageMissing)
	}
	if result.FilesCoveragePercent != 100 {
		t.Errorf("FilesCoveragePercent = %.1f, want 100", result.FilesCoveragePercent)
	}
}

func TestDecomposeCoverageResult_PartialCoverage(t *testing.T) {
	d := setupTestDB(t)
	// LLM only assigns 1 of 3 goal files
	output := `[{"title":"Fix it","description":"d","role":"implementer","priority":5,"files":["models.go"]}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:     "Update models.go, queries.go, and schema.sql",
		MaxTasks: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GoalFiles) != 3 {
		t.Errorf("GoalFiles = %v, want 3 files", result.GoalFiles)
	}
	if len(result.FilesCoverageMissing) != 2 {
		t.Errorf("FilesCoverageMissing = %v, want 2 files", result.FilesCoverageMissing)
	}
	// 1/3 covered by LLM = 33.3%
	if result.FilesCoveragePercent < 33 || result.FilesCoveragePercent > 34 {
		t.Errorf("FilesCoveragePercent = %.1f, want ~33.3", result.FilesCoveragePercent)
	}
}

func TestDecomposeCoverageResult_NoGoalFiles(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Fix bug","description":"d","role":"implementer","priority":5}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Fix the login bug", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GoalFiles) != 0 {
		t.Errorf("GoalFiles = %v, want empty", result.GoalFiles)
	}
	if result.FilesCoveragePercent != 0 {
		t.Errorf("FilesCoveragePercent = %.1f, want 0", result.FilesCoveragePercent)
	}
}

func TestInjectMissingFiles_DirectoryAffinity(t *testing.T) {
	tasks := []DecomposedTask{
		{ID: "t1", Title: "DB work", Role: "implementer", Files: []string{"internal/db/models.go", "internal/db/schema.sql"}},
		{ID: "t2", Title: "Orchestrator work", Role: "implementer", Files: []string{"internal/orchestrator/merge.go"}},
	}

	missing := []string{"internal/db/queries.go", "internal/orchestrator/go.go"}
	tasks = injectMissingFiles(tasks, missing)

	// queries.go should go to t1 (same directory: internal/db/)
	t1HasQueries := false
	for _, f := range tasks[0].Files {
		if f == "internal/db/queries.go" {
			t1HasQueries = true
		}
	}
	if !t1HasQueries {
		t.Error("internal/db/queries.go should be injected into task with internal/db/ files")
	}

	// go.go should go to t2 (same directory: internal/orchestrator/)
	t2HasGo := false
	for _, f := range tasks[1].Files {
		if f == "internal/orchestrator/go.go" {
			t2HasGo = true
		}
	}
	if !t2HasGo {
		t.Error("internal/orchestrator/go.go should be injected into task with internal/orchestrator/ files")
	}
}

func TestResolveGoalFilePaths(t *testing.T) {
	// Create a temp repo structure
	tmpDir := t.TempDir()
	dirs := []string{
		"internal/orchestrator",
		"internal/db",
		"internal/agent",
		"internal/tui/panels",
	}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(tmpDir, d), 0o755)
	}
	files := []string{
		"internal/orchestrator/go.go",
		"internal/orchestrator/iterative.go",
		"internal/orchestrator/decompose.go",
		"internal/db/models.go",
		"internal/db/queries.go",
		"internal/db/mutations.go",
		"internal/db/schema.sql",
		"internal/agent/spawner.go",
		"internal/tui/panels/tasks.go",
	}
	for _, f := range files {
		os.WriteFile(filepath.Join(tmpDir, f), []byte("package x"), 0o644)
	}

	t.Run("bare names resolved to full paths", func(t *testing.T) {
		input := []string{"go.go", "iterative.go", "models.go"}
		got := resolveGoalFilePaths(input, tmpDir)

		want := map[string]bool{
			"internal/orchestrator/go.go":        true,
			"internal/orchestrator/iterative.go":  true,
			"internal/db/models.go":               true,
		}
		for _, g := range got {
			if !want[g] {
				t.Errorf("unexpected resolved path: %q", g)
			}
			delete(want, g)
		}
		for w := range want {
			t.Errorf("expected path not found: %q", w)
		}
	})

	t.Run("full paths kept as-is", func(t *testing.T) {
		input := []string{"internal/db/models.go", "internal/orchestrator/go.go"}
		got := resolveGoalFilePaths(input, tmpDir)

		if len(got) != 2 {
			t.Fatalf("got %d paths, want 2: %v", len(got), got)
		}
		gotSet := map[string]bool{got[0]: true, got[1]: true}
		if !gotSet["internal/db/models.go"] || !gotSet["internal/orchestrator/go.go"] {
			t.Errorf("full paths should be preserved: %v", got)
		}
	})

	t.Run("mixed bare and full paths", func(t *testing.T) {
		input := []string{"internal/db/schema.sql", "spawner.go"}
		got := resolveGoalFilePaths(input, tmpDir)

		gotSet := make(map[string]bool)
		for _, g := range got {
			gotSet[g] = true
		}
		if !gotSet["internal/db/schema.sql"] {
			t.Error("full path should be preserved")
		}
		if !gotSet["internal/agent/spawner.go"] {
			t.Error("spawner.go should resolve to internal/agent/spawner.go")
		}
	})

	t.Run("unresolvable bare name kept", func(t *testing.T) {
		input := []string{"nonexistent.go"}
		got := resolveGoalFilePaths(input, tmpDir)

		if len(got) != 1 || got[0] != "nonexistent.go" {
			t.Errorf("unresolvable name should be kept: got %v", got)
		}
	})

	t.Run("empty repo root skips resolution", func(t *testing.T) {
		input := []string{"go.go", "models.go"}
		got := resolveGoalFilePaths(input, "")

		if len(got) != 2 || got[0] != "go.go" || got[1] != "models.go" {
			t.Errorf("empty repo root should return input unchanged: got %v", got)
		}
	})
}

func TestFileDifference_BasenameFuzzyMatch(t *testing.T) {
	// Goal has full paths, assigned has full paths — exact match
	t.Run("exact match", func(t *testing.T) {
		want := []string{"internal/db/models.go", "internal/orchestrator/go.go"}
		have := map[string]bool{
			"internal/db/models.go":         true,
			"internal/orchestrator/go.go":   true,
		}
		missing := fileDifference(want, have)
		if len(missing) != 0 {
			t.Errorf("expected no missing, got %v", missing)
		}
	})

	// Goal has full paths, assigned has bare names — basename match
	t.Run("basename match", func(t *testing.T) {
		want := []string{"internal/orchestrator/go.go", "internal/db/models.go"}
		have := map[string]bool{
			"go.go":     true,
			"models.go": true,
		}
		missing := fileDifference(want, have)
		if len(missing) != 0 {
			t.Errorf("expected no missing (basename match), got %v", missing)
		}
	})

	// Goal has bare names, assigned has full paths — basename match
	t.Run("reverse basename match", func(t *testing.T) {
		want := []string{"go.go", "models.go"}
		have := map[string]bool{
			"internal/orchestrator/go.go": true,
			"internal/db/models.go":       true,
		}
		missing := fileDifference(want, have)
		if len(missing) != 0 {
			t.Errorf("expected no missing (reverse basename match), got %v", missing)
		}
	})

	// Genuinely missing
	t.Run("genuinely missing", func(t *testing.T) {
		want := []string{"internal/orchestrator/go.go", "internal/db/queries.go"}
		have := map[string]bool{
			"internal/db/models.go": true,
		}
		missing := fileDifference(want, have)
		if len(missing) != 2 {
			t.Errorf("expected 2 missing, got %v", missing)
		}
	})
}

func TestDecompose_AcceptanceCriteria(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Add field","description":"Add PriorityLabel to Task struct","role":"implementer","priority":5,"files":["internal/db/models.go"],"acceptance_criteria":"- [ ] Task struct has PriorityLabel sql.NullString field\n- [ ] Field is after Priority int field"}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add priority label field"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}

	// Verify AC flows to the DecomposedTask
	if result.Tasks[0].AcceptanceCriteria == "" {
		t.Error("expected non-empty AcceptanceCriteria on decomposed task")
	}
	if !containsSubstring(result.Tasks[0].AcceptanceCriteria, "PriorityLabel") {
		t.Error("AcceptanceCriteria should contain PriorityLabel")
	}

	// Verify AC is persisted in DB
	task, err := d.GetTaskByID(ctx, result.Tasks[0].ID)
	if err != nil {
		t.Fatalf("error getting task: %v", err)
	}
	if !task.AcceptanceCriteria.Valid || task.AcceptanceCriteria.String == "" {
		t.Error("expected AcceptanceCriteria to be stored in DB")
	}
	if !containsSubstring(task.AcceptanceCriteria.String, "PriorityLabel") {
		t.Error("DB AcceptanceCriteria should contain PriorityLabel")
	}

	// Verify AC quality event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "decompose_ac_quality" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected decompose_ac_quality event")
	}
}

func TestDecompose_AcceptanceCriteria_Missing(t *testing.T) {
	d := setupTestDB(t)
	// LLM returns no acceptance_criteria field — should be handled gracefully
	output := `[{"title":"Fix bug","description":"Fix the bug","role":"implementer","priority":5}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Fix bug"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tasks[0].AcceptanceCriteria != "" {
		t.Errorf("expected empty AC when not provided, got %q", result.Tasks[0].AcceptanceCriteria)
	}

	// DB should have NULL/empty AC
	task, _ := d.GetTaskByID(ctx, result.Tasks[0].ID)
	if task.AcceptanceCriteria.Valid {
		t.Error("expected AcceptanceCriteria to be NULL in DB when not provided")
	}
}

func TestContainsVagueAC(t *testing.T) {
	tests := []struct {
		name string
		ac   string
		want bool
	}{
		{"empty", "", false},
		{"concrete", "- [ ] Task struct has PriorityLabel field\n- [ ] Field type is sql.NullString", false},
		{"vague verify flows", "- [ ] Verify the field flows through the system", true},
		{"vague confirm not dropped", "- [ ] Confirm the field is not dropped during serialization", true},
		{"vague no direct changes", "- [ ] No direct changes expected but check integration", true},
		{"vague ensure works", "- [ ] Ensure the pipeline works correctly", true},
		{"vague check still", "- [ ] Check that the tests still pass", true},
		{"concrete with verify keyword in context", "- [ ] Add VerifyChecksum function to utils.go", false},
		{"mixed concrete and vague", "- [ ] Add PriorityLabel field\n- [ ] Verify the field flows through", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsVagueAC(tt.ac)
			if got != tt.want {
				t.Errorf("containsVagueAC(%q) = %v, want %v", tt.ac, got, tt.want)
			}
		})
	}
}

func TestDecompose_PromptIncludesACRules(t *testing.T) {
	d := setupTestDB(t)
	output := `[{"title":"Test","description":"d","role":"implementer","priority":3}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	c.Decompose(ctx, DecomposeOpts{Goal: "test goal", MaxTasks: 3})

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.Calls))
	}
	prompt := runner.Calls[0].Prompt
	if !containsSubstring(prompt, "acceptance_criteria") {
		t.Error("prompt should contain acceptance_criteria in output format")
	}
	if !containsSubstring(prompt, "2-5 specific, testable acceptance criteria") {
		t.Error("prompt should contain AC rule about 2-5 criteria")
	}
	if !containsSubstring(prompt, "BAD:") {
		t.Error("prompt should contain BAD examples for vague ACs")
	}
	if !containsSubstring(prompt, "GOOD:") {
		t.Error("prompt should contain GOOD examples for concrete ACs")
	}
}

func TestParseDecomposeOutput_WrappedFormat(t *testing.T) {
	// --json-schema mode returns {"tasks": [...]} wrapper
	input := `{"tasks":[{"title":"Fix the bug","description":"Fix login","role":"implementer","priority":5,"files":["main.go"],"acceptance_criteria":"- [ ] Bug is fixed"}]}`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Fix the bug" {
		t.Errorf("title = %q, want %q", tasks[0].Title, "Fix the bug")
	}
	if tasks[0].AcceptanceCriteria != "- [ ] Bug is fixed" {
		t.Errorf("acceptance_criteria = %q, want %q", tasks[0].AcceptanceCriteria, "- [ ] Bug is fixed")
	}
	if len(tasks[0].Files) != 1 || tasks[0].Files[0] != "main.go" {
		t.Errorf("files = %v, want [main.go]", tasks[0].Files)
	}
}

func TestParseDecomposeOutput_WrappedMultipleTasks(t *testing.T) {
	input := `{"tasks":[
		{"title":"Add schema","description":"Add column","role":"implementer","priority":5,"files":["schema.sql"],"acceptance_criteria":"- [ ] Column added"},
		{"title":"Add model","description":"Add Go field","role":"implementer","priority":4,"files":["models.go"],"acceptance_criteria":"- [ ] Field added","depends_on":["Add schema"]}
	]}`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[1].DependsOn[0] != "Add schema" {
		t.Errorf("depends_on = %v, want [Add schema]", tasks[1].DependsOn)
	}
}

func TestParseDecomposeOutput_FallbackArray(t *testing.T) {
	// Legacy text mode returns bare JSON array
	input := `[{"title":"Do stuff","description":"details","role":"implementer","priority":5}]`
	tasks, err := parseDecomposeOutput(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Do stuff" {
		t.Errorf("title = %q, want %q", tasks[0].Title, "Do stuff")
	}
}

func TestParseDecomposeOutput_WrappedMaxTasks(t *testing.T) {
	input := `{"tasks":[
		{"title":"Task 1","description":"d","role":"implementer","priority":5,"files":[],"acceptance_criteria":"- [ ] Done"},
		{"title":"Task 2","description":"d","role":"implementer","priority":4,"files":[],"acceptance_criteria":"- [ ] Done"},
		{"title":"Task 3","description":"d","role":"implementer","priority":3,"files":[],"acceptance_criteria":"- [ ] Done"}
	]}`
	tasks, err := parseDecomposeOutput(input, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks (max), got %d", len(tasks))
	}
}

func TestDecompose_UsesJSONSchema(t *testing.T) {
	d := setupTestDB(t)
	// Simulate wrapped format output that --json-schema would produce
	output := `{"tasks":[{"title":"Fix it","description":"Fix the thing","role":"implementer","priority":5,"files":["main.go"],"acceptance_criteria":"- [ ] Fixed"}]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Fix the thing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	// Verify the runner was called with JSONSchema set
	if len(runner.Calls) < 1 {
		t.Fatal("expected at least 1 runner call")
	}
	if runner.Calls[0].JSONSchema == "" {
		t.Error("expected JSONSchema to be set in runner call")
	}
}

// --- B-207: findVagueFiles tests ---

func TestFindVagueFiles_AllConcrete(t *testing.T) {
	task := DecomposedTask{
		Title:              "Add priority_label field",
		Description:        "Add PriorityLabel field to models.go and add column in schema.sql",
		AcceptanceCriteria: "- [ ] models.go has PriorityLabel sql.NullString field\n- [ ] schema.sql has priority_label TEXT column",
		Files:              []string{"internal/db/models.go", "internal/db/schema.sql"},
	}
	vague := findVagueFiles(task)
	if len(vague) != 0 {
		t.Errorf("expected no vague files, got %v", vague)
	}
}

func TestFindVagueFiles_VagueDetected(t *testing.T) {
	task := DecomposedTask{
		Title:              "Add priority_label end-to-end",
		Description:        "Add PriorityLabel to models.go. Verify the field flows through spawner.go correctly. Ensure iterative.go works.",
		AcceptanceCriteria: "- [ ] models.go has PriorityLabel field\n- [ ] Verify spawner.go flows the field\n- [ ] Ensure iterative.go still works",
		Files:              []string{"internal/db/models.go", "internal/agent/spawner.go", "internal/orchestrator/iterative.go"},
	}
	vague := findVagueFiles(task)
	// spawner.go and iterative.go have vague instructions
	if len(vague) != 2 {
		t.Fatalf("expected 2 vague files, got %d: %v", len(vague), vague)
	}
	vagueSet := map[string]bool{vague[0]: true, vague[1]: true}
	if !vagueSet["internal/agent/spawner.go"] {
		t.Error("expected spawner.go to be vague")
	}
	if !vagueSet["internal/orchestrator/iterative.go"] {
		t.Error("expected iterative.go to be vague")
	}
}

func TestFindVagueFiles_UnmentionedFile(t *testing.T) {
	task := DecomposedTask{
		Title:              "Add feature",
		Description:        "Add the feature to models.go",
		AcceptanceCriteria: "- [ ] models.go has the feature",
		Files:              []string{"internal/db/models.go", "internal/db/queries.go"},
	}
	vague := findVagueFiles(task)
	// queries.go is not mentioned at all — should be vague
	if len(vague) != 1 || vague[0] != "internal/db/queries.go" {
		t.Errorf("expected [internal/db/queries.go], got %v", vague)
	}
}

func TestFindVagueFiles_NoFiles(t *testing.T) {
	task := DecomposedTask{
		Title:       "Research task",
		Description: "Research the topic",
	}
	vague := findVagueFiles(task)
	if len(vague) != 0 {
		t.Errorf("expected nil, got %v", vague)
	}
}

// --- B-207: mergeExpandedActions tests ---

func TestMergeExpandedActions_AppendsConcrete(t *testing.T) {
	task := DecomposedTask{
		Title:              "Add field",
		Description:        "Original description",
		AcceptanceCriteria: "- [ ] Original AC",
		Files:              []string{"models.go", "spawner.go"},
	}
	result := fileActionExpansionResult{
		Expansions: []fileActionExpansion{
			{
				File:                "spawner.go",
				ConcreteAction:      "Add PriorityLabel parameter to SpawnAgent function",
				AcceptanceCriterion: "SpawnAgent accepts PriorityLabel string parameter",
				Skip:                false,
			},
		},
	}

	merged := mergeExpandedActions(task, result)

	if !strings.Contains(merged.Description, "Expanded File Actions") {
		t.Error("expected expanded actions section in description")
	}
	if !strings.Contains(merged.Description, "Add PriorityLabel parameter") {
		t.Error("expected concrete action in description")
	}
	if !strings.Contains(merged.AcceptanceCriteria, "SpawnAgent accepts PriorityLabel") {
		t.Error("expected expanded AC")
	}
	// Both files should still be present
	if len(merged.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(merged.Files))
	}
}

func TestMergeExpandedActions_SkipsFiles(t *testing.T) {
	task := DecomposedTask{
		Title:              "Add field",
		Description:        "Original",
		AcceptanceCriteria: "- [ ] Original",
		Files:              []string{"models.go", "unneeded.go", "queries.go"},
	}
	result := fileActionExpansionResult{
		Expansions: []fileActionExpansion{
			{
				File:       "unneeded.go",
				Skip:       true,
				SkipReason: "No changes needed for this feature",
			},
		},
	}

	merged := mergeExpandedActions(task, result)

	// unneeded.go should be removed
	if len(merged.Files) != 2 {
		t.Fatalf("expected 2 files after skip, got %d: %v", len(merged.Files), merged.Files)
	}
	for _, f := range merged.Files {
		if f == "unneeded.go" {
			t.Error("skipped file should be removed")
		}
	}
}

func TestMergeExpandedActions_EmptyExpansions(t *testing.T) {
	task := DecomposedTask{
		Title:              "Test",
		Description:        "Original",
		AcceptanceCriteria: "- [ ] AC",
		Files:              []string{"a.go", "b.go"},
	}
	result := fileActionExpansionResult{}

	merged := mergeExpandedActions(task, result)

	// Should be unchanged
	if merged.Description != "Original" {
		t.Error("description should be unchanged with empty expansions")
	}
	if len(merged.Files) != 2 {
		t.Errorf("files should be unchanged, got %d", len(merged.Files))
	}
}

// --- B-207: Action expansion integration test ---

func TestDecompose_ActionExpansionIntegration(t *testing.T) {
	d := setupTestDB(t)

	// First call: decomposition returns tasks with vague file instructions
	// Second call: action expansion returns concrete actions
	runner := &MockRunner{
		Outputs: []string{
			// Decomposition output
			`{"tasks":[{"title":"Add priority_label","description":"Add PriorityLabel to models.go. Verify the field flows through spawner.go.","role":"implementer","priority":5,"files":["internal/db/models.go","internal/agent/spawner.go"],"acceptance_criteria":"- [ ] models.go has PriorityLabel field\n- [ ] Verify spawner.go flows the field"}]}`,
			// Action expansion output for spawner.go
			`{"expansions":[{"file":"internal/agent/spawner.go","concrete_action":"Add PriorityLabel to SpawnOpts struct and pass to SpecGenOpts","acceptance_criterion":"SpawnOpts has PriorityLabel string field","skip":false}]}`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add priority_label field"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The task should have expanded actions merged in
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}

	task := result.Tasks[0]
	if !strings.Contains(task.Description, "Expanded File Actions") {
		t.Error("expected expanded actions in task description")
	}
	if !strings.Contains(task.AcceptanceCriteria, "SpawnOpts has PriorityLabel") {
		t.Error("expected expanded AC in task")
	}

	// Verify runner was called twice (decompose + expansion)
	if len(runner.Calls) != 2 {
		t.Errorf("expected 2 runner calls (decompose + expansion), got %d", len(runner.Calls))
	}
}

func TestDecompose_ActionExpansionFailOpen(t *testing.T) {
	d := setupTestDB(t)

	// First call: decomposition returns tasks with vague instructions
	// Second call: expansion fails
	runner := &MockRunner{
		Outputs: []string{
			`{"tasks":[{"title":"Add field","description":"Add field to models.go. Verify spawner.go flows it.","role":"implementer","priority":5,"files":["models.go","spawner.go"],"acceptance_criteria":"- [ ] models.go has field\n- [ ] Verify spawner.go flows"}]}`,
			"not valid json",
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Add field"})
	if err != nil {
		t.Fatalf("unexpected error (should fail-open): %v", err)
	}

	// Task should still exist with original description (fail-open)
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	if strings.Contains(result.Tasks[0].Description, "Expanded File Actions") {
		t.Error("failed expansion should not add expanded actions")
	}
}

// --- B-207: extractFileContext tests ---

func TestExtractFileContext(t *testing.T) {
	text := "add prioritylabel to models.go and verify spawner.go flows the field through the system"

	ctx := extractFileContext(text, "models.go")
	if ctx == "" {
		t.Error("expected non-empty context for models.go")
	}
	if !strings.Contains(ctx, "models.go") {
		t.Error("context should contain filename")
	}

	ctx2 := extractFileContext(text, "nonexistent.go")
	if ctx2 != "" {
		t.Errorf("expected empty context for nonexistent file, got %q", ctx2)
	}
}

func TestHasConcreteAction(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"add PriorityLabel field to the struct", true},
		{"create a new function for validation", true},
		{"implement the handler for errors", true},
		{"verify the field flows through", false},
		{"ensure the system works correctly", false},
		{"confirm nothing is dropped", false},
		{"update the schema to include new column", true},
		{"pass the parameter to the next function", true},
	}
	for _, tt := range tests {
		got := hasConcreteAction(tt.text)
		if got != tt.want {
			t.Errorf("hasConcreteAction(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// --- B-236: computeEffectiveMaxTasks tests ---

func TestComputeEffectiveMaxTasks(t *testing.T) {
	tests := []struct {
		name          string
		maxTasks      int
		maxFiles      int
		goalFileCount int
		want          int
	}{
		{"no goal files", 8, 15, 0, 8},
		{"unlimited file cap", 8, 0, 50, 8},
		{"small goal within cap", 8, 15, 10, 8},
		{"exact fit", 8, 15, 120, 8},
		{"needs more tasks", 4, 15, 50, 4},   // ceil(50/15) = 4, same as max
		{"dynamic floor exceeds max", 4, 15, 80, 6}, // ceil(80/15) = 6 > 4
		{"large goal forces many tasks", 3, 15, 100, 7}, // ceil(100/15) = 7
		{"one file per task", 2, 1, 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeEffectiveMaxTasks(tt.maxTasks, tt.maxFiles, tt.goalFileCount)
			if got != tt.want {
				t.Errorf("computeEffectiveMaxTasks(%d, %d, %d) = %d, want %d",
					tt.maxTasks, tt.maxFiles, tt.goalFileCount, got, tt.want)
			}
		})
	}
}

// --- B-236: enforceFileCap tests ---

func TestEnforceFileCap_NoSplit(t *testing.T) {
	tasks := []DecomposedTask{
		{Title: "Small task", Files: []string{"a.go", "b.go", "c.go"}},
	}
	result := enforceFileCap(tasks, 15)
	if len(result) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result))
	}
	if result[0].Title != "Small task" {
		t.Errorf("title = %q, want %q", result[0].Title, "Small task")
	}
}

func TestEnforceFileCap_SplitsOversizedTask(t *testing.T) {
	// 20 files across 2 directories, cap at 10
	files := make([]string, 20)
	for i := 0; i < 10; i++ {
		files[i] = "internal/db/" + string(rune('a'+i)) + ".go"
	}
	for i := 10; i < 20; i++ {
		files[i] = "internal/orchestrator/" + string(rune('a'+i-10)) + ".go"
	}

	tasks := []DecomposedTask{
		{
			Title:    "Big task",
			Role:     "implementer",
			Priority: 5,
			Files:    files,
		},
	}
	result := enforceFileCap(tasks, 10)

	if len(result) != 2 {
		t.Fatalf("expected 2 tasks after split, got %d", len(result))
	}

	// Verify all files are present
	allFiles := make(map[string]bool)
	for _, task := range result {
		for _, f := range task.Files {
			allFiles[f] = true
		}
	}
	if len(allFiles) != 20 {
		t.Errorf("expected 20 unique files across splits, got %d", len(allFiles))
	}

	// Verify each split is within cap
	for _, task := range result {
		if len(task.Files) > 10 {
			t.Errorf("task %q has %d files, exceeds cap of 10", task.Title, len(task.Files))
		}
	}

	// Verify part numbering
	if !strings.Contains(result[0].Title, "part 1/2") {
		t.Errorf("first split title = %q, want 'part 1/2' suffix", result[0].Title)
	}
	if !strings.Contains(result[1].Title, "part 2/2") {
		t.Errorf("second split title = %q, want 'part 2/2' suffix", result[1].Title)
	}
}

func TestEnforceFileCap_InheritsMetadata(t *testing.T) {
	files := make([]string, 20)
	for i := range files {
		files[i] = "pkg/" + string(rune('a'+i)) + ".go"
	}

	tasks := []DecomposedTask{
		{
			Title:         "Original",
			Role:          "architect",
			Priority:      3,
			PriorityLabel: "medium",
			DependsOn:     FlexStringArr{"t-001"},
			Files:         files,
		},
	}
	result := enforceFileCap(tasks, 10)

	for _, task := range result {
		if task.Role != "architect" {
			t.Errorf("role = %q, want %q", task.Role, "architect")
		}
		if task.Priority != 3 {
			t.Errorf("priority = %d, want 3", task.Priority)
		}
		if task.PriorityLabel != "medium" {
			t.Errorf("priorityLabel = %q, want %q", task.PriorityLabel, "medium")
		}
		if len(task.DependsOn) != 1 || task.DependsOn[0] != "t-001" {
			t.Errorf("dependsOn = %v, want [t-001]", task.DependsOn)
		}
	}
}

func TestEnforceFileCap_MixedTasks(t *testing.T) {
	// One small task (no split) + one large task (splits into 2)
	tasks := []DecomposedTask{
		{Title: "Small", Files: []string{"a.go", "b.go"}},
		{Title: "Big", Files: []string{
			"d1/a.go", "d1/b.go", "d1/c.go", "d1/d.go", "d1/e.go",
			"d2/a.go", "d2/b.go", "d2/c.go", "d2/d.go", "d2/e.go",
		}},
	}
	result := enforceFileCap(tasks, 5)

	if len(result) != 3 {
		t.Fatalf("expected 3 tasks (1 small + 2 from split), got %d", len(result))
	}
	if result[0].Title != "Small" {
		t.Errorf("first task should be unchanged, got %q", result[0].Title)
	}
}

// --- B-236: splitTaskByFileCap tests ---

func TestSplitTaskByFileCap_DirectoryGrouping(t *testing.T) {
	task := DecomposedTask{
		Title:    "Multi-dir task",
		Role:     "implementer",
		Priority: 5,
		Files: []string{
			"internal/db/models.go",
			"internal/db/queries.go",
			"internal/db/mutations.go",
			"internal/orchestrator/go.go",
			"internal/orchestrator/merge.go",
			"internal/agent/spawner.go",
		},
	}

	splits := splitTaskByFileCap(task, 3)

	// Should produce at least 2 splits
	if len(splits) < 2 {
		t.Fatalf("expected at least 2 splits, got %d", len(splits))
	}

	// Verify files in each split are from the same directory when possible
	for _, s := range splits {
		if len(s.Files) > 3 {
			t.Errorf("split %q has %d files, exceeds cap of 3", s.Title, len(s.Files))
		}
	}

	// Verify total file count preserved
	total := 0
	for _, s := range splits {
		total += len(s.Files)
	}
	if total != 6 {
		t.Errorf("total files across splits = %d, want 6", total)
	}
}

func TestSplitTaskByFileCap_SingleDirExceedsCap(t *testing.T) {
	// 8 files in one directory, cap at 3
	task := DecomposedTask{
		Title: "All same dir",
		Files: []string{
			"pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go",
			"pkg/e.go", "pkg/f.go", "pkg/g.go", "pkg/h.go",
		},
	}

	splits := splitTaskByFileCap(task, 3)

	// ceil(8/3) = 3 splits
	if len(splits) != 3 {
		t.Fatalf("expected 3 splits, got %d", len(splits))
	}

	// Verify no split exceeds cap
	for _, s := range splits {
		if len(s.Files) > 3 {
			t.Errorf("split has %d files, exceeds cap", len(s.Files))
		}
	}
}

func TestSplitTaskByFileCap_UnderCap(t *testing.T) {
	task := DecomposedTask{
		Title: "Small",
		Files: []string{"a.go", "b.go"},
	}

	splits := splitTaskByFileCap(task, 15)

	if len(splits) != 1 {
		t.Fatalf("expected 1 task (no split needed), got %d", len(splits))
	}
	if splits[0].Title != "Small" {
		t.Errorf("title should be unchanged")
	}
}

// --- B-236: filterACForFiles tests ---

func TestFilterACForFiles_MatchesFileNames(t *testing.T) {
	ac := "- [ ] models.go has PriorityLabel field\n- [ ] schema.sql has priority_label column\n- [ ] queries.go has GetByPriority function"
	files := []string{"internal/db/models.go", "internal/db/schema.sql"}

	result := filterACForFiles(ac, files)

	if !strings.Contains(result, "models.go") {
		t.Error("should keep AC referencing models.go")
	}
	if !strings.Contains(result, "schema.sql") {
		t.Error("should keep AC referencing schema.sql")
	}
	if strings.Contains(result, "queries.go") {
		t.Error("should not keep AC referencing queries.go")
	}
}

func TestFilterACForFiles_NoMatchReturnsFullAC(t *testing.T) {
	ac := "- [ ] All tests pass\n- [ ] No regressions"
	files := []string{"internal/db/models.go"}

	result := filterACForFiles(ac, files)

	// No file names matched, so return full AC
	if result != ac {
		t.Errorf("expected full AC when no lines match, got %q", result)
	}
}

func TestFilterACForFiles_EmptyAC(t *testing.T) {
	result := filterACForFiles("", []string{"a.go"})
	if result != "" {
		t.Errorf("expected empty string for empty AC, got %q", result)
	}
}

// --- B-236: prompt scaling test ---

func TestDecompose_LargeGoalPromptScaling(t *testing.T) {
	d := setupTestDB(t)
	output := `{"tasks":[{"title":"Test","description":"d","role":"implementer","priority":3,"files":[],"acceptance_criteria":"- [ ] Done"}]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// Build a goal with >20 file references
	var goalParts []string
	for i := 0; i < 25; i++ {
		goalParts = append(goalParts, fmt.Sprintf("file%d.go", i))
	}
	bigGoal := "Update " + strings.Join(goalParts, ", ")

	c.Decompose(ctx, DecomposeOpts{Goal: bigGoal, MaxTasks: 8})

	if len(runner.Calls) < 1 {
		t.Fatalf("expected at least 1 runner call, got %d", len(runner.Calls))
	}
	prompt := runner.Calls[0].Prompt
	if !containsSubstring(prompt, "large goal") {
		t.Error("prompt should contain 'large goal' for >20 file goals")
	}
	if !containsSubstring(prompt, "at most 25 files") {
		t.Error("prompt should mention the 25-file cap")
	}
}

func TestDecompose_DefaultMaxTasksIs8(t *testing.T) {
	d := setupTestDB(t)
	output := `{"tasks":[{"title":"Test","description":"d","role":"implementer","priority":3,"files":[],"acceptance_criteria":"- [ ] Done"}]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// MaxTasks = 0 (use default)
	c.Decompose(ctx, DecomposeOpts{Goal: "test goal"})

	if len(runner.Calls) < 1 {
		t.Fatal("expected at least 1 runner call")
	}
	prompt := runner.Calls[0].Prompt
	// Default should be 8 tasks
	if !containsSubstring(prompt, "at most 8 tasks") {
		t.Error("prompt should contain 'at most 8 tasks' as the default")
	}
}

// --- B-240c: filterReadOnlyFiles tests ---

func TestFilterReadOnlyFiles(t *testing.T) {
	tasks := []DecomposedTask{
		{
			Title: "Task A",
			Files: []string{"internal/db/models.go", "tests/test_models.py", "internal/db/queries.go"},
		},
		{
			Title: "Task B",
			Files: []string{"internal/orchestrator/go.go", "tests/test_go.py"},
		},
	}

	// Filter out test files
	result := filterReadOnlyFiles(tasks, []string{"tests/test_models.py", "tests/test_go.py"})

	// Task A should only have models.go and queries.go
	if len(result[0].Files) != 2 {
		t.Fatalf("task A: expected 2 files after filter, got %d: %v", len(result[0].Files), result[0].Files)
	}
	for _, f := range result[0].Files {
		if f == "tests/test_models.py" {
			t.Error("tests/test_models.py should be filtered out")
		}
	}

	// Task B should only have go.go
	if len(result[1].Files) != 1 {
		t.Fatalf("task B: expected 1 file after filter, got %d: %v", len(result[1].Files), result[1].Files)
	}
	if result[1].Files[0] != "internal/orchestrator/go.go" {
		t.Errorf("expected go.go, got %q", result[1].Files[0])
	}
}

func TestFilterReadOnlyFiles_BasenameMatch(t *testing.T) {
	tasks := []DecomposedTask{
		{
			Title: "Task A",
			Files: []string{"internal/db/models.go", "src/tests/test_main.py"},
		},
	}

	// Filter by basename only (no path prefix)
	result := filterReadOnlyFiles(tasks, []string{"test_main.py"})

	if len(result[0].Files) != 1 {
		t.Fatalf("expected 1 file after basename filter, got %d: %v", len(result[0].Files), result[0].Files)
	}
	if result[0].Files[0] != "internal/db/models.go" {
		t.Errorf("expected models.go, got %q", result[0].Files[0])
	}
}

func TestFilterReadOnlyFiles_Empty(t *testing.T) {
	tasks := []DecomposedTask{
		{Title: "Task A", Files: []string{"a.go", "b.go"}},
	}

	// No read-only files — should be a no-op
	result := filterReadOnlyFiles(tasks, nil)

	if len(result[0].Files) != 2 {
		t.Errorf("expected 2 files unchanged, got %d", len(result[0].Files))
	}
}

// --- L-011: filterPathList tests ---

func TestFilterPathList(t *testing.T) {
	t.Run("full path match", func(t *testing.T) {
		paths := []string{"internal/db/models.go", "tests/test_models.py", "internal/db/queries.go"}
		result := filterPathList(paths, []string{"tests/test_models.py"})
		if len(result) != 2 {
			t.Fatalf("expected 2 paths, got %d: %v", len(result), result)
		}
		for _, p := range result {
			if p == "tests/test_models.py" {
				t.Error("tests/test_models.py should be excluded")
			}
		}
	})

	t.Run("basename match", func(t *testing.T) {
		paths := []string{"src/tests/test_main.py", "internal/db/models.go"}
		result := filterPathList(paths, []string{"test_main.py"})
		if len(result) != 1 {
			t.Fatalf("expected 1 path, got %d: %v", len(result), result)
		}
		if result[0] != "internal/db/models.go" {
			t.Errorf("expected models.go, got %q", result[0])
		}
	})

	t.Run("empty exclude", func(t *testing.T) {
		paths := []string{"a.go", "b.go"}
		result := filterPathList(paths, nil)
		if len(result) != 2 {
			t.Errorf("expected 2 paths unchanged, got %d", len(result))
		}
	})
}

func TestDecomposeCoverageValidation_ReadOnlyExcluded(t *testing.T) {
	d := setupTestDB(t)
	// LLM returns 2 tasks covering models.go and queries.go; test_models.py is read-only
	output := `[
		{"title":"Add field to models","description":"Add status field to models.go","role":"implementer","priority":5,"files":["internal/db/models.go"]},
		{"title":"Update queries","description":"Update queries.go for new field","role":"implementer","priority":4,"files":["internal/db/queries.go"]}
	]`
	runner := &MockRunner{Outputs: []string{output}}

	// Create repo structure with all 3 files
	repoRoot := t.TempDir()
	for _, p := range []string{"internal/db/models.go", "internal/db/queries.go", "tests/test_models.py"} {
		full := filepath.Join(repoRoot, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("package placeholder"), 0o644)
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:          "Update internal/db/models.go internal/db/queries.go tests/test_models.py",
		ReadOnlyFiles: []string{"tests/test_models.py"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Coverage should be 100% because read-only file is excluded from goal files
	if result.FilesCoveragePercent != 100.0 {
		t.Errorf("expected 100%% coverage (read-only excluded), got %.0f%%", result.FilesCoveragePercent)
	}
	if len(result.FilesCoverageMissing) != 0 {
		t.Errorf("expected no missing files, got %v", result.FilesCoverageMissing)
	}
}

// --- B-240d: DisableActionExpansion test ---

func TestDecompose_DisableActionExpansion(t *testing.T) {
	d := setupTestDB(t)
	// Output has vague instructions — would normally trigger expansion
	output := `{"tasks":[{"title":"Add field","description":"Add field to models.go. Verify spawner.go flows it.","role":"implementer","priority":5,"files":["models.go","spawner.go"],"acceptance_criteria":"- [ ] models.go has field\n- [ ] Verify spawner.go flows"}]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:                   "Add field",
		DisableActionExpansion: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only 1 runner call (decompose), no expansion call
	if len(runner.Calls) != 1 {
		t.Errorf("expected 1 runner call (no expansion), got %d", len(runner.Calls))
	}

	// Task should not have expanded actions
	if strings.Contains(result.Tasks[0].Description, "Expanded File Actions") {
		t.Error("disabled expansion should not add expanded actions")
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- G100: readGoalFileContents tests ---

func TestReadGoalFileContents_Basic(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two test files
	os.WriteFile(filepath.Join(tmpDir, "main.py"), []byte("import dask\n\ndef process():\n    return 42\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "util.go"), []byte("package util\n\nfunc Helper() int {\n\treturn 1\n}\n"), 0644)

	result := readGoalFileContents([]string{"main.py", "util.go"}, tmpDir, 24000)

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "FILE CONTENTS") {
		t.Error("missing FILE CONTENTS header")
	}
	if !strings.Contains(result, "### main.py") {
		t.Error("missing main.py header")
	}
	if !strings.Contains(result, "### util.go") {
		t.Error("missing util.go header")
	}
	if !strings.Contains(result, "import dask") {
		t.Error("missing main.py content")
	}
	if !strings.Contains(result, "func Helper()") {
		t.Error("missing util.go content")
	}
	if !strings.Contains(result, "```py") {
		t.Error("missing python code fence")
	}
	if !strings.Contains(result, "```go") {
		t.Error("missing go code fence")
	}
}

func TestReadGoalFileContents_Truncation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with 300 lines
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, fmt.Sprintf("line %d: content here", i))
	}
	os.WriteFile(filepath.Join(tmpDir, "big.py"), []byte(strings.Join(lines, "\n")), 0644)

	result := readGoalFileContents([]string{"big.py"}, tmpDir, 24000)

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should contain the omission marker
	if !strings.Contains(result, "lines omitted") {
		t.Error("expected truncation marker for >200 line file")
	}
	// Should contain first line
	if !strings.Contains(result, "line 0:") {
		t.Error("expected first line to be included")
	}
	// Should contain last line
	if !strings.Contains(result, "line 299:") {
		t.Error("expected last line to be included")
	}
	// Should NOT contain a middle line (line 100)
	if strings.Contains(result, "line 100:") {
		t.Error("expected middle lines to be omitted")
	}
}

func TestReadGoalFileContents_BudgetLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file that's ~1000 chars
	content := strings.Repeat("x", 800) + "\n"
	os.WriteFile(filepath.Join(tmpDir, "a.py"), []byte(content), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.py"), []byte(content), 0644)
	os.WriteFile(filepath.Join(tmpDir, "c.py"), []byte(content), 0644)

	// Set a tight budget that can't fit all 3 files
	result := readGoalFileContents([]string{"a.py", "b.py", "c.py"}, tmpDir, 1500)

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should have included at least one file but not necessarily all three
	fileCount := strings.Count(result, "### ")
	if fileCount < 1 {
		t.Error("expected at least 1 file in budget-limited output")
	}
	// Result length should be near the budget (with header overhead)
	if len(result) > 2000 { // some overhead for headers
		t.Errorf("result length %d exceeds expected budget ceiling", len(result))
	}
}

func TestReadGoalFileContents_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create one real file, reference one that doesn't exist
	os.WriteFile(filepath.Join(tmpDir, "exists.py"), []byte("print('hello')\n"), 0644)

	result := readGoalFileContents([]string{"exists.py", "nonexistent.py"}, tmpDir, 24000)

	if result == "" {
		t.Fatal("expected non-empty result (should include the file that exists)")
	}
	if !strings.Contains(result, "### exists.py") {
		t.Error("existing file should be included")
	}
	if strings.Contains(result, "nonexistent.py") {
		t.Error("missing file should be silently skipped")
	}
}

func TestReadGoalFileContents_Empty(t *testing.T) {
	result := readGoalFileContents(nil, "/tmp", 24000)
	if result != "" {
		t.Errorf("empty file list should return empty string, got %q", result)
	}

	result = readGoalFileContents([]string{}, "/tmp", 24000)
	if result != "" {
		t.Errorf("empty file list should return empty string, got %q", result)
	}
}

func TestReadGoalFileContents_SkipsBinaryAndLockfiles(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "image.png"), []byte("\x89PNG\r\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "go.sum"), []byte("module hash"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "real.go"), []byte("package main\n"), 0644)

	result := readGoalFileContents([]string{"image.png", "go.sum", "real.go"}, tmpDir, 24000)

	if !strings.Contains(result, "### real.go") {
		t.Error("real.go should be included")
	}
	if strings.Contains(result, "image.png") {
		t.Error("binary file should be skipped")
	}
	if strings.Contains(result, "go.sum") {
		t.Error("lockfile should be skipped")
	}
}

func TestDecomposePrompt_ContainsFileContents(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the file that the goal references
	os.WriteFile(filepath.Join(tmpDir, "handler.py"), []byte("def handle_request(req):\n    return 200\n"), 0644)

	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"tasks":[{"title":"Update handler","description":"Update the request handler","role":"implementer","priority":5,"files":["handler.py"],"acceptance_criteria":"- [ ] handler returns JSON"}]}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: tmpDir, Runner: runner})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "Modify handler.py to return JSON", MaxTasks: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that the prompt sent to the runner contained file contents
	if len(runner.Calls) == 0 {
		t.Fatal("expected at least one runner call")
	}
	prompt := runner.Calls[0].Prompt
	if !strings.Contains(prompt, "FILE CONTENTS") {
		t.Error("decompose prompt should contain FILE CONTENTS section")
	}
	if !strings.Contains(prompt, "def handle_request") {
		t.Error("decompose prompt should contain actual file content")
	}
}

// --- B-281: Hierarchical decomposition tests ---

func TestDecomposeHierarchical_ParsesClusters(t *testing.T) {
	d := setupTestDB(t)
	output := `{"tasks": [
		{"title":"Add auth middleware","description":"Auth layer","role":"implementer","priority":5,
		 "files":["auth.go"],"acceptance_criteria":"- [ ] middleware added","feature_cluster":"auth"},
		{"title":"Add login endpoint","description":"Login route","role":"implementer","priority":4,
		 "files":["login.go"],"acceptance_criteria":"- [ ] endpoint works","feature_cluster":"auth"},
		{"title":"Add user model","description":"DB model","role":"implementer","priority":5,
		 "files":["models.go"],"acceptance_criteria":"- [ ] model created","feature_cluster":"data-layer"},
		{"title":"Add user API","description":"REST API","role":"implementer","priority":3,
		 "files":["api.go"],"acceptance_criteria":"- [ ] API returns users","feature_cluster":"api"}
	]}`
	runner := &MockRunner{Outputs: []string{output}}
	ctx := context.Background()

	// Create conductor record for FK constraint
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-hier-test", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-hier-test", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	c.ConductorID = "c-hier-test"

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:         "Add user authentication",
		Hierarchical: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(result.Tasks))
	}

	// Verify feature_cluster was parsed
	clusters := make(map[string]int)
	for _, task := range result.Tasks {
		clusters[task.FeatureCluster]++
	}
	if clusters["auth"] != 2 {
		t.Errorf("expected 2 tasks in 'auth' cluster, got %d", clusters["auth"])
	}
	if clusters["data-layer"] != 1 {
		t.Errorf("expected 1 task in 'data-layer' cluster, got %d", clusters["data-layer"])
	}
	if clusters["api"] != 1 {
		t.Errorf("expected 1 task in 'api' cluster, got %d", clusters["api"])
	}

	// Verify feature_cluster persisted to DB
	task, err := d.GetTaskByID(ctx, result.Tasks[0].ID)
	if err != nil {
		t.Fatalf("error getting task: %v", err)
	}
	if !task.FeatureCluster.Valid || task.FeatureCluster.String != "auth" {
		t.Errorf("DB feature_cluster = %v, want 'auth'", task.FeatureCluster)
	}

	// Verify hierarchical schema was used (prompt should contain clustering rules)
	if len(runner.Calls) == 0 {
		t.Fatal("expected at least one runner call")
	}
	prompt := runner.Calls[0].Prompt
	if !strings.Contains(prompt, "FEATURE CLUSTERING RULES") {
		t.Error("hierarchical decompose prompt should contain clustering rules")
	}
	schema := runner.Calls[0].JSONSchema
	if !strings.Contains(schema, "feature_cluster") {
		t.Error("hierarchical schema should contain feature_cluster field")
	}

	// Verify cluster queries work
	dbClusters, err := d.ListDistinctClusters(ctx, c.ConductorID)
	if err != nil {
		t.Fatalf("ListDistinctClusters error: %v", err)
	}
	if len(dbClusters) != 3 {
		t.Errorf("expected 3 distinct clusters in DB, got %d: %v", len(dbClusters), dbClusters)
	}

	authTasks, err := d.ListTasksByCluster(ctx, c.ConductorID, "auth")
	if err != nil {
		t.Fatalf("ListTasksByCluster error: %v", err)
	}
	if len(authTasks) != 2 {
		t.Errorf("expected 2 auth cluster tasks, got %d", len(authTasks))
	}
}

func TestDecomposeHierarchical_FallbackToFlat(t *testing.T) {
	d := setupTestDB(t)
	// All tasks in one cluster — should fall back to flat
	output := `{"tasks": [
		{"title":"Task A","description":"d","role":"implementer","priority":5,
		 "files":["a.go"],"acceptance_criteria":"- [ ] done","feature_cluster":"everything"},
		{"title":"Task B","description":"d","role":"implementer","priority":4,
		 "files":["b.go"],"acceptance_criteria":"- [ ] done","feature_cluster":"everything"},
		{"title":"Task C","description":"d","role":"implementer","priority":3,
		 "files":["c.go"],"acceptance_criteria":"- [ ] done","feature_cluster":"everything"}
	]}`
	runner := &MockRunner{Outputs: []string{output}}
	ctx := context.Background()

	// Create conductor record for FK constraint
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-fallback-test", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-fallback-test", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	c.ConductorID = "c-fallback-test"

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:         "Add three things",
		Hierarchical: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All clusters should be cleared (fallen back to flat)
	for _, task := range result.Tasks {
		if task.FeatureCluster != "" {
			t.Errorf("expected empty feature_cluster after fallback, got %q for task %q",
				task.FeatureCluster, task.Title)
		}
	}

	// DB should have no clusters
	dbClusters, err := d.ListDistinctClusters(ctx, c.ConductorID)
	if err != nil {
		t.Fatalf("ListDistinctClusters error: %v", err)
	}
	if len(dbClusters) != 0 {
		t.Errorf("expected 0 clusters in DB after fallback, got %d: %v", len(dbClusters), dbClusters)
	}
}

func TestDecomposeHierarchical_NoClusters_FallbackToFlat(t *testing.T) {
	d := setupTestDB(t)
	// Tasks with no feature_cluster field at all — should fall back to flat
	output := `{"tasks": [
		{"title":"Task A","description":"d","role":"implementer","priority":5,
		 "files":["a.go"],"acceptance_criteria":"- [ ] done"},
		{"title":"Task B","description":"d","role":"implementer","priority":4,
		 "files":["b.go"],"acceptance_criteria":"- [ ] done"}
	]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{
		Goal:         "Two tasks",
		Hierarchical: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, task := range result.Tasks {
		if task.FeatureCluster != "" {
			t.Errorf("expected empty feature_cluster after fallback, got %q", task.FeatureCluster)
		}
	}
}

func TestDecomposeNonHierarchical_NoClusters(t *testing.T) {
	d := setupTestDB(t)
	// Non-hierarchical mode: feature_cluster should not be in schema
	output := `[{"title":"Simple task","description":"d","role":"implementer","priority":5,
	  "files":["main.go"],"acceptance_criteria":"- [ ] done"}]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "Simple goal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify standard schema used (no feature_cluster required)
	if len(runner.Calls) == 0 {
		t.Fatal("expected at least one runner call")
	}
	schema := runner.Calls[0].JSONSchema
	if strings.Contains(schema, "feature_cluster") {
		t.Error("non-hierarchical schema should NOT contain feature_cluster field")
	}
	prompt := runner.Calls[0].Prompt
	if strings.Contains(prompt, "FEATURE CLUSTERING RULES") {
		t.Error("non-hierarchical prompt should NOT contain clustering rules")
	}
}

func TestValidateFeatureClusters(t *testing.T) {
	// Create a minimal conductor for logging
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	tests := []struct {
		name           string
		tasks          []DecomposedTask
		expectFallback bool
		expectMisc     bool
	}{
		{
			name: "valid clusters",
			tasks: []DecomposedTask{
				{Title: "A", FeatureCluster: "auth"},
				{Title: "B", FeatureCluster: "auth"},
				{Title: "C", FeatureCluster: "api"},
			},
			expectFallback: false,
		},
		{
			name: "all same cluster",
			tasks: []DecomposedTask{
				{Title: "A", FeatureCluster: "everything"},
				{Title: "B", FeatureCluster: "everything"},
			},
			expectFallback: true,
		},
		{
			name: "no clusters",
			tasks: []DecomposedTask{
				{Title: "A"},
				{Title: "B"},
			},
			expectFallback: true,
		},
		{
			name: "partial clusters — misc assigned",
			tasks: []DecomposedTask{
				{Title: "A", FeatureCluster: "auth"},
				{Title: "B", FeatureCluster: "api"},
				{Title: "C"},
			},
			expectFallback: false,
			expectMisc:     true,
		},
		{
			name: "single task — no fallback needed",
			tasks: []DecomposedTask{
				{Title: "A", FeatureCluster: "solo"},
			},
			expectFallback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateFeatureClusters(tt.tasks, c)
			if tt.expectFallback {
				for _, task := range result {
					if task.FeatureCluster != "" {
						t.Errorf("expected empty cluster after fallback, got %q for %q",
							task.FeatureCluster, task.Title)
					}
				}
			} else if tt.expectMisc {
				hasMisc := false
				for _, task := range result {
					if task.FeatureCluster == "misc" {
						hasMisc = true
					}
				}
				if !hasMisc {
					t.Error("expected at least one task with 'misc' cluster")
				}
			}
		})
	}
}

func TestDistinctClusters(t *testing.T) {
	tasks := []DecomposedTask{
		{Title: "A", FeatureCluster: "auth"},
		{Title: "B", FeatureCluster: "api"},
		{Title: "C", FeatureCluster: "auth"},
		{Title: "D"}, // no cluster
		{Title: "E", FeatureCluster: "ui"},
	}
	clusters := distinctClusters(tasks)
	if len(clusters) != 3 {
		t.Fatalf("expected 3 clusters, got %d: %v", len(clusters), clusters)
	}
	// Should be sorted
	expected := []string{"api", "auth", "ui"}
	for i, c := range clusters {
		if c != expected[i] {
			t.Errorf("cluster[%d] = %q, want %q", i, c, expected[i])
		}
	}
}

// --- G137: File Path Correction Tests ---

func TestCorrectDecomposerFilePaths_ExactMatch(t *testing.T) {
	// Files already match goal — no corrections needed
	tasks := []DecomposedTask{
		{Title: "T1", Files: []string{"chapters/ch01.md", "chapters/ch02.md"}},
	}
	goalFiles := []string{"chapters/ch01.md", "chapters/ch02.md"}

	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 0 {
		t.Errorf("expected 0 corrections, got %d: %v", len(corrections), corrections)
	}
	if corrected[0].Files[0] != "chapters/ch01.md" {
		t.Errorf("file should be unchanged, got %s", corrected[0].Files[0])
	}
}

func TestCorrectDecomposerFilePaths_OrdinalRename(t *testing.T) {
	// LLM renamed ch01.md → chapter-01.md (same dir, same ext, same ordinal)
	tasks := []DecomposedTask{
		{Title: "Write Ch1", Files: []string{"chapters/chapter-01.md"}},
		{Title: "Write Ch2", Files: []string{"chapters/chapter-02.md"}},
	}
	goalFiles := []string{"chapters/ch01.md", "chapters/ch02.md"}

	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 2 {
		t.Fatalf("expected 2 corrections, got %d", len(corrections))
	}
	if corrected[0].Files[0] != "chapters/ch01.md" {
		t.Errorf("expected chapters/ch01.md, got %s", corrected[0].Files[0])
	}
	if corrected[1].Files[0] != "chapters/ch02.md" {
		t.Errorf("expected chapters/ch02.md, got %s", corrected[1].Files[0])
	}
	if corrections[0].Method != "ordinal" {
		t.Errorf("expected ordinal method, got %s", corrections[0].Method)
	}
}

func TestCorrectDecomposerFilePaths_LevenshteinRename(t *testing.T) {
	// LLM renamed helpers.go → helper_funcs.go (same dir, same ext, no ordinal)
	tasks := []DecomposedTask{
		{Title: "T1", Files: []string{"src/helper_funcs.go"}},
	}
	goalFiles := []string{"src/helpers.go"}

	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction, got %d", len(corrections))
	}
	if corrected[0].Files[0] != "src/helpers.go" {
		t.Errorf("expected src/helpers.go, got %s", corrected[0].Files[0])
	}
	if corrections[0].Method != "levenshtein" {
		t.Errorf("expected levenshtein method, got %s", corrections[0].Method)
	}
}

func TestCorrectDecomposerFilePaths_NoGoalFiles(t *testing.T) {
	// No goal files — pass through unchanged
	tasks := []DecomposedTask{
		{Title: "T1", Files: []string{"src/main.go"}},
	}

	corrected, corrections := correctDecomposerFilePaths(tasks, nil)
	if len(corrections) != 0 {
		t.Errorf("expected 0 corrections, got %d", len(corrections))
	}
	if corrected[0].Files[0] != "src/main.go" {
		t.Errorf("file should be unchanged")
	}
}

func TestCorrectDecomposerFilePaths_NewFileKept(t *testing.T) {
	// LLM added a new file not in goal — keep it as-is
	tasks := []DecomposedTask{
		{Title: "T1", Files: []string{"chapters/ch01.md", "chapters/appendix.md"}},
	}
	goalFiles := []string{"chapters/ch01.md"}

	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 0 {
		t.Errorf("expected 0 corrections (appendix.md is new), got %d", len(corrections))
	}
	if len(corrected[0].Files) != 2 {
		t.Errorf("expected 2 files (original kept), got %d", len(corrected[0].Files))
	}
}

func TestCorrectDecomposerFilePaths_MultiTaskSameOrdinal(t *testing.T) {
	// Two tasks rename files — each should map to a different goal file
	tasks := []DecomposedTask{
		{Title: "Batch 1", Files: []string{"chapters/chapter-01.md", "chapters/chapter-02.md"}},
		{Title: "Batch 2", Files: []string{"chapters/chapter-03.md", "chapters/chapter-04.md"}},
	}
	goalFiles := []string{"chapters/ch01.md", "chapters/ch02.md", "chapters/ch03.md", "chapters/ch04.md"}

	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 4 {
		t.Fatalf("expected 4 corrections, got %d", len(corrections))
	}
	// Verify no duplicate mappings
	seen := make(map[string]bool)
	for _, task := range corrected {
		for _, f := range task.Files {
			if seen[f] {
				t.Errorf("duplicate corrected file: %s", f)
			}
			seen[f] = true
		}
	}
}

func TestCorrectDecomposerFilePaths_MixedCorrectAndRenamed(t *testing.T) {
	// Some files are correct, some renamed
	tasks := []DecomposedTask{
		{Title: "T1", Files: []string{"src/main.go", "chapters/chapter-05.md", "config.yaml"}},
	}
	goalFiles := []string{"src/main.go", "chapters/ch05.md", "config.yaml"}

	_, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction (only ch05), got %d", len(corrections))
	}
	if corrections[0].Original != "chapters/chapter-05.md" {
		t.Errorf("expected chapter-05.md to be corrected, got %s", corrections[0].Original)
	}
	if corrections[0].Corrected != "chapters/ch05.md" {
		t.Errorf("expected correction to ch05.md, got %s", corrections[0].Corrected)
	}
}

func TestCorrectDecomposerFilePaths_SkipsAdditionalFiles(t *testing.T) {
	// G138: additional_files should NOT be corrected by the path fixer.
	// Edit reports (e.g., ch01-dev-edit.md) share ordinals with chapter files
	// (ch01.md) and were being incorrectly mapped before the reorder fix.
	tasks := []DecomposedTask{
		{
			Title:           "Dev edit chapters 1-3",
			Files:           []string{"chapters/ch01.md", "chapters/ch02.md", "chapters/ch03.md"},
			AdditionalFiles: []string{"edit-reports/ch01-dev-edit.md", "edit-reports/ch02-dev-edit.md", "edit-reports/ch03-dev-edit.md"},
		},
	}
	goalFiles := []string{"chapters/ch01.md", "chapters/ch02.md", "chapters/ch03.md"}

	// Run correction on tasks BEFORE merging additional_files (the G138 fix).
	// The correction should find 0 renames because Files already match goal files exactly.
	corrected, corrections := correctDecomposerFilePaths(tasks, goalFiles)
	if len(corrections) != 0 {
		t.Errorf("expected 0 corrections (Files already match goal), got %d", len(corrections))
		for _, c := range corrections {
			t.Logf("  unwanted correction: %s → %s (method: %s)", c.Original, c.Corrected, c.Method)
		}
	}

	// Verify Files unchanged
	if len(corrected[0].Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(corrected[0].Files))
	}
	for i, want := range goalFiles {
		if corrected[0].Files[i] != want {
			t.Errorf("Files[%d] = %q, want %q", i, corrected[0].Files[i], want)
		}
	}

	// Verify AdditionalFiles are untouched (still in the struct, not corrected)
	if len(corrected[0].AdditionalFiles) != 3 {
		t.Fatalf("expected 3 additional_files, got %d", len(corrected[0].AdditionalFiles))
	}
	for i, want := range []string{"edit-reports/ch01-dev-edit.md", "edit-reports/ch02-dev-edit.md", "edit-reports/ch03-dev-edit.md"} {
		if corrected[0].AdditionalFiles[i] != want {
			t.Errorf("AdditionalFiles[%d] = %q, want %q", i, corrected[0].AdditionalFiles[i], want)
		}
	}
}

func TestCorrectDecomposerFilePaths_AdditionalFilesWouldBeWronglyMapped(t *testing.T) {
	// G138 regression: if additional_files WERE in Files at correction time,
	// the ordinal matcher would map ch01-dev-edit.md → ch01.md (same dir, same ext,
	// same ordinal "01"). This test verifies the old (broken) behavior to document it.
	tasks := []DecomposedTask{
		{
			Title: "Dev edit chapters 1-3",
			Files: []string{
				"chapters/ch01.md", "chapters/ch02.md", "chapters/ch03.md",
				// Simulating merged additional_files (the pre-G138 bug)
				"edit-reports/ch01-dev-edit.md", "edit-reports/ch02-dev-edit.md", "edit-reports/ch03-dev-edit.md",
			},
		},
	}
	goalFiles := []string{"chapters/ch01.md", "chapters/ch02.md", "chapters/ch03.md"}

	// With edit-reports mixed in, the corrector should NOT map them (they're in a
	// different directory), but verify it doesn't corrupt anything.
	corrected, _ := correctDecomposerFilePaths(tasks, goalFiles)

	// The edit-reports files are in a different directory than goalFiles, so
	// ordinal matching (which requires same dir) shouldn't fire. They should
	// be kept as-is.
	if len(corrected[0].Files) != 6 {
		t.Fatalf("expected 6 files unchanged, got %d", len(corrected[0].Files))
	}
}

func TestExtractOrdinal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ch01.md", "01"},
		{"chapter-15.md", "15"},
		{"helpers.go", ""},
		{"lesson-3.mdx", "3"},
		{"dev-edit-ch07.md", "07"},
		{"file.txt", ""},
		{"v2-config.yaml", "2"},
	}
	for _, tc := range tests {
		got := extractOrdinal(tc.input)
		if got != tc.want {
			t.Errorf("extractOrdinal(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"ch01.md", "chapter-01.md", 6},
		{"helpers.go", "helper_funcs.go", 5},
		{"kitten", "sitting", 3},
	}
	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// --- G137 Enhancement: Enum-constrained schema tests ---

func TestBuildEnumConstrainedSchema_Basic(t *testing.T) {
	goalFiles := []string{"src/main.go", "internal/db/schema.sql", "README.md"}
	schema := buildEnumConstrainedSchema(goalFiles, false)

	// Must be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	// Check enum values are present in files.items
	tasks := parsed["properties"].(map[string]interface{})["tasks"].(map[string]interface{})
	items := tasks["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})
	files := props["files"].(map[string]interface{})
	filesItems := files["items"].(map[string]interface{})
	enumVals := filesItems["enum"].([]interface{})
	if len(enumVals) != 3 {
		t.Fatalf("expected 3 enum values, got %d", len(enumVals))
	}
	if enumVals[0].(string) != "src/main.go" {
		t.Errorf("expected first enum value to be src/main.go, got %s", enumVals[0])
	}

	// Check additional_files field exists
	addFiles := props["additional_files"].(map[string]interface{})
	if addFiles["type"] != "array" {
		t.Errorf("additional_files should be array type, got %v", addFiles["type"])
	}

	// Check additional_files is NOT in required
	required := items["required"].([]interface{})
	for _, r := range required {
		if r.(string) == "additional_files" {
			t.Error("additional_files should NOT be in required fields")
		}
	}

	// Check feature_cluster is NOT present (non-hierarchical)
	if _, ok := props["feature_cluster"]; ok {
		t.Error("feature_cluster should not be present in non-hierarchical schema")
	}
}

func TestBuildEnumConstrainedSchema_Hierarchical(t *testing.T) {
	goalFiles := []string{"src/main.go"}
	schema := buildEnumConstrainedSchema(goalFiles, true)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	tasks := parsed["properties"].(map[string]interface{})["tasks"].(map[string]interface{})
	items := tasks["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})

	// feature_cluster must be present
	if _, ok := props["feature_cluster"]; !ok {
		t.Error("feature_cluster should be present in hierarchical schema")
	}

	// feature_cluster must be in required
	required := items["required"].([]interface{})
	found := false
	for _, r := range required {
		if r.(string) == "feature_cluster" {
			found = true
		}
	}
	if !found {
		t.Error("feature_cluster should be in required for hierarchical mode")
	}
}

func TestBuildEnumConstrainedSchema_ValidJSON(t *testing.T) {
	// Test with various goal file counts including large lists
	for _, count := range []int{1, 5, 50, 200} {
		files := make([]string, count)
		for i := range files {
			files[i] = fmt.Sprintf("chapters/ch%02d.md", i+1)
		}
		schema := buildEnumConstrainedSchema(files, false)
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
			t.Errorf("count=%d: schema is not valid JSON: %v", count, err)
		}
	}
}

func TestDecompose_AdditionalFilesUnmarshal(t *testing.T) {
	// Verify additional_files JSON field maps to AdditionalFiles struct field
	input := `{"tasks":[{
		"title":"Add feature",
		"description":"Do it",
		"role":"implementer",
		"priority":5,
		"files":["src/main.go"],
		"additional_files":["src/new_file.go","src/helper.go"],
		"acceptance_criteria":"- [ ] Done"
	}]}`

	tasks, err := parseDecomposeOutput(input, 10)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if len(tasks[0].AdditionalFiles) != 2 {
		t.Fatalf("expected 2 additional files, got %d", len(tasks[0].AdditionalFiles))
	}
	if tasks[0].AdditionalFiles[0] != "src/new_file.go" {
		t.Errorf("expected src/new_file.go, got %s", tasks[0].AdditionalFiles[0])
	}
}

func TestDecompose_MergeAdditionalFiles(t *testing.T) {
	// Test that additional_files are merged into files list during Decompose
	d := setupTestDB(t)
	output := `{"tasks":[{
		"title":"Build feature",
		"description":"Implement the thing",
		"role":"implementer",
		"priority":5,
		"files":["src/main.go"],
		"additional_files":["src/brand_new.go"],
		"acceptance_criteria":"- [ ] Feature works"
	}]}`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Decompose(ctx, DecomposeOpts{Goal: "Build feature"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	// Files should include both original and additional
	files := result.Tasks[0].Files
	if len(files) != 2 {
		t.Fatalf("expected 2 files after merge, got %d: %v", len(files), files)
	}
	foundNew := false
	for _, f := range files {
		if f == "src/brand_new.go" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("expected src/brand_new.go in merged files, got %v", files)
	}
}

func TestDecompose_EnumSchemaUsedWithGoalFiles(t *testing.T) {
	// When goal mentions files, the enum-constrained schema should be used
	d := setupTestDB(t)
	output := `{"tasks":[{"title":"Fix it","description":"Fix the thing","role":"implementer","priority":5,"files":["main.go"],"acceptance_criteria":"- [ ] Fixed"}]}`
	runner := &MockRunner{Outputs: []string{output}}
	// Create a temp dir with a file so resolveGoalFilePaths finds it
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: tmpDir, Runner: runner})
	ctx := context.Background()

	// Goal that mentions a file path
	_, err := c.Decompose(ctx, DecomposeOpts{Goal: "Fix main.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The runner should have been called with a schema containing "enum"
	if len(runner.Calls) < 1 {
		t.Fatal("expected at least 1 runner call")
	}
	schema := runner.Calls[0].JSONSchema
	if !strings.Contains(schema, `"enum"`) {
		t.Error("expected enum-constrained schema when goal files are present")
	}
	if !strings.Contains(schema, "main.go") {
		t.Error("expected goal file path in enum schema")
	}
}
