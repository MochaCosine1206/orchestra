package governor

import (
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// BudgetConfig defines token budget limits for orchestration sessions.
type BudgetConfig struct {
	// DailyTokenLimit is the maximum tokens allowed per calendar day (0 = unlimited).
	DailyTokenLimit int64
	// TaskTokenLimit is the maximum tokens allowed per individual task (0 = unlimited).
	TaskTokenLimit int64
	// WarnThreshold is the fraction (0.0–1.0) at which a budget warning is logged.
	WarnThreshold float64
}

// DefaultBudgetConfig returns a sensible default budget configuration.
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		DailyTokenLimit: 0, // unlimited by default (Claude Max)
		TaskTokenLimit:  0,
		WarnThreshold:   0.8,
	}
}

// BudgetStatus represents the current token usage for a given scope (day or task).
type BudgetStatus struct {
	// TokensUsed is the total tokens consumed in this scope.
	TokensUsed int64
	// TokenLimit is the configured limit (0 = unlimited).
	TokenLimit int64
	// Remaining is TokenLimit - TokensUsed (or -1 if unlimited).
	Remaining int64
	// UtilizationPct is the fraction of budget consumed (0.0–1.0, or 0 if unlimited).
	UtilizationPct float64
	// OverBudget is true when TokensUsed >= TokenLimit and limit > 0.
	OverBudget bool
}

// ResourceGovernor coordinates budget tracking, rate limiting, runaway detection,
// and circuit breaking for orchestrated agent sessions.
type ResourceGovernor struct {
	DB     *sql.DB
	Config BudgetConfig
	Logger *slog.Logger

	// RateLimit tracks Claude Max 5-hour rolling windows.
	RateLimit *RateLimitTracker

	// CircuitBreakers keyed by agent/task ID.
	circuitMu      sync.RWMutex
	circuitBreakers map[string]*CircuitBreaker
}

// NewResourceGovernor creates a ResourceGovernor with the given configuration.
func NewResourceGovernor(db *sql.DB, cfg BudgetConfig, logger *slog.Logger) *ResourceGovernor {
	return &ResourceGovernor{
		DB:              db,
		Config:          cfg,
		Logger:          logger,
		RateLimit:       NewRateLimitTracker(logger),
		circuitBreakers: make(map[string]*CircuitBreaker),
	}
}

// GetCircuitBreaker returns the circuit breaker for the given key, creating one if needed.
func (rg *ResourceGovernor) GetCircuitBreaker(key string) *CircuitBreaker {
	rg.circuitMu.RLock()
	cb, ok := rg.circuitBreakers[key]
	rg.circuitMu.RUnlock()
	if ok {
		return cb
	}

	rg.circuitMu.Lock()
	defer rg.circuitMu.Unlock()
	// Double-check after acquiring write lock.
	if cb, ok = rg.circuitBreakers[key]; ok {
		return cb
	}
	cb = NewCircuitBreaker(key, CircuitBreakerConfig{
		FailureThreshold: 3,
		AlertThreshold:   5,
		OpenDuration:     15 * time.Minute,
	}, rg.Logger)
	rg.circuitBreakers[key] = cb
	return cb
}
