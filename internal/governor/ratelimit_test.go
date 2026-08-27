package governor

import (
	"log/slog"
	"testing"
	"time"
)

func TestRecordMessage_AndWindowStatus(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())
	rt.SetEstimatedCapacity(100)

	rt.RecordMessage("agent-1")
	rt.RecordMessage("agent-2")
	rt.RecordMessage("agent-1")

	ws := rt.GetWindowStatus()
	if ws.MessagesInWindow != 3 {
		t.Errorf("expected 3 messages in window, got %d", ws.MessagesInWindow)
	}
	if ws.EstimatedCapacity != 100 {
		t.Errorf("expected capacity 100, got %d", ws.EstimatedCapacity)
	}
	expectedUtil := 0.03
	if ws.UtilizationPct < expectedUtil-0.001 || ws.UtilizationPct > expectedUtil+0.001 {
		t.Errorf("expected utilization ~%.2f, got %.4f", expectedUtil, ws.UtilizationPct)
	}
}

func TestShouldThrottle_BelowThreshold(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())
	rt.SetEstimatedCapacity(100)

	// Add a few messages — well below 80%.
	for i := 0; i < 10; i++ {
		rt.RecordMessage("agent-1")
	}

	throttle, backoff := rt.ShouldThrottle(1)
	if throttle {
		t.Error("expected no throttle at 10% utilization")
	}
	if backoff != 0 {
		t.Errorf("expected zero backoff, got %v", backoff)
	}
}

func TestShouldThrottle_AboveThreshold(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())
	rt.SetEstimatedCapacity(100)

	// Add 85 messages — above 80% threshold.
	for i := 0; i < 85; i++ {
		rt.RecordMessage("agent-1")
	}

	throttle, backoff := rt.ShouldThrottle(1)
	if !throttle {
		t.Error("expected throttle at 85% utilization")
	}
	if backoff <= 0 {
		t.Error("expected positive backoff duration")
	}
}

func TestShouldThrottle_ConcurrentAgentsLowerThreshold(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())
	rt.SetEstimatedCapacity(100)

	// Add 45 messages — above 80%/2 = 40% threshold for 2 agents.
	for i := 0; i < 45; i++ {
		rt.RecordMessage("agent-1")
	}

	throttle, _ := rt.ShouldThrottle(2)
	if !throttle {
		t.Error("expected throttle at 45% utilization with 2 concurrent agents (threshold 40%)")
	}

	// Same count should not throttle with 1 agent.
	rt2 := NewRateLimitTracker(slog.Default())
	rt2.SetEstimatedCapacity(100)
	for i := 0; i < 45; i++ {
		rt2.RecordMessage("agent-1")
	}
	throttle2, _ := rt2.ShouldThrottle(1)
	if throttle2 {
		t.Error("expected no throttle at 45% utilization with 1 agent")
	}
}

func TestOptimalConcurrency(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())
	rt.SetEstimatedCapacity(100)

	// No messages — should return max.
	optimal := rt.OptimalConcurrency(8)
	if optimal != 8 {
		t.Errorf("expected 8 optimal agents at 0%% utilization, got %d", optimal)
	}

	// 80%+ utilization — should return 1.
	for i := 0; i < 85; i++ {
		rt.RecordMessage("agent-1")
	}
	optimal = rt.OptimalConcurrency(8)
	if optimal != 1 {
		t.Errorf("expected 1 optimal agent at 85%% utilization, got %d", optimal)
	}
}

func TestPruneOld(t *testing.T) {
	t.Parallel()
	rt := NewRateLimitTracker(slog.Default())

	// Manually inject old messages beyond the 5-hour window.
	rt.mu.Lock()
	old := time.Now().Add(-6 * time.Hour)
	rt.messages = append(rt.messages, messageRecord{Timestamp: old, AgentID: "old-agent"})
	rt.messages = append(rt.messages, messageRecord{Timestamp: old, AgentID: "old-agent"})
	rt.mu.Unlock()

	// Add a fresh one.
	rt.RecordMessage("new-agent")

	ws := rt.GetWindowStatus()
	if ws.MessagesInWindow != 1 {
		t.Errorf("expected 1 message after pruning, got %d", ws.MessagesInWindow)
	}
}
