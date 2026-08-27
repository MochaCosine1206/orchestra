package daemon

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/priority"
	v2 "github.com/MochaCosine1206/orchestra/internal/priority/v2"
)

func TestIsQuietHours_SameDay(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		hour  int
		min   int
		want  bool
	}{
		{"inside", "09:00", "17:00", 12, 0, true},
		{"before", "09:00", "17:00", 8, 0, false},
		{"after", "09:00", "17:00", 18, 0, false},
		{"at start", "09:00", "17:00", 9, 0, true},
		{"at end", "09:00", "17:00", 17, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 3, 25, tt.hour, tt.min, 0, 0, time.UTC)
			got := IsQuietHours(tt.start, tt.end, now)
			if got != tt.want {
				t.Errorf("IsQuietHours(%q, %q, %02d:%02d) = %v, want %v",
					tt.start, tt.end, tt.hour, tt.min, got, tt.want)
			}
		})
	}
}

func TestIsQuietHours_MidnightWrap(t *testing.T) {
	tests := []struct {
		name string
		hour int
		min  int
		want bool
	}{
		{"late night", 23, 0, true},
		{"midnight", 0, 0, true},
		{"early morning", 6, 0, true},
		{"at end", 7, 0, false},
		{"daytime", 12, 0, false},
		{"at start", 22, 0, true},
		{"before start", 21, 59, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 3, 25, tt.hour, tt.min, 0, 0, time.UTC)
			got := IsQuietHours("22:00", "07:00", now)
			if got != tt.want {
				t.Errorf("IsQuietHours(22:00, 07:00, %02d:%02d) = %v, want %v",
					tt.hour, tt.min, got, tt.want)
			}
		})
	}
}

func TestIsQuietHours_InvalidFormat(t *testing.T) {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	if IsQuietHours("invalid", "07:00", now) {
		t.Error("expected false for invalid start format")
	}
	if IsQuietHours("22:00", "bad", now) {
		t.Error("expected false for invalid end format")
	}
}

func TestRunOnce_AllPhases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	var logs []string
	d, err := New(nil, func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	result, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Without quiet hours config, should not be in quiet hours
	if result.QuietHours {
		t.Error("expected QuietHours=false with no config")
	}
	// No schedules configured
	if len(result.CronDue) != 0 {
		t.Errorf("expected 0 cron due, got %d", len(result.CronDue))
	}
}

func TestRunOnce_QuietHoursSkipsSpawn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	// Configure quiet hours that cover "now"
	d.Loader.Config.QuietHours = &config.QuietHoursConfig{
		Start: "00:00",
		End:   "23:59",
	}

	ctx := context.Background()
	result, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !result.QuietHours {
		t.Error("expected QuietHours=true")
	}
	if !result.SpawnSkipped {
		t.Error("expected SpawnSkipped=true during quiet hours")
	}
	if result.SpawnReason != "quiet hours" {
		t.Errorf("expected reason 'quiet hours', got %q", result.SpawnReason)
	}
}

func TestDaemon_StartStop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.interval = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	if !d.Running() {
		t.Error("expected Running=true after Start")
	}

	d.Stop()
	err = <-done
	if err != nil {
		t.Errorf("Run returned error: %v", err)
	}
	if d.Running() {
		t.Error("expected Running=false after Stop")
	}
}

func TestDaemon_DoubleRunReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.interval = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	err = d.Run(ctx)
	if err == nil {
		t.Error("expected error on double Run")
	}
	d.Stop()
}

func TestRecalcPriorities_WithEngine(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	// Set up global daemon DB
	daemonDB, err := orchestra.OpenDaemonDB()
	if err != nil {
		t.Fatalf("OpenDaemonDB: %v", err)
	}
	defer daemonDB.Close()
	if err := orchestra.InitDaemonSchema(context.Background(), daemonDB); err != nil {
		t.Fatalf("InitDaemonSchema: %v", err)
	}

	// Insert queue entries
	ctx := context.Background()
	_, err = daemonDB.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES (?, ?, ?, ?)`,
		"q1", "/tmp/proj1", "build-tests", 0)
	if err != nil {
		t.Fatalf("insert q1: %v", err)
	}
	_, err = daemonDB.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES (?, ?, ?, ?)`,
		"q2", "/tmp/proj2", "fix-bugs", 0)
	if err != nil {
		t.Fatalf("insert q2: %v", err)
	}

	// Create engine (nil runner = no LLM, all alignment = 1.0)
	engine := priority.NewEngine(nil, "build software")

	d, err := New(engine, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.DB = daemonDB

	// Run recalc
	updated, err := d.recalcPriorities(ctx)
	if err != nil {
		t.Fatalf("recalcPriorities: %v", err)
	}
	if updated != 2 {
		t.Errorf("expected 2 updated, got %d", updated)
	}

	// Verify priorities were updated (should be non-zero)
	var ep float64
	err = daemonDB.QueryRowContext(ctx,
		`SELECT effective_priority FROM priority_queue WHERE id = ?`, "q1").Scan(&ep)
	if err != nil {
		t.Fatalf("query q1 priority: %v", err)
	}
	if ep == 0 {
		t.Error("expected non-zero effective_priority for q1 after recalc")
	}
}

func TestLaunchQueueEntry_RecordsRunHistory(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	daemonDB, err := orchestra.OpenDaemonDB()
	if err != nil {
		t.Fatalf("OpenDaemonDB: %v", err)
	}
	defer daemonDB.Close()
	orchestra.InitDaemonSchema(context.Background(), daemonDB)

	// Insert a queue entry
	ctx := context.Background()
	_, err = daemonDB.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES (?, ?, ?, ?)`,
		"q-launch-1", "/tmp/nonexistent-project", "test goal", 100)
	if err != nil {
		t.Fatalf("insert queue entry: %v", err)
	}

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.DB = daemonDB

	entry := &orchestra.QueueEntry{
		ID:          "q-launch-1",
		ProjectPath: "/tmp/nonexistent-project",
		TaskID:      "test goal",
	}

	// Launch will fail (orchestra binary doesn't exist at that path), but should still
	// record run_history and remove from queue
	_ = d.launchQueueEntry(ctx, entry)

	// Verify queue entry was removed
	var count int
	daemonDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM priority_queue WHERE id = ?`, "q-launch-1").Scan(&count)
	if count != 0 {
		t.Errorf("expected queue entry removed, got count=%d", count)
	}

	// Verify run_history was recorded
	var histCount int
	daemonDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_history`).Scan(&histCount)
	if histCount != 1 {
		t.Errorf("expected 1 run_history entry, got %d", histCount)
	}
}

func TestRunPostSessionEval_DBOpenError(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	// Point to a directory with no orchestrator.db — method should return (0,0,nil)
	noDBPath := t.TempDir()
	ctx := context.Background()
	passed, failed, evalErr := d.runPostSessionEval(ctx, noDBPath, "session-no-db")
	if evalErr != nil {
		t.Errorf("expected nil error, got %v", evalErr)
	}
	if passed != 0 || failed != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", passed, failed)
	}
}

func TestRunPostSessionEval_NoScenarios(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	// Create a project directory with a valid .orchestra/orchestrator.db but no eval scenarios
	projectDir := t.TempDir()
	os.MkdirAll(filepath.Join(projectDir, ".orchestra"), 0o755)
	dbPath := filepath.Join(projectDir, ".orchestra", "orchestrator.db")
	projectDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := projectDB.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	projectDB.Close()

	ctx := context.Background()
	passed, failed, evalErr := d.runPostSessionEval(ctx, projectDir, "session-no-scenarios")
	if evalErr != nil {
		t.Errorf("expected nil error, got %v", evalErr)
	}
	if passed != 0 || failed != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", passed, failed)
	}
}

func TestRunPostSessionEval_WithScenarios(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	d, err := New(nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	// Create a project directory with .orchestra/orchestrator.db seeded with eval scenarios
	projectDir := t.TempDir()
	os.MkdirAll(filepath.Join(projectDir, ".orchestra"), 0o755)
	dbPath := filepath.Join(projectDir, ".orchestra", "orchestrator.db")
	projectDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	ctx := context.Background()
	if err := projectDB.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Seed a scenario
	scenario := db.EvalScenario{
		ID:              "test-scenario-1",
		Role:            "implementer",
		Category:        sql.NullString{String: "unit-test", Valid: true},
		Goal:            "Implement a sorting function",
		ExpectedOutcome: sql.NullString{String: "Tests pass", Valid: true},
		Difficulty:      sql.NullString{String: "easy", Valid: true},
	}
	if err := projectDB.InsertEvalScenario(ctx, scenario); err != nil {
		t.Fatalf("InsertEvalScenario: %v", err)
	}
	projectDB.Close()

	// Run eval — Judge will fail (no claude binary), but eval_runs should still be created
	passed, failed, evalErr := d.runPostSessionEval(ctx, projectDir, "session-with-scenarios")
	if evalErr != nil {
		t.Errorf("expected nil error (fail-open), got %v", evalErr)
	}

	// Judge fails → all scenarios count as failed
	if passed != 0 {
		t.Errorf("expected 0 passed (judge unavailable), got %d", passed)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed (judge unavailable), got %d", failed)
	}

	// Verify DB state: eval_runs row should exist
	projectDB, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("re-open DB: %v", err)
	}
	defer projectDB.Close()

	var runCount int
	err = projectDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM eval_runs`).Scan(&runCount)
	if err != nil {
		t.Fatalf("counting eval_runs: %v", err)
	}
	if runCount < 1 {
		t.Errorf("expected at least 1 eval_runs row, got %d", runCount)
	}

	// Verify eval_versions row was created
	var versionCount int
	err = projectDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM eval_versions`).Scan(&versionCount)
	if err != nil {
		t.Fatalf("counting eval_versions: %v", err)
	}
	if versionCount != 1 {
		t.Errorf("expected 1 eval_versions row, got %d", versionCount)
	}
}

func TestRunOnce_V2ScoringEngine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	daemonDB, err := orchestra.OpenDaemonDB()
	if err != nil {
		t.Fatalf("OpenDaemonDB: %v", err)
	}
	defer daemonDB.Close()
	if err := orchestra.InitDaemonSchema(context.Background(), daemonDB); err != nil {
		t.Fatalf("InitDaemonSchema: %v", err)
	}

	var logs []string
	d, err := New(nil, func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	d.DB = daemonDB

	// Create v2 ScoringEngine with no collectors (empty collect)
	se := v2.NewScoringEngine(daemonDB, nil, nil)
	d.ScoringEngine = se

	ctx := context.Background()
	result, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// With no collectors and no work items, collected=0, scored=0
	if result.CollectedItems != 0 {
		t.Errorf("expected CollectedItems=0, got %d", result.CollectedItems)
	}
	if result.ScoredItems != 0 {
		t.Errorf("expected ScoredItems=0, got %d", result.ScoredItems)
	}
	// PopulateQueue should still succeed (clears queue, inserts 0)
	if !result.QueuePopulated {
		t.Error("expected QueuePopulated=true")
	}
}

func TestRunOnce_V2FallbackToV1(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	daemonDB, err := orchestra.OpenDaemonDB()
	if err != nil {
		t.Fatalf("OpenDaemonDB: %v", err)
	}
	defer daemonDB.Close()
	orchestra.InitDaemonSchema(context.Background(), daemonDB)

	// Insert a queue entry for v1 recalc
	ctx := context.Background()
	_, err = daemonDB.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES (?, ?, ?, ?)`,
		"q-v1-test", "/tmp/proj", "test-task", 0)
	if err != nil {
		t.Fatalf("insert queue entry: %v", err)
	}

	engine := priority.NewEngine(nil, "test")
	d, err := New(engine, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.DB = daemonDB
	// ScoringEngine is nil — should use v1 fallback

	result, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// v2 fields should be zero (not used)
	if result.CollectedItems != 0 {
		t.Errorf("expected CollectedItems=0 (v1 mode), got %d", result.CollectedItems)
	}
	if result.ScoredItems != 0 {
		t.Errorf("expected ScoredItems=0 (v1 mode), got %d", result.ScoredItems)
	}
	if result.QueuePopulated {
		t.Error("expected QueuePopulated=false (v1 mode)")
	}

	// Verify v1 recalc ran — priority should be non-zero
	var ep float64
	err = daemonDB.QueryRowContext(ctx,
		`SELECT effective_priority FROM priority_queue WHERE id = ?`, "q-v1-test").Scan(&ep)
	if err != nil {
		t.Fatalf("query priority: %v", err)
	}
	if ep == 0 {
		t.Error("expected non-zero effective_priority from v1 recalc")
	}
}

func TestRecalcPriorities_EmptyQueue(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	daemonDB, err := orchestra.OpenDaemonDB()
	if err != nil {
		t.Fatalf("OpenDaemonDB: %v", err)
	}
	defer daemonDB.Close()
	orchestra.InitDaemonSchema(context.Background(), daemonDB)

	engine := priority.NewEngine(nil, "test")
	d, err := New(engine, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	d.DB = daemonDB

	updated, err := d.recalcPriorities(context.Background())
	if err != nil {
		t.Fatalf("recalcPriorities on empty queue: %v", err)
	}
	if updated != 0 {
		t.Errorf("expected 0 updated on empty queue, got %d", updated)
	}
}
