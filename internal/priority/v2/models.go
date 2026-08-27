package priority

import (
	"time"
)

// Tier represents a level in the B-051 priority hierarchy.
// Lower number = higher priority.
type Tier int

const (
	TierUser         Tier = 1 // User requests — Steven's picks, explicit commands
	TierRetry        Tier = 2 // Failed/blocked retries from previous runs
	TierGoalImpl     Tier = 3 // Goal-aligned implementation — specs ready
	TierGoalResearch Tier = 4 // Goal-aligned research — discovery findings
	TierValidation   Tier = 5 // Validation/A/B testing
	TierTechDebt     Tier = 6 // Tech debt / bug fixes
	TierSelfImprove  Tier = 7 // Self-improvement research
	TierExploratory  Tier = 8 // Exploratory research — idle only
)

// TierBaseScore returns the base score for a hierarchy tier.
// This establishes the fundamental ordering — a tier-1 item will
// almost always outrank a tier-8 item regardless of other signals.
func TierBaseScore(t Tier) float64 {
	switch t {
	case TierUser:
		return 1000
	case TierRetry:
		return 800
	case TierGoalImpl:
		return 600
	case TierGoalResearch:
		return 400
	case TierValidation:
		return 300
	case TierTechDebt:
		return 200
	case TierSelfImprove:
		return 100
	case TierExploratory:
		return 50
	default:
		return 50
	}
}

// Source identifies which collector produced a work item.
type Source string

const (
	SourceUser      Source = "user"
	SourceBacklog   Source = "backlog"
	SourceDiscovery Source = "discovery"
	SourceGrant     Source = "grant"
	SourceResearch  Source = "research"
	SourceRetry     Source = "retry"
	SourceStale     Source = "stale"
	SourceOSS       Source = "oss"
	SourceMarket    Source = "market"
)

// WorkItemStatus constants.
const (
	StatusPending   = "pending"
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusBlocked   = "blocked"
	StatusStale     = "stale"
	StatusCancelled = "cancelled"
)

// WorkItem is the universal unit of prioritizable work.
type WorkItem struct {
	ID          string
	Source      Source
	SourceID    string
	SourceRepo  string
	Title       string
	Description string

	// Hierarchy
	Tier         Tier
	BasePriority int

	// Discovery scores (nullable)
	Feasibility *float64
	Impact      *float64
	Uniqueness  *float64

	// Deadline
	Deadline     *time.Time
	DeadlineType string

	// Effort
	EffortHours      *float64
	EffortConfidence *float64

	// Dependencies
	BlockedBy []string
	Blocks    []string

	// User signals
	UserPicked       bool
	UserPickedAt     *time.Time
	UserPriorityRank *int

	// Market signals
	MarketSignalIDs   []string
	CompetitorShipped bool

	// Routing
	TargetRepo   string
	IsNewProject bool
	AgentRoles   []string

	// Computed (filled by scorer)
	GoalAlignment     float64
	EffectivePriority float64
	Explanation       string

	// Project classification
	LicenseType   string
	LicenseReason string

	// State
	Status            string
	RetryCount        int
	LastFailureReason string

	// Timestamps
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ScoredAt        *time.Time
	StaleAfter      *time.Time
	LastValidatedAt *time.Time
}

// UserPriority represents a single entry from priorities.md.
type UserPriority struct {
	ID          string
	Rank        int
	Title       string
	Description string
	RepoHint    string
	Active      bool
}

// DailyReport captures what the morning report contained and user response.
type DailyReport struct {
	ID             string
	GeneratedAt    time.Time
	TopItems       []string
	UserPicks      []string
	PickReceivedAt *time.Time
}

// ScoringResult holds the output of scoring a single work item.
type ScoringResult struct {
	EffectivePriority float64
	Explanation       string
	Components        ScoringComponents
}

// ScoringComponents breaks down every factor in the score for transparency.
type ScoringComponents struct {
	TierBase        float64
	DeadlineUrgency float64
	GoalAlignment   float64
	UserEngagement  float64
	EffortBonus     float64
	MarketSignal    float64
	StalenessDecay  float64
	DependencyGate  float64
	PreemptionBoost float64
	DiscoveryScore  float64
	BacklogPriority float64
}
