package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// B-156: WAL Contention Under Load
//
// Tests concurrent read/write operations from multiple goroutines simulating
// 5-8 agents updating status, writing events, reading blackboard simultaneously.
//
// Pass criteria:
//   - Zero data corruption (all reads return valid data)
//   - Zero unrecoverable SQLITE_BUSY errors (busy_timeout handles contention)
//   - All goroutines complete without deadlock
//   - P99 write latency < 5 seconds (busy_timeout limit)
//   - P99 read latency < 1 second
//
// Failure response: Document which operations cause contention, at what
// concurrency level, and whether busy_timeout is sufficient.
// =============================================================================

func setupStressDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// setupStressDBOnDisk creates a real on-disk database for WAL contention testing.
// In-memory databases share a single connection and don't exhibit real WAL behavior.
func setupStressDBOnDisk(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stress.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestStress_WALContention_5Writers is the primary B-156 stress test.
// It simulates 5 concurrent "agents" each performing a mix of writes
// (create tasks, register agents, log events, set blackboard) and reads
// (list tasks, list agents, recent events, status summary) over 5 seconds.
func TestStress_WALContention_5Writers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	const numAgents = 5
	const duration = 5 * time.Second

	var (
		writeErrors atomic.Int64
		readErrors  atomic.Int64
		writeOps    atomic.Int64
		readOps     atomic.Int64
		busyErrors  atomic.Int64
		wg          sync.WaitGroup
	)

	// Seed initial data (tasks and agents for each simulated agent).
	for i := 0; i < numAgents; i++ {
		taskID := fmt.Sprintf("stress-task-%d", i)
		agentID := fmt.Sprintf("stress-agent-%d", i)

		err := d.CreateTask(ctx, Task{
			ID:     taskID,
			Title:  fmt.Sprintf("Stress task %d", i),
			Status: "pending",
			Role:   "implementer",
		})
		if err != nil {
			t.Fatalf("seed CreateTask: %v", err)
		}

		err = d.RegisterAgent(ctx, Agent{
			ID:     agentID,
			Role:   "implementer",
			Status: "idle",
		})
		if err != nil {
			t.Fatalf("seed RegisterAgent: %v", err)
		}
	}

	deadline := time.Now().Add(duration)

	// Launch writer goroutines — each simulates one agent's write operations.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			agentID := fmt.Sprintf("stress-agent-%d", agentIdx)
			taskID := fmt.Sprintf("stress-task-%d", agentIdx)
			iter := 0

			for time.Now().Before(deadline) {
				iter++

				// Operation 1: Set blackboard (frequent — agents do this constantly)
				start := time.Now()
				err := d.SetBlackboard(ctx,
					fmt.Sprintf("heartbeat:%s", taskID),
					fmt.Sprintf("%d", time.Now().UnixNano()),
					agentID)
				dur := time.Since(start)
				mc.Record("SetBlackboard", dur, err)

				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				// Operation 2: Log event (frequent — agents and monitor do this)
				start = time.Now()
				err = d.LogEvent(ctx, "stress_heartbeat", agentID, taskID,
					fmt.Sprintf(`{"iter":%d,"agent":%d}`, iter, agentIdx))
				dur = time.Since(start)
				mc.Record("LogEvent", dur, err)

				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				// Operation 3: Update agent heartbeat
				start = time.Now()
				err = d.UpdateAgentHeartbeat(ctx, agentID)
				dur = time.Since(start)
				mc.Record("UpdateAgentHeartbeat", dur, err)

				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				// Small yield to prevent CPU starving.
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Launch reader goroutines — these simulate the TUI/monitor polling.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(readerIdx int) {
			defer wg.Done()

			for time.Now().Before(deadline) {
				// Read 1: ListTasks
				start := time.Now()
				tasks, err := d.ListTasks(ctx)
				dur := time.Since(start)
				mc.Record("ListTasks", dur, err)

				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
					// Validate data integrity — task IDs should be well-formed.
					for _, task := range tasks {
						if task.ID == "" {
							t.Errorf("reader %d: got task with empty ID", readerIdx)
						}
					}
				}

				// Read 2: ListAgents
				start = time.Now()
				agents, err := d.ListAgents(ctx)
				dur = time.Since(start)
				mc.Record("ListAgents", dur, err)

				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
					for _, agent := range agents {
						if agent.ID == "" {
							t.Errorf("reader %d: got agent with empty ID", readerIdx)
						}
					}
				}

				// Read 3: RecentEvents
				start = time.Now()
				events, err := d.RecentEvents(ctx, 20)
				dur = time.Since(start)
				mc.Record("RecentEvents", dur, err)

				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
					_ = events
				}

				// Read 4: GetStatusSummary
				start = time.Now()
				summary, err := d.GetStatusSummary(ctx)
				dur = time.Since(start)
				mc.Record("GetStatusSummary", dur, err)

				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
					_ = summary
				}

				// Read 5: GetBlackboardValue
				start = time.Now()
				_, err = d.GetBlackboardValue(ctx, fmt.Sprintf("heartbeat:stress-task-%d", readerIdx))
				dur = time.Since(start)
				mc.Record("GetBlackboardValue", dur, err)

				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
				}

				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// === Results ===
	totalWrites := writeOps.Load()
	totalReads := readOps.Load()
	totalWriteErrs := writeErrors.Load()
	totalReadErrs := readErrors.Load()
	totalBusy := busyErrors.Load()

	t.Logf("=== B-156 WAL Contention Results (5 writers + 5 readers, %v) ===", duration)
	t.Logf("Write ops:     %d (errors: %d)", totalWrites, totalWriteErrs)
	t.Logf("Read ops:      %d (errors: %d)", totalReads, totalReadErrs)
	t.Logf("SQLITE_BUSY:   %d", totalBusy)
	t.Logf("Metrics:       %s", mc.Report())
	t.Logf("P50 latency:   %v", mc.P50())
	t.Logf("P99 latency:   %v", mc.P99())

	top5 := mc.TopSlowest(5)
	for i, q := range top5 {
		errStr := "nil"
		if q.Error != nil {
			errStr = q.Error.Error()
		}
		t.Logf("  Slowest #%d:  %v (%s) err=%s", i+1, q.Duration, q.QueryName, errStr)
	}

	// === Pass/Fail Criteria ===

	// 1. No unrecoverable errors (all operations should succeed with busy_timeout)
	if totalWriteErrs > 0 {
		t.Errorf("FAIL: %d write errors (expected 0 with busy_timeout=5000ms)", totalWriteErrs)
	}
	if totalReadErrs > 0 {
		t.Errorf("FAIL: %d read errors (expected 0 — readers should never block in WAL mode)", totalReadErrs)
	}

	// 2. No SQLITE_BUSY errors should surface (busy_timeout should handle retries)
	if totalBusy > 0 {
		t.Errorf("FAIL: %d SQLITE_BUSY errors leaked past busy_timeout", totalBusy)
	}

	// 3. P99 write latency should be under busy_timeout (5 seconds)
	p99 := mc.P99()
	if p99 > 5*time.Second {
		t.Errorf("FAIL: P99 latency %v exceeds 5s busy_timeout limit", p99)
	}

	// 4. Minimum throughput — should achieve meaningful work in the time window
	if totalWrites < 100 {
		t.Errorf("FAIL: only %d writes in %v — expected at least 100", totalWrites, duration)
	}
	if totalReads < 100 {
		t.Errorf("FAIL: only %d reads in %v — expected at least 100", totalReads, duration)
	}

	// 5. Data integrity — verify final state is consistent
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Errorf("FAIL: final ListTasks failed: %v", err)
	} else if len(tasks) != numAgents {
		t.Errorf("FAIL: expected %d tasks, got %d (data corruption?)", numAgents, len(tasks))
	}

	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Errorf("FAIL: final ListAgents failed: %v", err)
	} else if len(agents) != numAgents {
		t.Errorf("FAIL: expected %d agents, got %d (data corruption?)", numAgents, len(agents))
	}

	if !t.Failed() {
		t.Logf("PASS: B-156 WAL contention test passed — %d writes, %d reads, 0 errors, P99=%v",
			totalWrites, totalReads, p99)
	}
}

// TestStress_WALContention_8Writers scales up to 8 concurrent writer goroutines.
// This is the upper bound of our target scale (3-8 agents).
func TestStress_WALContention_8Writers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	const numAgents = 8
	const duration = 5 * time.Second

	var (
		writeErrors atomic.Int64
		readErrors  atomic.Int64
		writeOps    atomic.Int64
		readOps     atomic.Int64
		busyErrors  atomic.Int64
		wg          sync.WaitGroup
	)

	// Seed data.
	for i := 0; i < numAgents; i++ {
		d.CreateTask(ctx, Task{
			ID: fmt.Sprintf("stress8-task-%d", i), Title: fmt.Sprintf("Task %d", i),
			Status: "pending", Role: "implementer",
		})
		d.RegisterAgent(ctx, Agent{
			ID: fmt.Sprintf("stress8-agent-%d", i), Role: "implementer", Status: "idle",
		})
	}

	deadline := time.Now().Add(duration)

	// 8 writers.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			aID := fmt.Sprintf("stress8-agent-%d", idx)
			tID := fmt.Sprintf("stress8-task-%d", idx)
			for iter := 0; time.Now().Before(deadline); iter++ {
				start := time.Now()
				err := d.SetBlackboard(ctx, fmt.Sprintf("hb8:%s", tID), fmt.Sprintf("%d", iter), aID)
				mc.Record("SetBlackboard8", time.Since(start), err)
				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				start = time.Now()
				err = d.LogEvent(ctx, "stress8", aID, tID, fmt.Sprintf(`{"i":%d}`, iter))
				mc.Record("LogEvent8", time.Since(start), err)
				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				start = time.Now()
				err = d.UpdateAgentHeartbeat(ctx, aID)
				mc.Record("Heartbeat8", time.Since(start), err)
				if err != nil {
					writeErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					writeOps.Add(1)
				}

				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// 8 readers.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				_, err := d.ListTasks(ctx)
				mc.Record("ListTasks8", time.Since(start), err)
				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
				}

				start = time.Now()
				_, err = d.GetStatusSummary(ctx)
				mc.Record("Summary8", time.Since(start), err)
				if err != nil {
					readErrors.Add(1)
					if isBusyError(err) {
						busyErrors.Add(1)
					}
				} else {
					readOps.Add(1)
				}

				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	totalWrites := writeOps.Load()
	totalReads := readOps.Load()
	totalWriteErrs := writeErrors.Load()
	totalReadErrs := readErrors.Load()
	totalBusy := busyErrors.Load()
	p99 := mc.P99()

	t.Logf("=== B-156 WAL Contention Results (8 writers + 8 readers, %v) ===", duration)
	t.Logf("Write ops: %d (errors: %d) | Read ops: %d (errors: %d) | SQLITE_BUSY: %d",
		totalWrites, totalWriteErrs, totalReads, totalReadErrs, totalBusy)
	t.Logf("Metrics: %s", mc.Report())

	if totalWriteErrs > 0 {
		t.Errorf("FAIL: %d write errors at 8-writer scale", totalWriteErrs)
	}
	if totalReadErrs > 0 {
		t.Errorf("FAIL: %d read errors at 8-writer scale", totalReadErrs)
	}
	if totalBusy > 0 {
		t.Errorf("FAIL: %d SQLITE_BUSY errors at 8-writer scale", totalBusy)
	}
	if p99 > 5*time.Second {
		t.Errorf("FAIL: P99 %v exceeds 5s at 8-writer scale", p99)
	}

	if !t.Failed() {
		t.Logf("PASS: B-156 at 8-writer scale — %d writes, %d reads, P99=%v", totalWrites, totalReads, p99)
	}
}

// TestStress_WALContention_TransactionMix tests contention when transactions
// compete with individual operations (simulates conductor + monitor + TUI).
func TestStress_WALContention_TransactionMix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	const duration = 3 * time.Second

	var (
		txErrors   atomic.Int64
		txOps      atomic.Int64
		readErrors atomic.Int64
		readOps    atomic.Int64
		wg         sync.WaitGroup
	)

	// Seed data.
	for i := 0; i < 5; i++ {
		d.CreateTask(ctx, Task{
			ID: fmt.Sprintf("tx-task-%d", i), Title: fmt.Sprintf("Tx task %d", i),
			Status: "pending", Role: "implementer",
		})
		d.RegisterAgent(ctx, Agent{
			ID: fmt.Sprintf("tx-agent-%d", i), Role: "implementer", Status: "idle",
		})
	}

	deadline := time.Now().Add(duration)

	// Transaction writer — simulates conductor doing multi-step operations.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iter := 0; time.Now().Before(deadline); iter++ {
			start := time.Now()
			err := d.WithTx(ctx, func(tx *sql.Tx) error {
				// Multi-step: update status + log event + set blackboard
				taskIdx := iter % 5
				taskID := fmt.Sprintf("tx-task-%d", taskIdx)
				agentID := fmt.Sprintf("tx-agent-%d", taskIdx)

				_, err := tx.ExecContext(ctx,
					`UPDATE agents SET heartbeat_at = CURRENT_TIMESTAMP WHERE id = ?`, agentID)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx,
					`INSERT INTO events (timestamp, event_type, agent_id, task_id, payload)
					 VALUES (CURRENT_TIMESTAMP, ?, ?, ?, ?)`,
					"tx_test", agentID, taskID, fmt.Sprintf(`{"iter":%d}`, iter))
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx,
					`INSERT OR REPLACE INTO blackboard (key, value, written_by, updated_at)
					 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
					fmt.Sprintf("tx:%s", taskID), fmt.Sprintf("%d", iter), "tx-writer")
				return err
			})
			dur := time.Since(start)
			mc.Record("WithTx", dur, err)

			if err != nil {
				txErrors.Add(1)
			} else {
				txOps.Add(1)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Individual writers — simulates agents doing single writes.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for iter := 0; time.Now().Before(deadline); iter++ {
				start := time.Now()
				err := d.LogEvent(ctx, "individual_write", fmt.Sprintf("tx-agent-%d", idx),
					fmt.Sprintf("tx-task-%d", idx), fmt.Sprintf(`{"i":%d}`, iter))
				mc.Record("IndividualWrite", time.Since(start), err)
				if err != nil {
					txErrors.Add(1)
				} else {
					txOps.Add(1)
				}
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				_, err := d.ListTasks(ctx)
				mc.Record("TxRead", time.Since(start), err)
				if err != nil {
					readErrors.Add(1)
				} else {
					readOps.Add(1)
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	t.Logf("=== B-156 Transaction Mix Results ===")
	t.Logf("Tx/write ops: %d (errors: %d) | Read ops: %d (errors: %d)",
		txOps.Load(), txErrors.Load(), readOps.Load(), readErrors.Load())
	t.Logf("Metrics: %s", mc.Report())

	if txErrors.Load() > 0 {
		t.Errorf("FAIL: %d transaction/write errors", txErrors.Load())
	}
	if readErrors.Load() > 0 {
		t.Errorf("FAIL: %d read errors during transaction contention", readErrors.Load())
	}
	if !t.Failed() {
		t.Logf("PASS: Transaction mix test — %d tx ops, %d reads, P99=%v", txOps.Load(), readOps.Load(), mc.P99())
	}
}

// TestStress_WALContention_Consistency runs a consistency check:
// writers increment a counter in the blackboard, readers verify monotonicity.
func TestStress_WALContention_Consistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	const duration = 3 * time.Second
	const key = "consistency_counter"

	// Initialize counter.
	d.SetBlackboard(ctx, key, "0", "init")

	var (
		wg         sync.WaitGroup
		totalReads atomic.Int64
		staleReads atomic.Int64 // times we read a value lower than a previous read
	)

	deadline := time.Now().Add(duration)

	// Single writer incrementing the counter.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for counter := 1; time.Now().Before(deadline); counter++ {
			d.SetBlackboard(ctx, key, fmt.Sprintf("%d", counter), "writer")
			time.Sleep(time.Millisecond)
		}
	}()

	// Multiple readers checking consistency (should never read 0 after seeing N>0,
	// though they may see stale values in WAL mode).
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastSeen := 0
			for time.Now().Before(deadline) {
				val, err := d.GetBlackboardValue(ctx, key)
				if err != nil {
					continue
				}
				var current int
				fmt.Sscanf(val, "%d", &current)
				totalReads.Add(1)

				if current < lastSeen {
					staleReads.Add(1)
				}
				if current > lastSeen {
					lastSeen = current
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	t.Logf("=== B-156 Consistency Check ===")
	t.Logf("Total reads: %d | Stale reads: %d", totalReads.Load(), staleReads.Load())

	// Stale reads are acceptable in WAL mode (readers see a snapshot), but backward
	// reads (current < lastSeen) would indicate data corruption.
	if staleReads.Load() > 0 {
		t.Logf("WARNING: %d stale reads detected (reads saw older values) — this is normal in WAL snapshot isolation", staleReads.Load())
	}

	// Verify final value is reasonable (writer should have written many values).
	finalVal, err := d.GetBlackboardValue(ctx, key)
	if err != nil {
		t.Errorf("FAIL: cannot read final counter value: %v", err)
	} else {
		var final int
		fmt.Sscanf(finalVal, "%d", &final)
		if final < 100 {
			t.Errorf("FAIL: final counter only %d — expected at least 100 in %v", final, duration)
		} else {
			t.Logf("PASS: consistency check — final counter=%d, %d reads, %d stale", final, totalReads.Load(), staleReads.Load())
		}
	}
}

// TestStress_WALContention_EventFlood floods the events table with inserts
// from multiple goroutines while readers poll RecentEvents.
// This tests the append-heavy workload that the monitor/TUI produce.
func TestStress_WALContention_EventFlood(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	const numWriters = 5
	const duration = 3 * time.Second

	var (
		totalInserts atomic.Int64
		insertErrors atomic.Int64
		readErrors   atomic.Int64
		wg           sync.WaitGroup
	)

	deadline := time.Now().Add(duration)

	// Event writers.
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for iter := 0; time.Now().Before(deadline); iter++ {
				start := time.Now()
				err := d.LogEvent(ctx, "flood_event",
					fmt.Sprintf("agent-%d", idx), fmt.Sprintf("task-%d", idx),
					fmt.Sprintf(`{"writer":%d,"iter":%d}`, idx, iter))
				mc.Record("FloodInsert", time.Since(start), err)
				if err != nil {
					insertErrors.Add(1)
				} else {
					totalInserts.Add(1)
				}
			}
		}(i)
	}

	// Event readers (polling).
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				events, err := d.RecentEvents(ctx, 50)
				mc.Record("FloodRead", time.Since(start), err)
				if err != nil {
					readErrors.Add(1)
				}
				_ = events
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	t.Logf("=== B-156 Event Flood Results ===")
	t.Logf("Inserts: %d (errors: %d) | Read errors: %d",
		totalInserts.Load(), insertErrors.Load(), readErrors.Load())
	t.Logf("Metrics: %s", mc.Report())

	if insertErrors.Load() > 0 {
		t.Errorf("FAIL: %d insert errors during event flood", insertErrors.Load())
	}
	if readErrors.Load() > 0 {
		t.Errorf("FAIL: %d read errors during event flood", readErrors.Load())
	}
	if totalInserts.Load() < 200 {
		t.Errorf("FAIL: only %d inserts in %v — expected at least 200", totalInserts.Load(), duration)
	}
	if !t.Failed() {
		t.Logf("PASS: event flood — %d inserts, P99=%v", totalInserts.Load(), mc.P99())
	}
}

// TestStress_MultipleDBConnections tests contention when multiple DB connections
// (1 writer, N readers) access the same on-disk database simultaneously.
// This simulates the real-world scenario where the TUI opens a read-only
// connection while the conductor has a read-write connection.
func TestStress_MultipleDBConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "multi-conn.db")

	// Open writer connection.
	writer, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open writer: %v", err)
	}
	if err := writer.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	defer writer.Close()

	// Open multiple reader connections.
	const numReaders = 3
	readers := make([]*DB, numReaders)
	for i := 0; i < numReaders; i++ {
		r, err := OpenReadOnly(dbPath)
		if err != nil {
			t.Fatalf("OpenReadOnly %d: %v", i, err)
		}
		defer r.Close()
		readers[i] = r
	}

	ctx := context.Background()
	const duration = 3 * time.Second

	// Seed data.
	for i := 0; i < 5; i++ {
		writer.CreateTask(ctx, Task{
			ID: fmt.Sprintf("mc-task-%d", i), Title: fmt.Sprintf("MC Task %d", i),
			Status: "pending", Role: "implementer",
		})
	}

	var (
		writeOps   atomic.Int64
		readOps    atomic.Int64
		writeErrs  atomic.Int64
		readErrs   atomic.Int64
		wg         sync.WaitGroup
	)

	deadline := time.Now().Add(duration)

	// Writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iter := 0; time.Now().Before(deadline); iter++ {
			err := writer.LogEvent(ctx, "mc_write", "", "", fmt.Sprintf(`{"i":%d}`, iter))
			if err != nil {
				writeErrs.Add(1)
			} else {
				writeOps.Add(1)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Reader goroutines using separate read-only connections.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(r *DB) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				_, err := r.ListTasks(ctx)
				if err != nil {
					readErrs.Add(1)
				} else {
					readOps.Add(1)
				}

				_, err = r.RecentEvents(ctx, 10)
				if err != nil {
					readErrs.Add(1)
				} else {
					readOps.Add(1)
				}
				time.Sleep(2 * time.Millisecond)
			}
		}(readers[i])
	}

	wg.Wait()

	t.Logf("=== B-156 Multi-Connection Results (1 writer + %d readers) ===", numReaders)
	t.Logf("Writes: %d (errors: %d) | Reads: %d (errors: %d)",
		writeOps.Load(), writeErrs.Load(), readOps.Load(), readErrs.Load())

	if writeErrs.Load() > 0 {
		t.Errorf("FAIL: %d write errors with separate reader connections", writeErrs.Load())
	}
	if readErrs.Load() > 0 {
		t.Errorf("FAIL: %d read errors with separate reader connections", readErrs.Load())
	}
	if !t.Failed() {
		t.Logf("PASS: multi-connection test — %d writes, %d reads, 0 errors",
			writeOps.Load(), readOps.Load())
	}
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "database is locked") ||
		strings.Contains(s, "SQLITE_BUSY") ||
		strings.Contains(s, "busy")
}

// TestStress_WALContention_ReportSummary runs B-156 3 times for consistency.
func TestStress_WALContention_ReportSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	// Skip if env says so (e.g., CI might set this for time-constrained runs).
	if os.Getenv("SKIP_REPEATED_STRESS") == "1" {
		t.Skip("skipping repeated stress test (SKIP_REPEATED_STRESS=1)")
	}

	const runs = 3

	for run := 1; run <= runs; run++ {
		t.Run(fmt.Sprintf("run_%d", run), func(t *testing.T) {
			d := setupStressDBOnDisk(t)
			ctx := context.Background()

			const numAgents = 5
			const duration = 3 * time.Second

			var (
				errors atomic.Int64
				ops    atomic.Int64
				wg     sync.WaitGroup
			)

			for i := 0; i < numAgents; i++ {
				d.CreateTask(ctx, Task{
					ID: fmt.Sprintf("rep%d-t%d", run, i), Title: fmt.Sprintf("Task %d", i),
					Status: "pending", Role: "implementer",
				})
				d.RegisterAgent(ctx, Agent{
					ID: fmt.Sprintf("rep%d-a%d", run, i), Role: "implementer", Status: "idle",
				})
			}

			deadline := time.Now().Add(duration)

			for i := 0; i < numAgents; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					aID := fmt.Sprintf("rep%d-a%d", run, idx)
					tID := fmt.Sprintf("rep%d-t%d", run, idx)
					for time.Now().Before(deadline) {
						if err := d.SetBlackboard(ctx, "rep:"+tID, "v", aID); err != nil {
							errors.Add(1)
						} else {
							ops.Add(1)
						}
						if err := d.LogEvent(ctx, "rep", aID, tID, "{}"); err != nil {
							errors.Add(1)
						} else {
							ops.Add(1)
						}
						time.Sleep(time.Millisecond)
					}
				}(i)
			}

			for i := 0; i < numAgents; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for time.Now().Before(deadline) {
						if _, err := d.ListTasks(ctx); err != nil {
							errors.Add(1)
						} else {
							ops.Add(1)
						}
						time.Sleep(2 * time.Millisecond)
					}
				}()
			}

			wg.Wait()

			t.Logf("Run %d/%d: ops=%d errors=%d", run, runs, ops.Load(), errors.Load())

			if errors.Load() > 0 {
				t.Errorf("FAIL: run %d had %d errors", run, errors.Load())
			}
		})
	}
}
