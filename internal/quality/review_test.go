package quality

import (
	"context"
	"fmt"
	"testing"
)

// mockReviewProvider implements ReviewProvider for testing.
type mockReviewProvider struct {
	result *ReviewResult
	err    error
}

func (m *mockReviewProvider) Review(_ context.Context, _ string, _ string) (*ReviewResult, error) {
	return m.result, m.err
}

func TestReviewGateName(t *testing.T) {
	g := NewReviewGate(&mockReviewProvider{})
	if g.Name() != "review" {
		t.Errorf("expected name 'review', got %q", g.Name())
	}
}

func TestReviewGateCheck(t *testing.T) {
	tests := []struct {
		name       string
		result     *ReviewResult
		err        error
		wantPassed bool
		wantScore  float64
	}{
		{
			name: "low risk passes",
			result: &ReviewResult{
				RiskScore:  RiskLow,
				Findings:   0,
				BlockMerge: false,
				Summary:    "Clean code",
			},
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name: "medium risk passes",
			result: &ReviewResult{
				RiskScore:  RiskMedium,
				Findings:   2,
				BlockMerge: false,
				Summary:    "Minor issues",
			},
			wantPassed: true,
			wantScore:  0.7,
		},
		{
			name: "high risk passes when not blocking",
			result: &ReviewResult{
				RiskScore:  RiskHigh,
				Findings:   5,
				BlockMerge: false,
				Summary:    "Significant issues",
			},
			wantPassed: true,
			wantScore:  0.3,
		},
		{
			name: "critical risk blocks when block_merge set",
			result: &ReviewResult{
				RiskScore:  RiskCritical,
				Findings:   10,
				BlockMerge: true,
				Summary:    "Security vulnerability",
			},
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name: "high risk blocks when block_merge set",
			result: &ReviewResult{
				RiskScore:  RiskHigh,
				Findings:   3,
				BlockMerge: true,
				Summary:    "Breaking changes",
			},
			wantPassed: false,
			wantScore:  0.3,
		},
		{
			name:       "provider error fails open",
			err:        fmt.Errorf("connection timeout"),
			wantPassed: true,
			wantScore:  0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &mockReviewProvider{result: tt.result, err: tt.err}
			gate := NewReviewGate(provider)

			result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Passed != tt.wantPassed {
				t.Errorf("passed: got %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.Score != tt.wantScore {
				t.Errorf("score: got %v, want %v", result.Score, tt.wantScore)
			}
			if result.Layer != "review" {
				t.Errorf("layer: got %q, want 'review'", result.Layer)
			}
			if result.Duration <= 0 {
				t.Error("duration should be positive")
			}
		})
	}
}

func TestRiskScoreToScore(t *testing.T) {
	tests := []struct {
		risk RiskScore
		want float64
	}{
		{RiskLow, 1.0},
		{RiskMedium, 0.7},
		{RiskHigh, 0.3},
		{RiskCritical, 0.0},
		{RiskScore("unknown"), 0.5},
	}
	for _, tt := range tests {
		t.Run(string(tt.risk), func(t *testing.T) {
			got := riskScoreToScore(tt.risk)
			if got != tt.want {
				t.Errorf("riskScoreToScore(%q) = %v, want %v", tt.risk, got, tt.want)
			}
		})
	}
}

func TestParseReviewJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    *ReviewResult
	}{
		{
			name:  "clean JSON",
			input: `{"risk_score":"low","findings":0,"block_merge":false,"summary":"All clear"}`,
			want:  &ReviewResult{RiskScore: RiskLow, Findings: 0, BlockMerge: false, Summary: "All clear"},
		},
		{
			name:  "JSON in markdown code block",
			input: "```json\n{\"risk_score\":\"high\",\"findings\":3,\"block_merge\":true,\"summary\":\"Issues\"}\n```",
			want:  &ReviewResult{RiskScore: RiskHigh, Findings: 3, BlockMerge: true, Summary: "Issues"},
		},
		{
			name:  "unknown risk score defaults to medium",
			input: `{"risk_score":"severe","findings":1,"block_merge":false,"summary":"Odd"}`,
			want:  &ReviewResult{RiskScore: RiskMedium, Findings: 1, BlockMerge: false, Summary: "Odd"},
		},
		{
			name:    "invalid JSON",
			input:   "not json at all",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseReviewJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.RiskScore != tt.want.RiskScore {
				t.Errorf("RiskScore: got %q, want %q", got.RiskScore, tt.want.RiskScore)
			}
			if got.Findings != tt.want.Findings {
				t.Errorf("Findings: got %d, want %d", got.Findings, tt.want.Findings)
			}
			if got.BlockMerge != tt.want.BlockMerge {
				t.Errorf("BlockMerge: got %v, want %v", got.BlockMerge, tt.want.BlockMerge)
			}
			if got.Summary != tt.want.Summary {
				t.Errorf("Summary: got %q, want %q", got.Summary, tt.want.Summary)
			}
		})
	}
}
