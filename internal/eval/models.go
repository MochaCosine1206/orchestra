package eval

import "time"

// Scenario is the domain model for an eval scenario.
type Scenario struct {
	ID              string
	Role            string
	Category        string
	RepoPath        string
	Goal            string
	ExpectedOutcome string
	Difficulty      string
	TaskID          string // source task ID for curated scenarios (used to find JSONL transcript)
	CreatedAt       time.Time
}

// Version is the domain model for an eval version (code revision under test).
type Version struct {
	ID         string
	ParentID   string
	Branch     string
	CommitHash string
	CreatedAt  time.Time
	Status     string
}

// Run is the domain model for an eval run (one version × one scenario).
type Run struct {
	ID          string
	VersionID   string
	ScenarioID  string
	StartedAt   time.Time
	CompletedAt time.Time
	Status      string
	RawOutput   string
}

// Result is the domain model for a single metric result within a run.
type Result struct {
	ID      string
	RunID   string
	Metric  string
	Score   float64
	Weight  float64
	Details string
}

// ComparisonResult holds the outcome of comparing two versions.
type ComparisonResult struct {
	BaseVersionID      string
	CandidateVersionID string
	BaseScore          float64
	CandidateScore     float64
	Delta              float64
	Improved           bool
}

// GateResult holds the pass/fail decision for a version gate check.
type GateResult struct {
	VersionID string
	Passed    bool
	Score     float64
	Threshold float64
	Details   string
}

// BootstrapCIResult holds bootstrap confidence interval statistics.
type BootstrapCIResult struct {
	Mean       float64
	Lower      float64
	Upper      float64
	Confidence float64
	Iterations int
}

// RubricWeight defines a single metric weight within a rubric.
type RubricWeight struct {
	Metric string
	Weight float64
}

// Rubric defines the scoring rubric for a specific role.
type Rubric struct {
	Role    string
	Weights []RubricWeight
}

// ImplementerRubric defines scoring weights for the implementer role.
var ImplementerRubric = Rubric{
	Role: "implementer",
	Weights: []RubricWeight{
		{Metric: "completion", Weight: 0.30},
		{Metric: "test_pass", Weight: 0.25},
		{Metric: "merge_clean", Weight: 0.15},
		{Metric: "quality", Weight: 0.15},
		{Metric: "efficiency", Weight: 0.15},
	},
}

// ReviewerRubric defines scoring weights for the reviewer role.
var ReviewerRubric = Rubric{
	Role: "reviewer",
	Weights: []RubricWeight{
		{Metric: "bug_detection", Weight: 0.35},
		{Metric: "false_positive", Weight: 0.25},
		{Metric: "actionability", Weight: 0.20},
		{Metric: "severity", Weight: 0.20},
	},
}

// ResearcherRubric defines scoring weights for the researcher role.
var ResearcherRubric = Rubric{
	Role: "researcher",
	Weights: []RubricWeight{
		{Metric: "sources", Weight: 0.20},
		{Metric: "claims", Weight: 0.20},
		{Metric: "saturation", Weight: 0.20},
		{Metric: "citations", Weight: 0.20},
		{Metric: "actionability", Weight: 0.20},
	},
}

// ArchitectRubric defines scoring weights for the architect role.
var ArchitectRubric = Rubric{
	Role: "architect",
	Weights: []RubricWeight{
		{Metric: "downstream", Weight: 0.30},
		{Metric: "completeness", Weight: 0.25},
		{Metric: "coverage", Weight: 0.20},
		{Metric: "no_overlap", Weight: 0.15},
		{Metric: "dag_valid", Weight: 0.10},
	},
}
