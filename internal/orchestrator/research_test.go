package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// makePlanResponse returns a valid JSON string for a ResearchPlan with n sub-questions.
func makePlanResponse(n int) string {
	plan := ResearchPlan{}
	for i := 1; i <= n; i++ {
		plan.SubQuestions = append(plan.SubQuestions, SubQuestion{
			ID:               fmt.Sprintf("q%d", i),
			Question:         fmt.Sprintf("Question %d?", i),
			CompletionSignal: fmt.Sprintf("Signal %d", i),
		})
	}
	data, _ := json.Marshal(plan)
	return string(data)
}

// makeSearchRoundResponse returns a valid JSON string for a SearchRound.
// newClaims controls how many claims are classified as "new" vs "duplicate".
func makeSearchRoundResponse(roundNum int, questionIDs []string, newClaims, dupClaims int, totalPerTopic int) string {
	round := SearchRound{RoundNumber: roundNum}
	for _, qid := range questionIDs {
		topic := TopicResult{
			QuestionID:      qid,
			QueriesExecuted: []string{"query1", "query2"},
			SourcesThisRound: newClaims + dupClaims,
			NewClaimsCount:   newClaims,
			TotalClaims:      totalPerTopic,
			Status:           "exploring",
		}
		for i := 0; i < newClaims; i++ {
			topic.Claims = append(topic.Claims, Claim{
				Text:           fmt.Sprintf("New claim %d for %s round %d", i, qid, roundNum),
				SourceURL:      fmt.Sprintf("https://example%d.com/article%d", i, roundNum),
				SourceTitle:    fmt.Sprintf("Article %d", i),
				Classification: "new",
			})
		}
		for i := 0; i < dupClaims; i++ {
			topic.Claims = append(topic.Claims, Claim{
				Text:           fmt.Sprintf("Duplicate claim %d for %s", i, qid),
				SourceURL:      fmt.Sprintf("https://example%d.com/old", i),
				Classification: "duplicate",
			})
		}
		if totalPerTopic >= 3 {
			topic.Status = "saturated"
		}
		round.Topics = append(round.Topics, topic)
	}
	round.Metrics = RoundMetrics{
		NewClaimRate:    float64(newClaims) / float64(newClaims+dupClaims),
		TopicCoverage:   1.0,
		SourceDiversity: 0.8,
	}
	data, _ := json.Marshal(round)
	return string(data)
}

// makeSynthesisResponse returns a valid JSON string for a SynthesisResult.
func makeSynthesisResponse(rounds, claims int) string {
	synth := SynthesisResult{
		Document: "# Research Document\n\nFindings here.",
		SaturationReport: SaturationReport{
			RoundsCompleted: rounds,
			TotalSources:    claims * 2,
			UniqueDomains:   claims,
			TotalClaims:     claims,
			FinalMetrics: RoundMetrics{
				NewClaimRate:  0.1,
				TopicCoverage: 1.0,
			},
		},
		Gaps: []string{"Gap 1", "Gap 2"},
	}
	data, _ := json.Marshal(synth)
	return string(data)
}

// newResearchTestConductor creates a minimal Conductor with a MockRunner and in-memory DB for testing.
func newResearchTestConductor(t *testing.T, mock *MockRunner) *Conductor {
	t.Helper()
	testDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := testDB.InitSchema(context.Background()); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	t.Cleanup(func() { testDB.Close() })

	return &Conductor{
		DB:       testDB,
		Runner:   mock,
		RepoRoot: t.TempDir(),
		Log:      func(s string) { t.Logf("conductor: %s", s) },
	}
}

func TestResearchLoopPlan(t *testing.T) {
	qIDs := []string{"q1", "q2", "q3"}
	mock := &MockRunner{
		Outputs: []string{
			makePlanResponse(3),
			// 3 rounds (MinRounds) — all high yield so we hit MaxRounds=3
			makeSearchRoundResponse(1, qIDs, 5, 1, 5),
			makeSearchRoundResponse(2, qIDs, 5, 1, 10),
			makeSearchRoundResponse(3, qIDs, 5, 1, 15),
			makeSynthesisResponse(3, 45),
		},
	}

	c := newResearchTestConductor(t, mock)
	config := DefaultResearchLoopConfig()
	config.MaxRounds = 3

	result, err := c.ResearchLoop(context.Background(), "Research task", "AC here", []string{"out.md"}, config)
	if err != nil {
		t.Fatalf("ResearchLoop: %v", err)
	}

	if len(result.Plan.SubQuestions) != 3 {
		t.Errorf("expected 3 sub-questions, got %d", len(result.Plan.SubQuestions))
	}
	for i, q := range result.Plan.SubQuestions {
		if q.ID == "" {
			t.Errorf("sub-question %d has empty ID", i)
		}
		if q.Question == "" {
			t.Errorf("sub-question %d has empty question", i)
		}
	}
}

func TestResearchLoopMinRounds(t *testing.T) {
	qIDs := []string{"q1", "q2", "q3"}
	// Round 1 already looks saturated (0 new claims), but MinRounds=3 forces 3 rounds.
	mock := &MockRunner{
		Outputs: []string{
			makePlanResponse(3),
			makeSearchRoundResponse(1, qIDs, 0, 5, 5),  // 0% new — would saturate
			makeSearchRoundResponse(2, qIDs, 0, 5, 5),  // 0% new — would saturate
			makeSearchRoundResponse(3, qIDs, 0, 5, 5),  // 0% new — saturates at round 3 (MinRounds met)
			makeSynthesisResponse(3, 0),
		},
	}

	c := newResearchTestConductor(t, mock)
	config := DefaultResearchLoopConfig()
	config.MinRounds = 3
	config.MaxRounds = 8

	result, err := c.ResearchLoop(context.Background(), "task", "ac", []string{"out.md"}, config)
	if err != nil {
		t.Fatalf("ResearchLoop: %v", err)
	}

	if result.RoundsCompleted < 3 {
		t.Errorf("expected at least 3 rounds (MinRounds), got %d", result.RoundsCompleted)
	}

	// Verify MockRunner was called: 1 plan + 3 rounds + 1 synthesis = 5
	if len(mock.Calls) != 5 {
		t.Errorf("expected 5 runner calls (1 plan + 3 rounds + 1 synthesis), got %d", len(mock.Calls))
	}
}

func TestResearchLoopSaturation(t *testing.T) {
	qIDs := []string{"q1", "q2", "q3"}
	// Rounds 1-3: high yield. Rounds 4-5: low yield → saturation at round 5.
	mock := &MockRunner{
		Outputs: []string{
			makePlanResponse(3),
			makeSearchRoundResponse(1, qIDs, 5, 1, 3),  // 83% new
			makeSearchRoundResponse(2, qIDs, 5, 1, 6),  // 83% new
			makeSearchRoundResponse(3, qIDs, 5, 1, 9),  // 83% new
			makeSearchRoundResponse(4, qIDs, 0, 5, 9),  // 0% new — low yield 1
			makeSearchRoundResponse(5, qIDs, 0, 5, 9),  // 0% new — low yield 2 → saturated
			makeSynthesisResponse(5, 45),
		},
	}

	c := newResearchTestConductor(t, mock)
	config := DefaultResearchLoopConfig()
	config.MinRounds = 3
	config.MaxRounds = 8
	config.ConsecutiveLowYieldReq = 2
	config.TopicCoverageReq = 0.85

	result, err := c.ResearchLoop(context.Background(), "task", "ac", []string{"out.md"}, config)
	if err != nil {
		t.Fatalf("ResearchLoop: %v", err)
	}

	if result.RoundsCompleted != 5 {
		t.Errorf("expected saturation at round 5, got %d rounds", result.RoundsCompleted)
	}

	// 1 plan + 5 rounds + 1 synthesis = 7
	if len(mock.Calls) != 7 {
		t.Errorf("expected 7 runner calls, got %d", len(mock.Calls))
	}
}

func TestResearchLoopMaxRounds(t *testing.T) {
	qIDs := []string{"q1", "q2", "q3"}
	// All rounds have high yield — never saturates, hits MaxRounds circuit breaker.
	outputs := []string{makePlanResponse(3)}
	maxRounds := 4
	for i := 1; i <= maxRounds; i++ {
		outputs = append(outputs, makeSearchRoundResponse(i, qIDs, 5, 1, i*5))
	}
	outputs = append(outputs, makeSynthesisResponse(maxRounds, maxRounds*15))

	mock := &MockRunner{Outputs: outputs}

	c := newResearchTestConductor(t, mock)
	config := DefaultResearchLoopConfig()
	config.MinRounds = 3
	config.MaxRounds = maxRounds

	result, err := c.ResearchLoop(context.Background(), "task", "ac", []string{"out.md"}, config)
	if err != nil {
		t.Fatalf("ResearchLoop: %v", err)
	}

	if result.RoundsCompleted != maxRounds {
		t.Errorf("expected %d rounds (MaxRounds), got %d", maxRounds, result.RoundsCompleted)
	}
}

func TestComputeMetrics(t *testing.T) {
	plan := ResearchPlan{
		SubQuestions: []SubQuestion{
			{ID: "q1"}, {ID: "q2"}, {ID: "q3"}, {ID: "q4"},
		},
	}

	round := SearchRound{
		Topics: []TopicResult{
			{QuestionID: "q1", TotalClaims: 5, Claims: []Claim{
				{Text: "c1", SourceURL: "https://a.com/1", Classification: "new"},
				{Text: "c2", SourceURL: "https://b.com/1", Classification: "new"},
				{Text: "c3", SourceURL: "https://a.com/2", Classification: "duplicate"},
			}},
			{QuestionID: "q2", TotalClaims: 4, Claims: []Claim{
				{Text: "c4", SourceURL: "https://c.com/1", Classification: "new"},
				{Text: "c5", SourceURL: "https://d.com/1", Classification: "duplicate"},
			}},
			{QuestionID: "q3", TotalClaims: 3, Claims: []Claim{
				{Text: "c6", SourceURL: "https://e.com/1", Classification: "conflict"},
			}},
			{QuestionID: "q4", TotalClaims: 1, Claims: []Claim{
				{Text: "c7", SourceURL: "https://f.com/1", Classification: "new"},
			}},
		},
	}

	allClaims := []Claim{
		{SourceURL: "https://a.com/1"},
		{SourceURL: "https://b.com/1"},
		{SourceURL: "https://c.com/1"},
		{SourceURL: "https://d.com/1"},
		{SourceURL: "https://e.com/1"},
		{SourceURL: "https://f.com/1"},
		{SourceURL: "https://a.com/2"},
	}

	metrics := computeMetrics(round, allClaims, plan)

	// New claim rate: 4 new (c1,c2,c4,c7) out of 7 total extracted = 4/7 ~ 0.5714
	expectedRate := 4.0 / 7.0
	if diff := metrics.NewClaimRate - expectedRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("NewClaimRate: got %.4f, want ~%.4f", metrics.NewClaimRate, expectedRate)
	}

	// Topic coverage: q1(5>=3), q2(4>=3), q3(3>=3), q4(1<3) → 3/4 = 0.75
	if diff := metrics.TopicCoverage - 0.75; diff > 0.01 || diff < -0.01 {
		t.Errorf("TopicCoverage: got %.4f, want 0.75", metrics.TopicCoverage)
	}

	// Source diversity: 6 unique domains (a,b,c,d,e,f) / 7 total = ~0.857
	expectedDiv := 6.0 / 7.0
	if diff := metrics.SourceDiversity - expectedDiv; diff > 0.01 || diff < -0.01 {
		t.Errorf("SourceDiversity: got %.4f, want ~%.4f", metrics.SourceDiversity, expectedDiv)
	}
}

func TestComputeMetricsZeroClaims(t *testing.T) {
	plan := ResearchPlan{SubQuestions: []SubQuestion{{ID: "q1"}}}
	round := SearchRound{Topics: []TopicResult{
		{QuestionID: "q1", TotalClaims: 0, Claims: nil},
	}}

	metrics := computeMetrics(round, nil, plan)

	if metrics.NewClaimRate != 0.0 {
		t.Errorf("NewClaimRate should be 0 with no claims, got %f", metrics.NewClaimRate)
	}
	if metrics.TopicCoverage != 0.0 {
		t.Errorf("TopicCoverage should be 0 with no covered topics, got %f", metrics.TopicCoverage)
	}
	if metrics.SourceDiversity != 0.0 {
		t.Errorf("SourceDiversity should be 0 with no claims, got %f", metrics.SourceDiversity)
	}
}

func TestIsSaturated(t *testing.T) {
	config := DefaultResearchLoopConfig()

	tests := []struct {
		name     string
		metrics  RoundMetrics
		round    int
		expected bool
	}{
		{
			name:     "below MinRounds never saturates",
			metrics:  RoundMetrics{NewClaimRate: 0.0, ConsecutiveLowYield: 5, TopicCoverage: 1.0},
			round:    2,
			expected: false,
		},
		{
			name:     "at MinRounds with all criteria met",
			metrics:  RoundMetrics{NewClaimRate: 0.05, ConsecutiveLowYield: 2, TopicCoverage: 0.90},
			round:    3,
			expected: true,
		},
		{
			name:     "high new claim rate prevents saturation",
			metrics:  RoundMetrics{NewClaimRate: 0.50, ConsecutiveLowYield: 2, TopicCoverage: 1.0},
			round:    5,
			expected: false,
		},
		{
			name:     "insufficient consecutive low yield",
			metrics:  RoundMetrics{NewClaimRate: 0.05, ConsecutiveLowYield: 1, TopicCoverage: 1.0},
			round:    5,
			expected: false,
		},
		{
			name:     "low topic coverage prevents saturation",
			metrics:  RoundMetrics{NewClaimRate: 0.05, ConsecutiveLowYield: 3, TopicCoverage: 0.50},
			round:    5,
			expected: false,
		},
		{
			name:     "all criteria met at late round",
			metrics:  RoundMetrics{NewClaimRate: 0.10, ConsecutiveLowYield: 3, TopicCoverage: 0.95},
			round:    7,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSaturated(tt.metrics, tt.round, config)
			if got != tt.expected {
				t.Errorf("isSaturated() = %v, want %v (metrics=%+v, round=%d)", got, tt.expected, tt.metrics, tt.round)
			}
		})
	}
}

func TestBuildClaimsContextSummarizesAfterRound5(t *testing.T) {
	claims := make([]Claim, 10)
	for i := range claims {
		claims[i] = Claim{
			Text:      fmt.Sprintf("Claim %d", i),
			SourceURL: fmt.Sprintf("https://domain%d.com/article", i),
		}
	}

	// Round 5: full JSON
	ctx5, err := buildClaimsContext(claims, 5)
	if err != nil {
		t.Fatalf("buildClaimsContext round 5: %v", err)
	}
	// Should be a JSON array
	var arr []Claim
	if err := json.Unmarshal([]byte(ctx5), &arr); err != nil {
		t.Errorf("round 5 output should be a JSON array: %v", err)
	}

	// Round 6: summarized
	ctx6, err := buildClaimsContext(claims, 6)
	if err != nil {
		t.Fatalf("buildClaimsContext round 6: %v", err)
	}
	// Should be a JSON object with total_claims
	var summary struct {
		TotalClaims int    `json:"total_claims"`
		Note        string `json:"note"`
	}
	if err := json.Unmarshal([]byte(ctx6), &summary); err != nil {
		t.Errorf("round 6 output should be a summary object: %v", err)
	}
	if summary.TotalClaims != 10 {
		t.Errorf("summary total_claims: got %d, want 10", summary.TotalClaims)
	}
}

func TestBuildClaimsContextEmpty(t *testing.T) {
	ctx, err := buildClaimsContext(nil, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != "[]" {
		t.Errorf("expected '[]' for empty claims, got %q", ctx)
	}
}

func TestResearchLoopSynthesisOutput(t *testing.T) {
	qIDs := []string{"q1", "q2", "q3"}
	mock := &MockRunner{
		Outputs: []string{
			makePlanResponse(3),
			makeSearchRoundResponse(1, qIDs, 0, 5, 5),
			makeSearchRoundResponse(2, qIDs, 0, 5, 5),
			makeSearchRoundResponse(3, qIDs, 0, 5, 5),
			makeSynthesisResponse(3, 0),
		},
	}

	c := newResearchTestConductor(t, mock)
	config := DefaultResearchLoopConfig()
	config.MaxRounds = 8

	result, err := c.ResearchLoop(context.Background(), "task", "ac", []string{"out.md"}, config)
	if err != nil {
		t.Fatalf("ResearchLoop: %v", err)
	}

	if result.Synthesis.Document == "" {
		t.Error("expected non-empty synthesis document")
	}
	if len(result.Gaps) != 2 {
		t.Errorf("expected 2 gaps, got %d", len(result.Gaps))
	}
	if result.SaturationReport.RoundsCompleted != 3 {
		t.Errorf("expected saturation report with 3 rounds, got %d", result.SaturationReport.RoundsCompleted)
	}
}
