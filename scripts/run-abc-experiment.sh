#!/bin/bash
# run-abc-experiment.sh — Runs the 9-run A/B/C file enforcement experiment
# Usage: ./scripts/run-abc-experiment.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="e0031a8"
RESULTS_FILE="/tmp/abc-experiment-results.csv"
ORCHESTRA="${REPO_ROOT}/bin/orchestra"
DB="${REPO_ROOT}/.orchestra/orchestrator.db"
GOAL='Add a priority_label TEXT field (sql.NullString) to the Task model. This human-readable label should flow through: schema/001_initial.sql -> models.go -> queries.go -> mutations.go -> spawner.go -> go.go -> iterative.go -> TUI display.'

cd "$REPO_ROOT"

# Create CSV header only if file doesn't exist
if [ ! -f "$RESULTS_FILE" ]; then
    echo "run,arm,flag,files_correct,build_pass,merge_conflicts,merge_errors,wall_clock_s,ownership_violations,task_count,files_detail,verdict" > "$RESULTS_FILE"
fi

# Start from run N (set to 1 for fresh start, or higher to resume)
START_RUN="${START_RUN:-1}"

# Run matrix (randomized order from plan)
declare -a ARMS=("B" "A" "C" "A" "C" "B" "C" "B" "A")
declare -a FLAGS=("defense" "" "pessimistic" "" "pessimistic" "defense" "pessimistic" "defense" "")

run_experiment() {
    local run_num=$1
    local arm="${ARMS[$((run_num-1))]}"
    local flag="${FLAGS[$((run_num-1))]}"

    echo ""
    echo "================================================================"
    echo "  RUN $run_num / 9 — Arm $arm $([ -n "$flag" ] && echo "(--file-enforcement $flag)" || echo "(baseline)")"
    echo "================================================================"
    echo ""

    # Reset and clean — preserve scripts across hard reset
    echo "[$(date +%H:%M:%S)] Resetting..."
    $ORCHESTRA reset 2>&1 || true

    # Stash scripts before hard reset so they survive
    cp scripts/run-abc-experiment.sh /tmp/run-abc-experiment.sh.bak
    cp scripts/verify-benchmark.sh /tmp/verify-benchmark.sh.bak

    git reset --hard "$BASE" 2>&1

    # Restore scripts after hard reset
    cp /tmp/run-abc-experiment.sh.bak scripts/run-abc-experiment.sh
    cp /tmp/verify-benchmark.sh.bak scripts/verify-benchmark.sh
    chmod +x scripts/verify-benchmark.sh scripts/run-abc-experiment.sh 2>/dev/null || true

    # Rebuild after reset (ensure binary is current)
    make build 2>&1 | tail -1

    # Build command as array to avoid eval quoting issues (G121)
    local args=("$ORCHESTRA" "go" "--goal" "$GOAL" "--max-tasks" "3" "--test-cmd" "go build ./..." "--foreground")
    if [ -n "$flag" ]; then
        args+=("--file-enforcement" "$flag")
    fi

    # Run with timing
    echo "[$(date +%H:%M:%S)] Starting experiment..."
    local start_ts=$(date +%s)

    "${args[@]}" 2>&1
    local exit_code=$?

    local end_ts=$(date +%s)
    local wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s (exit: $exit_code)"

    # Collect metrics
    # 1. Files correct + build from verify script
    local verify_output
    verify_output=$(bash "${REPO_ROOT}/scripts/verify-benchmark.sh" "$REPO_ROOT" 2>&1)
    local files_correct
    # Count only file check PASS lines (exclude build PASS line)
    files_correct=$(echo "$verify_output" | grep '  PASS:' | grep -cv 'go build' || true)

    local build_pass="false"
    if echo "$verify_output" | grep -q 'Build: true'; then
        build_pass="true"
    fi

    local verdict="FAIL"
    if echo "$verify_output" | grep -q 'VERDICT: PASS'; then
        verdict="PASS"
    fi

    # 2. Merge conflicts and errors from events table
    local merge_conflicts=0
    local merge_errors=0
    if [ -f "$DB" ]; then
        merge_conflicts=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_conflict';" 2>/dev/null || echo 0)
        merge_errors=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_error';" 2>/dev/null || echo 0)
    fi

    # 3. Ownership violations from events table
    local ownership_violations=0
    if [ -f "$DB" ] && [ -n "$flag" ]; then
        ownership_violations=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type LIKE '%ownership_violation%';" 2>/dev/null || echo 0)
    fi

    # 4. Task count
    local task_count=0
    if [ -f "$DB" ]; then
        task_count=$(sqlite3 "$DB" "SELECT COUNT(*) FROM tasks;" 2>/dev/null || echo 0)
    fi

    # 5. Merge success count
    local merged_count=0
    if [ -f "$DB" ]; then
        merged_count=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'branch_merged';" 2>/dev/null || echo 0)
    fi

    # 6. Per-file detail from verify script
    local files_detail
    files_detail=$(echo "$verify_output" | grep '^FILES_DETAIL:' | sed 's/^FILES_DETAIL: //' || echo "")

    echo ""
    echo "--- Run $run_num Results ---"
    echo "Arm: $arm | Flag: ${flag:-(none)}"
    echo "Files correct: $files_correct/8"
    echo "Build pass: $build_pass"
    echo "Merge conflicts: $merge_conflicts"
    echo "Merge errors: $merge_errors"
    echo "Merged: $merged_count"
    echo "Ownership violations: $ownership_violations"
    echo "Wall clock: ${wall_clock}s"
    echo "Task count: $task_count"
    echo "Files detail: $files_detail"
    echo "Verdict: $verdict"
    echo ""
    echo "$verify_output"

    # Append to CSV (absolute path)
    echo "$run_num,$arm,${flag:-(none)},$files_correct,$build_pass,$merge_conflicts,$merge_errors,$wall_clock,$ownership_violations,$task_count,$files_detail,$verdict" >> "$RESULTS_FILE"
    echo "[CSV written] Run $run_num appended to $RESULTS_FILE"
}

echo "Starting A/B/C File Enforcement Experiment"
echo "BASE commit: $BASE"
echo "Results file: $RESULTS_FILE"
echo "CWD: $(pwd)"
echo "================================================"

for i in $(seq "$START_RUN" 9); do
    run_experiment "$i"

    if [ "$i" -lt 9 ]; then
        echo ""
        echo "[$(date +%H:%M:%S)] Cooling down for 120s..."
        sleep 120
    fi
done

echo ""
echo "================================================"
echo "All 9 runs complete. Results in: $RESULTS_FILE"
echo "================================================"
cat "$RESULTS_FILE"
