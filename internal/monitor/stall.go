package monitor

import (
	"hash/fnv"
	"time"
)

// Phase represents the detected activity phase of an agent.
type Phase string

const (
	PhaseExploration    Phase = "exploration"
	PhaseImplementation Phase = "implementation"
	PhaseTesting        Phase = "testing"
)

// ThresholdPair holds the monitor and reset thresholds for a phase.
type ThresholdPair struct {
	Monitor float64
	Reset   float64
}

// StallScore holds the individual signal scores and the composite score.
type StallScore struct {
	ToolRepetition  float64 // 30% weight — tool call fingerprint repetition
	ProgressDelta   float64 // 25% weight — file modification rate delta
	FileCoverage    float64 // 20% weight — unique files touched / expected files
	ErrorRepetition float64 // 15% weight — repeated error hash count
	ReadWriteDrift  float64 // 10% weight — read-heavy with no writes after first edit
	Composite       float64 // weighted combination of the 5 signals
	Timestamp       time.Time
}

// Signal weights for the composite stall score.
const (
	weightToolRepetition  = 0.30
	weightProgressDelta   = 0.25
	weightFileCoverage    = 0.20
	weightErrorRepetition = 0.15
	weightReadWriteDrift  = 0.10
)

// ToolCall represents a single tool invocation for fingerprint analysis.
type ToolCall struct {
	Name  string
	Input string // truncated input
}

// FileActivity records file modification counts across time windows.
type FileActivity struct {
	RecentModifications int // file modifications in last 5min
	PriorModifications  int // file modifications in 5-10min ago window
	UniqueFilesTouched  int // distinct files modified so far
	ExpectedFiles       int // files from task spec
}

// ErrorEntry represents a parsed error with its hash.
type ErrorEntry struct {
	Hash uint64
}

// ReadWriteStats tracks read vs write tool call counts.
type ReadWriteStats struct {
	Reads     int
	Writes    int
	HasEdited bool // whether the agent has made any edit at all
}

// AgentActivity aggregates all signals needed for stall scoring.
type AgentActivity struct {
	RecentToolCalls []ToolCall    // last 10 tool calls
	FileActivity    FileActivity  // file modification data
	Errors          []ErrorEntry  // recent error entries
	ReadWrite       ReadWriteStats
	HasEdits        bool   // agent has made file edits
	RunningTests    bool   // agent is executing test commands
	CompactedAt     *time.Time // last compaction event time
}

// ComputeStallScore calculates the composite stall score from agent activity signals.
// Each signal produces a 0.0-1.0 score; the composite is the weighted sum.
// If a compaction event occurred within the last 2 minutes, all signals are suppressed (score=0).
func ComputeStallScore(activity AgentActivity, now time.Time) StallScore {
	score := StallScore{Timestamp: now}

	// Suppress stall signals for 2min after compaction
	if activity.CompactedAt != nil && now.Sub(*activity.CompactedAt) < 2*time.Minute {
		return score
	}

	score.ToolRepetition = computeToolRepetition(activity.RecentToolCalls)
	score.ProgressDelta = computeProgressDelta(activity.FileActivity)
	score.FileCoverage = computeFileCoverage(activity.FileActivity)
	score.ErrorRepetition = computeErrorRepetition(activity.Errors)
	score.ReadWriteDrift = computeReadWriteDrift(activity.ReadWrite)

	score.Composite = weightToolRepetition*score.ToolRepetition +
		weightProgressDelta*score.ProgressDelta +
		weightFileCoverage*score.FileCoverage +
		weightErrorRepetition*score.ErrorRepetition +
		weightReadWriteDrift*score.ReadWriteDrift

	return score
}

// computeToolRepetition scores tool call fingerprint repetition.
// FNV-1a hashes of toolName+truncatedInput over last 10 calls.
// Score is high when 3+ identical hashes are found.
func computeToolRepetition(calls []ToolCall) float64 {
	if len(calls) < 3 {
		return 0.0
	}

	freq := make(map[uint64]int)
	for _, c := range calls {
		h := fnv.New64a()
		h.Write([]byte(c.Name))
		h.Write([]byte(c.Input))
		freq[h.Sum64()]++
	}

	maxFreq := 0
	for _, count := range freq {
		if count > maxFreq {
			maxFreq = count
		}
	}

	if maxFreq < 3 {
		return 0.0
	}

	// Scale: 3 repeats = 0.8, 4+ = 0.9, all identical = 1.0
	n := len(calls)
	if maxFreq >= n {
		return 1.0
	}
	if maxFreq >= n-1 {
		return 0.95
	}
	if maxFreq >= 4 {
		return 0.9
	}
	return 0.8
}

// computeProgressDelta scores the drop in file modification rate.
// Compares recent 5min window to prior 5min window.
// High score when recent activity is much lower than prior.
func computeProgressDelta(fa FileActivity) float64 {
	if fa.PriorModifications == 0 {
		// No prior activity — can't measure a drop
		return 0.0
	}

	ratio := float64(fa.RecentModifications) / float64(fa.PriorModifications)
	if ratio >= 1.0 {
		return 0.0 // activity maintained or increased
	}
	if ratio <= 0.0 {
		return 1.0 // complete stop
	}
	// Linear: 50% drop = 0.5, 100% drop = 1.0
	return 1.0 - ratio
}

// computeFileCoverage scores how many expected files remain untouched.
// High score means low coverage of expected files.
func computeFileCoverage(fa FileActivity) float64 {
	if fa.ExpectedFiles <= 0 {
		return 0.0 // no expectations, no penalty
	}

	coverage := float64(fa.UniqueFilesTouched) / float64(fa.ExpectedFiles)
	if coverage >= 1.0 {
		return 0.0 // all expected files touched
	}
	// Invert: low coverage = high stall signal
	return 1.0 - coverage
}

// computeErrorRepetition scores repeated error patterns.
// Score is high when the same error hash appears 3+ times.
func computeErrorRepetition(errors []ErrorEntry) float64 {
	if len(errors) < 3 {
		return 0.0
	}

	freq := make(map[uint64]int)
	for _, e := range errors {
		freq[e.Hash]++
	}

	maxFreq := 0
	for _, count := range freq {
		if count > maxFreq {
			maxFreq = count
		}
	}

	if maxFreq < 3 {
		return 0.0
	}

	// 3 repeats = 0.8, 5+ = 1.0
	if maxFreq >= 5 {
		return 1.0
	}
	if maxFreq >= 4 {
		return 0.9
	}
	return 0.8
}

// computeReadWriteDrift scores read-heavy activity with no writes after first edit.
// >=60% reads with zero writes after the agent has started editing = stall signal.
func computeReadWriteDrift(rw ReadWriteStats) float64 {
	if !rw.HasEdited {
		return 0.0 // hasn't started editing yet — reads are expected
	}
	total := rw.Reads + rw.Writes
	if total == 0 {
		return 0.0
	}
	if rw.Writes > 0 {
		return 0.0 // still writing — no drift
	}
	readRatio := float64(rw.Reads) / float64(total)
	if readRatio >= 0.6 {
		return 1.0 // all reads, no writes after having edited
	}
	return 0.0
}

// DetectPhase returns the current activity phase based on agent behavior.
func DetectPhase(activity AgentActivity) Phase {
	if activity.RunningTests {
		return PhaseTesting
	}
	if activity.HasEdits {
		return PhaseImplementation
	}
	return PhaseExploration
}

// PhaseThresholds returns the monitor/reset threshold pair for the given phase.
// tierAdj applies a ±0.05 adjustment: negative for tighter (Tier 1), positive for looser (Tier 3).
// tierAdj should be one of -0.05, 0.0, or 0.05.
func PhaseThresholds(phase Phase, tierAdj float64) ThresholdPair {
	var base ThresholdPair

	switch phase {
	case PhaseExploration:
		base = ThresholdPair{Monitor: 0.85, Reset: 0.95}
	case PhaseImplementation:
		base = ThresholdPair{Monitor: 0.70, Reset: 0.85}
	case PhaseTesting:
		base = ThresholdPair{Monitor: 0.80, Reset: 0.90}
	default:
		base = ThresholdPair{Monitor: 0.70, Reset: 0.85}
	}

	return ThresholdPair{
		Monitor: base.Monitor + tierAdj,
		Reset:   base.Reset + tierAdj,
	}
}

// StallRecord is a timestamped stall score used for sustained stall checking.
type StallRecord struct {
	Score     float64
	Timestamp time.Time
}

// SustainedStallCheck determines whether the composite stall score has exceeded
// a threshold for a sustained duration. Returns true for monitor level (2min sustained)
// or reset level (5min sustained).
//
// monitorThreshold and resetThreshold come from PhaseThresholds.
// history should be ordered oldest-first.
func SustainedStallCheck(history []StallRecord, monitorThreshold, resetThreshold float64, now time.Time) (monitor bool, reset bool) {
	if len(history) == 0 {
		return false, false
	}

	monitorDuration := 2 * time.Minute
	resetDuration := 5 * time.Minute

	monitor = checkSustained(history, monitorThreshold, monitorDuration, now)
	reset = checkSustained(history, resetThreshold, resetDuration, now)
	return
}

// checkSustained returns true if all records within the last `duration` from `now`
// exceed the threshold, AND there is at least one record old enough to cover the full duration.
func checkSustained(history []StallRecord, threshold float64, duration time.Duration, now time.Time) bool {
	cutoff := now.Add(-duration)

	var hasOldEnough bool
	for _, r := range history {
		if r.Timestamp.Before(cutoff) || r.Timestamp.Equal(cutoff) {
			hasOldEnough = true
		}
	}
	if !hasOldEnough {
		return false // not enough history to confirm sustained stall
	}

	// Check that every record in the window exceeds the threshold
	for _, r := range history {
		if r.Timestamp.Before(cutoff) {
			continue // before our window
		}
		if r.Score < threshold {
			return false // dip below threshold breaks sustained stall
		}
	}
	return true
}

// FingerprintToolCall computes an FNV-1a hash fingerprint for a tool call.
func FingerprintToolCall(name, input string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	h.Write([]byte(input))
	return h.Sum64()
}

// FingerprintError computes an FNV-1a hash for an error message.
func FingerprintError(errMsg string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(errMsg))
	return h.Sum64()
}

// RabbitHoleResult holds the outcome of a rabbit-hole detection check.
type RabbitHoleResult struct {
	Detected  bool
	Ratio     float64
	CallCount int
}

// DetectRabbitHole checks whether tool call activity indicates rabbit-hole behavior.
// Returns detected=true when there are 20+ tool calls and the ratio of unique
// Write/Edit file targets to total calls is below 0.1.
func DetectRabbitHole(calls []ToolCall) RabbitHoleResult {
	n := len(calls)
	if n < 20 {
		return RabbitHoleResult{CallCount: n}
	}

	uniqueFiles := make(map[string]struct{})
	for _, tc := range calls {
		if tc.Name == "Write" || tc.Name == "Edit" {
			uniqueFiles[tc.Input] = struct{}{}
		}
	}

	ratio := float64(len(uniqueFiles)) / float64(n)
	return RabbitHoleResult{
		Detected:  ratio < 0.1,
		Ratio:     ratio,
		CallCount: n,
	}
}
