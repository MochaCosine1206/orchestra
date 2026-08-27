package governor

import (
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"
)

const (
	// rollingWindowDuration is the 5-hour Claude Max rolling window.
	rollingWindowDuration = 5 * time.Hour

	// defaultThrottleThreshold is the fraction of the window at which throttling begins.
	defaultThrottleThreshold = 0.80

	// maxBackoff is the ceiling for exponential backoff.
	maxBackoff = 5 * time.Minute

	// baseBackoff is the initial backoff duration.
	baseBackoff = 5 * time.Second
)

// messageRecord stores the timestamp and agent for a single API message.
type messageRecord struct {
	Timestamp time.Time
	AgentID   string
}

// WindowStatus describes the current state of the rate limit window.
type WindowStatus struct {
	// MessagesInWindow is the count of messages in the rolling window.
	MessagesInWindow int
	// WindowStart is the oldest message timestamp still in the window.
	WindowStart time.Time
	// WindowEnd is now.
	WindowEnd time.Time
	// EstimatedCapacity is the approximate capacity (messages) based on history.
	EstimatedCapacity int
	// UtilizationPct is MessagesInWindow / EstimatedCapacity.
	UtilizationPct float64
}

// RateLimitTracker tracks Claude Max 5-hour rolling window usage
// and provides throttling recommendations for concurrent agents.
type RateLimitTracker struct {
	mu       sync.Mutex
	messages []messageRecord
	logger   *slog.Logger

	// estimatedCapacity is set by the caller or inferred. Default 0 means unknown.
	estimatedCapacity int

	// consecutiveThrottles tracks backoff escalation.
	consecutiveThrottles int
}

// NewRateLimitTracker creates a new tracker.
func NewRateLimitTracker(logger *slog.Logger) *RateLimitTracker {
	return &RateLimitTracker{
		logger:            logger,
		estimatedCapacity: 0,
	}
}

// SetEstimatedCapacity sets the estimated message capacity for the 5-hour window.
func (rt *RateLimitTracker) SetEstimatedCapacity(cap int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.estimatedCapacity = cap
}

// RecordMessage records a message sent by an agent.
func (rt *RateLimitTracker) RecordMessage(agentID string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.messages = append(rt.messages, messageRecord{
		Timestamp: time.Now(),
		AgentID:   agentID,
	})
	rt.pruneOld()
}

// GetWindowStatus returns the current rolling window status.
func (rt *RateLimitTracker) GetWindowStatus() WindowStatus {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.pruneOld()

	now := time.Now()
	ws := WindowStatus{
		MessagesInWindow:  len(rt.messages),
		WindowEnd:         now,
		WindowStart:       now.Add(-rollingWindowDuration),
		EstimatedCapacity: rt.estimatedCapacity,
	}

	if len(rt.messages) > 0 {
		ws.WindowStart = rt.messages[0].Timestamp
	}

	if rt.estimatedCapacity > 0 {
		ws.UtilizationPct = float64(len(rt.messages)) / float64(rt.estimatedCapacity)
	}

	return ws
}

// ShouldThrottle returns true if the current usage exceeds the throttle threshold
// adjusted for the number of concurrent agents. When throttled, it also returns
// a recommended backoff duration with exponential backoff and jitter.
func (rt *RateLimitTracker) ShouldThrottle(concurrentAgents int) (bool, time.Duration) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.pruneOld()

	if rt.estimatedCapacity <= 0 {
		// Unknown capacity — don't throttle.
		return false, 0
	}

	// Adjust threshold downward based on concurrent agents to leave headroom.
	threshold := defaultThrottleThreshold
	if concurrentAgents > 1 {
		threshold = defaultThrottleThreshold / float64(concurrentAgents)
	}

	utilization := float64(len(rt.messages)) / float64(rt.estimatedCapacity)
	if utilization < threshold {
		rt.consecutiveThrottles = 0
		return false, 0
	}

	// Exponential backoff with jitter.
	rt.consecutiveThrottles++
	backoff := rt.calcBackoff()

	rt.logger.Warn("rate limit throttle recommended",
		"utilization", utilization,
		"threshold", threshold,
		"concurrent_agents", concurrentAgents,
		"backoff", backoff,
	)

	return true, backoff
}

// OptimalConcurrency returns the recommended number of concurrent agents
// based on current window utilization.
func (rt *RateLimitTracker) OptimalConcurrency(maxAgents int) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.pruneOld()

	if rt.estimatedCapacity <= 0 || maxAgents <= 0 {
		return maxAgents
	}

	utilization := float64(len(rt.messages)) / float64(rt.estimatedCapacity)

	// Scale down linearly: at 0% utilization → maxAgents, at 80%+ → 1.
	if utilization >= defaultThrottleThreshold {
		return 1
	}

	headroom := 1.0 - (utilization / defaultThrottleThreshold)
	optimal := int(math.Ceil(float64(maxAgents) * headroom))
	if optimal < 1 {
		optimal = 1
	}
	if optimal > maxAgents {
		optimal = maxAgents
	}
	return optimal
}

// ResetThrottleCount resets the consecutive throttle counter (e.g., after a successful request).
func (rt *RateLimitTracker) ResetThrottleCount() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.consecutiveThrottles = 0
}

// pruneOld removes messages older than the rolling window. Must be called with mu held.
func (rt *RateLimitTracker) pruneOld() {
	cutoff := time.Now().Add(-rollingWindowDuration)
	i := 0
	for i < len(rt.messages) && rt.messages[i].Timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		rt.messages = rt.messages[i:]
	}
}

// calcBackoff returns an exponential backoff duration with jitter. Must be called with mu held.
func (rt *RateLimitTracker) calcBackoff() time.Duration {
	exp := math.Pow(2, float64(rt.consecutiveThrottles-1))
	backoff := time.Duration(float64(baseBackoff) * exp)
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	// Add jitter: +/- 25%.
	jitter := 0.75 + rand.Float64()*0.5 //nolint:gosec // jitter doesn't need crypto rand
	return time.Duration(float64(backoff) * jitter)
}
