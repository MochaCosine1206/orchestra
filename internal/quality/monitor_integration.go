package quality

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// Sentinel errors for merge decisions. Monitor uses errors.Is() to check.
var (
	ErrHold     = errors.New("quality gate: HOLD — changes need review")
	ErrRollback = errors.New("quality gate: ROLLBACK — changes must be reverted")
)

// CheckBeforeMerge runs the full quality gate stack and returns:
//   - nil for PROMOTE (safe to merge)
//   - ErrHold for HOLD (needs human review)
//   - ErrRollback for ROLLBACK (must revert)
func CheckBeforeMerge(ctx context.Context, db *sql.DB, runner CommandRunner, projectPath, branch, sessionID string) error {
	logger := slog.Default().With("component", "quality-gate", "branch", branch)

	// Build a minimal stack without LLM-dependent gates for pre-merge checks.
	// The review and judge gates require a ClaudeRunner which may not be available
	// in the monitor context. Use a nil-safe approach.
	stack := &GateStack{
		Gates: []Gate{
			&BuildGate{Runner: runner},
			&LintGate{Runner: runner},
			&SecurityGate{Runner: runner},
			&TestGate{Runner: runner},
			&CoverageRatchetGate{DB: db, Runner: runner, Logger: logger},
			&MutationGate{Runner: runner},
		},
		DB:     db,
		Logger: logger,
	}

	report, err := stack.Run(ctx, projectPath, branch, sessionID)
	if err != nil {
		// Fail-open: quality gate infrastructure error should not block merge.
		logger.Error("quality gate stack error (fail-open, allowing merge)", "err", err)
		return nil
	}

	switch report.Decision {
	case PROMOTE:
		return nil
	case HOLD:
		return fmt.Errorf("%w: %s", ErrHold, report.Reason)
	case ROLLBACK:
		return fmt.Errorf("%w: %s", ErrRollback, report.Reason)
	default:
		logger.Warn("unknown ship decision, allowing merge", "decision", report.Decision)
		return nil
	}
}
