package orchestra

import (
	"context"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// FairnessEngine applies boost adjustments to the priority_queue to ensure
// all projects receive attention over time.
type FairnessEngine struct {
	DB *db.DB
}

// MinimumAttentionBoost adds +100 to effective_priority for every project
// that has zero completed runs in the current calendar day (UTC).
// Returns the number of projects boosted.
func (f *FairnessEngine) MinimumAttentionBoost(ctx context.Context, now time.Time) (int, error) {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05")

	res, err := f.DB.ExecContext(ctx,
		`UPDATE priority_queue
		 SET effective_priority = effective_priority + 100,
		     computed_at = datetime('now')
		 WHERE project_path NOT IN (
		   SELECT DISTINCT project_path FROM run_history
		   WHERE started_at >= ?
		 )`, dayStart,
	)
	if err != nil {
		return 0, fmt.Errorf("applying minimum attention boost: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// StarvedProject holds info about a project that hasn't run in over 24 hours.
type StarvedProject struct {
	ProjectPath string
	LastRun     time.Time
	HoursSince  float64
}

// DetectStarvation returns projects present in the priority_queue whose most
// recent run_history entry is older than 24 hours, or who have never run.
func (f *FairnessEngine) DetectStarvation(ctx context.Context, now time.Time) ([]StarvedProject, error) {
	cutoff := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05")

	rows, err := f.DB.QueryContext(ctx,
		`SELECT DISTINCT pq.project_path,
		        COALESCE(MAX(rh.started_at), '') AS last_run
		 FROM priority_queue pq
		 LEFT JOIN run_history rh ON rh.project_path = pq.project_path
		 GROUP BY pq.project_path
		 HAVING last_run = '' OR last_run < ?`, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("detecting starvation: %w", err)
	}
	defer rows.Close()

	var starved []StarvedProject
	for rows.Next() {
		var sp StarvedProject
		var lastRunStr string
		if err := rows.Scan(&sp.ProjectPath, &lastRunStr); err != nil {
			return nil, fmt.Errorf("scanning starved project: %w", err)
		}
		if lastRunStr != "" {
			sp.LastRun, _ = time.Parse("2006-01-02 15:04:05", lastRunStr)
			sp.HoursSince = now.Sub(sp.LastRun).Hours()
		} else {
			sp.HoursSince = -1 // never run
		}
		starved = append(starved, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating starved projects: %w", err)
	}
	return starved, nil
}

// StarvationBoost adds +200 to effective_priority for all projects that are
// detected as starved (no run in >24 hours or never run). Returns the count boosted.
func (f *FairnessEngine) StarvationBoost(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05")

	res, err := f.DB.ExecContext(ctx,
		`UPDATE priority_queue
		 SET effective_priority = effective_priority + 200,
		     computed_at = datetime('now')
		 WHERE project_path IN (
		   SELECT pq2.project_path
		   FROM priority_queue pq2
		   LEFT JOIN run_history rh ON rh.project_path = pq2.project_path
		   GROUP BY pq2.project_path
		   HAVING MAX(rh.started_at) IS NULL OR MAX(rh.started_at) < ?
		 )`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("applying starvation boost: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
