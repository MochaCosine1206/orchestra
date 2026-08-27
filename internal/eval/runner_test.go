package eval

import (
	"math"
	"testing"
)

func TestBootstrapIdentical(t *testing.T) {
	baseline := []float64{0.5, 0.5, 0.5, 0.5, 0.5}
	candidate := []float64{0.5, 0.5, 0.5, 0.5, 0.5}

	ci := Bootstrap(baseline, candidate, 1000)
	// Identical samples should give CI around 0
	if math.Abs(ci[0]) > 0.01 || math.Abs(ci[1]) > 0.01 {
		t.Errorf("CI for identical samples = [%.3f, %.3f], want ~[0, 0]", ci[0], ci[1])
	}
}

func TestBootstrapClearDifference(t *testing.T) {
	baseline := []float64{0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3}
	candidate := []float64{0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9}

	ci := Bootstrap(baseline, candidate, 10000)
	// Clear improvement: CI should be entirely positive
	if ci[0] <= 0 {
		t.Errorf("lower CI = %.3f, expected > 0 for clear improvement", ci[0])
	}
	if ci[1] <= 0 {
		t.Errorf("upper CI = %.3f, expected > 0 for clear improvement", ci[1])
	}
	// Delta should be around 0.6
	midpoint := (ci[0] + ci[1]) / 2
	if math.Abs(midpoint-0.6) > 0.1 {
		t.Errorf("CI midpoint = %.3f, expected ~0.6", midpoint)
	}
}

func TestBootstrapEmpty(t *testing.T) {
	ci := Bootstrap(nil, nil, 1000)
	if ci[0] != 0 || ci[1] != 0 {
		t.Errorf("CI for empty = [%.3f, %.3f], want [0, 0]", ci[0], ci[1])
	}
}

func TestWilcoxonIdentical(t *testing.T) {
	baseline := []float64{0.5, 0.6, 0.7, 0.8}
	candidate := []float64{0.5, 0.6, 0.7, 0.8}

	p := WilcoxonSignedRank(baseline, candidate)
	// Identical values: p should be 1.0 (no differences)
	if p != 1.0 {
		t.Errorf("p = %.3f for identical, want 1.0", p)
	}
}

func TestWilcoxonClearDifference(t *testing.T) {
	baseline := []float64{0.1, 0.2, 0.15, 0.18, 0.12, 0.1, 0.2, 0.15, 0.18, 0.12}
	candidate := []float64{0.9, 0.8, 0.85, 0.88, 0.92, 0.9, 0.8, 0.85, 0.88, 0.92}

	p := WilcoxonSignedRank(baseline, candidate)
	if p >= 0.05 {
		t.Errorf("p = %.4f for large difference, expected < 0.05", p)
	}
}

func TestWilcoxonEmpty(t *testing.T) {
	p := WilcoxonSignedRank(nil, nil)
	if p != 1.0 {
		t.Errorf("p = %.3f for empty, want 1.0", p)
	}
}

func TestWilcoxonSmallSample(t *testing.T) {
	baseline := []float64{0.5, 0.6}
	candidate := []float64{0.7, 0.8}

	p := WilcoxonSignedRank(baseline, candidate)
	// Small sample, should still produce a valid p-value
	if p < 0 || p > 1 {
		t.Errorf("p = %.3f, should be in [0, 1]", p)
	}
}

func TestMean(t *testing.T) {
	tests := []struct {
		name string
		vals []float64
		want float64
	}{
		{"empty", nil, 0},
		{"single", []float64{5.0}, 5.0},
		{"multiple", []float64{1.0, 2.0, 3.0}, 2.0},
		{"decimals", []float64{0.1, 0.2, 0.3}, 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mean(tt.vals)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("mean(%v) = %.3f, want %.3f", tt.vals, got, tt.want)
			}
		})
	}
}

func TestNormalCDF(t *testing.T) {
	// Known values
	tests := []struct {
		x    float64
		want float64
		tol  float64
	}{
		{0, 0.5, 0.001},
		{-3, 0.00135, 0.001},
		{3, 0.99865, 0.001},
	}

	for _, tt := range tests {
		got := normalCDF(tt.x)
		if math.Abs(got-tt.want) > tt.tol {
			t.Errorf("normalCDF(%.1f) = %.5f, want %.5f", tt.x, got, tt.want)
		}
	}
}
