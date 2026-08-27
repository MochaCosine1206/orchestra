package governor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CircuitState represents the three states of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed means the circuit is functioning normally.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit is tripped — all calls are rejected.
	CircuitOpen
	// CircuitHalfOpen means the circuit allows a single probe call.
	CircuitHalfOpen
)

// String returns the human-readable state name.
func (cs CircuitState) String() string {
	switch cs {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return fmt.Sprintf("unknown(%d)", int(cs))
	}
}

// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures to trip the breaker (default 3).
	FailureThreshold int
	// AlertThreshold is the number of total failures that triggers an alert + pause (default 5).
	AlertThreshold int
	// OpenDuration is how long the breaker stays open before transitioning to half-open (default 15 min).
	OpenDuration time.Duration
}

// CircuitBreaker implements the circuit breaker pattern for agent/task failure management.
// 3 consecutive failures trip the breaker open for 15 minutes. 5 total failures trigger
// an alert and pause. rate_limit failures bypass the breaker entirely.
type CircuitBreaker struct {
	mu     sync.Mutex
	key    string
	config CircuitBreakerConfig
	logger *slog.Logger

	state               CircuitState
	consecutiveFailures int
	totalFailures       int
	lastFailureAt       time.Time
	openedAt            time.Time
	alertFired          bool
}

// NewCircuitBreaker creates a circuit breaker for the given key (agent or task ID).
func NewCircuitBreaker(key string, cfg CircuitBreakerConfig, logger *slog.Logger) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.AlertThreshold <= 0 {
		cfg.AlertThreshold = 5
	}
	if cfg.OpenDuration <= 0 {
		cfg.OpenDuration = 15 * time.Minute
	}
	return &CircuitBreaker{
		key:    key,
		config: cfg,
		logger: logger,
		state:  CircuitClosed,
	}
}

// State returns the current circuit state, transitioning from open to half-open
// if the open duration has elapsed.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.stateLocked()
}

// stateLocked returns the state and performs the open→half-open transition. Must hold mu.
func (cb *CircuitBreaker) stateLocked() CircuitState {
	if cb.state == CircuitOpen && time.Since(cb.openedAt) >= cb.config.OpenDuration {
		cb.state = CircuitHalfOpen
		cb.logger.Info("circuit breaker transitioned to half-open",
			"key", cb.key,
			"open_duration", cb.config.OpenDuration,
		)
	}
	return cb.state
}

// Allow returns true if a call should be permitted through the breaker.
// In closed state: always allowed.
// In half-open state: allowed (single probe).
// In open state: rejected.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	s := cb.stateLocked()
	return s == CircuitClosed || s == CircuitHalfOpen
}

// RecordSuccess records a successful call. Resets the breaker to closed state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0
	if cb.state != CircuitClosed {
		cb.logger.Info("circuit breaker reset to closed",
			"key", cb.key,
			"previous_state", cb.state.String(),
		)
	}
	cb.state = CircuitClosed
}

// RecordFailure records a failed call. rate_limit failures are tracked but do not
// trip the breaker (they bypass circuit breaking per project convention).
func (cb *CircuitBreaker) RecordFailure(failureType string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailureAt = time.Now()
	cb.totalFailures++

	// rate_limit failures bypass the circuit breaker — infrastructure issue, not agent fault.
	if failureType == "rate_limit" {
		cb.logger.Info("rate_limit failure recorded (bypasses circuit breaker)",
			"key", cb.key,
			"total_failures", cb.totalFailures,
		)
		return
	}

	cb.consecutiveFailures++

	cb.logger.Warn("circuit breaker recorded failure",
		"key", cb.key,
		"failure_type", failureType,
		"consecutive", cb.consecutiveFailures,
		"total", cb.totalFailures,
	)

	// Check alert threshold first (5 total failures → alert + pause).
	if cb.totalFailures >= cb.config.AlertThreshold && !cb.alertFired {
		cb.alertFired = true
		cb.state = CircuitOpen
		cb.openedAt = time.Now()
		cb.logger.Error("circuit breaker ALERT: too many failures, pausing",
			"key", cb.key,
			"total_failures", cb.totalFailures,
			"alert_threshold", cb.config.AlertThreshold,
		)
		return
	}

	// Check consecutive failure threshold (3 consecutive → open for 15 min).
	if cb.consecutiveFailures >= cb.config.FailureThreshold {
		cb.state = CircuitOpen
		cb.openedAt = time.Now()
		cb.logger.Warn("circuit breaker tripped open",
			"key", cb.key,
			"consecutive_failures", cb.consecutiveFailures,
			"open_duration", cb.config.OpenDuration,
		)
	}
}

// Stats returns diagnostic information about the circuit breaker.
type CircuitBreakerStats struct {
	Key                 string
	State               CircuitState
	ConsecutiveFailures int
	TotalFailures       int
	LastFailureAt       time.Time
	AlertFired          bool
}

// Stats returns the current breaker statistics.
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return CircuitBreakerStats{
		Key:                 cb.key,
		State:               cb.stateLocked(),
		ConsecutiveFailures: cb.consecutiveFailures,
		TotalFailures:       cb.totalFailures,
		LastFailureAt:       cb.lastFailureAt,
		AlertFired:          cb.alertFired,
	}
}
