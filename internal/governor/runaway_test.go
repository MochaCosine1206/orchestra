package governor

import (
	"strings"
	"testing"
	"time"
)

func TestDetectRepetition_NoLoop(t *testing.T) {
	t.Parallel()
	// All unique lines — no repetition.
	lines := []string{
		"line 1", "line 2", "line 3", "line 4", "line 5",
		"line 6", "line 7", "line 8", "line 9", "line 10",
	}
	output := strings.Join(lines, "\n")
	result := DetectRepetition(output)
	if result.IsLoop {
		t.Errorf("expected no loop, got loop with fraction %.2f", result.RepetitionFraction)
	}
}

func TestDetectRepetition_Loop(t *testing.T) {
	t.Parallel()
	// 80% repetition of the same line.
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, "ERROR: retry failed")
	}
	for i := 0; i < 20; i++ {
		lines = append(lines, "unique line "+string(rune('a'+i)))
	}
	output := strings.Join(lines, "\n")
	result := DetectRepetition(output)
	if !result.IsLoop {
		t.Errorf("expected loop detection, got fraction %.2f", result.RepetitionFraction)
	}
	if result.MostRepeatedLine != "ERROR: retry failed" {
		t.Errorf("expected most repeated line to be 'ERROR: retry failed', got %q", result.MostRepeatedLine)
	}
}

func TestDetectRepetition_EmptyInput(t *testing.T) {
	t.Parallel()
	result := DetectRepetition("")
	if result.IsLoop {
		t.Error("expected no loop for empty input")
	}
	if result.RepetitionFraction != 0 {
		t.Errorf("expected 0 fraction for empty input, got %.2f", result.RepetitionFraction)
	}
}

func TestDetectNoProgress_Stalled(t *testing.T) {
	t.Parallel()
	// Last progress was 45 minutes ago — exceeds 30 min threshold.
	lastProgress := time.Now().Add(-45 * time.Minute)
	result := DetectNoProgress(lastProgress)
	if !result.IsStalled {
		t.Errorf("expected stalled, got not stalled (duration: %v)", result.StalledDuration)
	}
}

func TestDetectNoProgress_Active(t *testing.T) {
	t.Parallel()
	// Last progress was 5 minutes ago — within threshold.
	lastProgress := time.Now().Add(-5 * time.Minute)
	result := DetectNoProgress(lastProgress)
	if result.IsStalled {
		t.Errorf("expected active, got stalled (duration: %v)", result.StalledDuration)
	}
}

func TestDetectRepetition_SlidingWindow(t *testing.T) {
	t.Parallel()
	// 200 lines: first 100 are unique, last 100 are all the same.
	// The sliding window should only look at the last 100.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "unique line "+string(rune(i%26+'a')))
	}
	for i := 0; i < 100; i++ {
		lines = append(lines, "stuck in a loop")
	}
	output := strings.Join(lines, "\n")
	result := DetectRepetition(output)
	if !result.IsLoop {
		t.Errorf("expected loop in sliding window, got fraction %.2f", result.RepetitionFraction)
	}
}
