#!/bin/bash
# run-b206b-experiment.sh — A/B test for B-206b structured output enforcement
#
# Arm A (control): Binary built from PRE_B206B (B-206 prompt AC only)
# Arm B (treatment): Binary built from HEAD (B-206b: JSON schema + completeness validator)
# Both arms reset source files to PRE_B206 (before priority_label existed).
#
# Key metrics:
#   - File coverage: how many of the 8 files get priority_label
#   - Build pass: does go build succeed
#   - Completeness: does the new validator catch partial work
#   - AC quality: are acceptance criteria populated (both arms should have AC)
#
# Usage: ./scripts/run-b206b-experiment.sh
# Resume: START_RUN=2 ./scripts/run-b206b-experiment.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PRE_B206="7a56f6c"          # Before priority_label existed (source reset target)
PRE_B206B="89c1b76"         # Has B-206 but NOT B-206b
CURRENT_HEAD="$(git rev-parse HEAD)"
RESULTS_FILE="/tmp/b206b-experiment-results.csv"
BIN_CONTROL="/tmp/orchestra-pre-b206b"
BIN_TREATMENT="/tmp/orchestra-b206b"
DB="${REPO_ROOT}/.orchestra/orchestrator.db"
GOAL='Add a priority_label TEXT field (sql.NullString) to the Task model. This human-readable label should flow through: schema/001_initial.sql -> models.go -> queries.go -> mutations.go -> spawner.go -> go.go -> iterative.go -> TUI display.'

cd "$REPO_ROOT"

# ── Phase 1: Build both binaries ──────────────────────────────────────
echo "=== Building binaries ==="

# Build treatment binary from current HEAD first (B-206b)
echo "[$(date +%H:%M:%S)] Building B-206b treatment binary from $CURRENT_HEAD..."
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_TREATMENT"
echo "  → $BIN_TREATMENT"

# Save scripts before checkout
cp scripts/run-b206b-experiment.sh /tmp/run-b206b-experiment.sh.bak
cp scripts/verify-benchmark.sh /tmp/verify-benchmark.sh.bak

# Build control binary from PRE_B206B (B-206 only)
echo "[$(date +%H:%M:%S)] Building control binary from $PRE_B206B..."
git stash --include-untracked 2>/dev/null || true
git checkout "$PRE_B206B" 2>&1
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_CONTROL"
echo "  → $BIN_CONTROL"

# Return to current HEAD
git checkout - 2>&1
git stash pop 2>/dev/null || true

# Restore scripts
cp /tmp/run-b206b-experiment.sh.bak scripts/run-b206b-experiment.sh
cp /tmp/verify-benchmark.sh.bak scripts/verify-benchmark.sh
chmod +x scripts/run-b206b-experiment.sh scripts/verify-benchmark.sh 2>/dev/null || true

echo "Both binaries ready."
echo ""

# ── Phase 2: Run experiments ──────────────────────────────────────────

if [ ! -f "$RESULTS_FILE" ]; then
    echo "run,arm,binary,files_correct,build_pass,merge_conflicts,wall_clock_s,task_count,ac_events,vague_acs,files_assigned,partial_work_failures,files_detail,verdict" > "$RESULTS_FILE"
fi

START_RUN="${START_RUN:-1}"

# 2-run design: A (control) then B (treatment)
declare -a ARMS=("A" "B")

run_experiment() {
    local run_num=$1
    local arm="${ARMS[$((run_num-1))]}"
    local binary

    if [ "$arm" = "A" ]; then
        binary="$BIN_CONTROL"
    else
        binary="$BIN_TREATMENT"
    fi

    echo ""
    echo "================================================================"
    echo "  RUN $run_num / 2 — Arm $arm ($([ "$arm" = "A" ] && echo "B-206 control" || echo "B-206b treatment"))"
    echo "================================================================"
    echo ""

    # Reset to base (before priority_label existed)
    echo "[$(date +%H:%M:%S)] Resetting to base $PRE_B206..."
    "$binary" reset 2>&1 || true

    # Delete DB so each binary creates its own schema
    rm -f "$DB" "${DB}-shm" "${DB}-wal"

    # Stash scripts before hard reset
    cp scripts/run-b206b-experiment.sh /tmp/run-b206b-experiment.sh.bak
    cp scripts/verify-benchmark.sh /tmp/verify-benchmark.sh.bak

    git reset --hard "$PRE_B206" 2>&1

    # Restore scripts after reset
    cp /tmp/run-b206b-experiment.sh.bak scripts/run-b206b-experiment.sh
    cp /tmp/verify-benchmark.sh.bak scripts/verify-benchmark.sh
    chmod +x scripts/run-b206b-experiment.sh scripts/verify-benchmark.sh 2>/dev/null || true

    # Run the experiment
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

    # 4. AC quality events
    local ac_events=0
    local vague_acs=0
    if [ -f "$DB" ]; then
        ac_events=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_ac_quality';" 2>/dev/null || echo 0)
        vague_acs=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_ac_quality' AND payload LIKE '%\"vague\":true%';" 2>/dev/null || echo 0)
    fi

    # 5. Files assigned to tasks
    local files_assigned=""
    if [ -f "$DB" ]; then
        files_assigned=$(sqlite3 "$DB" "SELECT file_path FROM file_locks ORDER BY file_path;" 2>/dev/null | tr '\n' '|' || echo "")
    fi

    # 6. B-206b specific: partial work failures (completeness validator)
    local partial_work_failures=0
    if [ -f "$DB" ]; then
        partial_work_failures=$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type LIKE '%partial_work%' OR (event_type = 'validation_failed' AND payload LIKE '%partial_work%');" 2>/dev/null || echo 0)
    fi

    # 7. Per-file detail
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
    echo "AC quality events: $ac_events"
    echo "Vague ACs: $vague_acs"
    echo "Files assigned: $files_assigned"
    echo "Partial work failures: $partial_work_failures"
    echo "Files detail: $files_detail"
    echo "Verdict: $verdict"
    echo ""
    echo "$verify_output"

    # Dump AC and completeness info
    if [ -f "$DB" ]; then
        echo ""
        echo "=== Acceptance Criteria Quality Events ==="
        sqlite3 -header "$DB" "SELECT task_id, payload FROM events WHERE event_type = 'decompose_ac_quality';" 2>/dev/null || true

        echo ""
        echo "=== Task Details ==="
        sqlite3 -header "$DB" "SELECT id, title, status, acceptance_criteria FROM tasks;" 2>/dev/null || true

        echo ""
        echo "=== Validation Events ==="
        sqlite3 -header "$DB" "SELECT event_type, task_id, payload FROM events WHERE event_type LIKE '%validat%' OR event_type LIKE '%partial%';" 2>/dev/null || true

        echo ""
        echo "=== File Locks ==="
        sqlite3 -header "$DB" "SELECT task_id, file_path FROM file_locks ORDER BY task_id, file_path;" 2>/dev/null || true
    fi

    # Append to CSV
    echo "$run_num,$arm,$(basename "$binary"),$files_correct,$build_pass,$merge_conflicts,$wall_clock,$task_count,$ac_events,$vague_acs,$files_assigned,$partial_work_failures,$files_detail,$verdict" >> "$RESULTS_FILE"
    echo "[CSV written] Run $run_num appended to $RESULTS_FILE"
}

echo "Starting B-206b Structured Output Enforcement A/B Experiment"
echo "Control (B-206 only): $PRE_B206B"
echo "Treatment (B-206b):   $CURRENT_HEAD"
echo "Source reset target:  $PRE_B206"
echo "Results file:         $RESULTS_FILE"
echo "================================================"

for i in $(seq "$START_RUN" 2); do
    run_experiment "$i"

    if [ "$i" -lt 2 ]; then
        echo ""
        echo "[$(date +%H:%M:%S)] Cooling down for 120s..."
        sleep 120
    fi
done

# ── Phase 3: Summary ──────────────────────────────────────────────────
echo ""
echo "================================================"
echo "Both runs complete. Results in: $RESULTS_FILE"
echo "================================================"
echo ""
cat "$RESULTS_FILE"
echo ""

# Calculate arm summaries
arm_a_files=$(grep ',A,' "$RESULTS_FILE" | cut -d',' -f4 || echo "?")
arm_b_files=$(grep ',B,' "$RESULTS_FILE" | cut -d',' -f4 || echo "?")
arm_a_pass=$(grep ',A,' "$RESULTS_FILE" | grep -c ',PASS$' || true)
arm_b_pass=$(grep ',B,' "$RESULTS_FILE" | grep -c ',PASS$' || true)

echo "=== Summary ==="
echo "Arm A (B-206 control):    $arm_a_files/8 files, $arm_a_pass/1 passed"
echo "Arm B (B-206b treatment): $arm_b_files/8 files, $arm_b_pass/1 passed"
echo ""
echo "Key questions:"
echo "1. Does B-206b improve file coverage vs B-206? (files_correct column)"
echo "2. Does JSON schema guarantee acceptance_criteria + files? (ac_events > 0 for both arms)"
echo "3. Does completeness validator catch partial work? (partial_work_failures in Arm B)"
echo "4. Does retry after partial_work failure lead to better coverage?"
