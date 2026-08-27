package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// PendingDecision represents a decision awaiting human input.
type PendingDecision struct {
	ID         string
	WorkItemID string
	Question   string
	Context    string
	Options    []string
	CreatedAt  time.Time
}

// DecisionQueue manages human-in-the-loop decision routing via SQLite.
type DecisionQueue struct {
	DB     *sql.DB
	Logger *slog.Logger
}

// ensureTable creates the decision_queue table if it does not exist.
func (dq *DecisionQueue) ensureTable(ctx context.Context) error {
	_, err := dq.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS decision_queue (
			id TEXT PRIMARY KEY,
			work_item_id TEXT,
			question TEXT NOT NULL,
			context TEXT,
			options TEXT,
			decision TEXT,
			decided_at DATETIME,
			telegram_message_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating decision_queue table: %w", err)
	}
	return nil
}

// Submit inserts a pending decision and returns its ID.
func (dq *DecisionQueue) Submit(ctx context.Context, workItemID, question, qContext string, options []string) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", fmt.Errorf("generating decision id: %w", err)
	}

	var optionsJSON *string
	if len(options) > 0 {
		b, err := json.Marshal(options)
		if err != nil {
			return "", fmt.Errorf("marshalling options: %w", err)
		}
		s := string(b)
		optionsJSON = &s
	}

	var ctxVal *string
	if qContext != "" {
		ctxVal = &qContext
	}

	var wiVal *string
	if workItemID != "" {
		wiVal = &workItemID
	}

	_, err = dq.DB.ExecContext(ctx,
		`INSERT INTO decision_queue (id, work_item_id, question, context, options) VALUES (?, ?, ?, ?, ?)`,
		id, wiVal, question, ctxVal, optionsJSON,
	)
	if err != nil {
		return "", fmt.Errorf("inserting decision: %w", err)
	}

	dq.Logger.Info("decision submitted", "id", id, "work_item_id", workItemID, "question", truncate(question, 80))
	return id, nil
}

// Decide records a human's decision for the given decision ID.
func (dq *DecisionQueue) Decide(ctx context.Context, id, decision string) error {
	res, err := dq.DB.ExecContext(ctx,
		`UPDATE decision_queue SET decision = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ?`,
		decision, id,
	)
	if err != nil {
		return fmt.Errorf("recording decision %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for decision %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("decision %s not found", id)
	}
	dq.Logger.Info("decision recorded", "id", id, "decision", truncate(decision, 80))
	return nil
}

// PendingDecisions returns all decisions that have not yet been decided.
func (dq *DecisionQueue) PendingDecisions(ctx context.Context) ([]PendingDecision, error) {
	rows, err := dq.DB.QueryContext(ctx,
		`SELECT id, work_item_id, question, context, options, created_at
		 FROM decision_queue
		 WHERE decision IS NULL
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying pending decisions: %w", err)
	}
	defer rows.Close()

	var results []PendingDecision
	for rows.Next() {
		var d PendingDecision
		var wiID, qCtx, optJSON sql.NullString
		var createdAt string
		if err := rows.Scan(&d.ID, &wiID, &d.Question, &qCtx, &optJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning pending decision: %w", err)
		}
		d.WorkItemID = wiID.String
		d.Context = qCtx.String
		if optJSON.Valid {
			if err := json.Unmarshal([]byte(optJSON.String), &d.Options); err != nil {
				return nil, fmt.Errorf("unmarshalling options for %s: %w", d.ID, err)
			}
		}
		t, err := time.Parse("2006-01-02 15:04:05", createdAt)
		if err != nil {
			// Try alternate format with timezone.
			t, err = time.Parse(time.RFC3339, createdAt)
			if err != nil {
				return nil, fmt.Errorf("parsing created_at for %s: %w", d.ID, err)
			}
		}
		d.CreatedAt = t
		results = append(results, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending decisions: %w", err)
	}
	return results, nil
}

// GetDecision returns the decision string for the given work item ID.
// Returns (decision, true, nil) if decided, ("", false, nil) if pending or not found.
func (dq *DecisionQueue) GetDecision(ctx context.Context, workItemID string) (string, bool, error) {
	var decision sql.NullString
	err := dq.DB.QueryRowContext(ctx,
		`SELECT decision FROM decision_queue WHERE work_item_id = ? ORDER BY created_at DESC LIMIT 1`,
		workItemID,
	).Scan(&decision)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("getting decision for work item %s: %w", workItemID, err)
	}
	if !decision.Valid {
		return "", false, nil
	}
	return decision.String, true, nil
}

// AutoDecideStale auto-decides items older than maxAge with the given default decision.
// Returns the number of items auto-decided.
func (dq *DecisionQueue) AutoDecideStale(ctx context.Context, maxAge time.Duration, defaultDecision string) (int, error) {
	cutoff := time.Now().Add(-maxAge).UTC().Format("2006-01-02 15:04:05")

	res, err := dq.DB.ExecContext(ctx,
		`UPDATE decision_queue
		 SET decision = ?, decided_at = CURRENT_TIMESTAMP
		 WHERE decision IS NULL AND created_at < ?`,
		defaultDecision, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("auto-deciding stale decisions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected for auto-decide: %w", err)
	}
	if n > 0 {
		dq.Logger.Info("auto-decided stale decisions", "count", n, "max_age", maxAge, "default", defaultDecision)
	}
	return int(n), nil
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
