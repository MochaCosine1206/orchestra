package priority

import (
	"math"
	"testing"
	"time"
)

func TestAgingBoost_ZeroDuration(t *testing.T) {
	got := AgingBoost(0)
	if got != 0 {
		t.Errorf("AgingBoost(0) = %f, want 0", got)
	}
}

func TestAgingBoost_OneHour(t *testing.T) {
	got := AgingBoost(time.Hour)
	if got != 1.0 {
		t.Errorf("AgingBoost(1h) = %f, want 1.0", got)
	}
}

func TestAgingBoost_FiftyHours(t *testing.T) {
	got := AgingBoost(50 * time.Hour)
	if got != 50.0 {
		t.Errorf("AgingBoost(50h) = %f, want 50.0", got)
	}
}

func TestAgingBoost_CapAt50(t *testing.T) {
	got := AgingBoost(100 * time.Hour)
	if got != 50.0 {
		t.Errorf("AgingBoost(100h) = %f, want 50.0 (capped)", got)
	}
}

func TestAgingBoost_NegativeDuration(t *testing.T) {
	got := AgingBoost(-time.Hour)
	if got != 0 {
		t.Errorf("AgingBoost(-1h) = %f, want 0", got)
	}
}

func TestAgingBoost_FractionalHours(t *testing.T) {
	got := AgingBoost(30 * time.Minute)
	if got != 0.5 {
		t.Errorf("AgingBoost(30m) = %f, want 0.5", got)
	}
}

func TestActivityBoost_Zero(t *testing.T) {
	got := ActivityBoost(0)
	if got != 0 {
		t.Errorf("ActivityBoost(0) = %f, want 0", got)
	}
}

func TestActivityBoost_One(t *testing.T) {
	got := ActivityBoost(1)
	if got != 5.0 {
		t.Errorf("ActivityBoost(1) = %f, want 5.0", got)
	}
}

func TestActivityBoost_Five(t *testing.T) {
	got := ActivityBoost(5)
	if got != 25.0 {
		t.Errorf("ActivityBoost(5) = %f, want 25.0 (at cap)", got)
	}
}

func TestActivityBoost_CapAt25(t *testing.T) {
	got := ActivityBoost(10)
	if got != 25.0 {
		t.Errorf("ActivityBoost(10) = %f, want 25.0 (capped)", got)
	}
}

func TestActivityBoost_Negative(t *testing.T) {
	got := ActivityBoost(-3)
	if got != 0 {
		t.Errorf("ActivityBoost(-3) = %f, want 0", got)
	}
}

func TestPreemptionBoost_Zero(t *testing.T) {
	got := PreemptionBoost(0)
	if got != 0 {
		t.Errorf("PreemptionBoost(0) = %f, want 0", got)
	}
}

func TestPreemptionBoost_One(t *testing.T) {
	got := PreemptionBoost(1)
	if got != 10.0 {
		t.Errorf("PreemptionBoost(1) = %f, want 10.0", got)
	}
}

func TestPreemptionBoost_Three(t *testing.T) {
	got := PreemptionBoost(3)
	if got != 30.0 {
		t.Errorf("PreemptionBoost(3) = %f, want 30.0 (at cap)", got)
	}
}

func TestPreemptionBoost_CapAt30(t *testing.T) {
	got := PreemptionBoost(5)
	if got != 30.0 {
		t.Errorf("PreemptionBoost(5) = %f, want 30.0 (capped)", got)
	}
}

func TestPreemptionBoost_Negative(t *testing.T) {
	got := PreemptionBoost(-1)
	if got != 0 {
		t.Errorf("PreemptionBoost(-1) = %f, want 0", got)
	}
}

func TestComputeEffectivePriority_Basic(t *testing.T) {
	now := time.Now()
	input := PriorityInput{
		BasePriority:    10,
		GoalAlignment:   1.0,
		Urgency:         1.0,
		CreatedAt:       now,
		ActivityCount:   0,
		PreemptionCount: 0,
	}
	got := ComputeEffectivePriority(input)
	// base=10*1*1=10, aging~0, activity=0, preemption=0
	if got < 10.0 || got > 10.1 {
		t.Errorf("ComputeEffectivePriority = %f, want ~10.0", got)
	}
}

func TestComputeEffectivePriority_WithAllBoosts(t *testing.T) {
	input := PriorityInput{
		BasePriority:    5,
		GoalAlignment:   0.8,
		Urgency:         1.5,
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		ActivityCount:   3,
		PreemptionCount: 2,
	}
	got := ComputeEffectivePriority(input)
	// base=5*0.8*1.5=6, aging~2, activity=15, preemption=20 => ~43
	base := 5.0 * 0.8 * 1.5
	expected := base + 2.0 + 15.0 + 20.0
	if math.Abs(got-expected) > 0.1 {
		t.Errorf("ComputeEffectivePriority = %f, want ~%f", got, expected)
	}
}

func TestComputeEffectivePriority_ZeroBase(t *testing.T) {
	input := PriorityInput{
		BasePriority:    0,
		GoalAlignment:   1.0,
		Urgency:         1.0,
		CreatedAt:       time.Now(),
		ActivityCount:   1,
		PreemptionCount: 1,
	}
	got := ComputeEffectivePriority(input)
	// base=0, aging~0, activity=5, preemption=10 => ~15
	if got < 14.9 || got > 15.1 {
		t.Errorf("ComputeEffectivePriority = %f, want ~15.0", got)
	}
}
