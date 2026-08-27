package monitor

import (
	"testing"
	"time"
)

func TestComputeStallScore_AllZero(t *testing.T) {
	activity := AgentActivity{}
	score := ComputeStallScore(activity, time.Now())
	if score.Composite != 0.0 {
		t.Errorf("expected composite 0.0 for empty activity, got %f", score.Composite)
	}
}

func TestComputeStallScore_CompactionSuppression(t *testing.T) {
	compactedAt := time.Now().Add(-1 * time.Minute) // 1min ago, within 2min window
	activity := AgentActivity{
		CompactedAt: &compactedAt,
		RecentToolCalls: makeRepeatedCalls("Read", "foo.go", 10),
		Errors:          makeRepeatedErrors(0xDEAD, 5),
		ReadWrite:       ReadWriteStats{Reads: 10, Writes: 0, HasEdited: true},
		FileActivity:    FileActivity{RecentModifications: 0, PriorModifications: 10, ExpectedFiles: 5, UniqueFilesTouched: 0},
	}
	score := ComputeStallScore(activity, time.Now())
	if score.Composite != 0.0 {
		t.Errorf("expected composite 0.0 during compaction suppression, got %f", score.Composite)
	}
}

func TestComputeStallScore_CompactionExpired(t *testing.T) {
	compactedAt := time.Now().Add(-3 * time.Minute) // 3min ago, outside 2min window
	activity := AgentActivity{
		CompactedAt:     &compactedAt,
		RecentToolCalls: makeRepeatedCalls("Read", "foo.go", 10),
		ReadWrite:       ReadWriteStats{Reads: 10, Writes: 0, HasEdited: true},
	}
	score := ComputeStallScore(activity, time.Now())
	if score.Composite == 0.0 {
		t.Error("expected non-zero composite after compaction suppression expires")
	}
}

func TestToolRepetition_BelowThreshold(t *testing.T) {
	calls := []ToolCall{
		{Name: "Read", Input: "a.go"},
		{Name: "Read", Input: "b.go"},
		{Name: "Write", Input: "c.go"},
	}
	score := computeToolRepetition(calls)
	if score != 0.0 {
		t.Errorf("expected 0.0 for diverse calls, got %f", score)
	}
}

func TestToolRepetition_ThreeRepeats(t *testing.T) {
	calls := makeRepeatedCalls("Read", "foo.go", 5)
	// 5 identical calls out of 5
	score := computeToolRepetition(calls)
	if score < 0.8 {
		t.Errorf("expected >= 0.8 for 5 identical calls, got %f", score)
	}
}

func TestToolRepetition_AllIdentical(t *testing.T) {
	calls := makeRepeatedCalls("Read", "foo.go", 10)
	score := computeToolRepetition(calls)
	if score != 1.0 {
		t.Errorf("expected 1.0 for all identical calls, got %f", score)
	}
}

func TestToolRepetition_TooFewCalls(t *testing.T) {
	calls := []ToolCall{{Name: "Read", Input: "a.go"}, {Name: "Read", Input: "a.go"}}
	score := computeToolRepetition(calls)
	if score != 0.0 {
		t.Errorf("expected 0.0 for < 3 calls, got %f", score)
	}
}

func TestProgressDelta_NoPrior(t *testing.T) {
	fa := FileActivity{RecentModifications: 5, PriorModifications: 0}
	score := computeProgressDelta(fa)
	if score != 0.0 {
		t.Errorf("expected 0.0 with no prior, got %f", score)
	}
}

func TestProgressDelta_CompleteStop(t *testing.T) {
	fa := FileActivity{RecentModifications: 0, PriorModifications: 10}
	score := computeProgressDelta(fa)
	if score != 1.0 {
		t.Errorf("expected 1.0 for complete stop, got %f", score)
	}
}

func TestProgressDelta_HalfDrop(t *testing.T) {
	fa := FileActivity{RecentModifications: 5, PriorModifications: 10}
	score := computeProgressDelta(fa)
	if score < 0.49 || score > 0.51 {
		t.Errorf("expected ~0.5 for half drop, got %f", score)
	}
}

func TestProgressDelta_Increasing(t *testing.T) {
	fa := FileActivity{RecentModifications: 15, PriorModifications: 10}
	score := computeProgressDelta(fa)
	if score != 0.0 {
		t.Errorf("expected 0.0 for increasing activity, got %f", score)
	}
}

func TestFileCoverage_AllCovered(t *testing.T) {
	fa := FileActivity{UniqueFilesTouched: 5, ExpectedFiles: 5}
	score := computeFileCoverage(fa)
	if score != 0.0 {
		t.Errorf("expected 0.0 for full coverage, got %f", score)
	}
}

func TestFileCoverage_NoneExpected(t *testing.T) {
	fa := FileActivity{UniqueFilesTouched: 3, ExpectedFiles: 0}
	score := computeFileCoverage(fa)
	if score != 0.0 {
		t.Errorf("expected 0.0 when no files expected, got %f", score)
	}
}

func TestFileCoverage_HalfCovered(t *testing.T) {
	fa := FileActivity{UniqueFilesTouched: 2, ExpectedFiles: 4}
	score := computeFileCoverage(fa)
	if score < 0.49 || score > 0.51 {
		t.Errorf("expected ~0.5 for half coverage, got %f", score)
	}
}

func TestErrorRepetition_NoErrors(t *testing.T) {
	score := computeErrorRepetition(nil)
	if score != 0.0 {
		t.Errorf("expected 0.0, got %f", score)
	}
}

func TestErrorRepetition_ThreeRepeats(t *testing.T) {
	errors := makeRepeatedErrors(0xABCD, 3)
	score := computeErrorRepetition(errors)
	if score != 0.8 {
		t.Errorf("expected 0.8 for 3 repeats, got %f", score)
	}
}

func TestErrorRepetition_FiveRepeats(t *testing.T) {
	errors := makeRepeatedErrors(0xABCD, 5)
	score := computeErrorRepetition(errors)
	if score != 1.0 {
		t.Errorf("expected 1.0 for 5 repeats, got %f", score)
	}
}

func TestErrorRepetition_MixedBelowThreshold(t *testing.T) {
	errors := []ErrorEntry{{Hash: 1}, {Hash: 2}, {Hash: 3}, {Hash: 4}}
	score := computeErrorRepetition(errors)
	if score != 0.0 {
		t.Errorf("expected 0.0 for all different errors, got %f", score)
	}
}

func TestReadWriteDrift_NotEdited(t *testing.T) {
	rw := ReadWriteStats{Reads: 10, Writes: 0, HasEdited: false}
	score := computeReadWriteDrift(rw)
	if score != 0.0 {
		t.Errorf("expected 0.0 before first edit, got %f", score)
	}
}

func TestReadWriteDrift_StillWriting(t *testing.T) {
	rw := ReadWriteStats{Reads: 8, Writes: 2, HasEdited: true}
	score := computeReadWriteDrift(rw)
	if score != 0.0 {
		t.Errorf("expected 0.0 while still writing, got %f", score)
	}
}

func TestReadWriteDrift_AllReadsAfterEdit(t *testing.T) {
	rw := ReadWriteStats{Reads: 10, Writes: 0, HasEdited: true}
	score := computeReadWriteDrift(rw)
	if score != 1.0 {
		t.Errorf("expected 1.0 for all reads after edit, got %f", score)
	}
}

func TestDetectPhase_Exploration(t *testing.T) {
	phase := DetectPhase(AgentActivity{HasEdits: false, RunningTests: false})
	if phase != PhaseExploration {
		t.Errorf("expected exploration, got %s", phase)
	}
}

func TestDetectPhase_Implementation(t *testing.T) {
	phase := DetectPhase(AgentActivity{HasEdits: true, RunningTests: false})
	if phase != PhaseImplementation {
		t.Errorf("expected implementation, got %s", phase)
	}
}

func TestDetectPhase_Testing(t *testing.T) {
	phase := DetectPhase(AgentActivity{HasEdits: true, RunningTests: true})
	if phase != PhaseTesting {
		t.Errorf("expected testing, got %s", phase)
	}
}

func TestDetectPhase_TestingTakesPrecedence(t *testing.T) {
	phase := DetectPhase(AgentActivity{HasEdits: false, RunningTests: true})
	if phase != PhaseTesting {
		t.Errorf("expected testing to take precedence over exploration, got %s", phase)
	}
}

func TestPhaseThresholds_Exploration(t *testing.T) {
	tp := PhaseThresholds(PhaseExploration, 0.0)
	if tp.Monitor != 0.85 || tp.Reset != 0.95 {
		t.Errorf("expected 0.85/0.95, got %f/%f", tp.Monitor, tp.Reset)
	}
}

func TestPhaseThresholds_Implementation(t *testing.T) {
	tp := PhaseThresholds(PhaseImplementation, 0.0)
	if tp.Monitor != 0.70 || tp.Reset != 0.85 {
		t.Errorf("expected 0.70/0.85, got %f/%f", tp.Monitor, tp.Reset)
	}
}

func TestPhaseThresholds_Testing(t *testing.T) {
	tp := PhaseThresholds(PhaseTesting, 0.0)
	if tp.Monitor != 0.80 || tp.Reset != 0.90 {
		t.Errorf("expected 0.80/0.90, got %f/%f", tp.Monitor, tp.Reset)
	}
}

func TestPhaseThresholds_TierAdjustment(t *testing.T) {
	const eps = 1e-9

	// Tier 1 (tighter): -0.05
	tp := PhaseThresholds(PhaseImplementation, -0.05)
	if floatAbs(tp.Monitor-0.65) > eps || floatAbs(tp.Reset-0.80) > eps {
		t.Errorf("expected 0.65/0.80 for tier1, got %f/%f", tp.Monitor, tp.Reset)
	}

	// Tier 3 (looser): +0.05
	tp = PhaseThresholds(PhaseImplementation, 0.05)
	if floatAbs(tp.Monitor-0.75) > eps || floatAbs(tp.Reset-0.90) > eps {
		t.Errorf("expected 0.75/0.90 for tier3, got %f/%f", tp.Monitor, tp.Reset)
	}
}

func floatAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestSustainedStallCheck_EmptyHistory(t *testing.T) {
	mon, rst := SustainedStallCheck(nil, 0.70, 0.85, time.Now())
	if mon || rst {
		t.Error("expected false/false for empty history")
	}
}

func TestSustainedStallCheck_MonitorTrigger(t *testing.T) {
	now := time.Now()
	// 3 minutes of scores above monitor threshold (0.70) — covers 2min sustained
	history := []StallRecord{
		{Score: 0.75, Timestamp: now.Add(-3 * time.Minute)},
		{Score: 0.80, Timestamp: now.Add(-2 * time.Minute)},
		{Score: 0.78, Timestamp: now.Add(-1 * time.Minute)},
		{Score: 0.76, Timestamp: now},
	}
	mon, rst := SustainedStallCheck(history, 0.70, 0.85, now)
	if !mon {
		t.Error("expected monitor=true for sustained stall above 0.70")
	}
	if rst {
		t.Error("expected reset=false — scores below 0.85")
	}
}

func TestSustainedStallCheck_ResetTrigger(t *testing.T) {
	now := time.Now()
	// 6 minutes of scores above reset threshold (0.85) — covers 5min sustained
	history := []StallRecord{
		{Score: 0.90, Timestamp: now.Add(-6 * time.Minute)},
		{Score: 0.92, Timestamp: now.Add(-5 * time.Minute)},
		{Score: 0.88, Timestamp: now.Add(-4 * time.Minute)},
		{Score: 0.91, Timestamp: now.Add(-3 * time.Minute)},
		{Score: 0.89, Timestamp: now.Add(-2 * time.Minute)},
		{Score: 0.93, Timestamp: now.Add(-1 * time.Minute)},
		{Score: 0.90, Timestamp: now},
	}
	mon, rst := SustainedStallCheck(history, 0.70, 0.85, now)
	if !mon {
		t.Error("expected monitor=true")
	}
	if !rst {
		t.Error("expected reset=true for sustained stall above 0.85 for 5min")
	}
}

func TestSustainedStallCheck_TransientSpike(t *testing.T) {
	now := time.Now()
	// Score dips below threshold in the middle — not sustained
	history := []StallRecord{
		{Score: 0.90, Timestamp: now.Add(-3 * time.Minute)},
		{Score: 0.50, Timestamp: now.Add(-2 * time.Minute)}, // dip
		{Score: 0.90, Timestamp: now.Add(-1 * time.Minute)},
		{Score: 0.90, Timestamp: now},
	}
	mon, _ := SustainedStallCheck(history, 0.70, 0.85, now)
	if mon {
		t.Error("expected monitor=false — transient dip breaks sustained stall")
	}
}

func TestSustainedStallCheck_InsufficientHistory(t *testing.T) {
	now := time.Now()
	// Only 1min of history — not enough for 2min sustained
	history := []StallRecord{
		{Score: 0.90, Timestamp: now.Add(-1 * time.Minute)},
		{Score: 0.90, Timestamp: now},
	}
	mon, rst := SustainedStallCheck(history, 0.70, 0.85, now)
	if mon {
		t.Error("expected monitor=false — insufficient history")
	}
	if rst {
		t.Error("expected reset=false — insufficient history")
	}
}

func TestComputeStallScore_FullSignals(t *testing.T) {
	activity := AgentActivity{
		RecentToolCalls: makeRepeatedCalls("Read", "foo.go", 10),
		FileActivity:    FileActivity{RecentModifications: 0, PriorModifications: 10, ExpectedFiles: 5, UniqueFilesTouched: 0},
		Errors:          makeRepeatedErrors(0xDEAD, 5),
		ReadWrite:       ReadWriteStats{Reads: 10, Writes: 0, HasEdited: true},
		HasEdits:        true,
	}
	score := ComputeStallScore(activity, time.Now())

	// All signals should be at or near 1.0
	if score.ToolRepetition < 0.8 {
		t.Errorf("expected ToolRepetition >= 0.8, got %f", score.ToolRepetition)
	}
	if score.ProgressDelta != 1.0 {
		t.Errorf("expected ProgressDelta 1.0, got %f", score.ProgressDelta)
	}
	if score.FileCoverage != 1.0 {
		t.Errorf("expected FileCoverage 1.0, got %f", score.FileCoverage)
	}
	if score.ErrorRepetition < 0.8 {
		t.Errorf("expected ErrorRepetition >= 0.8, got %f", score.ErrorRepetition)
	}
	if score.ReadWriteDrift != 1.0 {
		t.Errorf("expected ReadWriteDrift 1.0, got %f", score.ReadWriteDrift)
	}
	if score.Composite < 0.9 {
		t.Errorf("expected composite >= 0.9 with all signals high, got %f", score.Composite)
	}
}

func TestComputeStallScore_WeightsSum(t *testing.T) {
	total := weightToolRepetition + weightProgressDelta + weightFileCoverage + weightErrorRepetition + weightReadWriteDrift
	if total < 0.999 || total > 1.001 {
		t.Errorf("weights should sum to 1.0, got %f", total)
	}
}

func TestFingerprintToolCall(t *testing.T) {
	h1 := FingerprintToolCall("Read", "foo.go")
	h2 := FingerprintToolCall("Read", "foo.go")
	h3 := FingerprintToolCall("Read", "bar.go")

	if h1 != h2 {
		t.Error("identical inputs should produce identical hashes")
	}
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestFingerprintError(t *testing.T) {
	h1 := FingerprintError("file not found")
	h2 := FingerprintError("file not found")
	h3 := FingerprintError("permission denied")

	if h1 != h2 {
		t.Error("identical errors should produce identical hashes")
	}
	if h1 == h3 {
		t.Error("different errors should produce different hashes")
	}
}

// --- rabbit-hole detection tests ---

func TestDetectRabbitHole_FiresWhenThresholdCrossed(t *testing.T) {
	// 25 tool calls: 24 Reads + 1 Write = ratio 1/25 = 0.04 (< 0.1)
	calls := makeRepeatedCalls("Read", "foo.go", 24)
	calls = append(calls, ToolCall{Name: "Write", Input: "bar.go"})
	rh := DetectRabbitHole(calls)
	if !rh.Detected {
		t.Errorf("expected rabbit-hole detected with ratio %.4f, got detected=false", rh.Ratio)
	}
	if rh.CallCount != 25 {
		t.Errorf("expected CallCount=25, got %d", rh.CallCount)
	}
}

func TestDetectRabbitHole_DoesNotFireAboveThreshold(t *testing.T) {
	// 20 tool calls: 17 Reads + 3 Writes to unique files = ratio 3/20 = 0.15 (> 0.1)
	calls := makeRepeatedCalls("Read", "foo.go", 17)
	calls = append(calls, ToolCall{Name: "Write", Input: "a.go"})
	calls = append(calls, ToolCall{Name: "Edit", Input: "b.go"})
	calls = append(calls, ToolCall{Name: "Write", Input: "c.go"})
	rh := DetectRabbitHole(calls)
	if rh.Detected {
		t.Errorf("expected no rabbit-hole with ratio %.4f (above 0.1), got detected=true", rh.Ratio)
	}
}

func TestDetectRabbitHole_DoesNotFireBelowMinCalls(t *testing.T) {
	// 15 tool calls with 0 writes = ratio 0.0, but below 20-call minimum
	calls := makeRepeatedCalls("Read", "foo.go", 15)
	rh := DetectRabbitHole(calls)
	if rh.Detected {
		t.Errorf("expected no rabbit-hole below 20 tool calls, got detected=true")
	}
}

// --- helpers ---

func makeRepeatedCalls(name, input string, n int) []ToolCall {
	calls := make([]ToolCall, n)
	for i := range calls {
		calls[i] = ToolCall{Name: name, Input: input}
	}
	return calls
}

func makeRepeatedErrors(hash uint64, n int) []ErrorEntry {
	errors := make([]ErrorEntry, n)
	for i := range errors {
		errors[i] = ErrorEntry{Hash: hash}
	}
	return errors
}
