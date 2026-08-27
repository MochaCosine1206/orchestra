package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Parsing Tests ---

func TestParseSpec_ValidBillingExample(t *testing.T) {
	spec, err := ParseSpec("../assets/examples/billing-api-spec.yaml")
	if err != nil {
		t.Fatalf("ParseSpec failed: %v", err)
	}
	if spec.Version != "1" {
		t.Errorf("version = %q, want %q", spec.Version, "1")
	}
	if spec.Metadata.Title != "Billing API" {
		t.Errorf("title = %q, want %q", spec.Metadata.Title, "Billing API")
	}
	if spec.Metadata.TechStack["language"] != "go" {
		t.Errorf("tech_stack.language = %q, want %q", spec.Metadata.TechStack["language"], "go")
	}
	if len(spec.Metadata.Constraints) != 2 {
		t.Errorf("constraints count = %d, want 2", len(spec.Metadata.Constraints))
	}
	if len(spec.Phases) != 3 {
		t.Errorf("phase count = %d, want 3", len(spec.Phases))
	}

	// Check foundation phase
	p := spec.Phases[0]
	if p.ID != "foundation" {
		t.Errorf("phase[0].id = %q, want %q", p.ID, "foundation")
	}
	if len(p.Tasks) != 3 {
		t.Errorf("phase[0].tasks count = %d, want 3", len(p.Tasks))
	}
	if p.Gate.TestCmd != "go build ./..." {
		t.Errorf("phase[0].gate.test_cmd = %q, want %q", p.Gate.TestCmd, "go build ./...")
	}

	// Check task fields
	task := p.Tasks[0]
	if task.Title != "Project scaffolding" {
		t.Errorf("task.title = %q, want %q", task.Title, "Project scaffolding")
	}
	if task.Role != "implementer" {
		t.Errorf("task.role = %q, want %q", task.Role, "implementer")
	}
	if len(task.Files) != 3 {
		t.Errorf("task.files count = %d, want 3", len(task.Files))
	}
	if len(task.AcceptanceCriteria) != 3 {
		t.Errorf("task.acceptance_criteria count = %d, want 3", len(task.AcceptanceCriteria))
	}

	// Check phase dependencies
	if len(spec.Phases[1].DependsOn) != 1 || spec.Phases[1].DependsOn[0] != "foundation" {
		t.Errorf("phase[1].depends_on = %v, want [foundation]", spec.Phases[1].DependsOn)
	}
}

func TestParseSpec_ValidContentExample(t *testing.T) {
	spec, err := ParseSpec("../assets/examples/content-book-spec.yaml")
	if err != nil {
		t.Fatalf("ParseSpec failed: %v", err)
	}
	if spec.Version != "1" {
		t.Errorf("version = %q, want %q", spec.Version, "1")
	}
	if spec.Metadata.Title != "Developer Psychology with AI Agents" {
		t.Errorf("title = %q, want %q", spec.Metadata.Title, "Developer Psychology with AI Agents")
	}
	if len(spec.Phases) != 4 {
		t.Errorf("phase count = %d, want 4", len(spec.Phases))
	}

	// Research phase uses researcher role
	if spec.Phases[0].ID != "research" {
		t.Errorf("phase[0].id = %q, want %q", spec.Phases[0].ID, "research")
	}
	if spec.Phases[0].Tasks[0].Role != "researcher" {
		t.Errorf("research task role = %q, want %q", spec.Phases[0].Tasks[0].Role, "researcher")
	}

	// Editorial phase uses editor role
	editPhase := spec.Phases[3]
	if editPhase.ID != "developmental-edit" {
		t.Errorf("phase[3].id = %q, want %q", editPhase.ID, "developmental-edit")
	}
	if editPhase.Tasks[0].Role != "editor" {
		t.Errorf("edit task role = %q, want %q", editPhase.Tasks[0].Role, "editor")
	}

	// Validation should pass
	v := ValidateSpec(spec)
	if !v.IsValid() {
		t.Errorf("content example should validate, got errors: %v", v.Errors)
	}
}

func TestParseSpec_MinimalSpec(t *testing.T) {
	yaml := `
version: "1"
metadata:
  title: "Minimal"
  description: "A minimal spec"
phases:
  - id: "only-phase"
    name: "Only Phase"
    description: "Single phase"
    gate:
      test_cmd: "echo ok"
      acceptance:
        - "It works"
    tasks:
      - title: "Single task"
        role: "implementer"
        files:
          - "main.go"
        description: "Do the thing"
        acceptance_criteria:
          - "Thing is done"
`
	path := writeTemp(t, "minimal.yaml", yaml)
	spec, err := ParseSpec(path)
	if err != nil {
		t.Fatalf("ParseSpec failed: %v", err)
	}
	if len(spec.Phases) != 1 {
		t.Errorf("phase count = %d, want 1", len(spec.Phases))
	}
	if len(spec.Phases[0].Tasks) != 1 {
		t.Errorf("task count = %d, want 1", len(spec.Phases[0].Tasks))
	}
}

func TestParseSpec_FileNotFound(t *testing.T) {
	_, err := ParseSpec("/nonexistent/path/spec.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestParseSpec_InvalidYAML(t *testing.T) {
	path := writeTemp(t, "bad.yaml", "{{{{not yaml at all::::")
	_, err := ParseSpec(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestParseSpec_EmptyFile(t *testing.T) {
	path := writeTemp(t, "empty.yaml", "")
	_, err := ParseSpec(path)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

// --- Validation Tests ---

func TestValidateSpec_Valid(t *testing.T) {
	spec := validSpec()
	v := ValidateSpec(spec)
	if !v.IsValid() {
		t.Errorf("expected valid, got errors: %v", v.Errors)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", v.Warnings)
	}
}

func TestValidateSpec_NoPhases(t *testing.T) {
	spec := validSpec()
	spec.Phases = nil
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for spec with no phases")
	}
	assertHasError(t, v, "no_phases")
}

func TestValidateSpec_DuplicatePhaseID(t *testing.T) {
	spec := validSpec()
	spec.Phases = append(spec.Phases, Phase{
		ID:   "phase-1",
		Name: "Duplicate",
		Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
		Tasks: []SpecTask{{
			Title: "Task", Role: "implementer", Files: []string{"z.go"},
			Description: "desc", AcceptanceCriteria: []string{"done"},
		}},
	})
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for duplicate phase ID")
	}
	assertHasError(t, v, "duplicate_phase_id")
}

func TestValidateSpec_PhaseDagCycle(t *testing.T) {
	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Cycle", Description: "test"},
		Phases: []Phase{
			{
				ID: "a", Name: "A", DependsOn: []string{"b"},
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{{
					Title: "T", Role: "implementer", Files: []string{"a.go"},
					Description: "d", AcceptanceCriteria: []string{"ok"},
				}},
			},
			{
				ID: "b", Name: "B", DependsOn: []string{"a"},
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{{
					Title: "T", Role: "implementer", Files: []string{"b.go"},
					Description: "d", AcceptanceCriteria: []string{"ok"},
				}},
			},
		},
	}
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for cyclic phase dependency")
	}
	assertHasError(t, v, "phase_dag_cycle")
}

func TestValidateSpec_FileExclusivityWithinPhase(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].Tasks = append(spec.Phases[0].Tasks, SpecTask{
		Title: "Conflicting task", Role: "implementer",
		Files: []string{"main.go"}, // same file as first task
		Description: "conflict", AcceptanceCriteria: []string{"done"},
	})
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for file exclusivity violation")
	}
	assertHasError(t, v, "file_exclusivity")
}

func TestValidateSpec_CrossPhaseFileOverlap(t *testing.T) {
	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Overlap", Description: "test"},
		Phases: []Phase{
			{
				ID: "p1", Name: "Phase 1", DependsOn: []string{},
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{{
					Title: "T1", Role: "implementer", Files: []string{"shared.go"},
					Description: "d", AcceptanceCriteria: []string{"ok"},
				}},
			},
			{
				ID: "p2", Name: "Phase 2", DependsOn: []string{"p1"},
				Gate: PhaseGate{TestCmd: "echo ok", Acceptance: []string{"ok"}},
				Tasks: []SpecTask{{
					Title: "T2", Role: "implementer", Files: []string{"shared.go"},
					Description: "d", AcceptanceCriteria: []string{"ok"},
				}},
			},
		},
	}
	v := ValidateSpec(spec)
	if !v.IsValid() {
		t.Fatalf("cross-phase overlap should be warning not error, got errors: %v", v.Errors)
	}
	assertHasWarning(t, v, "cross_phase_file_overlap")
}

func TestValidateSpec_InvalidRole(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].Tasks[0].Role = "janitor"
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for invalid role")
	}
	assertHasError(t, v, "invalid_role")
}

func TestValidateSpec_MissingTaskFields(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].Tasks[0] = SpecTask{} // all empty
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for missing task fields")
	}
	assertHasError(t, v, "missing_task_field")
}

func TestValidateSpec_EmptyGate(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].Gate = PhaseGate{} // empty gate
	v := ValidateSpec(spec)
	// B-288: empty gate is now a warning, not an error (content phases may omit)
	if !v.IsValid() {
		t.Fatalf("expected valid (empty gate is warning not error), got errors: %v", v.Errors)
	}
	assertHasWarning(t, v, "empty_gate")
}

func TestValidateSpec_EmptyGateTestCmdOnly(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].Gate = PhaseGate{
		Acceptance: []string{"chapters exist"},
	}
	v := ValidateSpec(spec)
	// B-288: empty test_cmd with acceptance is still a warning (auto-passes)
	if !v.IsValid() {
		t.Fatalf("expected valid, got errors: %v", v.Errors)
	}
	assertHasWarning(t, v, "empty_gate")
}

func TestValidateSpec_DanglingDependsOn(t *testing.T) {
	spec := validSpec()
	spec.Phases[0].DependsOn = []string{"nonexistent-phase"}
	v := ValidateSpec(spec)
	if v.IsValid() {
		t.Fatal("expected error for dangling depends_on")
	}
	assertHasError(t, v, "dangling_depends_on")
}

func TestValidateSpec_InvalidPhaseID(t *testing.T) {
	tests := []struct {
		id   string
		desc string
	}{
		{"Phase One", "spaces"},
		{"PhaseOne", "uppercase"},
		{"phase_one", "underscores"},
		{"", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			spec := validSpec()
			spec.Phases[0].ID = tt.id
			v := ValidateSpec(spec)
			if v.IsValid() {
				t.Fatalf("expected error for phase ID %q (%s)", tt.id, tt.desc)
			}
			assertHasError(t, v, "invalid_phase_id")
		})
	}
}

// --- TopoSort Tests ---

func TestTopoSortPhases_Linear(t *testing.T) {
	phases := []Phase{
		{ID: "a", DependsOn: []string{}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}
	order, err := TopoSortPhases(phases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"a", "b", "c"}
	if !sliceEqual(order, expected) {
		t.Errorf("order = %v, want %v", order, expected)
	}
}

func TestTopoSortPhases_Diamond(t *testing.T) {
	phases := []Phase{
		{ID: "a", DependsOn: []string{}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"a"}},
		{ID: "d", DependsOn: []string{"b", "c"}},
	}
	order, err := TopoSortPhases(phases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("order length = %d, want 4", len(order))
	}
	// a must be first, d must be last
	if order[0] != "a" {
		t.Errorf("order[0] = %q, want %q", order[0], "a")
	}
	if order[3] != "d" {
		t.Errorf("order[3] = %q, want %q", order[3], "d")
	}
	// b and c must both come before d
	posB := indexOf(order, "b")
	posC := indexOf(order, "c")
	posD := indexOf(order, "d")
	if posB >= posD || posC >= posD {
		t.Errorf("b (pos %d) and c (pos %d) must come before d (pos %d)", posB, posC, posD)
	}
}

func TestTopoSortPhases_NoDeps(t *testing.T) {
	phases := []Phase{
		{ID: "x", DependsOn: []string{}},
		{ID: "y", DependsOn: []string{}},
		{ID: "z", DependsOn: []string{}},
	}
	order, err := TopoSortPhases(phases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All independent — should maintain declaration order
	expected := []string{"x", "y", "z"}
	if !sliceEqual(order, expected) {
		t.Errorf("order = %v, want %v", order, expected)
	}
}

func TestTopoSortPhases_Cycle(t *testing.T) {
	phases := []Phase{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
	}
	_, err := TopoSortPhases(phases)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

// --- PhaseGoOpts Tests ---

func TestPhaseGoOpts_Basic(t *testing.T) {
	phase := Phase{
		ID:          "api",
		Name:        "API Layer",
		Description: "Build the API",
		Gate:        PhaseGate{TestCmd: "go test ./...", Acceptance: []string{"tests pass"}},
		Tasks: []SpecTask{
			{Title: "Handlers", Role: "implementer", Files: []string{"h.go"}, Description: "handlers", AcceptanceCriteria: []string{"ok"}},
			{Title: "Router", Role: "implementer", Files: []string{"r.go"}, Description: "router", AcceptanceCriteria: []string{"ok"}},
		},
	}
	meta := SpecMetadata{Title: "Test", Description: "test project"}

	cfg := PhaseGoOpts(phase, meta)
	if cfg.TestCmd != "go test ./..." {
		t.Errorf("TestCmd = %q, want %q", cfg.TestCmd, "go test ./...")
	}
	if cfg.MaxTasks != 2 {
		t.Errorf("MaxTasks = %d, want 2", cfg.MaxTasks)
	}
	if len(cfg.Tasks) != 2 {
		t.Errorf("Tasks count = %d, want 2", len(cfg.Tasks))
	}
	if cfg.FileEnforcement != "" {
		t.Errorf("FileEnforcement = %q, want empty (< 3 tasks)", cfg.FileEnforcement)
	}
	if cfg.Goal == "" {
		t.Error("Goal should not be empty")
	}
}

func TestPhaseGoOpts_AutoDefense(t *testing.T) {
	phase := Phase{
		ID:          "big-phase",
		Name:        "Big Phase",
		Description: "Many tasks",
		Gate:        PhaseGate{TestCmd: "make test", Acceptance: []string{"pass"}},
		Tasks: []SpecTask{
			{Title: "T1", Role: "implementer", Files: []string{"a.go"}, Description: "d", AcceptanceCriteria: []string{"ok"}},
			{Title: "T2", Role: "implementer", Files: []string{"b.go"}, Description: "d", AcceptanceCriteria: []string{"ok"}},
			{Title: "T3", Role: "implementer", Files: []string{"c.go"}, Description: "d", AcceptanceCriteria: []string{"ok"}},
		},
	}
	meta := SpecMetadata{Title: "Test", Description: "test"}

	cfg := PhaseGoOpts(phase, meta)
	if cfg.FileEnforcement != "defense" {
		t.Errorf("FileEnforcement = %q, want %q for 3+ tasks", cfg.FileEnforcement, "defense")
	}
}

// --- Helpers ---

func validSpec() *OrchestraSpec {
	return &OrchestraSpec{
		Version: "1",
		Metadata: SpecMetadata{
			Title:       "Test Spec",
			Description: "A test spec",
		},
		Phases: []Phase{
			{
				ID:        "phase-1",
				Name:      "Phase One",
				DependsOn: []string{},
				Gate: PhaseGate{
					TestCmd:    "go test ./...",
					Acceptance: []string{"tests pass"},
				},
				Tasks: []SpecTask{
					{
						Title:              "Task one",
						Role:               "implementer",
						Files:              []string{"main.go"},
						Description:        "Implement the thing",
						AcceptanceCriteria: []string{"thing works"},
					},
				},
			},
		},
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertHasError(t *testing.T, v SpecValidation, rule string) {
	t.Helper()
	for _, e := range v.Errors {
		if e.Rule == rule {
			return
		}
	}
	t.Errorf("expected error with rule %q, got errors: %v", rule, v.Errors)
}

func assertHasWarning(t *testing.T, v SpecValidation, rule string) {
	t.Helper()
	for _, w := range v.Warnings {
		if w.Rule == rule {
			return
		}
	}
	t.Errorf("expected warning with rule %q, got warnings: %v", rule, v.Warnings)
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexOf(s []string, val string) int {
	for i, v := range s {
		if v == val {
			return i
		}
	}
	return -1
}
