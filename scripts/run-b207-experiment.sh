#!/bin/bash
# run-b207-experiment.sh — A/B test for B-207 goal specificity bottleneck fixes
#
# ISOLATION: Each run uses a fresh git clone in /tmp. The main repo is NEVER
# modified — no git reset, no worktree creation, no branch contamination.
# Binaries are built once from the main repo and reused across runs.
#
# Arm A (control): Binary built from PRE_B207 (B-206b only)
# Arm B (treatment): Binary built from HEAD (B-207: all features)
# Each run clones to /tmp, resets clone to PRE_B206, runs orchestra, collects metrics.
#
# Usage: ./scripts/run-b207-experiment.sh                     # 6 runs (3 per arm)
#        TOTAL_RUNS=2 ./scripts/run-b207-experiment.sh        # quick 2-run (1 per arm)
#        START_RUN=4 ./scripts/run-b207-experiment.sh          # resume from run 4
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PRE_B206="7a56f6c"          # Before priority_label existed (source reset target)
PRE_B207="e48c7f9"          # Has B-206b but NOT B-207
CURRENT_HEAD="$(git -C "$REPO_ROOT" rev-parse HEAD)"
RESULTS_FILE="/tmp/b207-experiment-results.csv"
BIN_CONTROL="/tmp/orchestra-pre-b207"
BIN_TREATMENT="/tmp/orchestra-b207"
CLONE_BASE="/tmp/b207-experiment"
GOAL='Add a priority_label TEXT field (sql.NullString) to the Task model. This human-readable label should flow through: schema/001_initial.sql -> models.go -> queries.go -> mutations.go -> spawner.go -> go.go -> iterative.go -> TUI display.'
START_RUN="${START_RUN:-1}"
TOTAL_RUNS="${TOTAL_RUNS:-6}"  # 3 per arm (A-B-A-B-A-B)

# Copy verify script to /tmp so it's available in clones
cp "$REPO_ROOT/scripts/verify-benchmark.sh" /tmp/verify-benchmark.sh.bak
chmod +x /tmp/verify-benchmark.sh.bak

# ── Phase 1: Build both binaries (in main repo, read-only after this) ─
echo "=== Building binaries ==="
cd "$REPO_ROOT"

# Build treatment binary from current HEAD (B-207)
echo "[$(date +%H:%M:%S)] Building B-207 treatment binary from $CURRENT_HEAD..."
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_TREATMENT"
echo "  → $BIN_TREATMENT ($(wc -c < "$BIN_TREATMENT") bytes)"

# Build control binary from PRE_B207 — use a temporary clone to avoid touching main repo
CONTROL_BUILD_DIR="/tmp/b207-control-build"
rm -rf "$CONTROL_BUILD_DIR"
echo "[$(date +%H:%M:%S)] Cloning for control binary build..."
git clone --quiet "$REPO_ROOT" "$CONTROL_BUILD_DIR" 2>&1
cd "$CONTROL_BUILD_DIR"
git checkout "$PRE_B207" 2>&1 | tail -1
echo "[$(date +%H:%M:%S)] Building B-206b control binary from $PRE_B207..."
make build 2>&1 | tail -1
cp bin/orchestra "$BIN_CONTROL"
echo "  → $BIN_CONTROL ($(wc -c < "$BIN_CONTROL") bytes)"
cd "$REPO_ROOT"
rm -rf "$CONTROL_BUILD_DIR"

echo "Both binaries ready. Main repo untouched."
echo ""

# ── Phase 2: Run experiments in isolated clones ───────────────────────

if [ ! -f "$RESULTS_FILE" ]; then
    echo "run,arm,binary,files_correct,build_pass,merge_conflicts,wall_clock_s,task_count,ac_events,vague_acs,partial_work_failures,action_expansion_fired,agent_files_skipped,files_detail,verdict" > "$RESULTS_FILE"
fi

run_experiment() {
    local run_num=$1
    local arm
    if [ $((run_num % 2)) -eq 1 ]; then arm="A"; else arm="B"; fi
    local binary
    if [ "$arm" = "A" ]; then binary="$BIN_CONTROL"; else binary="$BIN_TREATMENT"; fi

    local clone_dir="${CLONE_BASE}/run-${run_num}"

    echo ""
    echo "================================================================"
    echo "  RUN $run_num / $TOTAL_RUNS — Arm $arm ($([ "$arm" = "A" ] && echo "B-206b control" || echo "B-207 treatment"))"
    echo "================================================================"
    echo ""

    # Create fresh clone at PRE_B206
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Cloning repo to $clone_dir..."
    git clone --quiet "$REPO_ROOT" "$clone_dir" 2>&1
    cd "$clone_dir"
    git reset --hard "$PRE_B206" 2>&1 | tail -1
    echo "[$(date +%H:%M:%S)] Clone ready at $(git rev-parse --short HEAD)"

    # Initialize orchestra in the clone
    rm -rf "$clone_dir/.orchestra" "$clone_dir/.worktree"
    "$binary" init --clean 2>&1 | tail -1
    echo "[$(date +%H:%M:%S)] Orchestra initialized in clone"

    local db="$clone_dir/.orchestra/orchestrator.db"

    # Run the experiment in the clone
    echo "[$(date +%H:%M:%S)] Starting with $(basename "$binary")..."
    local start_ts=$(date +%s)

    cd "$clone_dir"
    "$binary" go --goal "$GOAL" --max-tasks 3 --test-cmd 'go build ./...' --foreground 2>&1
    local exit_code=$?

    local end_ts=$(date +%s)
    local wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s (exit: $exit_code)"

    # ── Collect metrics ──
    local verify_output
    verify_output=$(bash /tmp/verify-benchmark.sh.bak "$clone_dir" 2>&1)
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

    local merge_conflicts=0
    local task_count=0
    local ac_events=0
    local vague_acs=0
    local partial_work_failures=0
    local action_expansion_fired=0
    local agent_files_skipped=0

    if [ -f "$db" ]; then
        merge_conflicts=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_conflict';" 2>/dev/null || echo 0)
        task_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks;" 2>/dev/null || echo 0)
        ac_events=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_ac_quality';" 2>/dev/null || echo 0)
        vague_acs=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_ac_quality' AND payload LIKE '%\"vague\":true%';" 2>/dev/null || echo 0)
        partial_work_failures=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type LIKE '%partial_work%' OR (event_type = 'validation_failed' AND payload LIKE '%partial_work%');" 2>/dev/null || echo 0)
        action_expansion_fired=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'decompose_action_expanded';" 2>/dev/null || echo 0)
        agent_files_skipped=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'agent_file_skipped';" 2>/dev/null || echo 0)
    fi

    local files_detail
    files_detail=$(echo "$verify_output" | grep '^FILES_DETAIL:' | sed 's/^FILES_DETAIL: //' || echo "")

    echo ""
    echo "--- Run $run_num Results ---"
    echo "Arm: $arm | Binary: $(basename "$binary")"
    echo "Files correct: $files_correct/8 | Build: $build_pass | Conflicts: $merge_conflicts | Wall clock: ${wall_clock}s"
    echo "Action expansion: $action_expansion_fired | Agent skips: $agent_files_skipped"
    echo "Files: $files_detail"
    echo "Verdict: $verdict"
    echo ""

    # Append to CSV
    echo "$run_num,$arm,$(basename "$binary"),$files_correct,$build_pass,$merge_conflicts,$wall_clock,$task_count,$ac_events,$vague_acs,$partial_work_failures,$action_expansion_fired,$agent_files_skipped,$files_detail,$verdict" >> "$RESULTS_FILE"
    echo "[CSV written] Run $run_num appended to $RESULTS_FILE"

    # Preserve logs before cleanup
    local log_dest="/tmp/b207-logs/run-${run_num}"
    mkdir -p "$log_dest"
    if [ -d "$clone_dir/.orchestra/logs" ]; then
        cp -r "$clone_dir/.orchestra/logs" "$log_dest/"
        echo "[$(date +%H:%M:%S)] Agent logs saved to $log_dest/logs/"
    fi
    if [ -f "$db" ]; then
        cp "$db" "$log_dest/orchestrator.db"
        echo "[$(date +%H:%M:%S)] DB saved to $log_dest/orchestrator.db"
    fi

    # Clean up clone
    cd "$REPO_ROOT"
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Clone cleaned up."
}

echo "Starting B-207 Goal Specificity Bottleneck A/B Experiment (Clone-Isolated)"
echo "Control (B-206b only): $PRE_B207"
echo "Treatment (B-207):     $CURRENT_HEAD"
echo "Source reset target:   $PRE_B206"
echo "Results file:          $RESULTS_FILE"
echo "Clone base:            $CLONE_BASE"
echo "Total runs:            $TOTAL_RUNS (alternating A-B)"
echo "================================================"
echo "ISOLATION: Each run uses a fresh clone. Main repo is never modified."
echo "================================================"

for i in $(seq "$START_RUN" "$TOTAL_RUNS"); do
    run_experiment "$i"

    if [ "$i" -lt "$TOTAL_RUNS" ]; then
        echo ""
        echo "[$(date +%H:%M:%S)] Cooling down for 120s..."
        sleep 120
    fi
done

# ── Phase 3: Summary ──────────────────────────────────────────────────
echo ""
echo "================================================"
echo "All $TOTAL_RUNS runs complete. Results in: $RESULTS_FILE"
echo "================================================"
echo ""
cat "$RESULTS_FILE"
echo ""

arm_a_avg=$(grep ',A,' "$RESULTS_FILE" | awk -F',' '{sum+=$4; n++} END {printf "%.1f", n>0?sum/n:0}')
arm_b_avg=$(grep ',B,' "$RESULTS_FILE" | awk -F',' '{sum+=$4; n++} END {printf "%.1f", n>0?sum/n:0}')
arm_a_count=$(grep -c ',A,' "$RESULTS_FILE" || echo 0)
arm_b_count=$(grep -c ',B,' "$RESULTS_FILE" || echo 0)
arm_a_pass=$(grep ',A,' "$RESULTS_FILE" | grep -c ',PASS$' || true)
arm_b_pass=$(grep ',B,' "$RESULTS_FILE" | grep -c ',PASS$' || true)

echo "=== Summary ==="
echo "Arm A (B-206b control):  avg $arm_a_avg/8 files, $arm_a_pass/$arm_a_count passed"
echo "Arm B (B-207 treatment): avg $arm_b_avg/8 files, $arm_b_pass/$arm_b_count passed"
echo ""
echo "Main repo state: $(git -C "$REPO_ROOT" rev-parse --short HEAD) (unchanged)"
