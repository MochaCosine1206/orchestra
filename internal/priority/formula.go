package priority

import (
	"math"
	"time"
)

// PriorityInput holds the raw inputs for computing effective priority.
type PriorityInput struct {
	BasePriority    int
	GoalAlignment   float64
	Urgency         float64
	CreatedAt       time.Time
	LastRunAt       time.Time
	ActivityCount   int
	PreemptionCount int
}

// AgingBoost returns +1 per hour since creation, capped at +50.
func AgingBoost(sinceCreation time.Duration) float64 {
	if sinceCreation < 0 {
		return 0
	}
	hours := sinceCreation.Hours()
	return math.Min(hours, 50)
}

// ActivityBoost returns +5 per activity in the last 24h, capped at +25.
func ActivityBoost(count int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Min(float64(count)*5, 25)
}

// PreemptionBoost returns +10 per preemption, capped at +30.
func PreemptionBoost(count int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Min(float64(count)*10, 30)
}

// ComputeEffectivePriority calculates the effective priority score.
// Formula: (base * goal_alignment * urgency) + AgingBoost + ActivityBoost + PreemptionBoost
func ComputeEffectivePriority(input PriorityInput) float64 {
	base := float64(input.BasePriority) * input.GoalAlignment * input.Urgency
	aging := AgingBoost(time.Since(input.CreatedAt))
	activity := ActivityBoost(input.ActivityCount)
	preemption := PreemptionBoost(input.PreemptionCount)
	return base + aging + activity + preemption
}
