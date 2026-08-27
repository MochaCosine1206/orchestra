package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/eval"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/priority"
	v2 "github.com/MochaCosine1206/orchestra/internal/priority/v2"
)

const defaultInterval = 30 * time.Second

// GovernorChecker checks whether spawning is allowed given resource constraints.
// Wire to governor.ResourceGovernor.CanSpawn.
type GovernorChecker interface {
	CanSpawn(ctx context.Context) (bool, string)
}

// Daemon is the core runtime loop that orchestrates watchdog, cron, priority,
// and load-aware spawning across all registered projects.
type Daemon struct {
	Loader        *Loader
	Watchdog      *Watchdog
	Engine        *priority.Engine
	ScoringEngine *v2.ScoringEngine // v2 priority engine (nil = v1 fallback)
	Scheduler     *orchestra.Scheduler
	DB            *db.DB // global daemon DB for queue operations
	Governor      GovernorChecker    // optional: Phase 5 resource governor pre-spawn gate
	Log           func(string)

	interval time.Duration
	stopCh   chan struct{}
	mu       sync.Mutex
	running  bool
}

// New creates a Daemon with default configuration.
func New(engine *priority.Engine, log func(string)) (*Daemon, error) {
	loader, err := NewLoader()
	if err != nil {
		return nil, fmt.Errorf("creating loader: %w", err)
	}
	interval := defaultInterval
	if loader.Config.Interval != "" {
		if d, err := time.ParseDuration(loader.Config.Interval); err == nil && d > 0 {
			interval = d
		}
	}
	return &Daemon{
		Loader:   loader,
		Watchdog: NewWatchdog(log),
		Engine:   engine,
		Log:      log,
		interval: interval,
	}, nil
}

// CycleResult captures what happened during one daemon cycle.
type CycleResult struct {
	QuietHours     bool
	ConfigReloaded bool
	CronDue        []ScheduleConfig
	CollectedItems int
	ScoredItems    int
	QueuePopulated bool
	WatchdogStatus []ProjectStatus
	SpawnSkipped    bool
	SpawnReason     string
	GovernorBlocked bool
	SelectedEntry  *orchestra.QueueEntry
	PreemptChecked bool
	EvalRan        bool
	EvalPassed     int
	EvalFailed     int
}

// Run starts the daemon loop and blocks until ctx is cancelled or Stop is called.
func (d *Daemon) Run(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("daemon already running")
	}
	d.running = true
	d.stopCh = make(chan struct{})
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
	}()

	d.logf("daemon started (interval=%s)", d.interval)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			d.logf("daemon stopped via Stop()")
			return nil
		case <-ctx.Done():
			d.logf("daemon stopped via context")
			return ctx.Err()
		case <-ticker.C:
			if _, err := d.RunOnce(ctx); err != nil {
				d.logf("cycle error: %v", err)
			}
		}
	}
}

// Stop signals the daemon loop to exit.
func (d *Daemon) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		close(d.stopCh)
	}
}

// Running returns whether the daemon loop is active.
func (d *Daemon) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// RunOnce executes a single daemon cycle. Callable from tests.
// Phase order: (0) quiet hours, (1) config reload, (2) cron check,
// (3) priority recalc, (4) watchdog, (5) spawn-if-needed.
func (d *Daemon) RunOnce(ctx context.Context) (*CycleResult, error) {
	result := &CycleResult{}
	now := time.Now()

	// Phase 0: Quiet hours check
	if d.Loader.Config.QuietHours != nil {
		result.QuietHours = IsQuietHours(
			d.Loader.Config.QuietHours.Start,
			d.Loader.Config.QuietHours.End,
			now,
		)
	}

	// Phase 1: Config reload (mtime-based)
	reloaded, err := d.Loader.Reload()
	if err != nil {
		d.logf("config reload error: %v", err)
	}
	result.ConfigReloaded = reloaded
	if reloaded {
		d.logf("config reloaded")
		// Update interval if changed
		if d.Loader.Config.Interval != "" {
			if dur, err := time.ParseDuration(d.Loader.Config.Interval); err == nil && dur > 0 {
				d.interval = dur
			}
		}
	}

	// Phase 2: Cron check — due schedules become work items in the priority queue.
	// Priority levels: "background" (tier 6), "daily" (tier 4 + today deadline),
	// "critical" (tier 1), "fix" (tier 2). Default is "background".
	schedules := convertSchedules(d.Loader.Config.Schedules)
	result.CronDue = CheckSchedules(schedules, now)
	if len(result.CronDue) > 0 {
		d.logf("cron: %d schedule(s) due", len(result.CronDue))
		if d.DB != nil {
			for _, sched := range result.CronDue {
				tier := v2.TierTechDebt // default: background
				var deadline *time.Time
				switch sched.Priority {
				case "daily":
					tier = v2.TierGoalResearch
					eod := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, now.Location())
					deadline = &eod
				case "critical":
					tier = v2.TierUser
				case "fix":
					tier = v2.TierRetry
				}
				_, err := d.DB.ExecContext(ctx,
					`INSERT OR IGNORE INTO priority_queue (id, project_path, task_id, effective_priority)
					 VALUES (?, ?, ?, ?)`,
					db.GenID("cron"), sched.Project, sched.Goal, float64(tier)*100)
				if err != nil {
					d.logf("cron: failed to queue %s: %v", sched.Goal, err)
				} else {
					d.logf("cron: queued '%s' (priority=%s, tier=%d)", sched.Goal, sched.Priority, tier)
				}
				_ = deadline // TODO: wire deadline into work_items when v2 CronCollector is added
			}
		}
	}

	// Phase 2b: v2 collection — gather work items from all sources
	if d.ScoringEngine != nil {
		collected, err := d.ScoringEngine.CollectAll(ctx)
		if err != nil {
			d.logf("v2 collect: error: %v", err)
		} else {
			result.CollectedItems = collected
			if collected > 0 {
				d.logf("v2 collect: gathered %d work items", collected)
			}
		}
	}

	// Phase 3: Priority recalc — v2 scoring or v1 fallback
	if d.ScoringEngine != nil {
		scored, err := d.ScoringEngine.ScoreAll(ctx)
		if err != nil {
			d.logf("v2 score: error: %v", err)
		} else {
			result.ScoredItems = scored
			if scored > 0 {
				d.logf("v2 score: scored %d work items", scored)
			}
		}
	} else if d.Engine != nil && d.DB != nil {
		if updated, err := d.recalcPriorities(ctx); err != nil {
			d.logf("priority: recalc error: %v", err)
		} else if updated > 0 {
			d.logf("priority: recalculated %d queue entries", updated)
		}
	}

	// Phase 3a: v2 queue population — bridge scored items to priority_queue
	if d.ScoringEngine != nil {
		repos := d.registeredRepoPaths()
		if err := d.ScoringEngine.PopulateQueue(ctx, 20, repos); err != nil {
			d.logf("v2 queue: populate error: %v", err)
		} else {
			result.QueuePopulated = true
			d.logf("v2 queue: populated priority_queue")
		}
	}

	// Phase 3b: Scheduler selection and preemption check
	if d.Scheduler != nil {
		entry, err := d.Scheduler.SelectNext(ctx)
		if err != nil {
			d.logf("scheduler: SelectNext error: %v", err)
		} else if entry != nil {
			result.SelectedEntry = entry
			d.logf("scheduler: selected %s (project=%s, priority=%.1f)",
				entry.TaskID, entry.ProjectPath, entry.EffectivePriority)

			// Check preemption against any running conductors
			for _, s := range d.Watchdog.Check(d.loadProjects()) {
				if s.Alive && s.PID > 0 {
					candidate := PreemptionCandidate{
						TaskID:            s.Path,
						PID:               s.PID,
						EffectivePriority: 0, // running task base priority
					}
					if ShouldPreempt(entry.EffectivePriority, candidate, now) {
						d.logf("scheduler: preemption warranted for %s (pending=%.1f, running PID=%d)",
							s.Path, entry.EffectivePriority, s.PID)
						if d.DB != nil {
							preResult, preErr := EvaluatePreemption(ctx, d.DB, entry.EffectivePriority, candidate, s.Path, now)
							if preErr != nil {
								d.logf("scheduler: preemption failed: %v", preErr)
							} else if preResult.Preempted {
								d.logf("scheduler: preempted PID %d at %s (boosted to %.1f)", s.PID, s.Path, preResult.BoostedTo)
							} else if preResult.AntiThrashed {
								d.logf("scheduler: anti-thrashing blocked: %s", preResult.Reason)
							}
						}
					}
					result.PreemptChecked = true
				}
			}
		}
	}

	// Phase 4: Watchdog
	projects := d.loadProjects()
	result.WatchdogStatus = d.Watchdog.Check(projects)
	for _, s := range result.WatchdogStatus {
		if s.Tripped {
			d.logf("watchdog: circuit breaker tripped for %s", s.Path)
		}
	}

	// Phase 5: Spawn-if-needed (skip during quiet hours)
	if result.QuietHours {
		result.SpawnSkipped = true
		result.SpawnReason = "quiet hours"
		d.logf("spawn: skipped (quiet hours)")
		return result, nil
	}

	// Gate spawn on scheduler: if scheduler is configured but selected nothing, skip spawn.
	if d.Scheduler != nil && result.SelectedEntry == nil {
		result.SpawnSkipped = true
		result.SpawnReason = "no queue entry selected"
		d.logf("spawn: skipped (no queue entry selected)")
		return result, nil
	}

	// Phase 5 governor check: budget, rate-limit, circuit breaker.
	if d.Governor != nil {
		if ok, reason := d.Governor.CanSpawn(ctx); !ok {
			result.SpawnSkipped = true
			result.SpawnReason = fmt.Sprintf("governor: %s", reason)
			result.GovernorBlocked = true
			d.logf("spawn: governor blocked (%s)", reason)
			return result, nil
		}
	}

	cpu, mem := systemLoad()
	if cpu > 80 {
		result.SpawnSkipped = true
		result.SpawnReason = fmt.Sprintf("CPU load %.0f%% > 80%%", cpu)
		d.logf("spawn: skipped (%s)", result.SpawnReason)
		return result, nil
	}
	if mem > 85 {
		result.SpawnSkipped = true
		result.SpawnReason = fmt.Sprintf("memory %.0f%% > 85%%", mem)
		d.logf("spawn: skipped (%s)", result.SpawnReason)
		return result, nil
	}

	// Phase 5b: Launch selected queue entry
	if result.SelectedEntry != nil && d.DB != nil {
		if err := d.launchQueueEntry(ctx, result.SelectedEntry, result); err != nil {
			d.logf("spawn: launch failed: %v", err)
			result.SpawnReason = fmt.Sprintf("launch error: %v", err)
		}
	}

	return result, nil
}

// IsQuietHours checks if the current time falls within the quiet hours window.
// Handles midnight wrap-around (e.g., "22:00" to "07:00").
// Times are in "HH:MM" format.
func IsQuietHours(start, end string, now time.Time) bool {
	startMin, err1 := parseHHMM(start)
	endMin, err2 := parseHHMM(end)
	if err1 != nil || err2 != nil {
		return false
	}

	nowMin := now.Hour()*60 + now.Minute()

	if startMin <= endMin {
		// Same-day window (e.g., 09:00-17:00)
		return nowMin >= startMin && nowMin < endMin
	}
	// Midnight wrap (e.g., 22:00-07:00)
	return nowMin >= startMin || nowMin < endMin
}

func parseHHMM(s string) (int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time format %q (expected HH:MM)", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	return h*60 + m, nil
}

// systemLoad returns approximate CPU usage percentage and memory usage percentage.
func systemLoad() (cpuPct, memPct float64) {
	// Memory: use runtime.MemStats for Go process approximation
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// Use Sys (total memory obtained from OS) as an approximation
	// Note: this is Go process memory, not system-wide. For the daemon
	// use case (single long-running process), this is a reasonable proxy.
	if m.Sys > 0 {
		memPct = float64(m.Alloc) / float64(m.Sys) * 100
	}

	// CPU: use NumGoroutine as a rough load proxy.
	// A more accurate implementation would read /proc/loadavg (Linux) or
	// use sysctl (macOS), but this avoids platform-specific code for now.
	goroutines := runtime.NumGoroutine()
	numCPU := runtime.NumCPU()
	if numCPU > 0 {
		cpuPct = float64(goroutines) / float64(numCPU) * 10 // rough scaling
		if cpuPct > 100 {
			cpuPct = 100
		}
	}

	return cpuPct, memPct
}

func convertSchedules(entries []config.ScheduleEntry) []ScheduleConfig {
	out := make([]ScheduleConfig, len(entries))
	for i, e := range entries {
		out[i] = ScheduleConfig{
			Project:  e.Project,
			Cron:     e.Cron,
			Goal:     e.Goal,
			Priority: e.Priority,
			Disabled: e.Disabled,
		}
	}
	return out
}

// registeredRepoPaths returns the paths of all registered projects.
func (d *Daemon) registeredRepoPaths() []string {
	reg, err := config.LoadProjects()
	if err != nil {
		return nil
	}
	var paths []string
	for _, p := range reg.Projects {
		paths = append(paths, p.Path)
	}
	return paths
}

func (d *Daemon) loadProjects() []ProjectInfo {
	reg, err := config.LoadProjects()
	if err != nil {
		d.logf("failed to load projects registry: %v", err)
		return nil
	}
	var projects []ProjectInfo
	for _, p := range reg.Projects {
		projects = append(projects, ProjectInfo{
			Path:    p.Path,
			PidsDir: filepath.Join(p.Path, ".orchestra", "pids"),
			DBPath:  filepath.Join(p.Path, ".orchestra", "orchestrator.db"),
		})
	}
	return projects
}

// launchQueueEntry spawns `orchestra go` for a selected queue entry.
// Records the run in run_history, removes the entry from priority_queue,
// and launches the conductor as a detached process in the project directory.
// After successful completion, runs Phase 6 post-session eval if the project
// has eval scenarios configured. An optional CycleResult receives eval outcomes.
func (d *Daemon) launchQueueEntry(ctx context.Context, entry *orchestra.QueueEntry, crs ...*CycleResult) error {
	sessionID := db.GenID("s")
	goal := entry.TaskID // task_id stores the goal text in priority_queue

	// Default to first registered project if no target repo specified
	if entry.ProjectPath == "" {
		if reg, err := config.LoadProjects(); err == nil {
			for _, p := range reg.Projects {
				entry.ProjectPath = p.Path
				break
			}
		}
	}
	if entry.ProjectPath == "" {
		return fmt.Errorf("no project path for queue entry and no registered projects")
	}

	// New project detection: if the work item is flagged as a new project,
	// create a new directory in ~/symphonies/ instead of using the Orchestra repo.
	if d.isNewProject(ctx, goal) {
		projectName := d.slugify(goal)
		symphoniesDir := filepath.Join(os.Getenv("HOME"), "symphonies")
		newProjectPath := filepath.Join(symphoniesDir, projectName)

		if _, err := os.Stat(newProjectPath); os.IsNotExist(err) {
			d.logf("spawn: creating new project at %s", newProjectPath)
			if err := os.MkdirAll(newProjectPath, 0755); err != nil {
				return fmt.Errorf("creating new project dir: %w", err)
			}
			// Initialize git repo
			initCmd := exec.CommandContext(ctx, "git", "init")
			initCmd.Dir = newProjectPath
			initCmd.CombinedOutput()

			// Create minimal CLAUDE.md
			claudeMD := fmt.Sprintf("# %s\n\nProject created by Dark Factory on %s.\n\n## Goal\n\n%s\n",
				goal, time.Now().Format("2006-01-02"), goal)
			os.WriteFile(filepath.Join(newProjectPath, "CLAUDE.md"), []byte(claudeMD), 0644)

			// Initial commit
			addCmd := exec.CommandContext(ctx, "git", "add", "-A")
			addCmd.Dir = newProjectPath
			addCmd.CombinedOutput()
			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", "init: project scaffolded by Dark Factory")
			commitCmd.Dir = newProjectPath
			commitCmd.CombinedOutput()

			d.logf("spawn: new project initialized at %s", newProjectPath)
		}
		entry.ProjectPath = newProjectPath
	}

	d.logf("spawn: launching %s in %s (goal: %.60s...)", sessionID, entry.ProjectPath, goal)

	// Record run in history
	_, err := d.DB.ExecContext(ctx,
		`INSERT INTO run_history (id, project_path, session_id, status) VALUES (?, ?, ?, 'running')`,
		db.GenID("rh"), entry.ProjectPath, sessionID)
	if err != nil {
		return fmt.Errorf("recording run_history: %w", err)
	}

	// Remove from queue (claimed)
	_, err = d.DB.ExecContext(ctx,
		`DELETE FROM priority_queue WHERE id = ?`, entry.ID)
	if err != nil {
		d.logf("spawn: warning: failed to remove queue entry %s: %v", entry.ID, err)
	}

	// Find the orchestra binary — prefer the project's bin/, then PATH
	orchestraBin := filepath.Join(entry.ProjectPath, "bin", "orchestra")
	if _, err := os.Stat(orchestraBin); os.IsNotExist(err) {
		orchestraBin = "orchestra" // fall back to PATH
	}

	// Special goals handled directly by the daemon (no agent spawn needed).
	if goal == "taxonomy-research" {
		d.logf("spawn: running ALL taxonomy domain research for session %s", sessionID)
		ts := &TaxonomyScanner{
			ProjectPath: entry.ProjectPath,
			StateFile:   filepath.Join(entry.ProjectPath, ".orchestra", "taxonomy-state.json"),
			Logger:      slog.Default().With("component", "taxonomy"),
		}
		domains := AllDomains()
		completed := 0
		for _, domain := range domains {
			d.logf("taxonomy: [%d/%d] researching %s", domain.ID, len(domains), domain.Name)
			outputPath, err := ts.RunDomainResearch(ctx, domain)
			if err != nil {
				d.logf("taxonomy: domain %d (%s) error: %v", domain.ID, domain.Name, err)
				continue // don't stop all domains for one failure
			}
			d.logf("taxonomy: domain %d complete → %s", domain.ID, outputPath)
			completed++
		}
		d.logf("taxonomy: finished %d/%d domains", completed, len(domains))
		return nil
	}

	if goal == "discovery-scan" {
		d.logf("spawn: running discovery scanner for session %s", sessionID)
		scanner := &DiscoveryScanner{
			DB:           d.DB,
			Logger:       slog.Default().With("component", "scanner"),
			LastScanFile: filepath.Join(entry.ProjectPath, ".orchestra", "last-scan.json"),
		}
		// Load signal sources from registry (TODO: parse signal-sources-registry.md)
		// For now, use a curated starter set of high-value feeds
		sources := defaultSignalSources()
		discoveries, err := scanner.ScanAll(ctx, sources)
		if err != nil {
			d.logf("scanner: error: %v", err)
		}
		newDiscoveries, _ := scanner.FilterNew(ctx, discoveries)
		ingested, _ := scanner.IngestDiscoveries(ctx, newDiscoveries)
		d.logf("scanner: found %d items, %d new, %d ingested", len(discoveries), len(newDiscoveries), ingested)
		return nil
	}

	// Route execution by mode. ModeConduct uses orchestra conductor-run (multi-agent).
	// ModeResearch/ModeGrant/ModeDirect use claude -p (single agent).
	var cmd *exec.Cmd
	mode := entry.ExecutionMode
	if mode == "" {
		mode = orchestra.ModeConduct
	}

	// All factory outputs go to ~/symphonies/ for easy access.
	symphoniesDir := filepath.Join(os.Getenv("HOME"), "symphonies")
	var researchDir string
	switch entry.ExecutionMode {
	case orchestra.ModeGrant:
		researchDir = filepath.Join(symphoniesDir, "grants")
	case orchestra.ModeResearch:
		researchDir = filepath.Join(symphoniesDir, "research")
	default:
		researchDir = filepath.Join(entry.ProjectPath, "notes", "factory-research")
	}
	os.MkdirAll(researchDir, 0755)
	logDir := filepath.Join(entry.ProjectPath, ".orchestra", "research")
	os.MkdirAll(logDir, 0755)
	outputFile := filepath.Join(researchDir, sessionID+".md")

	switch mode {
	case orchestra.ModeResearch:
		// Run research/grant from temp dir to avoid branch pollution in the project repo.
		// Claude's hooks can switch branches — running from the project dir corrupts the repo state.
		tmpDir, _ := os.MkdirTemp("", "orchestra-research-"+sessionID)
		if tmpDir != "" {
			defer os.RemoveAll(tmpDir)
		}
		d.logf("spawn: research mode for %s → %s (workdir: %s)", sessionID, outputFile, tmpDir)
		prompt := fmt.Sprintf(`You are a deep research specialist for Plyne Technologies. Research the following project idea thoroughly as of %s.

PROJECT: %s

Produce a COMPLETE requirements document with these sections:

## 1. Domain Research
- What exists today (commercial and open-source)?
- What's the competitive landscape and pricing?
- Who are the target users and what are their pain points?
- What regulatory/compliance requirements apply?

## 2. Technical Architecture
- Recommended language + framework (with justification)
- Database choice (SQLite for local-first, Postgres if multi-user)
- Project structure (directory layout)
- API design if applicable

## 3. Tech Stack Decision
- Frontend/UI framework (Tauri for desktop, React/HTMX for web, React Native for mobile)
- Local LLM requirements (what models, what hardware minimum)
- External data sources (APIs, datasets, scraping)
- Key dependencies and libraries

## 4. Code Standards
- Testing strategy (unit, integration, e2e)
- Linting/formatting tools
- Error handling patterns
- Logging conventions

## 5. Deployment Model
- Zero-cost deployment options
- Local-first architecture decisions
- Update/distribution mechanism

## 6. Effort Estimate
- MVP scope (what's in, what's out)
- Estimated files and lines of code
- Key risks and unknowns

## 7. Ethical Classification
- Is this an ethical imperative (replacing exploitative pricing)?
- Grant alignment (which programs?)
- License recommendation (open-source, proprietary, hybrid)

## 8. Acceptance Criteria for MVP
- 5-10 specific, testable criteria that define "done" for the MVP

Search the web extensively. Cite sources. Write the output to: %s`, time.Now().Format("2006-01-02"), goal, outputFile)
		cmd = exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--model", "claude-opus-4-6", "--permission-mode", "bypassPermissions")
		if tmpDir != "" {
			cmd.Dir = tmpDir
		} else {
			cmd.Dir = os.TempDir()
		}

	case orchestra.ModeGrant:
		tmpDir, _ := os.MkdirTemp("", "orchestra-grant-"+sessionID)
		if tmpDir != "" {
			defer os.RemoveAll(tmpDir)
		}
		d.logf("spawn: grant mode for %s → %s (workdir: %s)", sessionID, outputFile, tmpDir)
		prompt := fmt.Sprintf(`You are a grant application specialist for Plyne Technologies (AI-augmented tools for education, creative tech, developer experience). Research and draft a grant application for:

%s

Research as of %s. Produce a COMPLETE grant application document with:

## 1. Grant Program Research
- Program requirements, eligibility, deadlines
- Typical award size and duration
- What they fund and what they don't
- Success criteria and evaluation rubric

## 2. Fit Assessment
- How Plyne's projects match the grant's mission
- Specific deliverables we can propose
- Open-source requirements (if any) and our compliance
- Competitive advantage over other applicants

## 3. Application Draft
- Project summary / abstract (250 words)
- Problem statement
- Proposed solution with technical approach
- Timeline and milestones
- Budget outline
- Team qualifications
- Broader impacts / community benefit

## 4. Submission Logistics
- Application URL and portal
- Required documents and formats
- Deadlines (absolute dates)
- Contact info for program officers

## 5. Required Action Items
CRITICAL: List EVERY action needed to apply for and receive this grant.
Format each as a numbered item with these fields:
- **Action**: what specifically needs to be done
- **Type**: document | outreach | administrative | research | training
- **Deadline**: absolute date or "before submission"
- **Effort**: Low/Medium/High
- **Dependencies**: what must happen first

Include non-obvious items: account creation, team assembly, training requirements,
letters of support, institutional approvals, budget negotiations, etc.
These will be automatically added to the factory's work queue.

Write the output to: %s`, goal, time.Now().Format("2006-01-02"), outputFile)
		cmd = exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--model", "claude-opus-4-6", "--permission-mode", "bypassPermissions")
		if tmpDir != "" {
			cmd.Dir = tmpDir
		} else {
			cmd.Dir = os.TempDir()
		}

	case orchestra.ModeDirect:
		d.logf("spawn: direct mode for %s", sessionID)
		cmd = exec.CommandContext(ctx, "claude", "-p", goal, "--output-format", "stream-json", "--verbose", "--model", "claude-opus-4-6", "--permission-mode", "bypassPermissions")
		cmd.Dir = entry.ProjectPath

	default: // ModeConduct
		// Full Dark Factory pipeline: generate-spec → Ralph refinement → validate → exec --spec
		specPath := filepath.Join(entry.ProjectPath, "specs", "auto-generated.yaml")
		reqPath := filepath.Join(entry.ProjectPath, "REQUIREMENTS.md")

		// Check if a requirements doc exists — if so, use the spec pipeline
		if _, reqErr := os.Stat(reqPath); reqErr == nil {
			d.logf("spawn: requirements doc found — using spec pipeline")

			// Step 1: Generate initial spec
			specDir := filepath.Join(entry.ProjectPath, "specs")
			os.MkdirAll(specDir, 0755)
			genCmd := exec.CommandContext(ctx, orchestraBin, "generate-spec",
				"--idea", goal,
				"--output", specPath)
			genCmd.Dir = entry.ProjectPath
			if genOut, genErr := genCmd.CombinedOutput(); genErr != nil {
				d.logf("spawn: generate-spec failed: %v\n%s", genErr, string(genOut))
			} else {
				d.logf("spawn: initial spec generated at %s", specPath)

				// Step 2: Ralph Wiggum refinement loop (task-count convergence)
				promptPath := filepath.Join(entry.ProjectPath, "PROMPT.md")
				os.WriteFile(promptPath, []byte(`Read specs/auto-generated.yaml and REQUIREMENTS.md.
Compare the spec against the requirements. Find gaps. Edit to fix.
Rules:
- Each file path must appear in EXACTLY ONE task within each phase
- If two tasks in the same phase need the same file, consolidate them
- Cross-phase file overlap is OK
- Keep correct existing tasks, add missing tasks
- All code tasks must use role: implementer
- Do not create duplicate tasks
- Verify YAML indentation after every Edit
When no file conflicts AND all requirements covered, output: COMPLETE
`), 0644)

				prevTasks := 0
				for iter := 1; iter <= 4; iter++ {
					currTasks := countYAMLTitles(specPath)
					if currTasks == prevTasks && iter > 1 {
						d.logf("spawn: Ralph converged at iteration %d (%d tasks)", iter, currTasks)
						break
					}
					prevTasks = currTasks
					d.logf("spawn: Ralph iteration %d (%d tasks)", iter, currTasks)

					ralphCmd := exec.CommandContext(ctx, "claude",
						"--model", "claude-opus-4-6",
						"--permission-mode", "bypassPermissions",
						"--output-format", "stream-json", "--verbose")
					ralphCmd.Dir = entry.ProjectPath
					ralphCmd.Stdin, _ = os.Open(promptPath)
					devNull2, _ := os.Open(os.DevNull)
					ralphCmd.Stdin = devNull2
					// Pipe PROMPT.md content via a different mechanism
					promptContent, _ := os.ReadFile(promptPath)
					ralphCmd = exec.CommandContext(ctx, "claude",
						"-p", string(promptContent),
						"--model", "claude-opus-4-6",
						"--permission-mode", "bypassPermissions",
						"--output-format", "stream-json", "--verbose")
					ralphCmd.Dir = entry.ProjectPath
					devNull3, _ := os.Open(os.DevNull)
					ralphCmd.Stdin = devNull3
					ralphCmd.CombinedOutput()
				}
				d.logf("spawn: Ralph complete — %d tasks", countYAMLTitles(specPath))

				// Step 3: Execute with spec
				launchArgs := []string{"exec",
					"--spec", specPath,
				}
				cmd = exec.CommandContext(ctx, orchestraBin, launchArgs...)
				cmd.Dir = entry.ProjectPath
			}
		}

		// Fallback: no requirements doc — use direct conductor-run
		if cmd == nil {
			launchArgs := []string{"conductor-run",
				"--goal", goal,
				"--session-id", sessionID,
				"--reconcile",
			}
			if d.Loader.Config.Sandbox {
				launchArgs = append(launchArgs, "--sandbox")
			}
			if testCmd := orchestrator.DetectTestCommand(entry.ProjectPath); testCmd != "" {
				launchArgs = append(launchArgs, "--test-cmd", testCmd)
			}
			cmd = exec.CommandContext(ctx, orchestraBin, launchArgs...)
			cmd.Dir = entry.ProjectPath
		}
	}
	cmd.Env = append(os.Environ(), "ORCHESTRA_DAEMON_LAUNCHED=1")

	// Redirect stdin from /dev/null for headless execution
	devNull, _ := os.Open(os.DevNull)
	if devNull != nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	// Capture stdout/stderr for non-conductor modes (research, grant, direct)
	d.logf("spawn: mode=%q conduct=%q match=%v", mode, orchestra.ModeConduct, mode != orchestra.ModeConduct)
	if mode != orchestra.ModeConduct {
		logPath := filepath.Join(logDir, sessionID+".log")
		d.logf("spawn: creating log at %s", logPath)
		if logFile, err := os.Create(logPath); err == nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			defer logFile.Close()
			d.logf("spawn: output → %s", logPath)
		} else {
			d.logf("spawn: WARNING: could not create log file %s: %v", logPath, err)
		}
	}

	if err := cmd.Start(); err != nil {
		// Mark run as failed
		d.DB.ExecContext(ctx,
			`UPDATE run_history SET status = 'failed', finished_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
			sessionID)
		return fmt.Errorf("starting orchestra go: %w", err)
	}

	d.logf("spawn: launched PID %d for session %s", cmd.Process.Pid, sessionID)

	// Wait for completion — the daemon must not launch the next goal while
	// the current conductor holds the repo flock. With max_concurrent=1 this
	// blocks the daemon cycle, which is the correct behavior.
	waitErr := cmd.Wait()
	status := "completed"
	if waitErr != nil {
		status = "failed"
		d.logf("spawn: session %s failed: %v", sessionID, waitErr)
	}
	d.DB.ExecContext(context.Background(),
		`UPDATE run_history SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
		status, sessionID)
	d.logf("spawn: session %s %s", sessionID, status)

	// Gap 2 fix: Update work_items status so completed/failed items don't re-queue.
	// TopN() filters WHERE status IN ('pending','queued'), so marking items
	// 'completed' or 'failed' prevents re-selection on next daemon cycle.
	if d.ScoringEngine != nil {
		workItemStatus := "completed"
		if status == "failed" {
			workItemStatus = "failed"
		}
		if _, dbErr := d.DB.ExecContext(context.Background(),
			`UPDATE work_items SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE title = ? AND status IN ('pending', 'queued')`,
			workItemStatus, goal); dbErr != nil {
			d.logf("spawn: warning: failed to update work_items status: %v", dbErr)
		}
	}

	// Feedback loop: parse completed research/grant output for action items
	// and ingest them back into work_items queue.
	if status == "completed" && (mode == orchestra.ModeGrant || mode == orchestra.ModeResearch) {
		d.ingestActionItems(ctx, outputFile, goal)
	}

	// Research → Build handoff: when research completes, automatically create
	// a build work item that uses the requirements doc to build the actual code.
	if status == "completed" && mode == orchestra.ModeResearch {
		buildTitle := "Build: " + goal
		buildDesc := fmt.Sprintf("Build the MVP for %s using the requirements document at %s. Follow the technical architecture, code standards, and acceptance criteria from the research.", goal, outputFile)
		sourceID := "build-" + d.slugify(goal)
		_, dbErr := d.DB.ExecContext(context.Background(),
			`INSERT OR IGNORE INTO work_items (id, source, source_id, title, description, tier, base_priority, effective_priority, status, target_repo, is_new_project, created_at, updated_at)
			 VALUES (?, 'discovery', ?, ?, ?, 1, 95, 950, 'pending', ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			db.GenID("wi"), sourceID, buildTitle, buildDesc, entry.ProjectPath)
		if dbErr != nil {
			d.logf("spawn: warning: failed to create build item for %s: %v", goal, dbErr)
		} else {
			d.logf("spawn: queued build item: %s → %s", buildTitle, entry.ProjectPath)
		}
	}

	// Phase 6: Post-session eval (only on successful completion)
	// If eval fails (more failures than passes), auto-revert the session's merge.
	if status == "completed" {
		passed, failed, evalErr := d.runPostSessionEval(ctx, entry.ProjectPath, sessionID)
		if evalErr != nil {
			d.logf("eval: post-session eval error: %v", evalErr)
		}
		if passed+failed > 0 {
			d.logf("eval: session %s — %d passed, %d failed", sessionID, passed, failed)
			if len(crs) > 0 && crs[0] != nil {
				crs[0].EvalRan = true
				crs[0].EvalPassed = passed
				crs[0].EvalFailed = failed
			}

			// Two-step recovery on eval regression:
			// Step 1: Try self-healing (minor fix: build errors, missing imports)
			// Step 2: If healing fails or re-eval still fails → revert + re-queue
			if failed > passed {
				d.logf("eval: REGRESSION detected (%d failed > %d passed) — attempting self-healing", failed, passed)

				// Step 1: Try self-healing
				healCmd := exec.CommandContext(ctx, "go", "build", "./...")
				healCmd.Dir = entry.ProjectPath
				if healOut, healErr := healCmd.CombinedOutput(); healErr != nil {
					// Build failed — try healing fixes
					d.logf("eval: build failed, running healer: %s", string(healOut))
					fixCmd := exec.CommandContext(ctx, "goimports", "-w", ".")
					fixCmd.Dir = entry.ProjectPath
					fixCmd.CombinedOutput()
					tidyCmd := exec.CommandContext(ctx, "go", "mod", "tidy")
					tidyCmd.Dir = entry.ProjectPath
					tidyCmd.CombinedOutput()

					// Re-check build
					recheckCmd := exec.CommandContext(ctx, "go", "build", "./...")
					recheckCmd.Dir = entry.ProjectPath
					if _, recheckErr := recheckCmd.CombinedOutput(); recheckErr == nil {
						d.logf("eval: self-healing fixed build — re-running eval")
						passed2, failed2, _ := d.runPostSessionEval(ctx, entry.ProjectPath, sessionID+"-healed")
						if passed2 > failed2 {
							d.logf("eval: healed session %s passes eval (%d/%d)", sessionID, passed2, passed2+failed2)
						} else {
							d.logf("eval: still failing after heal — reverting session %s", sessionID)
							revertCmd := exec.CommandContext(ctx, "git", "revert", "--no-edit", "HEAD")
							revertCmd.Dir = entry.ProjectPath
							revertCmd.CombinedOutput()
						}
					} else {
						d.logf("eval: healing failed — reverting session %s", sessionID)
						revertCmd := exec.CommandContext(ctx, "git", "revert", "--no-edit", "HEAD")
						revertCmd.Dir = entry.ProjectPath
						revertCmd.CombinedOutput()
					}
				} else {
					// Build passes but eval failed — logic/design issue, revert
					d.logf("eval: build passes but eval regressed — reverting session %s", sessionID)
					revertCmd := exec.CommandContext(ctx, "git", "revert", "--no-edit", "HEAD")
					revertCmd.Dir = entry.ProjectPath
					revertCmd.CombinedOutput()
				}
			}
		}
	}

	return nil
}

// runPostSessionEval opens the project's orchestrator.db, checks for eval
// scenarios, and judges each one against the session's transcript. Results are
// stored via InsertEvalRun/InsertEvalResult. Fail-open: returns (0,0,nil) if
// the DB can't be opened or has no scenarios.
func (d *Daemon) runPostSessionEval(ctx context.Context, projectPath, sessionID string) (passed, failed int, err error) {
	dbPath := filepath.Join(projectPath, ".orchestra", "orchestrator.db")
	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		d.logf("eval: no .orchestra/orchestrator.db at %s, skipping", projectPath)
		return 0, 0, nil
	}

	projectDB, err := db.Open(dbPath)
	if err != nil {
		d.logf("eval: cannot open project DB %s: %v", dbPath, err)
		return 0, 0, nil
	}
	defer projectDB.Close()

	scenarios, err := projectDB.ListEvalScenarios(ctx)
	if err != nil {
		d.logf("eval: listing scenarios: %v", err)
		return 0, 0, nil
	}
	if len(scenarios) == 0 {
		d.logf("eval: no eval scenarios in %s", projectPath)
		return 0, 0, nil
	}

	d.logf("eval: running %d scenario(s) for session %s", len(scenarios), sessionID)

	// Create a version entry for this session
	versionID := fmt.Sprintf("daemon-%s", sessionID)
	if verErr := projectDB.InsertEvalVersion(ctx, db.EvalVersion{
		ID:     versionID,
		Status: "candidate",
	}); verErr != nil {
		d.logf("eval: failed to insert version: %v", verErr)
		return 0, 0, nil
	}

	runner := &orchestrator.ExecRunner{}
	logsDir := filepath.Join(projectPath, ".orchestra", "logs")

	for _, s := range scenarios {
		runID := fmt.Sprintf("run-%s-%s-%d", versionID, s.ID, time.Now().UnixNano())
		now := sql.NullTime{Time: time.Now(), Valid: true}
		run := db.EvalRun{
			ID:         runID,
			VersionID:  versionID,
			ScenarioID: s.ID,
			StartedAt:  now,
			Status:     "running",
		}
		if insertErr := projectDB.InsertEvalRun(ctx, run); insertErr != nil {
			d.logf("eval: failed to create run for scenario %s: %v", s.ID, insertErr)
			continue
		}

		scenario := dbScenarioToEvalScenario(s)
		rubric := eval.RubricForRole(s.Role)

		// Read real transcript if available, fall back to synthetic
		var transcript string
		if scenario.TaskID != "" {
			if t, readErr := eval.ReadTaskTranscript(logsDir, scenario.TaskID); readErr == nil {
				transcript = t
			}
		}
		if transcript == "" {
			transcript = eval.SyntheticTranscript(scenario, versionID)
		}

		result, judgeErr := eval.Judge(ctx, runner, scenario, transcript, rubric)
		if judgeErr != nil {
			d.logf("eval: judge failed for scenario %s: %v", s.ID, judgeErr)
			projectDB.UpdateEvalRunStatus(ctx, runID, "failed")
			failed++
			continue
		}

		// Store per-metric results
		for _, w := range rubric.Rubric.Weights {
			score, ok := result.Scores[w.Metric]
			if !ok {
				continue
			}
			resultID := fmt.Sprintf("res-%s-%s-%d", runID, w.Metric, time.Now().UnixNano())
			evalResult := db.EvalResult{
				ID:      resultID,
				RunID:   runID,
				Metric:  w.Metric,
				Score:   score,
				Weight:  w.Weight,
				Details: sql.NullString{String: result.Reasoning, Valid: result.Reasoning != ""},
			}
			if insertErr := projectDB.InsertEvalResult(ctx, evalResult); insertErr != nil {
				d.logf("eval: failed to store result %s: %v", w.Metric, insertErr)
			}
		}

		status := "passed"
		if !result.Pass {
			status = "failed"
		}
		projectDB.UpdateEvalRunStatus(ctx, runID, status)

		if result.Pass {
			passed++
		} else {
			failed++
		}
	}

	return passed, failed, nil
}

// dbScenarioToEvalScenario converts a db.EvalScenario to an eval.Scenario.
func dbScenarioToEvalScenario(s db.EvalScenario) eval.Scenario {
	category := ""
	if s.Category.Valid {
		category = s.Category.String
	}
	expectedOutcome := ""
	if s.ExpectedOutcome.Valid {
		expectedOutcome = s.ExpectedOutcome.String
	}
	difficulty := ""
	if s.Difficulty.Valid {
		difficulty = s.Difficulty.String
	}
	taskID := ""
	if strings.HasPrefix(s.ID, "curated-") {
		taskID = strings.TrimPrefix(s.ID, "curated-")
	}
	return eval.Scenario{
		ID:              s.ID,
		Role:            s.Role,
		Category:        category,
		Goal:            s.Goal,
		ExpectedOutcome: expectedOutcome,
		Difficulty:      difficulty,
		TaskID:          taskID,
		CreatedAt:       s.CreatedAt,
	}
}

// recalcPriorities reads all pending queue entries, runs them through the
// priority engine, and writes the updated effective_priority back to the DB.
func (d *Daemon) recalcPriorities(ctx context.Context) (int, error) {
	rows, err := d.DB.QueryContext(ctx,
		`SELECT id, project_path, task_id, effective_priority, computed_at FROM priority_queue`)
	if err != nil {
		return 0, fmt.Errorf("querying priority_queue: %w", err)
	}
	defer rows.Close()

	var tasks []priority.TaskPriority
	var ids []string
	for rows.Next() {
		var id, projectPath, taskID string
		var ep float64
		var computedAt time.Time
		if err := rows.Scan(&id, &projectPath, &taskID, &ep, &computedAt); err != nil {
			continue
		}
		ids = append(ids, id)
		tasks = append(tasks, priority.TaskPriority{
			ID:          id,
			Title:       taskID,
			Description: taskID, // task_id used as description for alignment scoring
			Priority:    50,     // default base priority for queue entries
			CreatedAt:   computedAt,
		})
	}

	if len(tasks) == 0 {
		return 0, nil
	}

	ranked := d.Engine.RankTasks(ctx, tasks)

	// Update effective_priority in DB
	for _, t := range ranked {
		_, err := d.DB.ExecContext(ctx,
			`UPDATE priority_queue SET effective_priority = ?, computed_at = ? WHERE id = ?`,
			t.EffectivePriority, time.Now(), t.ID)
		if err != nil {
			d.logf("priority: failed to update %s: %v", t.ID, err)
		}
	}

	return len(ranked), nil
}

func (d *Daemon) logf(format string, args ...interface{}) {
	if d.Log != nil {
		d.Log(fmt.Sprintf(format, args...))
	}
}

// defaultSignalSources returns a curated starter set of high-value signal feeds.
// TODO: Parse these from notes/signal-sources-registry.md dynamically.
func defaultSignalSources() []SignalSource {
	return []SignalSource{
		{Name: "Hacker News", Type: "api", URL: "https://hacker-news.firebaseio.com/v0/topstories.json", Frequency: "daily", Category: "startup"},
		{Name: "Anthropic blog", Type: "rss", URL: "https://www.anthropic.com/blog/rss", Frequency: "weekly", Category: "ai-devtools"},
		{Name: "Papers With Code", Type: "rss", URL: "https://paperswithcode.com/latest", Frequency: "daily", Category: "academic"},
		{Name: "ProductHunt", Type: "rss", URL: "https://www.producthunt.com/feed", Frequency: "daily", Category: "startup"},
		{Name: "Go blog", Type: "rss", URL: "https://go.dev/blog/feed.atom", Frequency: "monthly", Category: "ai-devtools"},
		{Name: "Cloudflare blog", Type: "rss", URL: "https://blog.cloudflare.com/rss", Frequency: "weekly", Category: "engineering"},
		{Name: "IndieHackers", Type: "rss", URL: "https://www.indiehackers.com/feed", Frequency: "weekly", Category: "startup"},
		{Name: "TechCrunch", Type: "rss", URL: "https://techcrunch.com/feed", Frequency: "daily", Category: "startup"},
	}
}

// isNewProject checks if a work item should create a new project directory.
// Looks up the work_items table for IsNewProject flag, or infers from goal text.
func (d *Daemon) isNewProject(ctx context.Context, goal string) bool {
	if d.DB == nil {
		return false
	}
	var isNew int
	err := d.DB.QueryRowContext(ctx,
		`SELECT is_new_project FROM work_items WHERE title = ? LIMIT 1`, goal).Scan(&isNew)
	if err == nil && isNew == 1 {
		return true
	}
	// Heuristic: if the goal doesn't match any registered project, it's new
	reg, err := config.LoadProjects()
	if err != nil {
		return false
	}
	goalLower := strings.ToLower(goal)
	for _, p := range reg.Projects {
		if strings.Contains(goalLower, strings.ToLower(p.Name)) {
			return false // matches existing project
		}
	}
	// Discovery items that don't match any project are new
	return true
}

// slugify converts a goal title into a directory-safe name.
func (d *Daemon) slugify(goal string) string {
	// Remove common prefixes
	s := goal
	for _, prefix := range []string{"Local-First ", "Open-Source ", "Grant: ", "AI "} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.ToLower(s)
	// Replace non-alphanumeric with hyphens
	var result []byte
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else if c == ' ' || c == '_' {
			result = append(result, '-')
		}
	}
	// Trim and limit length
	name := strings.Trim(string(result), "-")
	if len(name) > 40 {
		name = name[:40]
	}
	if name == "" {
		name = "factory-project"
	}
	return name
}

// ingestActionItems parses a completed research/grant output for action items
// and creates work_items in the priority queue for each one.
func (d *Daemon) ingestActionItems(ctx context.Context, outputFile, parentGoal string) {
	data, err := os.ReadFile(outputFile)
	if err != nil {
		d.logf("ingest: could not read output file %s: %v", outputFile, err)
		return
	}
	content := string(data)

	// Find the "Required Action Items" or "Suggested Work Items" section
	sections := []string{"## 5. Required Action Items", "### Suggested Work Items", "## Suggested Work Items"}
	sectionStart := -1
	for _, header := range sections {
		idx := strings.Index(content, header)
		if idx >= 0 {
			sectionStart = idx + len(header)
			break
		}
	}
	if sectionStart < 0 {
		d.logf("ingest: no action items section found in %s", outputFile)
		return
	}

	// Extract until next ## header or end of file
	remaining := content[sectionStart:]
	nextSection := strings.Index(remaining[1:], "\n## ")
	if nextSection > 0 {
		remaining = remaining[:nextSection+1]
	}

	// Parse numbered items (lines starting with digits or "- **Action**:")
	items := 0
	lines := strings.Split(remaining, "\n")
	var currentTitle string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for numbered items or bold action titles
		if len(line) > 3 && (line[0] >= '1' && line[0] <= '9') && line[1] == '.' {
			currentTitle = strings.TrimSpace(line[2:])
			// Remove markdown bold
			currentTitle = strings.ReplaceAll(currentTitle, "**", "")
			if len(currentTitle) > 100 {
				currentTitle = currentTitle[:100]
			}
		}
		if strings.HasPrefix(line, "- **Action**:") || strings.HasPrefix(line, "- **Title**:") {
			currentTitle = strings.TrimPrefix(line, "- **Action**:")
			currentTitle = strings.TrimPrefix(currentTitle, "- **Title**:")
			currentTitle = strings.TrimSpace(currentTitle)
			currentTitle = strings.ReplaceAll(currentTitle, "**", "")
		}

		// When we have a title, insert it
		if currentTitle != "" && (strings.HasPrefix(line, "- **Effort**") || strings.HasPrefix(line, "- **Type**") || strings.HasPrefix(line, "- **Deadline**") || line == "" || strings.HasPrefix(line, "---")) {
			if currentTitle != "" {
				sourceID := fmt.Sprintf("action-%s-%s", d.slugify(parentGoal), d.slugify(currentTitle))
				_, dbErr := d.DB.ExecContext(ctx,
					`INSERT OR IGNORE INTO work_items (id, source, source_id, title, description, tier, base_priority, status, created_at, updated_at)
					 VALUES (?, 'discovery', ?, ?, ?, 3, 50, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
					db.GenID("wi"), sourceID, currentTitle, fmt.Sprintf("Action item from: %s", parentGoal))
				if dbErr == nil {
					items++
				}
				currentTitle = ""
			}
		}
	}

	d.logf("ingest: extracted %d action items from %s", items, outputFile)
}

// countYAMLTitles counts "title:" lines in a YAML spec file for convergence detection.
func countYAMLTitles(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "title:") {
			count++
		}
	}
	return count
}
