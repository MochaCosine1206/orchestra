package priority

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// ScoringEngine is the v2 priority engine that handles multiple input
// sources, the B-051 hierarchy, and produces explained rankings.
type ScoringEngine struct {
	collectors []Collector
	scorer     AlignmentScorer
	db         *db.DB
	userGoal   string
	logger     *slog.Logger
}

// AlignmentScorer scores how well a work item aligns with the user's goal.
type AlignmentScorer interface {
	ScoreGoalAlignment(ctx context.Context, description, goal string) (float64, error)
}

// NewScoringEngine creates a v2 engine.
func NewScoringEngine(database *db.DB, scorer AlignmentScorer, collectors []Collector) *ScoringEngine {
	return &ScoringEngine{
		db:         database,
		scorer:     scorer,
		collectors: collectors,
		logger:     slog.Default(),
	}
}

// SetUserGoal sets the current user goal for alignment scoring.
func (e *ScoringEngine) SetUserGoal(goal string) {
	e.userGoal = goal
}

// CollectAll runs all collectors and upserts results into work_items.
// Fail-open per collector: logs errors and continues.
func (e *ScoringEngine) CollectAll(ctx context.Context) (int, error) {
	total := 0
	for _, c := range e.collectors {
		items, err := c.Collect(ctx)
		if err != nil {
			e.logger.Warn("collector failed", "source", c.Name(), "error", err)
			continue
		}
		for _, item := range items {
			if err := e.upsertWorkItem(ctx, item); err != nil {
				e.logger.Warn("upsert failed", "source", c.Name(), "source_id", item.SourceID, "error", err)
				continue
			}
			total++
		}
	}
	return total, nil
}

// upsertWorkItem inserts or updates a work item by (source, source_id).
func (e *ScoringEngine) upsertWorkItem(ctx context.Context, item WorkItem) error {
	if item.ID == "" {
		item.ID = db.GenID("wi")
	}
	if item.Status == "" {
		item.Status = StatusPending
	}

	blockedByJSON, _ := json.Marshal(item.BlockedBy)
	blocksJSON, _ := json.Marshal(item.Blocks)
	marketJSON, _ := json.Marshal(item.MarketSignalIDs)
	rolesJSON, _ := json.Marshal(item.AgentRoles)

	boolToInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	// Check if item already exists by (source, source_id) — use two-step
	// INSERT/UPDATE instead of ON CONFLICT with partial index, which has
	// compatibility issues with ncruces/go-sqlite3 WASM.
	var existingID string
	err := e.db.QueryRowContext(ctx,
		`SELECT id FROM work_items WHERE source = ? AND source_id = ?`,
		string(item.Source), item.SourceID,
	).Scan(&existingID)

	if err == nil {
		// Update existing — but NEVER overwrite terminal statuses (completed/failed/exhausted).
		// Collectors re-create items every cycle; without this guard, completed items
		// get reset to 'pending' and re-enter the queue.
		var existingStatus string
		e.db.QueryRowContext(ctx, `SELECT status FROM work_items WHERE id = ?`, existingID).Scan(&existingStatus)
		if existingStatus == "completed" || existingStatus == "failed" || existingStatus == "exhausted" {
			return nil // don't overwrite terminal items
		}

		_, err = e.db.ExecContext(ctx, `
			UPDATE work_items SET
				title = ?, description = ?, tier = ?, base_priority = ?,
				feasibility = ?, impact = ?, uniqueness = ?,
				deadline = ?, deadline_type = ?, effort_hours = ?, effort_confidence = ?,
				blocked_by = ?, blocks = ?,
				user_picked = ?, user_picked_at = ?, user_priority_rank = ?,
				market_signal_ids = ?, competitor_shipped = ?,
				target_repo = ?, is_new_project = ?, agent_roles = ?,
				license_type = ?, license_reason = ?,
				status = ?, retry_count = ?, last_failure_reason = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			item.Title, item.Description, int(item.Tier), item.BasePriority,
			item.Feasibility, item.Impact, item.Uniqueness,
			item.Deadline, item.DeadlineType, item.EffortHours, item.EffortConfidence,
			string(blockedByJSON), string(blocksJSON),
			boolToInt(item.UserPicked), item.UserPickedAt, item.UserPriorityRank,
			string(marketJSON), boolToInt(item.CompetitorShipped),
			item.TargetRepo, boolToInt(item.IsNewProject), string(rolesJSON),
			item.LicenseType, item.LicenseReason,
			item.Status, item.RetryCount, item.LastFailureReason,
			existingID,
		)
	} else {
		// Insert new
		_, err = e.db.ExecContext(ctx, `
			INSERT INTO work_items (
				id, source, source_id, source_repo, title, description,
				tier, base_priority, feasibility, impact, uniqueness,
				deadline, deadline_type, effort_hours, effort_confidence,
				blocked_by, blocks,
				user_picked, user_picked_at, user_priority_rank,
				market_signal_ids, competitor_shipped,
				target_repo, is_new_project, agent_roles,
				goal_alignment, effective_priority, priority_explanation,
				license_type, license_reason,
				status, retry_count, last_failure_reason,
				created_at, updated_at
			) VALUES (
				?, ?, ?, ?, ?, ?,
				?, ?, ?, ?, ?,
				?, ?, ?, ?,
				?, ?,
				?, ?, ?,
				?, ?,
				?, ?, ?,
				?, ?, ?,
				?, ?,
				?, ?, ?,
				CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
			)`,
			item.ID, string(item.Source), item.SourceID, item.SourceRepo, item.Title, item.Description,
			int(item.Tier), item.BasePriority, item.Feasibility, item.Impact, item.Uniqueness,
			item.Deadline, item.DeadlineType, item.EffortHours, item.EffortConfidence,
			string(blockedByJSON), string(blocksJSON),
			boolToInt(item.UserPicked), item.UserPickedAt, item.UserPriorityRank,
			string(marketJSON), boolToInt(item.CompetitorShipped),
			item.TargetRepo, boolToInt(item.IsNewProject), string(rolesJSON),
			item.GoalAlignment, item.EffectivePriority, item.Explanation,
			item.LicenseType, item.LicenseReason,
			item.Status, item.RetryCount, item.LastFailureReason,
		)
	}
	if err != nil {
		return fmt.Errorf("upserting work item %s: %w", item.SourceID, err)
	}
	return nil
}

// ScoreAll recomputes effective_priority for all pending/queued/blocked work items.
func (e *ScoringEngine) ScoreAll(ctx context.Context) (int, error) {
	items, err := e.loadScorable(ctx)
	if err != nil {
		return 0, fmt.Errorf("loading scorable items: %w", err)
	}

	now := time.Now()
	scored := 0

	for i := range items {
		if len(items[i].BlockedBy) > 0 {
			allClear, depErr := e.checkDependencies(ctx, items[i].BlockedBy)
			if depErr == nil && !allClear {
				items[i].Status = StatusBlocked
			} else if allClear {
				items[i].Status = StatusPending
			}
		}

		if items[i].GoalAlignment == 0 && e.scorer != nil {
			alignment, _ := e.scorer.ScoreGoalAlignment(ctx, items[i].Description, e.userGoal)
			items[i].GoalAlignment = alignment
		}

		oldPriority := items[i].EffectivePriority
		result := ScoreWorkItem(items[i], now, e.userGoal)
		items[i].EffectivePriority = result.EffectivePriority
		items[i].Explanation = result.Explanation

		if err := e.updateScore(ctx, items[i]); err != nil {
			e.logger.Warn("update score failed", "id", items[i].ID, "error", err)
			continue
		}

		if oldPriority != result.EffectivePriority {
			e.recordHistory(ctx, items[i].ID, oldPriority, result.EffectivePriority, "rescore")
		}
		scored++
	}

	return scored, nil
}

// loadScorable returns work items eligible for scoring.
func (e *ScoringEngine) loadScorable(ctx context.Context) ([]WorkItem, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, source, source_id, source_repo, title, description,
			tier, base_priority, feasibility, impact, uniqueness,
			deadline, deadline_type, effort_hours, effort_confidence,
			blocked_by, blocks,
			user_picked, user_picked_at, user_priority_rank,
			market_signal_ids, competitor_shipped,
			target_repo, is_new_project, agent_roles,
			goal_alignment, effective_priority, priority_explanation,
			license_type, license_reason,
			status, retry_count, last_failure_reason,
			created_at, updated_at, scored_at, stale_after, last_validated_at
		FROM work_items
		WHERE status IN ('pending', 'queued', 'blocked')`)
	if err != nil {
		return nil, fmt.Errorf("querying scorable items: %w", err)
	}
	defer rows.Close()

	var items []WorkItem
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning work item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// updateScore writes the computed score and explanation back to the DB.
func (e *ScoringEngine) updateScore(ctx context.Context, item WorkItem) error {
	_, err := e.db.ExecContext(ctx, `
		UPDATE work_items
		SET effective_priority = ?, priority_explanation = ?, scored_at = CURRENT_TIMESTAMP,
			status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		item.EffectivePriority, item.Explanation, item.Status, item.ID)
	if err != nil {
		return fmt.Errorf("updating score for %s: %w", item.ID, err)
	}
	return nil
}

// checkDependencies returns true if all blocker IDs have status=completed.
func (e *ScoringEngine) checkDependencies(ctx context.Context, blockerIDs []string) (bool, error) {
	for _, id := range blockerIDs {
		var status string
		err := e.db.QueryRowContext(ctx, `SELECT status FROM work_items WHERE id = ?`, id).Scan(&status)
		if err == sql.ErrNoRows {
			continue // missing dependency treated as clear
		}
		if err != nil {
			return false, fmt.Errorf("checking dependency %s: %w", id, err)
		}
		if status != StatusCompleted {
			return false, nil
		}
	}
	return true, nil
}

// recordHistory inserts a priority change record.
func (e *ScoringEngine) recordHistory(ctx context.Context, itemID string, oldPriority, newPriority float64, reason string) {
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO priority_history (work_item_id, old_priority, new_priority, reason)
		VALUES (?, ?, ?, ?)`,
		itemID, oldPriority, newPriority, reason)
	if err != nil {
		e.logger.Warn("recording priority history failed", "id", itemID, "error", err)
	}
}

// TopN returns the top N work items by effective_priority.
func (e *ScoringEngine) TopN(ctx context.Context, n int) ([]WorkItem, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, source, source_id, source_repo, title, description,
			tier, base_priority, feasibility, impact, uniqueness,
			deadline, deadline_type, effort_hours, effort_confidence,
			blocked_by, blocks,
			user_picked, user_picked_at, user_priority_rank,
			market_signal_ids, competitor_shipped,
			target_repo, is_new_project, agent_roles,
			goal_alignment, effective_priority, priority_explanation,
			license_type, license_reason,
			status, retry_count, last_failure_reason,
			created_at, updated_at, scored_at, stale_after, last_validated_at
		FROM work_items
		WHERE status IN ('pending', 'queued') AND effective_priority > 0
		ORDER BY effective_priority DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("querying top N items: %w", err)
	}
	defer rows.Close()

	var items []WorkItem
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning top N item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PopulateQueue moves the top N items into priority_queue for the daemon.
// Bridges v2 work_items to the existing v1 priority_queue consumed by Scheduler.SelectNext().
func (e *ScoringEngine) PopulateQueue(ctx context.Context, n int, registeredRepos []string) error {
	items, err := e.TopN(ctx, n)
	if err != nil {
		return fmt.Errorf("getting top N for queue: %w", err)
	}

	return e.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM priority_queue`); err != nil {
			return fmt.Errorf("clearing priority_queue: %w", err)
		}

		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		for i := range items {
			RouteWorkItem(&items[i], registeredRepos)
			execMode := DetermineExecutionMode(&items[i])

			_, err := tx.ExecContext(ctx, `
				INSERT INTO priority_queue (id, project_path, task_id, effective_priority, computed_at, execution_mode)
				VALUES (?, ?, ?, ?, ?, ?)`,
				items[i].ID, items[i].TargetRepo, items[i].Title, items[i].EffectivePriority, now, execMode)
			if err != nil {
				return fmt.Errorf("inserting into priority_queue: %w", err)
			}

			if _, err := tx.ExecContext(ctx, `
				UPDATE work_items SET status = 'queued', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				items[i].ID); err != nil {
				return fmt.Errorf("updating work item status to queued: %w", err)
			}
		}
		return nil
	})
}

// scanWorkItem scans a single work item row from a query result.
func scanWorkItem(rows *sql.Rows) (WorkItem, error) {
	var item WorkItem
	var source, sourceID, sourceRepo sql.NullString
	var tier int
	var blockedByJSON, blocksJSON, marketJSON, rolesJSON sql.NullString
	var userPicked, competitorShipped, isNewProject int
	var goalAlignment, effectivePriority sql.NullFloat64
	var explanation, licenseType, licenseReason sql.NullString
	var deadline, userPickedAt, scoredAt, staleAfter, lastValidatedAt sql.NullString
	var deadlineType, targetRepo, status, lastFailureReason sql.NullString
	var feasibility, impact, uniqueness, effortHours, effortConfidence sql.NullFloat64
	var userPriorityRank sql.NullInt64
	var createdAt, updatedAt string

	err := rows.Scan(
		&item.ID, &source, &sourceID, &sourceRepo, &item.Title, &item.Description,
		&tier, &item.BasePriority, &feasibility, &impact, &uniqueness,
		&deadline, &deadlineType, &effortHours, &effortConfidence,
		&blockedByJSON, &blocksJSON,
		&userPicked, &userPickedAt, &userPriorityRank,
		&marketJSON, &competitorShipped,
		&targetRepo, &isNewProject, &rolesJSON,
		&goalAlignment, &effectivePriority, &explanation,
		&licenseType, &licenseReason,
		&status, &item.RetryCount, &lastFailureReason,
		&createdAt, &updatedAt, &scoredAt, &staleAfter, &lastValidatedAt,
	)
	if err != nil {
		return WorkItem{}, err
	}

	if source.Valid {
		item.Source = Source(source.String)
	}
	if sourceID.Valid {
		item.SourceID = sourceID.String
	}
	if sourceRepo.Valid {
		item.SourceRepo = sourceRepo.String
	}
	item.Tier = Tier(tier)
	item.UserPicked = userPicked != 0
	item.CompetitorShipped = competitorShipped != 0
	item.IsNewProject = isNewProject != 0

	if feasibility.Valid {
		item.Feasibility = &feasibility.Float64
	}
	if impact.Valid {
		item.Impact = &impact.Float64
	}
	if uniqueness.Valid {
		item.Uniqueness = &uniqueness.Float64
	}
	if effortHours.Valid {
		item.EffortHours = &effortHours.Float64
	}
	if effortConfidence.Valid {
		item.EffortConfidence = &effortConfidence.Float64
	}
	if goalAlignment.Valid {
		item.GoalAlignment = goalAlignment.Float64
	}
	if effectivePriority.Valid {
		item.EffectivePriority = effectivePriority.Float64
	}
	if userPriorityRank.Valid {
		rank := int(userPriorityRank.Int64)
		item.UserPriorityRank = &rank
	}
	if explanation.Valid {
		item.Explanation = explanation.String
	}
	if licenseType.Valid {
		item.LicenseType = licenseType.String
	}
	if licenseReason.Valid {
		item.LicenseReason = licenseReason.String
	}
	if targetRepo.Valid {
		item.TargetRepo = targetRepo.String
	}
	if status.Valid {
		item.Status = status.String
	}
	if lastFailureReason.Valid {
		item.LastFailureReason = lastFailureReason.String
	}
	if deadlineType.Valid {
		item.DeadlineType = deadlineType.String
	}

	if deadline.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", deadline.String)
		if err == nil {
			item.Deadline = &t
		}
	}
	if userPickedAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", userPickedAt.String)
		if err == nil {
			item.UserPickedAt = &t
		}
	}
	if scoredAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", scoredAt.String)
		if err == nil {
			item.ScoredAt = &t
		}
	}
	if staleAfter.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", staleAfter.String)
		if err == nil {
			item.StaleAfter = &t
		}
	}
	if lastValidatedAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", lastValidatedAt.String)
		if err == nil {
			item.LastValidatedAt = &t
		}
	}

	item.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	item.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)

	if blockedByJSON.Valid {
		json.Unmarshal([]byte(blockedByJSON.String), &item.BlockedBy)
	}
	if blocksJSON.Valid {
		json.Unmarshal([]byte(blocksJSON.String), &item.Blocks)
	}
	if marketJSON.Valid {
		json.Unmarshal([]byte(marketJSON.String), &item.MarketSignalIDs)
	}
	if rolesJSON.Valid {
		json.Unmarshal([]byte(rolesJSON.String), &item.AgentRoles)
	}

	return item, nil
}
