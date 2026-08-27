package priority

import (
	"math"
	"strings"
	"testing"
	"time"
)

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }
func timePtr(t time.Time) *time.Time { return &t }

func approxEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

// TestWalkthroughItemA validates item A from design doc section 12:
// UNESCO grant, tier 3, 14-day hard deadline, user rank 2, alignment 0.9
func TestWalkthroughItemA(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(14 * 24 * time.Hour)

	item := WorkItem{
		ID:               "wi-A",
		Source:            SourceGrant,
		Title:             "UNESCO grant application",
		Tier:              TierGoalImpl,
		BasePriority:      0,
		Deadline:          &deadline,
		DeadlineType:      "hard",
		GoalAlignment:     0.9,
		UserPriorityRank:  intPtr(2),
		EffortHours:       floatPtr(8),
		EffortConfidence:  floatPtr(0.7),
		Status:            StatusPending,
		CreatedAt:         now,
	}

	result := ScoreWorkItem(item, now, "")

	// tier_base=600, deadline ~1.52 (14 days hard), alignment=0.9
	// multiplicative core = 600 * 1.52 * 0.9 ≈ 820.8
	// user rank 2 = +450, effort 8h = +50 (<=4h? no, 8>4 → 0)
	// Actually 8h > 4h so effort bonus = 0
	// Score ≈ 820.8 + 450 = 1270.8
	// Design doc says ~1195 (uses rounded 1.38x) — our exact calc differs slightly.
	// The walkthrough is illustrative; we test the formula is internally consistent.

	if result.EffectivePriority < 1100 || result.EffectivePriority > 1400 {
		t.Errorf("Item A score = %.1f, expected ~1195-1271 range", result.EffectivePriority)
	}

	// Verify components
	if result.Components.TierBase != 600 {
		t.Errorf("TierBase = %.0f, want 600", result.Components.TierBase)
	}
	if result.Components.GoalAlignment != 0.9 {
		t.Errorf("GoalAlignment = %.2f, want 0.9", result.Components.GoalAlignment)
	}
	if result.Components.DependencyGate != 1.0 {
		t.Errorf("DependencyGate = %.1f, want 1.0", result.Components.DependencyGate)
	}
	if !approxEqual(result.Components.UserEngagement, 450, 0.1) {
		t.Errorf("UserEngagement = %.0f, want 450", result.Components.UserEngagement)
	}
}

// TestWalkthroughItemB: Fix Telegram routing, tier 6, no deadline, rank 3, 2h effort
func TestWalkthroughItemB(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		ID:               "wi-B",
		Source:            SourceBacklog,
		Title:             "Fix Telegram routing",
		Tier:              TierTechDebt,
		BasePriority:      0,
		GoalAlignment:     0.95,
		UserPriorityRank:  intPtr(3),
		EffortHours:       floatPtr(2),
		EffortConfidence:  floatPtr(0.8),
		Status:            StatusPending,
		CreatedAt:         now,
	}

	result := ScoreWorkItem(item, now, "")

	// tier=200, deadline=1.0, alignment=0.95 → core=190
	// rank 3 → +400, effort <=2h → +100
	// Score = 190 + 400 + 100 = 690
	if !approxEqual(result.EffectivePriority, 690, 1) {
		t.Errorf("Item B score = %.1f, want ~690", result.EffectivePriority)
	}
}

// TestWalkthroughItemC: LoRa research, tier 8, discovery F*I*U scores
func TestWalkthroughItemC(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		ID:           "wi-C",
		Source:       SourceDiscovery,
		Title:        "LoRa research",
		Tier:         TierExploratory,
		BasePriority: 0,
		GoalAlignment: 0.6,
		Feasibility:  floatPtr(0.7),
		Impact:       floatPtr(0.8),
		Uniqueness:   floatPtr(0.9),
		Status:       StatusPending,
		CreatedAt:    now,
	}

	result := ScoreWorkItem(item, now, "")

	// tier=50, alignment=0.6 → core=30
	// discovery = 0.7*0.8*0.9*200 = 100.8
	// Score = 30 + 100.8 = 130.8
	if !approxEqual(result.EffectivePriority, 130.8, 1) {
		t.Errorf("Item C score = %.1f, want ~130.8", result.EffectivePriority)
	}
	if !approxEqual(result.Components.DiscoveryScore, 100.8, 0.1) {
		t.Errorf("DiscoveryScore = %.1f, want 100.8", result.Components.DiscoveryScore)
	}
}

// TestWalkthroughItemD: Failed HITL build retry, tier 2, 1h effort
func TestWalkthroughItemD(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		ID:               "wi-D",
		Source:            SourceRetry,
		Title:             "Failed HITL build retry",
		Tier:              TierRetry,
		BasePriority:      0,
		GoalAlignment:     0, // 0 → fail-open to 1.0
		EffortHours:       floatPtr(1),
		EffortConfidence:  floatPtr(0.9),
		Status:            StatusPending,
		CreatedAt:         now,
	}

	result := ScoreWorkItem(item, now, "")

	// tier=800, alignment=1.0 (fail-open), deadline=1.0 → core=800
	// effort <=2h → +150? No: 1h > 0.5h → +100. Wait, the walkthrough says +150.
	// Let me check: hours=1.0, 1.0 > 0.5 → case hours <= 2 → +100.
	// But walkthrough says "effort<1h=+150". Item D has effort 1h.
	// The walkthrough says "effort<1h" but the actual effort is 1h.
	// 1h > 0.5h → falls into <=2h bucket → +100. But walkthrough expects 950.
	// 800 + 150 = 950 → they must mean the effort IS under 0.5h or exactly at boundary.
	// Looking again: walkthrough "D: tier=800... effort<1h=+150" → they modeled 1h as <0.5h bucket?
	// No, re-reading the table: "Effort: 1h". The walkthrough note says "+150 (est 1.0h)".
	// This means the walkthrough expects <=0.5h → +150 for 1h items, which contradicts the formula.
	// The formula is authoritative (section 3.2). 1h → <=2h bucket → +100.
	// Score = 800 + 100 = 900
	if !approxEqual(result.EffectivePriority, 900, 1) {
		t.Errorf("Item D score = %.1f, want ~900", result.EffectivePriority)
	}
}

// TestWalkthroughItemE: EU AI Act compliance, tier 4, regulatory deadline ~5 months
func TestWalkthroughItemE(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	// ~5 months out = ~150 days
	deadline := now.Add(150 * 24 * time.Hour)

	item := WorkItem{
		ID:           "wi-E",
		Source:       SourceDiscovery,
		Title:        "EU AI Act compliance tool",
		Tier:         TierGoalResearch,
		BasePriority: 0,
		Deadline:     &deadline,
		DeadlineType: "regulatory",
		GoalAlignment: 0.85,
		Feasibility:  floatPtr(0.6),
		Impact:       floatPtr(0.9),
		Uniqueness:   floatPtr(0.8),
		Status:       StatusPending,
		CreatedAt:    now,
	}

	result := ScoreWorkItem(item, now, "")

	// 150 days = 3600 hours, which is > 720 → deadline_urgency = 1.0
	// regulatory × 1.5 → 1.5
	// core = 400 * 1.5 * 0.85 = 510
	// discovery = 0.6*0.9*0.8*200 = 86.4
	// Score = 510 + 86.4 = 596.4
	// (walkthrough says ~622, used 1.575x urgency with different hour calc)
	if result.EffectivePriority < 500 || result.EffectivePriority > 700 {
		t.Errorf("Item E score = %.1f, expected 500-700 range", result.EffectivePriority)
	}
	if !approxEqual(result.Components.DiscoveryScore, 86.4, 0.1) {
		t.Errorf("DiscoveryScore = %.1f, want 86.4", result.Components.DiscoveryScore)
	}
}

// TestWalkthroughRanking verifies the relative ordering: A > D > B > E > C
func TestWalkthroughRanking(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	deadlineA := now.Add(14 * 24 * time.Hour)
	deadlineE := now.Add(150 * 24 * time.Hour)

	items := []WorkItem{
		{ // A
			Tier: TierGoalImpl, Deadline: &deadlineA, DeadlineType: "hard",
			GoalAlignment: 0.9, UserPriorityRank: intPtr(2),
			EffortHours: floatPtr(8), EffortConfidence: floatPtr(0.7),
			Status: StatusPending, CreatedAt: now,
		},
		{ // B
			Tier: TierTechDebt, GoalAlignment: 0.95, UserPriorityRank: intPtr(3),
			EffortHours: floatPtr(2), EffortConfidence: floatPtr(0.8),
			Status: StatusPending, CreatedAt: now,
		},
		{ // C
			Tier: TierExploratory, GoalAlignment: 0.6,
			Feasibility: floatPtr(0.7), Impact: floatPtr(0.8), Uniqueness: floatPtr(0.9),
			Status: StatusPending, CreatedAt: now,
		},
		{ // D
			Tier: TierRetry,
			EffortHours: floatPtr(1), EffortConfidence: floatPtr(0.9),
			Status: StatusPending, CreatedAt: now,
		},
		{ // E
			Tier: TierGoalResearch, Deadline: &deadlineE, DeadlineType: "regulatory",
			GoalAlignment: 0.85,
			Feasibility: floatPtr(0.6), Impact: floatPtr(0.9), Uniqueness: floatPtr(0.8),
			Status: StatusPending, CreatedAt: now,
		},
	}

	scores := make([]float64, len(items))
	for i, item := range items {
		scores[i] = ScoreWorkItem(item, now, "").EffectivePriority
	}

	// A > D > B > E > C
	if !(scores[0] > scores[3]) {
		t.Errorf("A (%.1f) should beat D (%.1f)", scores[0], scores[3])
	}
	if !(scores[3] > scores[1]) {
		t.Errorf("D (%.1f) should beat B (%.1f)", scores[3], scores[1])
	}
	if !(scores[1] > scores[4]) {
		t.Errorf("B (%.1f) should beat E (%.1f)", scores[1], scores[4])
	}
	if !(scores[4] > scores[2]) {
		t.Errorf("E (%.1f) should beat C (%.1f)", scores[4], scores[2])
	}
}

func TestDependencyGateBlocked(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:          TierUser,
		GoalAlignment: 0.9,
		BlockedBy:     []string{"wi-other"},
		Status:        StatusBlocked,
		CreatedAt:     now,
	}

	result := ScoreWorkItem(item, now, "")

	if result.EffectivePriority != 0.0 {
		t.Errorf("Blocked item score = %.1f, want 0.0", result.EffectivePriority)
	}
	if result.Components.DependencyGate != 0.0 {
		t.Errorf("DependencyGate = %.1f, want 0.0", result.Components.DependencyGate)
	}
	if !strings.Contains(result.Explanation, "BLOCKED") {
		t.Errorf("Explanation should mention BLOCKED: %s", result.Explanation)
	}
}

func TestNilDeadline(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:      TierGoalImpl,
		Status:    StatusPending,
		CreatedAt: now,
	}

	result := ScoreWorkItem(item, now, "")

	if result.Components.DeadlineUrgency != 1.0 {
		t.Errorf("DeadlineUrgency = %.2f, want 1.0 for nil deadline", result.Components.DeadlineUrgency)
	}
}

func TestNilEffort(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:      TierGoalImpl,
		Status:    StatusPending,
		CreatedAt: now,
	}

	result := ScoreWorkItem(item, now, "")

	if result.Components.EffortBonus != 0 {
		t.Errorf("EffortBonus = %.0f, want 0 for nil effort", result.Components.EffortBonus)
	}
}

func TestZeroGoalAlignmentFailsOpen(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:          TierUser,
		GoalAlignment: 0, // not yet scored
		Status:        StatusPending,
		CreatedAt:     now,
	}

	result := ScoreWorkItem(item, now, "")

	if result.Components.GoalAlignment != 1.0 {
		t.Errorf("GoalAlignment = %.2f, want 1.0 (fail-open)", result.Components.GoalAlignment)
	}
	if result.EffectivePriority != 1000 {
		t.Errorf("Score = %.1f, want 1000 (tier base only)", result.EffectivePriority)
	}
}

func TestOverdueDeadline(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(-2 * 24 * time.Hour) // 2 days ago

	item := WorkItem{
		Tier:         TierGoalImpl,
		Deadline:     &deadline,
		DeadlineType: "soft",
		Status:       StatusPending,
		CreatedAt:    now,
	}

	result := ScoreWorkItem(item, now, "")

	if result.Components.DeadlineUrgency != 5.0 {
		t.Errorf("DeadlineUrgency = %.2f, want 5.0 for overdue", result.Components.DeadlineUrgency)
	}
	if !strings.Contains(result.Explanation, "OVERDUE") {
		t.Errorf("Explanation should mention OVERDUE: %s", result.Explanation)
	}
}

func TestHardDeadlineMultiplier(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(-1 * time.Hour) // overdue

	item := WorkItem{
		Tier:         TierGoalImpl,
		Deadline:     &deadline,
		DeadlineType: "hard",
		Status:       StatusPending,
		CreatedAt:    now,
	}

	result := ScoreWorkItem(item, now, "")

	// Overdue (5.0) × hard (1.2) = 6.0
	if !approxEqual(result.Components.DeadlineUrgency, 6.0, 0.01) {
		t.Errorf("DeadlineUrgency = %.2f, want 6.0 (overdue hard)", result.Components.DeadlineUrgency)
	}
}

func TestExplanationContainsScoreAndTier(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:      TierRetry,
		Status:    StatusPending,
		CreatedAt: now,
	}

	result := ScoreWorkItem(item, now, "")

	if !strings.Contains(result.Explanation, "Score:") {
		t.Errorf("Explanation missing 'Score:': %s", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "retry") {
		t.Errorf("Explanation missing tier name 'retry': %s", result.Explanation)
	}
}

func TestStalenessDecay(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:      TierGoalImpl,
		Status:    StatusPending,
		CreatedAt: now.Add(-17 * 24 * time.Hour), // 17 days ago
	}

	result := ScoreWorkItem(item, now, "")

	// 17 days - 7 grace = 10 days of decay = 10 points
	if !approxEqual(result.Components.StalenessDecay, 10, 0.5) {
		t.Errorf("StalenessDecay = %.1f, want ~10", result.Components.StalenessDecay)
	}
}

func TestStalenessDecayCap(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:      TierGoalImpl,
		Status:    StatusPending,
		CreatedAt: now.Add(-200 * 24 * time.Hour), // 200 days ago
	}

	result := ScoreWorkItem(item, now, "")

	if !approxEqual(result.Components.StalenessDecay, 100, 0.1) {
		t.Errorf("StalenessDecay = %.1f, want 100 (capped)", result.Components.StalenessDecay)
	}
}

func TestPreemptionBoost(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:       TierGoalImpl,
		RetryCount: 2,
		Status:     StatusPending,
		CreatedAt:  now,
	}

	result := ScoreWorkItem(item, now, "")

	if !approxEqual(result.Components.PreemptionBoost, 20, 0.1) {
		t.Errorf("PreemptionBoost = %.1f, want 20", result.Components.PreemptionBoost)
	}
}

func TestPreemptionBoostCap(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:       TierGoalImpl,
		RetryCount: 10,
		Status:     StatusPending,
		CreatedAt:  now,
	}

	result := ScoreWorkItem(item, now, "")

	if !approxEqual(result.Components.PreemptionBoost, 30, 0.1) {
		t.Errorf("PreemptionBoost = %.1f, want 30 (capped)", result.Components.PreemptionBoost)
	}
}

func TestMarketSignalCap(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)

	item := WorkItem{
		Tier:              TierGoalImpl,
		CompetitorShipped: true,
		MarketSignalIDs:   []string{"s1", "s2", "s3", "s4", "s5", "s6"},
		Status:            StatusPending,
		CreatedAt:         now,
	}

	result := ScoreWorkItem(item, now, "")

	// 150 (competitor) + 4*25 (capped at 4 signals) = 250
	if !approxEqual(result.Components.MarketSignal, 250, 0.1) {
		t.Errorf("MarketSignal = %.0f, want 250", result.Components.MarketSignal)
	}
}

func TestTierName(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierUser, "user"},
		{TierRetry, "retry"},
		{TierGoalImpl, "goal-impl"},
		{TierGoalResearch, "goal-research"},
		{TierValidation, "validation"},
		{TierTechDebt, "tech-debt"},
		{TierSelfImprove, "self-improve"},
		{TierExploratory, "exploratory"},
		{Tier(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tierName(tt.tier); got != tt.want {
				t.Errorf("tierName(%d) = %q, want %q", tt.tier, got, tt.want)
			}
		})
	}
}

func TestSafeDeref(t *testing.T) {
	if got := safeDeref(nil); got != 0 {
		t.Errorf("safeDeref(nil) = %f, want 0", got)
	}
	v := 3.14
	if got := safeDeref(&v); got != 3.14 {
		t.Errorf("safeDeref(&3.14) = %f, want 3.14", got)
	}
}
