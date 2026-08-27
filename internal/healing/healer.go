package healing

import (
	"context"
	"errors"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// HealResult describes the outcome of a healing attempt.
type HealResult struct {
	Fixed         bool
	FixID         string
	ErrorType     ErrorType
	FixApplied    string
	AffectedTasks []string
	Err           error
}

// Healer composes Budget, Registry, and Impact for end-to-end healing.
type Healer struct {
	Budget   *Budget
	Registry *Registry
	Impact   *Impact
}

// NewHealer creates a Healer with default registry.
func NewHealer(sessionID string, database *db.DB) *Healer {
	return &Healer{
		Budget:   NewBudget(sessionID, database),
		Registry: NewRegistry(),
		Impact:   NewImpact(database),
	}
}

// Heal parses stderr, checks budget, applies the first available fix, records it,
// checks impact, and returns a HealResult.
func (h *Healer) Heal(ctx context.Context, taskID string, stderr string, worktreePath string, modifiedFiles []string, candidateTaskIDs []string) HealResult {
	buildErrors := ParseBuildError(stderr)
	if len(buildErrors) == 0 {
		return HealResult{Err: errors.New("no parseable build errors")}
	}

	if !h.Budget.CanFix() {
		return HealResult{Err: errors.New("fix budget exhausted")}
	}

	// Try fixes in order until one succeeds.
	for _, be := range buildErrors {
		err := h.Registry.TryFix(be, worktreePath)
		if errors.Is(err, ErrNoFixAvailable) {
			continue
		}
		if err != nil {
			return HealResult{ErrorType: be.Type, Err: err}
		}

		// Fix succeeded — record it.
		fixName := string(be.Type)
		if err := h.Budget.RecordFix(ctx, taskID, be.Type, fixName, modifiedFiles, worktreePath); err != nil {
			return HealResult{ErrorType: be.Type, Err: err}
		}

		// Check impact.
		affected, _ := h.Impact.Check(ctx, taskID, candidateTaskIDs)

		return HealResult{
			Fixed:         true,
			FixID:         h.Budget.LastFixID(),
			ErrorType:     be.Type,
			FixApplied:    fixName,
			AffectedTasks: affected,
		}
	}

	return HealResult{Err: ErrNoFixAvailable}
}

// Confirm delegates to Budget.ConfirmFix.
func (h *Healer) Confirm(fixID string) {
	h.Budget.ConfirmFix(fixID)
}

// Close stops all pending timers.
func (h *Healer) Close() {
	h.Budget.Close()
}
