package eval

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// ABTestConfig configures an A/B test comparison between two versions.
type ABTestConfig struct {
	BaselineVersion   string
	CandidateVersion  string
	Scenarios         []Scenario
	Runner            orchestrator.ClaudeRunner
	Transcript        TranscriptFunc // optional: provides real transcripts; falls back to SyntheticTranscript
	NBootstrap        int            // number of bootstrap resamples (default: 10000)
	RegressionMaxDrop float64        // max allowed regression per metric (default: 0.05)
}

// ABTestResult holds the outcome of an A/B comparison.
type ABTestResult struct {
	BaselineVersion   string
	CandidateVersion  string
	BaselineScores    []float64
	CandidateScores   []float64
	BaselineMean      float64
	CandidateMean     float64
	Delta             float64
	BootstrapCI       [2]float64 // 95% CI of delta
	WilcoxonP         float64
	Verdict           string // "improved", "regressed", "no_change"
	ScenarioResults   []ABScenarioResult
}

// ABScenarioResult holds per-scenario results for both versions.
type ABScenarioResult struct {
	ScenarioID     string
	BaselineScore  float64
	CandidateScore float64
	BaselinePass   bool
	CandidatePass  bool
}

// RunABTest executes paired evaluation of two agent definition versions.
// For each scenario: judge baseline transcript, judge candidate transcript (blinded).
// Computes bootstrap CI and Wilcoxon signed-rank test.
func RunABTest(ctx context.Context, config ABTestConfig) (*ABTestResult, error) {
	if config.Runner == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	if len(config.Scenarios) == 0 {
		return nil, fmt.Errorf("no scenarios provided")
	}

	nBootstrap := config.NBootstrap
	if nBootstrap <= 0 {
		nBootstrap = 10000
	}
	regressionMax := config.RegressionMaxDrop
	if regressionMax <= 0 {
		regressionMax = 0.05
	}

	var (
		baselineScores  []float64
		candidateScores []float64
		scenarioResults []ABScenarioResult
	)

	for _, scenario := range config.Scenarios {
		rubric := RubricForRole(scenario.Role)

		// Get transcripts: use provided TranscriptFunc or fall back to synthetic.
		transcriptFn := config.Transcript
		if transcriptFn == nil {
			transcriptFn = SyntheticTranscript
		}
		baselineTranscript := transcriptFn(scenario, config.BaselineVersion)
		candidateTranscript := transcriptFn(scenario, config.CandidateVersion)

		baseResult, err := Judge(ctx, config.Runner, scenario, baselineTranscript, rubric)
		if err != nil {
			return nil, fmt.Errorf("judging baseline for scenario %s: %w", scenario.ID, err)
		}

		candResult, err := Judge(ctx, config.Runner, scenario, candidateTranscript, rubric)
		if err != nil {
			return nil, fmt.Errorf("judging candidate for scenario %s: %w", scenario.ID, err)
		}

		baselineScores = append(baselineScores, baseResult.Composite)
		candidateScores = append(candidateScores, candResult.Composite)
		scenarioResults = append(scenarioResults, ABScenarioResult{
			ScenarioID:     scenario.ID,
			BaselineScore:  baseResult.Composite,
			CandidateScore: candResult.Composite,
			BaselinePass:   baseResult.Pass,
			CandidatePass:  candResult.Pass,
		})
	}

	// Statistical analysis
	ci := Bootstrap(baselineScores, candidateScores, nBootstrap)
	pValue := WilcoxonSignedRank(baselineScores, candidateScores)

	baseMean := mean(baselineScores)
	candMean := mean(candidateScores)
	delta := candMean - baseMean

	// Verdict
	verdict := "no_change"
	if pValue < 0.05 && delta > 0 && ci[0] > 0 {
		verdict = "improved"
	} else if pValue < 0.05 && delta < -regressionMax {
		verdict = "regressed"
	}

	return &ABTestResult{
		BaselineVersion:   config.BaselineVersion,
		CandidateVersion:  config.CandidateVersion,
		BaselineScores:    baselineScores,
		CandidateScores:   candidateScores,
		BaselineMean:      baseMean,
		CandidateMean:     candMean,
		Delta:             delta,
		BootstrapCI:       ci,
		WilcoxonP:         pValue,
		Verdict:           verdict,
		ScenarioResults:   scenarioResults,
	}, nil
}

// Bootstrap computes a 95% confidence interval for the mean difference
// (candidate - baseline) via resampling.
func Bootstrap(baseline, candidate []float64, nResamples int) [2]float64 {
	if len(baseline) == 0 || len(candidate) == 0 {
		return [2]float64{0, 0}
	}
	n := len(baseline)
	if n != len(candidate) {
		// Use the shorter length
		if len(candidate) < n {
			n = len(candidate)
		}
	}

	deltas := make([]float64, nResamples)
	for i := 0; i < nResamples; i++ {
		var bSum, cSum float64
		for j := 0; j < n; j++ {
			idx := rand.Intn(n)
			bSum += baseline[idx]
			cSum += candidate[idx]
		}
		deltas[i] = (cSum - bSum) / float64(n)
	}

	sort.Float64s(deltas)
	lower := deltas[int(float64(nResamples)*0.025)]
	upper := deltas[int(float64(nResamples)*0.975)]
	return [2]float64{lower, upper}
}

// WilcoxonSignedRank computes an approximate p-value for a paired comparison
// using the Wilcoxon signed-rank test. For small samples (n < 10), uses exact
// enumeration would be ideal but we use a normal approximation for simplicity.
func WilcoxonSignedRank(baseline, candidate []float64) float64 {
	n := len(baseline)
	if n != len(candidate) {
		if len(candidate) < n {
			n = len(candidate)
		}
	}
	if n == 0 {
		return 1.0
	}

	// Compute differences and their absolute values
	type diffRank struct {
		absDiff float64
		sign    float64
	}
	var diffs []diffRank
	for i := 0; i < n; i++ {
		d := candidate[i] - baseline[i]
		if d == 0 {
			continue // exclude ties at zero
		}
		sign := 1.0
		if d < 0 {
			sign = -1.0
		}
		diffs = append(diffs, diffRank{absDiff: math.Abs(d), sign: sign})
	}

	if len(diffs) == 0 {
		return 1.0 // no differences
	}

	// Sort by absolute difference for ranking
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].absDiff < diffs[j].absDiff
	})

	// Assign ranks (average for ties)
	ranks := make([]float64, len(diffs))
	i := 0
	for i < len(diffs) {
		j := i + 1
		for j < len(diffs) && diffs[j].absDiff == diffs[i].absDiff {
			j++
		}
		avgRank := float64(i+1+j) / 2.0
		for k := i; k < j; k++ {
			ranks[k] = avgRank
		}
		i = j
	}

	// Compute W+ (sum of ranks for positive differences)
	wPlus := 0.0
	for i, d := range diffs {
		if d.sign > 0 {
			wPlus += ranks[i]
		}
	}

	// Normal approximation for p-value
	nn := float64(len(diffs))
	meanW := nn * (nn + 1) / 4.0
	varW := nn * (nn + 1) * (2*nn + 1) / 24.0
	if varW == 0 {
		return 1.0
	}

	z := (wPlus - meanW) / math.Sqrt(varW)
	// Two-tailed p-value using normal approximation
	p := 2.0 * normalCDF(-math.Abs(z))
	return p
}

// normalCDF approximates the cumulative distribution function of the standard normal.
func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// mean computes the arithmetic mean of a float slice.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
