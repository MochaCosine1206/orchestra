package priority

import (
	"math"
	"testing"
)

func TestComputeTrustScore_ZeroTotalRuns(t *testing.T) {
	got := ComputeTrustScore(TrustInput{TotalRuns: 0, TestTotalCount: 10, TestPassCount: 10})
	// successRate=0.5, testRate=1.0, overrideRate=0.5 => 0.4*0.5 + 0.3*1.0 + 0.3*0.5 = 0.65
	if math.Abs(got-0.65) > 0.001 {
		t.Errorf("got %f, want 0.65", got)
	}
}

func TestComputeTrustScore_ZeroTestTotal(t *testing.T) {
	got := ComputeTrustScore(TrustInput{TotalRuns: 10, SuccessfulRuns: 10, TestTotalCount: 0})
	// successRate=1.0, testRate=0.5, overrideRate=1.0 => 0.4*1.0 + 0.3*0.5 + 0.3*1.0 = 0.85
	if math.Abs(got-0.85) > 0.001 {
		t.Errorf("got %f, want 0.85", got)
	}
}

func TestComputeTrustScore_BothZero(t *testing.T) {
	got := ComputeTrustScore(TrustInput{})
	// all defaults: 0.4*0.5 + 0.3*0.5 + 0.3*0.5 = 0.5
	if math.Abs(got-0.5) > 0.001 {
		t.Errorf("got %f, want 0.5", got)
	}
}

func TestComputeTrustScore_PerfectScore(t *testing.T) {
	got := ComputeTrustScore(TrustInput{
		TotalRuns:      100,
		SuccessfulRuns: 100,
		TestPassCount:  50,
		TestTotalCount: 50,
		OverrideCount:  0,
	})
	// 0.4*1.0 + 0.3*1.0 + 0.3*1.0 = 1.0
	if math.Abs(got-1.0) > 0.001 {
		t.Errorf("got %f, want 1.0", got)
	}
}

func TestComputeTrustScore_WorstScore(t *testing.T) {
	got := ComputeTrustScore(TrustInput{
		TotalRuns:      10,
		SuccessfulRuns: 0,
		TestPassCount:  0,
		TestTotalCount: 10,
		OverrideCount:  10,
	})
	// 0.4*0 + 0.3*0 + 0.3*(1-1)=0 => 0.0
	if math.Abs(got-0.0) > 0.001 {
		t.Errorf("got %f, want 0.0", got)
	}
}

func TestComputeTrustScore_MixedInputs(t *testing.T) {
	got := ComputeTrustScore(TrustInput{
		TotalRuns:      20,
		SuccessfulRuns: 15,
		TestPassCount:  8,
		TestTotalCount: 10,
		OverrideCount:  2,
	})
	// 0.4*(15/20) + 0.3*(8/10) + 0.3*(1-2/20)
	// 0.4*0.75 + 0.3*0.8 + 0.3*0.9 = 0.3 + 0.24 + 0.27 = 0.81
	if math.Abs(got-0.81) > 0.001 {
		t.Errorf("got %f, want 0.81", got)
	}
}

func TestComputeTrustScore_InRange(t *testing.T) {
	inputs := []TrustInput{
		{TotalRuns: 1, SuccessfulRuns: 0, TestPassCount: 0, TestTotalCount: 1, OverrideCount: 1},
		{TotalRuns: 1, SuccessfulRuns: 1, TestPassCount: 1, TestTotalCount: 1, OverrideCount: 0},
		{TotalRuns: 50, SuccessfulRuns: 25, TestPassCount: 30, TestTotalCount: 40, OverrideCount: 10},
	}
	for i, input := range inputs {
		got := ComputeTrustScore(input)
		if got < 0 || got > 1 {
			t.Errorf("case %d: score %f out of [0,1] range", i, got)
		}
	}
}
