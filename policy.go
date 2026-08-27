package orchestra

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// ExecutionMode determines how the daemon executes a work item.
type ExecutionMode string

const (
	ModeConduct  ExecutionMode = "conduct"  // multi-agent decompose via conductor-run (default)
	ModeResearch ExecutionMode = "research" // single researcher agent via claude -p
	ModeGrant    ExecutionMode = "grant"    // grant application pipeline via claude -p
	ModeDirect   ExecutionMode = "direct"   // single claude -p call with goal as prompt
)

// QueueEntry represents a row from the priority_queue table.
type QueueEntry struct {
	ID                string
	ProjectPath       string
	TaskID            string
	EffectivePriority float64
	ComputedAt        time.Time
	ExecutionMode     ExecutionMode
}

// Scheduler selects the next task to run from the daemon's priority queue.
type Scheduler struct {
	DB            *db.DB
	MaxConcurrent int
}

// SelectNext returns the highest effective-priority entry from priority_queue,
// or nil if the queue is empty or MaxConcurrent running sessions are already active.
func (s *Scheduler) SelectNext(ctx context.Context) (*QueueEntry, error) {
	if s.MaxConcurrent > 0 {
		var running int
		err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM run_history WHERE status = 'running'`,
		).Scan(&running)
		if err != nil {
			return nil, fmt.Errorf("counting running sessions: %w", err)
		}
		if running >= s.MaxConcurrent {
			return nil, nil
		}
	}

	row := s.DB.QueryRowContext(ctx,
		`SELECT id, project_path, task_id, effective_priority, computed_at,
		        COALESCE(execution_mode, 'conduct')
		 FROM priority_queue
		 ORDER BY effective_priority DESC
		 LIMIT 1`,
	)

	var e QueueEntry
	var computedAt string
	var mode string
	err := row.Scan(&e.ID, &e.ProjectPath, &e.TaskID, &e.EffectivePriority, &computedAt, &mode)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		// Fallback: column might not exist yet (pre-migration)
		row2 := s.DB.QueryRowContext(ctx,
			`SELECT id, project_path, task_id, effective_priority, computed_at
			 FROM priority_queue ORDER BY effective_priority DESC LIMIT 1`)
		err = row2.Scan(&e.ID, &e.ProjectPath, &e.TaskID, &e.EffectivePriority, &computedAt)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("scanning priority_queue: %w", err)
		}
		mode = "conduct"
	}
	e.ExecutionMode = ExecutionMode(mode)
	e.ComputedAt, _ = time.Parse("2006-01-02 15:04:05", computedAt)
	return &e, nil
}

// ProjectWeight pairs a project path with a scheduling weight.
type ProjectWeight struct {
	ProjectPath string
	Weight      float64
}

// WeightedFairSelect implements Kueue-model allocation: each project's scheduling
// probability is proportional to its configured weight. It collects distinct projects
// from the priority queue, assigns weights from the provided map (defaulting to 1.0),
// and selects a project probabilistically. It then returns the highest-priority entry
// for that project.
func (s *Scheduler) WeightedFairSelect(ctx context.Context, weights map[string]float64) (*QueueEntry, error) {
	if s.MaxConcurrent > 0 {
		var running int
		err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM run_history WHERE status = 'running'`,
		).Scan(&running)
		if err != nil {
			return nil, fmt.Errorf("counting running sessions: %w", err)
		}
		if running >= s.MaxConcurrent {
			return nil, nil
		}
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT project_path FROM priority_queue`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying distinct projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectWeight
	var totalWeight float64
	for rows.Next() {
		var pw ProjectWeight
		if err := rows.Scan(&pw.ProjectPath); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		pw.Weight = 1.0
		if w, ok := weights[pw.ProjectPath]; ok && w > 0 {
			pw.Weight = w
		}
		totalWeight += pw.Weight
		projects = append(projects, pw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, nil
	}

	// Weighted random selection
	r := rand.Float64() * totalWeight
	var cumulative float64
	var selected string
	for _, pw := range projects {
		cumulative += pw.Weight
		if r <= cumulative {
			selected = pw.ProjectPath
			break
		}
	}
	if selected == "" {
		selected = projects[len(projects)-1].ProjectPath
	}

	row := s.DB.QueryRowContext(ctx,
		`SELECT id, project_path, task_id, effective_priority, computed_at
		 FROM priority_queue
		 WHERE project_path = ?
		 ORDER BY effective_priority DESC
		 LIMIT 1`, selected,
	)

	var e QueueEntry
	var computedAt string
	err = row.Scan(&e.ID, &e.ProjectPath, &e.TaskID, &e.EffectivePriority, &computedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning selected entry: %w", err)
	}
	e.ComputedAt, _ = time.Parse("2006-01-02 15:04:05", computedAt)
	return &e, nil
}
