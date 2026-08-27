package priority

// TrustInput holds the raw inputs for computing a trust score.
type TrustInput struct {
	TotalRuns      int
	SuccessfulRuns int
	TestPassCount  int
	TestTotalCount int
	OverrideCount  int
}

// ComputeTrustScore returns a trust score in [0.0, 1.0].
// Formula: 0.4*(successful/total) + 0.3*(test_pass/test_total) + 0.3*(1 - override/total)
// Returns 0.5 default for components where division by zero would occur.
func ComputeTrustScore(input TrustInput) float64 {
	var successRate, testRate, overrideRate float64

	if input.TotalRuns == 0 {
		successRate = 0.5
		overrideRate = 0.5
	} else {
		successRate = float64(input.SuccessfulRuns) / float64(input.TotalRuns)
		overrideRate = 1.0 - float64(input.OverrideCount)/float64(input.TotalRuns)
	}

	if input.TestTotalCount == 0 {
		testRate = 0.5
	} else {
		testRate = float64(input.TestPassCount) / float64(input.TestTotalCount)
	}

	score := 0.4*successRate + 0.3*testRate + 0.3*overrideRate

	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
