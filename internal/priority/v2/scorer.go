package priority

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ScoreWorkItem computes the effective priority for a single work item.
// All sub-scores are deterministic except GoalAlignment (LLM-cached).
func ScoreWorkItem(item WorkItem, now time.Time, userGoal string) ScoringResult {
	var c ScoringComponents

	// 1. Tier base score
	c.TierBase = TierBaseScore(item.Tier)

	// 2. Deadline urgency factor (1.0-5.0 range)
	c.DeadlineUrgency = 1.0
	if item.Deadline != nil {
		hoursUntil := item.Deadline.Sub(now).Hours()
		switch {
		case hoursUntil < 0:
			c.DeadlineUrgency = 5.0
		case hoursUntil < 24:
			c.DeadlineUrgency = 3.0 + (24-hoursUntil)/24*2.0
		case hoursUntil < 72:
			c.DeadlineUrgency = 2.0 + (72-hoursUntil)/72*1.0
		case hoursUntil < 168:
			c.DeadlineUrgency = 1.5 + (168-hoursUntil)/168*0.5
		case hoursUntil < 720:
			c.DeadlineUrgency = 1.0 + (720-hoursUntil)/720*0.5
		}
		switch item.DeadlineType {
		case "hard":
			c.DeadlineUrgency *= 1.2
		case "regulatory":
			c.DeadlineUrgency *= 1.5
		}
	}

	// 3. Goal alignment (fail-open to 1.0 if zero/not-yet-scored)
	c.GoalAlignment = item.GoalAlignment
	if c.GoalAlignment == 0 {
		c.GoalAlignment = 1.0
	}

	// 4. User engagement boost (additive)
	c.UserEngagement = 0
	if item.UserPicked {
		c.UserEngagement += 200
	}
	if item.UserPriorityRank != nil {
		rank := *item.UserPriorityRank
		if rank <= 0 {
			rank = 1
		}
		boost := 550 - float64(rank)*50
		if boost < 50 {
			boost = 50
		}
		c.UserEngagement += boost
	}

	// 5. Effort bonus (quick wins)
	c.EffortBonus = 0
	if item.EffortHours != nil && item.EffortConfidence != nil && *item.EffortConfidence > 0.5 {
		hours := *item.EffortHours
		switch {
		case hours <= 0.5:
			c.EffortBonus = 150
		case hours <= 2:
			c.EffortBonus = 100
		case hours <= 4:
			c.EffortBonus = 50
		}
	}

	// 6. Market signal boost
	c.MarketSignal = 0
	if item.CompetitorShipped {
		c.MarketSignal += 150
	}
	signalCount := len(item.MarketSignalIDs)
	if signalCount > 4 {
		signalCount = 4
	}
	c.MarketSignal += float64(signalCount) * 25

	// 7. Staleness decay (1pt/day after 7 days, capped at 100)
	c.StalenessDecay = 0
	referenceTime := item.CreatedAt
	if item.LastValidatedAt != nil {
		referenceTime = *item.LastValidatedAt
	}
	daysSince := now.Sub(referenceTime).Hours() / 24
	if daysSince > 7 {
		c.StalenessDecay = math.Min((daysSince-7)*1.0, 100)
	}

	// 8. Dependency gate (0.0 if blocked, 1.0 otherwise)
	c.DependencyGate = 1.0
	if len(item.BlockedBy) > 0 && item.Status == StatusBlocked {
		c.DependencyGate = 0.0
	}

	// 9. Preemption boost (+10 per retry, capped at +30)
	c.PreemptionBoost = preemptionBoost(item.RetryCount)

	// 10. Discovery score (F*I*U*200)
	c.DiscoveryScore = 0
	if item.Feasibility != nil && item.Impact != nil && item.Uniqueness != nil {
		c.DiscoveryScore = (*item.Feasibility) * (*item.Impact) * (*item.Uniqueness) * 200
	}

	// 11. Backlog priority (pass-through of BasePriority)
	c.BacklogPriority = 0
	if item.BasePriority > 0 {
		c.BacklogPriority = float64(item.BasePriority)
	}

	// Combine
	multiplicativeCore := c.TierBase * c.DeadlineUrgency * c.GoalAlignment
	additiveBonus := c.UserEngagement + c.EffortBonus + c.MarketSignal +
		c.PreemptionBoost + c.DiscoveryScore + c.BacklogPriority
	penalty := c.StalenessDecay

	score := c.DependencyGate * (multiplicativeCore + additiveBonus - penalty)

	explanation := buildExplanation(item, c, score, now)

	return ScoringResult{
		EffectivePriority: score,
		Explanation:       explanation,
		Components:        c,
	}
}

// preemptionBoost returns +10 per retry, capped at +30.
func preemptionBoost(count int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Min(float64(count)*10, 30)
}

// buildExplanation produces a human-readable reason for the priority score.
func buildExplanation(item WorkItem, c ScoringComponents, score float64, now time.Time) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Tier %d (%s, base %.0f)", item.Tier, tierName(item.Tier), c.TierBase))

	if c.DeadlineUrgency > 1.0 && item.Deadline != nil {
		days := item.Deadline.Sub(now).Hours() / 24
		if days < 0 {
			parts = append(parts, fmt.Sprintf("OVERDUE by %.0f days (%.1fx urgency)", -days, c.DeadlineUrgency))
		} else {
			parts = append(parts, fmt.Sprintf("deadline in %.0f days (%.1fx urgency)", days, c.DeadlineUrgency))
		}
	}
	if c.UserEngagement > 0 {
		parts = append(parts, fmt.Sprintf("user engagement +%.0f", c.UserEngagement))
	}
	if c.EffortBonus > 0 {
		parts = append(parts, fmt.Sprintf("quick win +%.0f (est %.1fh)", c.EffortBonus, safeDeref(item.EffortHours)))
	}
	if c.MarketSignal > 0 {
		parts = append(parts, fmt.Sprintf("market signal +%.0f", c.MarketSignal))
	}
	if c.DiscoveryScore > 0 {
		parts = append(parts, fmt.Sprintf("discovery F×I×U=%.0f", c.DiscoveryScore))
	}
	if c.StalenessDecay > 0 {
		parts = append(parts, fmt.Sprintf("staleness -%.0f", c.StalenessDecay))
	}
	if c.GoalAlignment < 0.5 {
		parts = append(parts, fmt.Sprintf("low alignment %.2f", c.GoalAlignment))
	}
	if c.DependencyGate == 0 {
		parts = append(parts, "BLOCKED by dependency")
	}

	return fmt.Sprintf("Score: %.0f | %s", score, strings.Join(parts, " | "))
}

// tierName returns a human-readable name for a tier.
func tierName(t Tier) string {
	switch t {
	case TierUser:
		return "user"
	case TierRetry:
		return "retry"
	case TierGoalImpl:
		return "goal-impl"
	case TierGoalResearch:
		return "goal-research"
	case TierValidation:
		return "validation"
	case TierTechDebt:
		return "tech-debt"
	case TierSelfImprove:
		return "self-improve"
	case TierExploratory:
		return "exploratory"
	default:
		return "unknown"
	}
}

// safeDeref returns the value of a float64 pointer, or 0 if nil.
func safeDeref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
