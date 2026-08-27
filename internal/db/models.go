package db

import (
	"database/sql"
	"time"
)

// Agent represents a row in the agents table.
type Agent struct {
	ID          string
	Role        string
	Status      string
	Worktree    sql.NullString
	PID         sql.NullInt64
	CurrentTask sql.NullString
	HeartbeatAt sql.NullTime
	CreatedAt   time.Time
}

// Task represents a row in the tasks table.
type Task struct {
	ID                 string
	Title              string
	Description        sql.NullString
	AcceptanceCriteria sql.NullString
	Status             string
	Priority           int
	PriorityLabel      sql.NullString
	Role               string
	AssignedTo    sql.NullString
	DependsOn     sql.NullString
	BlockedBy     sql.NullString
	Worktree      sql.NullString
	Branch        sql.NullString
	Result        sql.NullString
	ConductorID   sql.NullString // FK to conductors table (nullable for backward compat)
	PhaseID        sql.NullString // Phase identifier for multi-phase GoSpec() (G110)
	FeatureCluster sql.NullString // Feature cluster name for hierarchical decomposition (B-281)
	CreatedAt      time.Time
	StartedAt     sql.NullTime
	CompletedAt   sql.NullTime
}

// FileLock represents a row in the file_locks table.
type FileLock struct {
	FilePath  string
	LockedBy  sql.NullString
	TaskID    sql.NullString
	LockedAt  time.Time
	ExpiresAt sql.NullTime
}

// Event represents a row in the events table.
type Event struct {
	ID        int64
	Timestamp time.Time
	AgentID   sql.NullString
	TaskID    sql.NullString
	EventType string
	Payload   sql.NullString
}

// BlackboardEntry represents a row in the blackboard table.
type BlackboardEntry struct {
	Key       string
	Value     string
	WrittenBy sql.NullString
	UpdatedAt time.Time
}

// StatusSummary is the aggregate status used by `orchestra status`.
type StatusSummary struct {
	Tasks      StatusCounts
	Agents     StatusCounts
	LockCount  int
	EventCount int
}

// StatusCounts holds counts by status string.
type StatusCounts struct {
	Total    int
	ByStatus map[string]int
}

// ConductorRecord represents a row in the conductors table.
type ConductorRecord struct {
	ID              string
	PID             int
	Goal            string
	Status          string
	StagingBranch   string
	BaseBranch      string
	MaxParallel     int
	TestCmd         sql.NullString
	MergeReview     bool
	ModelStrategy   string
	Runtime         string
	RepoMap         bool
	LenientDeps     bool
	FileEnforcement string
	MergeMode       string // "local" (default) or "pr" (create GitHub PR)
	MergeStrategy   string // "batch" (default) or "fifo" (B-280)
	PhaseID         string // phase identifier for cross-invocation chaining (B-287)
	StartedAt       time.Time
	HeartbeatAt     time.Time
	CompletedAt     sql.NullTime

	// Merge state tracking (B-145): crash-recovery for mid-merge failures.
	MergeStatus       sql.NullString // NULL, "merging", "staging", "done"
	MergeStartedAt    sql.NullTime
	MergeBranchesDone []string       // parsed from JSON TEXT column
}

// MergeQueueEntry represents a row in the merge_queue_entries table (B-280).
type MergeQueueEntry struct {
	ID              string
	ConductorID     string
	TaskID          string
	Branch          string
	Position        int
	Status          string // pending, blocked, merging, merged, failed, refining
	ResolutionTier  sql.NullString
	ConflictFiles   sql.NullString
	TestResult      sql.NullString
	ErrorMessage    sql.NullString
	RefinementCount int
	EnqueuedAt      time.Time
	MergedAt        sql.NullTime
}

// StallScore represents a row in the stall_scores table (B-142).
type StallScore struct {
	ID                int64
	TaskID            string
	AgentID           string
	CompositeScore    float64
	SignalFingerprint float64
	SignalProgress    float64
	SignalFiles       float64
	SignalErrors      float64
	SignalReadWrite   float64
	Phase             sql.NullString
	CreatedAt         time.Time
}

// DriftScore represents a row in the drift_scores table.
type DriftScore struct {
	ID          int64
	SessionID   string
	CycleNumber int
	Score       float64
	Explanation sql.NullString
	ActionTaken sql.NullString
	CreatedAt   time.Time
}

// PlanCacheEntry represents a row in the plan_cache table.
type PlanCacheEntry struct {
	ID         int64
	GoalHash   string
	GoalText   string
	W5H2Key    sql.NullString
	Keywords   sql.NullString
	PlanJSON   string
	ActionType sql.NullString
	Tier       sql.NullInt64
	TTLDays    int
	FailCount  int
	HitCount   int
	FileMtimes sql.NullString
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// EvalScenario represents a row in the eval_scenarios table.
type EvalScenario struct {
	ID              string
	Role            string
	Category        sql.NullString
	RepoPath        sql.NullString
	Goal            string
	ExpectedOutcome sql.NullString
	Difficulty      sql.NullString
	CreatedAt       time.Time
}

// EvalVersion represents a row in the eval_versions table.
type EvalVersion struct {
	ID         string
	ParentID   sql.NullString
	Branch     sql.NullString
	CommitHash sql.NullString
	CreatedAt  time.Time
	Status     string
}

// EvalRun represents a row in the eval_runs table.
type EvalRun struct {
	ID          string
	VersionID   string
	ScenarioID  string
	StartedAt   sql.NullTime
	CompletedAt sql.NullTime
	Status      string
	RawOutput   sql.NullString
}

// EvalResult represents a row in the eval_results table.
type EvalResult struct {
	ID      string
	RunID   string
	Metric  string
	Score   float64
	Weight  float64
	Details sql.NullString
}

// HealingLog represents a row in the healing_log table.
type HealingLog struct {
	ID         string
	SessionID  string
	TaskID     sql.NullString
	ErrorType  sql.NullString
	FixApplied sql.NullString
	Success    int
	RolledBack int
	CreatedAt  time.Time
}

// StatusJSON is the JSON-serializable status output for orchestra status --json.
type StatusJSON struct {
	Tasks      []Task     `json:"tasks"`
	Agents     []Agent    `json:"agents"`
	Locks      []FileLock `json:"locks"`
	Events     []Event    `json:"events"`
	Violations []Event    `json:"violations,omitempty"`
	Summary    struct {
		TaskCounts  map[string]int `json:"task_counts"`
		AgentCounts map[string]int `json:"agent_counts"`
		LockCount   int            `json:"lock_count"`
	} `json:"summary"`
}
