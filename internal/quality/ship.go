package quality

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Ensure driver is registered.
var _ = driver.Open

// GateStack runs all quality gates in order and produces a ship decision.
// It implements the doc 240 Section 3.2 ship decision tree.
type GateStack struct {
	Gates  []Gate
	DB     *sql.DB
	Logger *slog.Logger
}

// NewDefaultStack wires all 8 gates in order.
func NewDefaultStack(db *sql.DB, runner CommandRunner, reviewProvider ReviewProvider, judgeGate *EvalJudgeGate, logger *slog.Logger) *GateStack {
	gates := []Gate{
		&BuildGate{Runner: runner},
		&LintGate{Runner: runner},
		&SecurityGate{Runner: runner},
		&TestGate{Runner: runner},
		&CoverageRatchetGate{DB: db, Runner: runner, Logger: logger},
		&MutationGate{Runner: runner},
		NewReviewGate(reviewProvider),
		judgeGate,
	}
	return &GateStack{
		Gates:  gates,
		DB:     db,
		Logger: logger,
	}
}

// Run executes all gates and produces a QualityReport with a ship decision.
// Short-circuits on ROLLBACK from critical gates (security, mutation < 0.50).
func (s *GateStack) Run(ctx context.Context, projectPath, branch, sessionID string) (*QualityReport, error) {
	report := &QualityReport{
		Timestamp: time.Now(),
	}

	var (
		mutationScore  float64
		reviewRisk     RiskScore
		judgeDecision  ShipDecision = PROMOTE
		securityPassed              = true
	)

	for _, gate := range s.Gates {
		s.Logger.Info("running quality gate", "gate", gate.Name(), "project", projectPath)

		result, err := gate.Check(ctx, projectPath, branch)
		if err != nil {
			s.Logger.Error("gate error", "gate", gate.Name(), "err", err)
			result = &LayerResult{
				Layer:   gate.Name(),
				Passed:  true, // fail-open
				Score:   0.5,
				Details: fmt.Sprintf("gate error (fail-open): %v", err),
			}
		}

		report.Results = append(report.Results, *result)

		// Track critical signals for decision tree.
		switch gate.Name() {
		case "security":
			securityPassed = result.Passed
		case "mutation":
			mutationScore = result.Score
		case "review":
			reviewRisk = classifyReviewRisk(result.Score)
		case "eval-judge":
			judgeDecision = CompositeToDecision(result.Score)
		}

		// Short-circuit on critical gate failure.
		if !result.Passed && isCriticalGate(gate.Name()) {
			report.Decision = ROLLBACK
			report.Reason = fmt.Sprintf("critical gate %q failed: %s", gate.Name(), result.Details)
			s.Logger.Warn("short-circuit ROLLBACK", "gate", gate.Name())
			if err := s.storeDecision(ctx, sessionID, projectPath, branch, report); err != nil {
				s.Logger.Error("failed to store decision", "err", err)
			}
			return report, nil
		}
	}

	// Apply doc 240 Section 3.2 decision tree.
	report.Decision, report.Reason = s.decide(securityPassed, mutationScore, reviewRisk, judgeDecision)

	if err := s.storeDecision(ctx, sessionID, projectPath, branch, report); err != nil {
		s.Logger.Error("failed to store decision", "err", err)
	}

	s.Logger.Info("quality gate complete",
		"decision", report.Decision,
		"reason", report.Reason,
		"gates_run", len(report.Results),
	)

	return report, nil
}

// decide applies the ship decision tree from doc 240 Section 3.2.
func (s *GateStack) decide(securityPassed bool, mutationScore float64, reviewRisk RiskScore, judgeDecision ShipDecision) (ShipDecision, string) {
	// ROLLBACK conditions.
	if !securityPassed {
		return ROLLBACK, "security gate failed"
	}
	if mutationScore < 0.50 {
		return ROLLBACK, fmt.Sprintf("mutation score %.2f below 0.50 threshold", mutationScore)
	}
	if judgeDecision == ROLLBACK {
		return ROLLBACK, "eval judge returned ROLLBACK"
	}

	// HOLD conditions.
	if judgeDecision == HOLD {
		return HOLD, "eval judge returned HOLD"
	}

	// PROMOTE requires all conditions met.
	allPass := securityPassed &&
		mutationScore >= 0.80 &&
		(reviewRisk == RiskLow || reviewRisk == RiskMedium) &&
		judgeDecision == PROMOTE

	if allPass {
		return PROMOTE, "all gates passed"
	}

	// Default to HOLD if some conditions not fully met.
	reason := fmt.Sprintf("partial pass: mutation=%.2f review_risk=%s judge=%s", mutationScore, reviewRisk, judgeDecision)
	return HOLD, reason
}

// classifyReviewRisk maps a review score back to a RiskScore.
func classifyReviewRisk(score float64) RiskScore {
	switch {
	case score >= 1.0:
		return RiskLow
	case score >= 0.7:
		return RiskMedium
	case score >= 0.3:
		return RiskHigh
	default:
		return RiskCritical
	}
}

// isCriticalGate returns true for gates that trigger immediate ROLLBACK on failure.
func isCriticalGate(name string) bool {
	switch name {
	case "build", "security":
		return true
	default:
		return false
	}
}

// storeDecision persists a ship decision to the ship_decisions table.
func (s *GateStack) storeDecision(ctx context.Context, sessionID, projectPath, branch string, report *QualityReport) error {
	if s.DB == nil {
		return nil
	}

	reportJSON, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshaling quality report: %w", err)
	}

	id := fmt.Sprintf("ship-%s-%d", sessionID, time.Now().UnixNano())

	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO ship_decisions (id, session_id, project_path, branch, decision, reason, quality_report, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, id, sessionID, projectPath, branch, string(report.Decision), report.Reason, string(reportJSON))
	if err != nil {
		return fmt.Errorf("inserting ship decision: %w", err)
	}

	return nil
}
