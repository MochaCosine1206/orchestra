package db

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema/001_initial.sql
var initialSchema string

//go:embed schema/002_conductors.sql
var conductorsSchema string

//go:embed schema/003_merge_state.sql
var mergeStateSchema string

//go:embed schema/004_merge_mode.sql
var mergeModeSchema string

//go:embed schema/005_phase_id.sql
var phaseIDSchema string

//go:embed schema/006_merge_queue.sql
var mergeQueueSchema string

//go:embed schema/007_feature_cluster.sql
var featureClusterSchema string

//go:embed schema/008_conductor_phase.sql
var conductorPhaseSchema string

//go:embed schema/009_stall_tracking.sql
var stallTrackingSchema string

//go:embed schema/010_drift_tracking.sql
var driftTrackingSchema string

//go:embed schema/011_plan_cache.sql
var planCacheSchema string

//go:embed schema/012_eval_healing.sql
var evalHealingSchema string

//go:embed schema/015_quality_infrastructure.sql
var qualityInfraSchema string

//go:embed schema/017_execution_mode.sql
var executionModeSchema string

// InitSchema creates all tables if they don't exist and runs migrations.
func (d *DB) InitSchema(ctx context.Context) error {
	if d.readOnly {
		return fmt.Errorf("cannot initialize schema on read-only connection")
	}
	_, err := d.ExecContext(ctx, initialSchema)
	if err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}

	// Migration 002: conductors table + conductor_id columns
	if err := d.migrate002Conductors(ctx); err != nil {
		return fmt.Errorf("migration 002 conductors: %w", err)
	}

	// Migration 003: merge state tracking columns on conductors (B-145)
	if err := d.migrate003MergeState(ctx); err != nil {
		return fmt.Errorf("migration 003 merge state: %w", err)
	}

	// Migration 004: merge_mode column on conductors (B-273)
	if err := d.migrate004MergeMode(ctx); err != nil {
		return fmt.Errorf("migration 004 merge mode: %w", err)
	}

	// Migration 005: phase_id column on tasks (G110)
	if err := d.migrate005PhaseID(ctx); err != nil {
		return fmt.Errorf("migration 005 phase_id: %w", err)
	}

	// Migration 006: merge queue table + merge_strategy column (B-280)
	if err := d.migrate006MergeQueue(ctx); err != nil {
		return fmt.Errorf("migration 006 merge queue: %w", err)
	}

	// Migration 007: feature_cluster column on tasks (B-281)
	if err := d.migrate007FeatureCluster(ctx); err != nil {
		return fmt.Errorf("migration 007 feature cluster: %w", err)
	}

	// Migration 008: phase_id column on conductors (B-287)
	if err := d.migrate008ConductorPhase(ctx); err != nil {
		return fmt.Errorf("migration 008 conductor phase: %w", err)
	}

	// Migration 009: stall score tracking table (B-142)
	if err := d.migrate009StallTracking(ctx); err != nil {
		return fmt.Errorf("migration 009 stall tracking: %w", err)
	}

	// Migration 010: drift score tracking table
	if err := d.migrate010DriftTracking(ctx); err != nil {
		return fmt.Errorf("migration 010 drift tracking: %w", err)
	}

	// Migration 011: plan cache table
	if err := d.migrate011PlanCache(ctx); err != nil {
		return fmt.Errorf("migration 011 plan cache: %w", err)
	}

	// Migration 012: eval framework and healing log tables
	if err := d.migrate012EvalHealing(ctx); err != nil {
		return fmt.Errorf("migration 012 eval healing: %w", err)
	}

	// Migration 015: quality infrastructure tables (ratchet + ship decisions)
	if err := d.migrate015QualityInfra(ctx); err != nil {
		return fmt.Errorf("migration 015 quality infrastructure: %w", err)
	}

	// Migration 017: add execution_mode to priority_queue
	if err := d.migrate017ExecutionMode(ctx); err != nil {
		return fmt.Errorf("migration 017 execution mode: %w", err)
	}

	return nil
}

// migrate003MergeState adds merge state tracking columns to the conductors table.
// ALTER TABLE ADD COLUMN is not idempotent in SQLite, so we guard against duplicates.
func (d *DB) migrate003MergeState(ctx context.Context) error {
	_ = mergeStateSchema // referenced so go:embed is used
	columns := []struct {
		name    string
		colType string
	}{
		{"merge_status", "TEXT"},
		{"merge_started_at", "DATETIME"},
		{"merge_branches_done", "TEXT"},
	}
	for _, col := range columns {
		_, err := d.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE conductors ADD COLUMN %s %s", col.name, col.colType))
		if err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("adding conductors.%s: %w", col.name, err)
		}
	}
	return nil
}

// migrate004MergeMode adds the merge_mode column to the conductors table.
// ALTER TABLE ADD COLUMN is not idempotent in SQLite, so we guard against duplicates.
func (d *DB) migrate004MergeMode(ctx context.Context) error {
	_ = mergeModeSchema // referenced so go:embed is used
	_, err := d.ExecContext(ctx,
		"ALTER TABLE conductors ADD COLUMN merge_mode TEXT DEFAULT 'local'")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("adding conductors.merge_mode: %w", err)
	}
	return nil
}

// migrate002Conductors applies the conductors migration.
// ALTER TABLE ADD COLUMN is not idempotent in SQLite, so we check
// for existing columns before adding them.
func (d *DB) migrate002Conductors(ctx context.Context) error {
	// Create the conductors table + index (IF NOT EXISTS makes this safe)
	_, err := d.ExecContext(ctx, conductorsSchema)
	if err != nil {
		return fmt.Errorf("creating conductors table: %w", err)
	}

	// Add conductor_id columns to existing tables (safe: ignore "duplicate column" errors)
	alterStmts := []struct {
		table  string
		column string
	}{
		{"tasks", "conductor_id"},
		{"agents", "conductor_id"},
		{"file_locks", "conductor_id"},
	}
	for _, s := range alterStmts {
		_, err := d.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT REFERENCES conductors(id)", s.table, s.column))
		if err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("adding %s.%s: %w", s.table, s.column, err)
		}
	}

	// Create indexes for the new FK columns
	indexStmts := []string{
		"CREATE INDEX IF NOT EXISTS idx_tasks_conductor ON tasks(conductor_id)",
		"CREATE INDEX IF NOT EXISTS idx_agents_conductor ON agents(conductor_id)",
	}
	for _, stmt := range indexStmts {
		if _, err := d.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating conductor index: %w", err)
		}
	}

	return nil
}

// migrate005PhaseID adds the phase_id column to the tasks table (G110).
// Enables phase-scoped merge filtering for multi-phase GoSpec() execution.
func (d *DB) migrate005PhaseID(ctx context.Context) error {
	_ = phaseIDSchema // referenced so go:embed is used
	_, err := d.ExecContext(ctx, "ALTER TABLE tasks ADD COLUMN phase_id TEXT")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("adding tasks.phase_id: %w", err)
	}
	_, err = d.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_tasks_phase ON tasks(phase_id)")
	if err != nil {
		return fmt.Errorf("creating phase_id index: %w", err)
	}
	return nil
}

// migrate007FeatureCluster adds the feature_cluster column to the tasks table (B-281).
// Enables hierarchical decomposition: tasks grouped into feature clusters.
func (d *DB) migrate007FeatureCluster(ctx context.Context) error {
	_ = featureClusterSchema // referenced so go:embed is used
	_, err := d.ExecContext(ctx, "ALTER TABLE tasks ADD COLUMN feature_cluster TEXT")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("adding tasks.feature_cluster: %w", err)
	}
	return nil
}

// migrate008ConductorPhase adds phase_id to the conductors table (B-287).
// Enables --start-phase to look up the prior phase's staging branch from a
// previous GoSpec() invocation.
func (d *DB) migrate008ConductorPhase(ctx context.Context) error {
	_ = conductorPhaseSchema // referenced so go:embed is used
	_, err := d.ExecContext(ctx, "ALTER TABLE conductors ADD COLUMN phase_id TEXT")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("adding conductors.phase_id: %w", err)
	}
	_, err = d.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_conductors_phase ON conductors(phase_id)")
	if err != nil {
		return fmt.Errorf("creating conductors phase_id index: %w", err)
	}
	return nil
}

// migrate009StallTracking creates the stall_scores table for stalled agent detection (B-142).
// CREATE TABLE IF NOT EXISTS makes this idempotent.
func (d *DB) migrate009StallTracking(ctx context.Context) error {
	_, err := d.ExecContext(ctx, stallTrackingSchema)
	if err != nil {
		return fmt.Errorf("creating stall_scores table: %w", err)
	}
	return nil
}

// migrate010DriftTracking creates the drift_scores table for goal drift detection.
// CREATE TABLE IF NOT EXISTS makes this idempotent.
func (d *DB) migrate010DriftTracking(ctx context.Context) error {
	_, err := d.ExecContext(ctx, driftTrackingSchema)
	if err != nil {
		return fmt.Errorf("creating drift_scores table: %w", err)
	}
	return nil
}

// migrate011PlanCache creates the plan_cache table for decomposition plan reuse.
// CREATE TABLE IF NOT EXISTS makes this idempotent.
func (d *DB) migrate011PlanCache(ctx context.Context) error {
	_, err := d.ExecContext(ctx, planCacheSchema)
	if err != nil {
		return fmt.Errorf("creating plan_cache table: %w", err)
	}
	return nil
}

// migrate012EvalHealing creates the eval framework and healing log tables.
// CREATE TABLE IF NOT EXISTS makes this idempotent.
func (d *DB) migrate012EvalHealing(ctx context.Context) error {
	_, err := d.ExecContext(ctx, evalHealingSchema)
	if err != nil {
		return fmt.Errorf("creating eval/healing tables: %w", err)
	}
	return nil
}

// migrate015QualityInfra creates the quality_ratchet and ship_decisions tables.
// CREATE TABLE IF NOT EXISTS makes this idempotent.
func (d *DB) migrate015QualityInfra(ctx context.Context) error {
	_, err := d.ExecContext(ctx, qualityInfraSchema)
	if err != nil {
		return fmt.Errorf("creating quality infrastructure tables: %w", err)
	}
	return nil
}

// migrate006MergeQueue creates the merge_queue_entries table and adds
// merge_strategy column to conductors (B-280).
func (d *DB) migrate006MergeQueue(ctx context.Context) error {
	// Create the merge_queue_entries table + indexes (IF NOT EXISTS makes this safe)
	_, err := d.ExecContext(ctx, mergeQueueSchema)
	if err != nil {
		return fmt.Errorf("creating merge_queue_entries table: %w", err)
	}
	// Add merge_strategy column to conductors
	_, err = d.ExecContext(ctx, "ALTER TABLE conductors ADD COLUMN merge_strategy TEXT DEFAULT 'batch'")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("adding conductors.merge_strategy: %w", err)
	}
	return nil
}

// migrate017ExecutionMode adds execution_mode column to priority_queue.
// The priority_queue table lives in the daemon DB but may be initialized here
// if the per-project DB also has it. Safe to call on both — guards against
// missing table and duplicate column.
func (d *DB) migrate017ExecutionMode(ctx context.Context) error {
	_ = executionModeSchema
	_, err := d.ExecContext(ctx,
		"ALTER TABLE priority_queue ADD COLUMN execution_mode TEXT DEFAULT 'conduct'")
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "duplicate column") || strings.Contains(errStr, "no such table") {
			return nil // table doesn't exist here or column already added
		}
		return fmt.Errorf("adding priority_queue.execution_mode: %w", err)
	}
	return nil
}
