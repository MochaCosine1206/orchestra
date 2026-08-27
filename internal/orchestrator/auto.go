package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AutoOpts configures autonomous multi-cycle mode.
type AutoOpts struct {
	Goal        string // optional starting goal
	Sources     string // comma-separated: audit,markers,gaps
	MaxCycles   int    // circuit breaker: max cycles
	TestCmd     string // test command per cycle
	MaxParallel int    // max concurrent agents per cycle
	Interval    int    // monitor poll interval in seconds
	Review      bool   // enable review gates per cycle
	DryRun      bool   // show plan without executing
}

// AutoResult captures the outcome of autonomous mode.
type AutoResult struct {
	CyclesRun    int
	GoalsFound   int
	TasksDone    int
	TasksFailed  int
	StopReason   string
	Duration     time.Duration
}

const (
	defaultAutoMaxCycles    = 10
	maxEmptyCycles          = 2
	maxConsecutiveAllFail   = 3
	autoTimeout             = 6 * time.Hour
)

// Auto runs autonomous discovery-and-execution cycles.
// Sources: audit (run audit to find work), markers (TODO/FIXME), gaps (GAPS.md).
// Circuit breakers: 2 empty cycles, 3 all-fail cycles, 6hr timeout.
func (c *Conductor) Auto(ctx context.Context, opts AutoOpts) (*AutoResult, error) {
	start := time.Now()

	maxCycles := opts.MaxCycles
	if maxCycles <= 0 {
		maxCycles = defaultAutoMaxCycles
	}

	sources := parseAutoSources(opts.Sources)
	if len(sources) == 0 {
		sources = []string{"audit", "markers", "gaps"}
	}

	result := &AutoResult{}
	emptyCycles := 0
	allFailCycles := 0

	c.log("Auto mode: max %d cycles, sources=%v", maxCycles, sources)

	// Store auto mode state in blackboard
	autoCtx, cancel := context.WithTimeout(ctx, autoTimeout)
	defer cancel()

	for cycle := 1; cycle <= maxCycles; cycle++ {
		// Check for pause/stop signals
		if stop := c.getBlackboard(autoCtx, "auto:stop"); stop != "" {
			result.StopReason = "stopped"
			c.log("Auto: stopped by blackboard signal (%s)", stop)
			break
		}
		if pause := c.getBlackboard(autoCtx, "auto:pause"); pause != "" {
			c.log("Auto: paused (%s), waiting...", pause)
			for {
				time.Sleep(5 * time.Second)
				if c.getBlackboard(autoCtx, "auto:pause") == "" {
					break
				}
				if autoCtx.Err() != nil {
					result.StopReason = "timeout"
					break
				}
			}
			if result.StopReason != "" {
				break
			}
		}

		// Check timeout
		if autoCtx.Err() != nil {
			result.StopReason = "timeout"
			break
		}

		c.log("Auto cycle %d/%d", cycle, maxCycles)

		// Discover work
		goal := c.discoverWork(autoCtx, sources, opts.Goal)
		if goal == "" {
			emptyCycles++
			c.log("Auto cycle %d: no work found (empty=%d/%d)", cycle, emptyCycles, maxEmptyCycles)
			if emptyCycles >= maxEmptyCycles {
				result.StopReason = "empty_cycles"
				c.log("Auto: stopped after %d empty cycles", emptyCycles)
				break
			}
			continue
		}
		emptyCycles = 0 // reset on work found
		result.GoalsFound++

		// Execute the goal
		goResult, err := c.Go(autoCtx, GoOpts{
			Goal:        goal,
			TestCmd:     opts.TestCmd,
			MaxParallel: opts.MaxParallel,
			Interval:    opts.Interval,
			Review:      opts.Review,
			DryRun:      false,
		})
		if err != nil {
			c.log("Auto cycle %d: execution failed: %v", cycle, err)
			allFailCycles++
			result.TasksFailed++
			if allFailCycles >= maxConsecutiveAllFail {
				result.StopReason = "all_fail"
				c.log("Auto: stopped after %d consecutive all-fail cycles", allFailCycles)
				break
			}
			continue
		}

		// Check for goal drift after each auto cycle
		if driftResult, driftErr := c.CheckDrift(autoCtx, cycle); driftErr != nil {
			c.log("Auto cycle %d: drift check error: %v", cycle, driftErr)
		} else if driftResult != nil {
			c.log("Auto cycle %d: drift score=%.2f action=%s", cycle, driftResult.Score, driftResult.Action)
		}

		result.CyclesRun++
		result.TasksDone += goResult.DoneCount
		result.TasksFailed += goResult.FailedCount

		if goResult.FailedCount > 0 && goResult.DoneCount == 0 {
			allFailCycles++
		} else {
			allFailCycles = 0 // reset on any success
		}

		if allFailCycles >= maxConsecutiveAllFail {
			result.StopReason = "all_fail"
			c.log("Auto: stopped after %d consecutive all-fail cycles", allFailCycles)
			break
		}

		// Clear the starting goal after first cycle
		opts.Goal = ""
	}

	if result.StopReason == "" {
		result.StopReason = "max_cycles"
	}

	result.Duration = time.Since(start)
	c.log("Auto completed: %d cycles, %d done, %d failed, reason=%s, dur=%s",
		result.CyclesRun, result.TasksDone, result.TasksFailed, result.StopReason,
		result.Duration.Round(time.Second))

	return result, nil
}

// discoverWork uses the configured sources to find actionable goals.
func (c *Conductor) discoverWork(ctx context.Context, sources []string, startingGoal string) string {
	// If a starting goal is provided, use it
	if startingGoal != "" {
		return startingGoal
	}

	for _, source := range sources {
		switch source {
		case "audit":
			goal := c.discoverFromAudit(ctx)
			if goal != "" {
				return goal
			}
		case "markers":
			goal := c.discoverFromMarkers(ctx)
			if goal != "" {
				return goal
			}
		case "gaps":
			goal := c.discoverFromGaps(ctx)
			if goal != "" {
				return goal
			}
		}
	}
	return ""
}

// discoverFromAudit runs a code audit to find work.
func (c *Conductor) discoverFromAudit(ctx context.Context) string {
	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeTests})
	if err != nil {
		return ""
	}
	if len(result.Findings) > 0 {
		f := result.Findings[0]
		return fmt.Sprintf("Fix: %s in %s", f.Message, f.File)
	}
	return ""
}

// discoverFromMarkers scans for TODO/FIXME markers.
func (c *Conductor) discoverFromMarkers(ctx context.Context) string {
	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode})
	if err != nil {
		return ""
	}
	if len(result.Findings) > 0 {
		f := result.Findings[0]
		return fmt.Sprintf("Address: %s in %s", f.Message, f.File)
	}
	return ""
}

// discoverFromGaps scans GAPS.md for open gaps.
func (c *Conductor) discoverFromGaps(ctx context.Context) string {
	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeGaps})
	if err != nil {
		return ""
	}
	if len(result.Findings) > 0 {
		f := result.Findings[0]
		return fmt.Sprintf("Research gap: %s", f.Message)
	}
	return ""
}

func parseAutoSources(s string) []string {
	if s == "" {
		return nil
	}
	var sources []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			sources = append(sources, part)
		}
	}
	return sources
}
