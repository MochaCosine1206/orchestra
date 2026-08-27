package governor

import (
	"log/slog"
	"testing"
	"time"
)

func newTestBreaker() *CircuitBreaker {
	return NewCircuitBreaker("test-agent", CircuitBreakerConfig{
		FailureThreshold: 3,
		AlertThreshold:   5,
		OpenDuration:     15 * time.Minute,
	}, slog.Default())
}

func TestCircuitBreaker_StartsClosedAndAllows(t *testing.T) {
	t.Parallel()
	cb := newTestBreaker()

	if cb.State() != CircuitClosed {
		t.Errorf("expected closed state, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Error("expected Allow() to return true in closed state")
	}
}

func TestCircuitBreaker_TripsAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()
	cb := newTestBreaker()

	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")
	if cb.State() != CircuitClosed {
		t.Errorf("expected closed after 2 failures, got %s", cb.State())
	}

	cb.RecordFailure("normal_failure")
	if cb.State() != CircuitOpen {
		t.Errorf("expected open after 3 consecutive failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Error("expected Allow() to return false in open state")
	}
}

func TestCircuitBreaker_RateLimitBypassesBreaker(t *testing.T) {
	t.Parallel()
	cb := newTestBreaker()

	// 10 rate_limit failures should not trip the breaker.
	for i := 0; i < 10; i++ {
		cb.RecordFailure("rate_limit")
	}

	if cb.State() != CircuitClosed {
		t.Errorf("expected closed after rate_limit failures, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Error("expected Allow() to return true — rate_limit bypasses breaker")
	}
}

func TestCircuitBreaker_SuccessResetsClosed(t *testing.T) {
	t.Parallel()
	cb := newTestBreaker()

	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")
	cb.RecordSuccess()

	// After success, consecutive counter resets — need 3 more to trip.
	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")
	if cb.State() != CircuitClosed {
		t.Errorf("expected closed after success reset + 2 failures, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenTransition(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreaker("test-agent", CircuitBreakerConfig{
		FailureThreshold: 3,
		AlertThreshold:   5,
		OpenDuration:     1 * time.Millisecond, // very short for testing
	}, slog.Default())

	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")

	if cb.State() != CircuitOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}

	// Wait for the open duration to elapse.
	time.Sleep(5 * time.Millisecond)

	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected half-open after open duration elapsed, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Error("expected Allow() to return true in half-open state")
	}
}

func TestCircuitBreaker_AlertThreshold(t *testing.T) {
	t.Parallel()
	cb := newTestBreaker()

	// Trip with 3 consecutive, then reset, then add 2 more for 5 total.
	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure")
	cb.RecordFailure("normal_failure") // trips open

	cb.RecordSuccess() // resets to closed

	cb.RecordFailure("normal_failure") // total=4
	cb.RecordFailure("normal_failure") // total=5 → alert

	stats := cb.Stats()
	if !stats.AlertFired {
		t.Error("expected alert to fire at 5 total failures")
	}
	if stats.State != CircuitOpen {
		t.Errorf("expected open after alert, got %s", stats.State)
	}
}
