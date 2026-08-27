#!/bin/bash
# run-b205-experiment.sh — A/B test for B-205 file coverage validation
#
# Arm A (control): Binary built from BASE (no B-205 fix)
# Arm B (treatment): Binary built from current HEAD (with B-205 fix)
# Both arms reset source files to BASE before each run.
#
# Usage: ./scripts/run-b205-experiment.sh
# Resume: START_RUN=4 ./scripts/run-b205-experiment.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="e0031a8"
CURRENT_HEAD="$(git rev-parse HEAD)"
RESULTS_FILE="/tmp/b205-experiment-results.csv"
BIN_BASE="/tmp/orchestra-base"
BIN_B205="/tmp/orchestra-b205"
DB="${REPO_ROOT}/.orchestra/orchestrator.db"
GOAL='Add a priority_label TEXT field (sql.NullString) to the Task model. This human-readable label should flow through: schema/001_initial.sql -> models.go -> queries.go -> mutations.go -> spawner.go -> go.go -> iterative.go -> TUI display.'

cd "$REPO_ROOT"

# ── Phase 1: Build both binaries ──────────────────────────────────────
echo "=== Building binaries ==="

# Build B-205 binary from current HEAD first
echo "[$(date +%H:%M:%S)] Building B-205 binary from $CURRENT_HEAD..."
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_B205"
echo "  → $BIN_B205"

# Stash scripts before checking out base
cp scripts/run-b205-experiment.sh /tmp/run-b205-experiment.sh.bak
cp scripts/verify-benchmark.sh /tmp/verify-benchmark.sh.bak

# Build base binary from BASE commit
echo "[$(date +%H:%M:%S)] Building base binary from $BASE..."
git stash --include-untracked 2>/dev/null || true
git checkout "$BASE" 2>&1
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_BASE"
echo "  → $BIN_BASE"

# Return to current HEAD
git checkout - 2>&1
git stash pop 2>/dev/null || true

# Restore scripts
cp /tmp/run-b205-experiment.sh.bak scripts/run-b205-experiment.sh
cp /tmp/verify-benchmark.sh.bak scripts/verify-benchmark.sh
chmod +x scripts/run-b205-experiment.sh scripts/verify-benchmark.sh 2>/dev/null || true

echo "Both binaries ready."
echo ""

# ── Phase 2: Run experiments ──────────────────────────────────────────

# Create CSV header only if file doesn't exist
if [ ! -f "$RESULTS_FILE" ]; then
    echo "run,arm,binary,files_correct,build_pass,merge_conflicts,wall_clock_s,task_count,coverage_gaps,files_assigned,files_detail,verdict" > "$RESULTS_FILE"
fi

START_RUN="${START_RUN:-1}"

# 6-run alternating design: A-B-B-A-B-A (counterbalanced)
declare -a ARMS=("A" "B" "B" "A" "B" "A")

run_experiment() {
    local run_num=$1
    local arm="${ARMS[$((run_num-1))]}"
    local binary

    if [ "$arm" = "A" ]; then
        binary="$BIN_BASE"
    else
        binary="$BIN_B205"
    fi

    echo ""
    echo "================================================================"
    echo "  RUN $run_num / 6 — Arm $arm ($([ "$arm" = "A" ] && echo "baseline" || echo "B-205"))"
    echo "================================================================"
    echo ""

    # Reset to base
    echo "[$(date +%H:%M:%S)] Resetting to base $BASE..."
    "$binary" reset 2>&1 || true

    # Delete DB so each binary creates its own schema (base binary lacks priority_label column)
    rm -f "$DB" "${DB}-shm" "${DB}-wal"

    # Stash scripts before hard reset
    cp scripts/run-b205-experiment.sh /tmp/run-b205-experiment.sh.bak
    cp scripts/verify-benchmark.sh /tmp/verify-benchmark.sh.bak

    git reset --hard "$BASE" 2>&1

    # Restore scripts after reset
    cp /tmp/run-b205-experiment.sh.bak scripts/run-b205-experiment.sh
    cp /tmp/verify-benchmark.sh.bak scripts/verify-benchmark.sh
    chmod +x scripts/run-b205-experiment.sh scripts/verify-benchmark.sh 2>/dev/null || true

    # Run the experiment using the selected binary
    echo "[$(date +%H:%M:%S)] Starting with $(basename "$binary")..."
    local start_ts=$(date +%s)

    "$binary" go --goal "$GOAL" --max-tasks 3 --test-cmd 'go build ./...' --foreground 2>&1
    local exit_code=$?

    local end_ts=$(date +%s)
    local wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s (exit: $exit_code)"

    # ── Collect metrics ──
    # 1. Files correct + build
    local verify_output
    verify_output=$(bash "${REPO_ROOT}/scripts/verify-benchmark.sh" "$REPO_ROOT" 2>&1)
    local files_correct
    files_correct=$(echo "$verify_output" | grep '  PASS:' | grep -cv 'go build' || true)

    local build_pass="false"
    if echo "$verify_output" | grep -q 'Build: true'; then
        build_pass="true"
    fi

    local verdict="FAIL"
    if echo "$verify_output" | grep -q 'VERDICT: PASS'; then
        verdict="PASS"
    fi

    # 2. Merge conflicts
    local merge_conflicts=0
    if [ -f "$DB" ]; then
        merge_conflicts=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_conflict';" 2>/dev/null || echo 0)
    fi

    # 3. Task count
    local task_count=0
    if [ -f "$DB" ]; then
        task_count=$(sqlite3 "$DB" "SELECT COUNT(*) FROM tasks;" 2>/dev/null || echo 0)
    fi

    # 4. B-205 specific: coverage gap events
    local coverage_gaps=0
    if [ -f "$DB" ]; then
        coverage_gaps=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_coverage_gap';" 2>/dev/null || echo 0)
    fi

    # 5. Files assigned to tasks (from file_locks table)
    local files_assigned=""
    if [ -f "$DB" ]; then
        files_assigned=$(sqlite3 "$DB" "SELECT file_path FROM file_locks ORDER BY file_path;" 2>/dev/null | tr '\n' '|' || echo "")
    fi

    # 6. Per-file detail from verify script
    local files_detail
    files_detail=$(echo "$verify_output" | grep '^FILES_DETAIL:' | sed 's/^FILES_DETAIL: //' || echo "")

    echo ""
    echo "--- Run $run_num Results ---"
    echo "Arm: $arm | Binary: $(basename "$binary")"
    echo "Files correct: $files_correct/8"
    echo "Build pass: $build_pass"
    echo "Merge conflicts: $merge_conflicts"
    echo "Wall clock: ${wall_clock}s"
    echo "Task count: $task_count"
    echo "Coverage gaps detected: $coverage_gaps"
    echo "Files assigned: $files_assigned"
    echo "Files detail: $files_detail"
    echo "Verdict: $verdict"
    echo ""
    echo "$verify_output"

    # Append to CSV
    echo "$run_num,$arm,$(basename "$binary"),$files_correct,$build_pass,$merge_conflicts,$wall_clock,$task_count,$coverage_gaps,$files_assigned,$files_detail,$verdict" >> "$RESULTS_FILE"
    echo "[CSV written] Run $run_num appended to $RESULTS_FILE"
}

echo "Starting B-205 File Coverage A/B Experiment"
echo "BASE commit: $BASE"
echo "HEAD commit: $CURRENT_HEAD"
echo "Results file: $RESULTS_FILE"
echo "================================================"

for i in $(seq "$START_RUN" 6); do
    run_experiment "$i"

    if [ "$i" -lt 6 ]; then
        echo ""
        echo "[$(date +%H:%M:%S)] Cooling down for 120s..."
        sleep 120
    fi
done

# ── Phase 3: Summary ──────────────────────────────────────────────────
echo ""
echo "================================================"
echo "All 6 runs complete. Results in: $RESULTS_FILE"
echo "================================================"
echo ""
cat "$RESULTS_FILE"
echo ""

# Calculate arm summaries
arm_a_pass=$(grep ',A,' "$RESULTS_FILE" | grep -c ',PASS$' || true)
arm_a_total=$(grep -c ',A,' "$RESULTS_FILE" || true)
arm_b_pass=$(grep ',B,' "$RESULTS_FILE" | grep -c ',PASS$' || true)
arm_b_total=$(grep -c ',B,' "$RESULTS_FILE" || true)

echo "=== Summary ==="
echo "Arm A (baseline): $arm_a_pass/$arm_a_total passed"
echo "Arm B (B-205):    $arm_b_pass/$arm_b_total passed"
echo ""
echo "Key question: Does B-205 increase the file coverage rate for go.go and iterative.go?"
echo "Check the files_assigned column for go.go and iterative.go presence in Arm B vs Arm A."
