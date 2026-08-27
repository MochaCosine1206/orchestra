package orchestrator

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// validPhaseIDRe matches lowercase letters, numbers, and hyphens (max 64 chars).
var validPhaseIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// validRoles lists the allowed agent roles for spec tasks.
var validRoles = map[string]bool{
	"implementer":       true,
	"researcher":        true,
	"deep-researcher":   true,
	"architect":         true,
	"reviewer":          true,
	"scout":             true,
	"editor":            true,
	"illustrator":       true,
	"visual-reviewer":   true,
	"functional-tester": true,
}

// --- Types ---

// OrchestraSpec is the top-level structure for a multi-phase orchestration spec.
type OrchestraSpec struct {
	Version  string       `yaml:"version" json:"version"`
	Metadata SpecMetadata `yaml:"metadata" json:"metadata"`
	Phases   []Phase      `yaml:"phases" json:"phases"`
}

// SpecMetadata holds project-level information for the spec.
type SpecMetadata struct {
	Title       string            `yaml:"title" json:"title"`
	Description string            `yaml:"description" json:"description"`
	TechStack   map[string]string `yaml:"tech_stack" json:"tech_stack"`
	Constraints []string          `yaml:"constraints" json:"constraints"`
	FinalE2ECmd string            `yaml:"final_e2e_cmd,omitempty" json:"final_e2e_cmd,omitempty"`
}

// Phase represents a single execution phase containing tasks.
type Phase struct {
	ID          string     `yaml:"id" json:"id"`
	Name        string     `yaml:"name" json:"name"`
	Description string     `yaml:"description" json:"description"`
	DependsOn   []string   `yaml:"depends_on" json:"depends_on"`
	Gate        PhaseGate  `yaml:"gate" json:"gate"`
	Tasks       []SpecTask `yaml:"tasks" json:"tasks"`
}

// PhaseGate defines the criteria that must be met before a phase is considered complete.
type PhaseGate struct {
	Mode         string   `yaml:"mode,omitempty" json:"mode,omitempty"` // B-288: "test", "acceptance", "none" (default: auto-detect)
	TestCmd      string   `yaml:"test_cmd" json:"test_cmd"`
	Acceptance   []string `yaml:"acceptance" json:"acceptance"`
	PostMergeCmd string   `yaml:"post_merge_cmd,omitempty" json:"post_merge_cmd,omitempty"` // G-CSS-7: optional validation after staging→dev merge
}

// SpecContract defines a shared interface contract between tasks.
// When two tasks share an API boundary, both receive the same type definition
// to prevent field name mismatches (the #1 recurring bug in multi-agent builds).
type SpecContract struct {
	Name       string `yaml:"name" json:"name"`             // e.g. "OllamaStatusAPI"
	Definition string `yaml:"definition" json:"definition"` // TypeScript interface or JSON schema
	Role       string `yaml:"role" json:"role"`              // "producer" or "consumer"
}

// SpecTask defines a single task within a phase.
type SpecTask struct {
	Title              string         `yaml:"title" json:"title"`
	Role               string         `yaml:"role" json:"role"`
	Files              []string       `yaml:"files" json:"files"`
	Description        string         `yaml:"description" json:"description"`
	AcceptanceCriteria []string       `yaml:"acceptance_criteria" json:"acceptance_criteria"`
	DependsOn          []string       `yaml:"depends_on" json:"depends_on"`
	Atomic             bool           `yaml:"atomic,omitempty" json:"atomic,omitempty"`
	Contracts          []SpecContract `yaml:"contracts,omitempty" json:"contracts,omitempty"`
}

// --- Validation Result ---

// SpecValidation holds the results of validating an OrchestraSpec.
type SpecValidation struct {
	Errors   []SpecIssue // fatal — spec cannot execute
	Warnings []SpecIssue // informational — spec can execute
}

// SpecIssue describes a single validation error or warning.
type SpecIssue struct {
	Rule    string // e.g. "phase_dag_cycle", "file_exclusivity"
	Phase   string // phase ID (if applicable)
	Task    string // task title (if applicable)
	Message string // human-readable description
}

// IsValid returns true if the spec has no fatal errors.
func (v SpecValidation) IsValid() bool { return len(v.Errors) == 0 }

// --- Phase Execution Config ---

// PhaseExecConfig holds Go()-compatible options derived from a phase definition.
type PhaseExecConfig struct {
	Goal            string     // constructed from phase description + task summaries
	TestCmd         string     // from phase.Gate.TestCmd
	Tasks           []SpecTask // the phase's tasks
	FileEnforcement string     // "defense" when phase has 3+ tasks
	MaxTasks        int        // len(phase.Tasks)
}

// --- Parser ---

// ParseSpec reads and unmarshals a YAML spec file.
func ParseSpec(path string) (*OrchestraSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("read spec: file is empty")
	}

	var spec OrchestraSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	return &spec, nil
}

// --- Validator ---

// ValidateSpec checks all validation rules against a parsed spec.
func ValidateSpec(spec *OrchestraSpec) SpecValidation {
	var v SpecValidation

	// Rule 1: at least one phase
	if len(spec.Phases) == 0 {
		v.Errors = append(v.Errors, SpecIssue{
			Rule:    "no_phases",
			Message: "spec must have at least 1 phase",
		})
		return v // can't validate further
	}

	phaseIDs := make(map[string]bool, len(spec.Phases))

	for _, p := range spec.Phases {
		// Rule 7: phase ID format
		if !validPhaseIDRe.MatchString(p.ID) {
			v.Errors = append(v.Errors, SpecIssue{
				Rule:    "invalid_phase_id",
				Phase:   p.ID,
				Message: fmt.Sprintf("phase ID %q must be lowercase letters, numbers, and hyphens only (max 64 chars)", p.ID),
			})
		}

		// Rule 2: phase ID uniqueness
		if phaseIDs[p.ID] {
			v.Errors = append(v.Errors, SpecIssue{
				Rule:    "duplicate_phase_id",
				Phase:   p.ID,
				Message: fmt.Sprintf("duplicate phase ID %q", p.ID),
			})
		}
		phaseIDs[p.ID] = true

		// Rule 4: gate — warn if empty (content phases may omit; gate auto-passes)
		if p.Gate.TestCmd == "" || len(p.Gate.Acceptance) == 0 {
			v.Warnings = append(v.Warnings, SpecIssue{
				Rule:    "empty_gate",
				Phase:   p.ID,
				Message: fmt.Sprintf("phase %q has empty gate fields (will auto-pass)", p.ID),
			})
		}

		// Validate tasks within this phase
		fileOwners := make(map[string]string) // file → first task title
		for _, task := range p.Tasks {
			// Rule 1 (schema completeness): required task fields
			var missing []string
			if task.Title == "" {
				missing = append(missing, "title")
			}
			if task.Role == "" {
				missing = append(missing, "role")
			}
			if len(task.Files) == 0 {
				missing = append(missing, "files")
			}
			if task.Description == "" {
				missing = append(missing, "description")
			}
			if len(task.AcceptanceCriteria) == 0 {
				missing = append(missing, "acceptance_criteria")
			}
			if len(missing) > 0 {
				v.Errors = append(v.Errors, SpecIssue{
					Rule:    "missing_task_field",
					Phase:   p.ID,
					Task:    task.Title,
					Message: fmt.Sprintf("task missing required fields: %s", strings.Join(missing, ", ")),
				})
			}

			// Rule 6: valid roles
			if task.Role != "" && !validRoles[task.Role] {
				v.Errors = append(v.Errors, SpecIssue{
					Rule:    "invalid_role",
					Phase:   p.ID,
					Task:    task.Title,
					Message: fmt.Sprintf("unknown role %q (valid: implementer, researcher, deep-researcher, architect, reviewer, scout, editor, illustrator, visual-reviewer, functional-tester)", task.Role),
				})
			}

			// Rule 4: file exclusivity within phase
			for _, f := range task.Files {
				if owner, exists := fileOwners[f]; exists {
					v.Errors = append(v.Errors, SpecIssue{
						Rule:    "file_exclusivity",
						Phase:   p.ID,
						Task:    task.Title,
						Message: fmt.Sprintf("file %q assigned to both %q and %q in phase %q", f, owner, task.Title, p.ID),
					})
				} else {
					fileOwners[f] = task.Title
				}
			}
		}
	}

	// Rule 8: dangling depends_on
	for _, p := range spec.Phases {
		for _, dep := range p.DependsOn {
			if !phaseIDs[dep] {
				v.Errors = append(v.Errors, SpecIssue{
					Rule:    "dangling_depends_on",
					Phase:   p.ID,
					Message: fmt.Sprintf("phase %q depends on %q which does not exist", p.ID, dep),
				})
			}
		}
	}

	// Rule 3: phase DAG acyclic
	if _, err := TopoSortPhases(spec.Phases); err != nil {
		v.Errors = append(v.Errors, SpecIssue{
			Rule:    "phase_dag_cycle",
			Message: fmt.Sprintf("phase dependency cycle detected: %v", err),
		})
	}

	// Rule 5: cross-phase file overlap (warning only)
	allPhaseFiles := make(map[string]string) // file → first phase ID
	for _, p := range spec.Phases {
		for _, task := range p.Tasks {
			for _, f := range task.Files {
				if prevPhase, exists := allPhaseFiles[f]; exists && prevPhase != p.ID {
					v.Warnings = append(v.Warnings, SpecIssue{
						Rule:    "cross_phase_file_overlap",
						Phase:   p.ID,
						Task:    task.Title,
						Message: fmt.Sprintf("file %q also appears in phase %q — expected when extending code, but worth reviewing", f, prevPhase),
					})
				}
				if _, exists := allPhaseFiles[f]; !exists {
					allPhaseFiles[f] = p.ID
				}
			}
		}
	}

	return v
}

// --- Topological Sort ---

// TopoSortPhases returns phase IDs in topological execution order using Kahn's algorithm.
// Returns error if a cycle is detected.
func TopoSortPhases(phases []Phase) ([]string, error) {
	if len(phases) == 0 {
		return nil, nil
	}

	phaseSet := make(map[string]bool, len(phases))
	inDegree := make(map[string]int, len(phases))
	dependents := make(map[string][]string) // dependency → phases that depend on it

	for _, p := range phases {
		phaseSet[p.ID] = true
		inDegree[p.ID] = 0
	}

	for _, p := range phases {
		for _, dep := range p.DependsOn {
			if !phaseSet[dep] {
				continue // skip references to unknown phases
			}
			dependents[dep] = append(dependents[dep], p.ID)
			inDegree[p.ID]++
		}
	}

	// Seed queue with phases that have no dependencies, in declaration order
	var queue []string
	for _, p := range phases {
		if inDegree[p.ID] == 0 {
			queue = append(queue, p.ID)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, id)

		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(phases) {
		return nil, fmt.Errorf("cycle detected: sorted %d of %d phases", len(sorted), len(phases))
	}

	return sorted, nil
}

// --- Helper: Phase-to-GoOpts Conversion ---

// PhaseGoOpts extracts Go()-compatible options from a phase definition.
// This doesn't execute anything — just prepares the data structure for B-275.
func PhaseGoOpts(phase Phase, metadata SpecMetadata) PhaseExecConfig {
	// Build goal from phase description + task summaries
	var parts []string
	parts = append(parts, fmt.Sprintf("Phase: %s — %s", phase.Name, phase.Description))
	if metadata.Title != "" {
		parts = append(parts, fmt.Sprintf("Project: %s", metadata.Title))
	}
	// G-CSS-9: Propagate spec-level description and constraints into the goal
	if metadata.Description != "" {
		parts = append(parts, fmt.Sprintf("Description: %s", metadata.Description))
	}
	if len(metadata.Constraints) > 0 {
		parts = append(parts, fmt.Sprintf("Constraints: %s", strings.Join(metadata.Constraints, "; ")))
	}
	for _, t := range phase.Tasks {
		if t.Atomic {
			parts = append(parts, fmt.Sprintf("- %s [ATOMIC — DO NOT SPLIT OR MERGE WITH OTHER TASKS]: %s", t.Title, t.Description))
		} else {
			parts = append(parts, fmt.Sprintf("- %s: %s", t.Title, t.Description))
		}
	}

	enforcement := ""
	if len(phase.Tasks) >= 3 {
		enforcement = "defense"
	}

	return PhaseExecConfig{
		Goal:            strings.Join(parts, "\n"),
		TestCmd:         phase.Gate.TestCmd,
		Tasks:           phase.Tasks,
		FileEnforcement: enforcement,
		MaxTasks:        len(phase.Tasks),
	}
}
