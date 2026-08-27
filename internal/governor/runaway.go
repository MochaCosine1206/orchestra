package governor

import (
	"strings"
	"time"
)

const (
	// repetitionThreshold is the fraction of repeated lines that indicates a loop.
	repetitionThreshold = 0.20

	// slidingWindowSize is the number of recent output lines to analyze.
	slidingWindowSize = 100

	// noProgressTimeout is the duration without meaningful progress before flagging.
	noProgressTimeout = 30 * time.Minute
)

// RepetitionResult contains the analysis of output repetition.
type RepetitionResult struct {
	// IsLoop is true when the repetition fraction exceeds the threshold.
	IsLoop bool
	// RepetitionFraction is the fraction of lines that are duplicates.
	RepetitionFraction float64
	// MostRepeatedLine is the most frequently repeated line.
	MostRepeatedLine string
	// MostRepeatedCount is how many times it appeared.
	MostRepeatedCount int
}

// ProgressResult contains the analysis of task progress.
type ProgressResult struct {
	// IsStalled is true when no progress has been detected within the timeout.
	IsStalled bool
	// LastProgressAt is the timestamp of the last meaningful progress event.
	LastProgressAt time.Time
	// StalledDuration is how long since the last progress event.
	StalledDuration time.Duration
}

// DetectRepetition analyzes a sliding window of output lines for repetition patterns.
// Lines are split on newlines. If more than 20% of lines in the window are duplicates
// of a single line, it flags as a loop.
func DetectRepetition(output string) RepetitionResult {
	lines := splitNonEmpty(output)

	// Take the last slidingWindowSize lines.
	if len(lines) > slidingWindowSize {
		lines = lines[len(lines)-slidingWindowSize:]
	}

	if len(lines) == 0 {
		return RepetitionResult{}
	}

	// Count occurrences of each line.
	counts := make(map[string]int)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		counts[trimmed]++
	}

	// Find the most repeated line.
	var maxLine string
	var maxCount int
	for line, count := range counts {
		if count > maxCount {
			maxCount = count
			maxLine = line
		}
	}

	total := len(lines)
	fraction := float64(maxCount) / float64(total)

	return RepetitionResult{
		IsLoop:             fraction > repetitionThreshold,
		RepetitionFraction: fraction,
		MostRepeatedLine:   maxLine,
		MostRepeatedCount:  maxCount,
	}
}

// DetectNoProgress checks whether any meaningful progress has been made within the
// noProgressTimeout window. The caller provides the timestamp of the last known progress
// event (e.g., last file written, last test passed, last commit).
func DetectNoProgress(lastProgressAt time.Time) ProgressResult {
	now := time.Now()
	elapsed := now.Sub(lastProgressAt)

	return ProgressResult{
		IsStalled:       elapsed >= noProgressTimeout,
		LastProgressAt:  lastProgressAt,
		StalledDuration: elapsed,
	}
}

// splitNonEmpty splits text on newlines and returns non-empty lines.
func splitNonEmpty(text string) []string {
	raw := strings.Split(text, "\n")
	result := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}
