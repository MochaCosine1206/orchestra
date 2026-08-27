#!/bin/bash
# run-pressure-test.sh — Non-deterministic progressive pressure test for package release readiness
#
# ISOLATION: Each run uses a fresh git clone in /tmp. The main repo is NEVER
# modified — no git reset, no worktree creation, no branch contamination.
# Binary is built once from the main repo and reused across runs.
#
# DESIGN: Progressive tier scaling (4→6→8→10→12 files). Each run generates a
# random field name/type, so agents can't pattern-match. Stops at the first
# tier with <50% pass rate to identify the current ceiling.
#
# Usage: ./scripts/run-pressure-test.sh                           # full run, all tiers
#        TIERS="4 6" ./scripts/run-pressure-test.sh               # specific tiers only
#        RUNS_PER_TIER=2 ./scripts/run-pressure-test.sh           # fewer runs per tier
#        START_TIER=8 ./scripts/run-pressure-test.sh               # skip to tier 8
#        MAX_TASKS=3 ./scripts/run-pressure-test.sh                # override max-tasks
#        COOLDOWN=60 ./scripts/run-pressure-test.sh                # shorter cooldown
#        STOP_ON_CEILING=false ./scripts/run-pressure-test.sh      # run all tiers even if failing
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CURRENT_HEAD="$(git -C "$REPO_ROOT" rev-parse HEAD)"
CURRENT_HEAD_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"

# ── Configuration ────────────────────────────────────────────────────

TIERS="${TIERS:-4 6 8 10 12 14 16 18 20 25 30 35 40 45 50}"
RUNS_PER_TIER="${RUNS_PER_TIER:-3}"
START_TIER="${START_TIER:-0}"
MAX_TASKS="${MAX_TASKS:-4}"
COOLDOWN="${COOLDOWN:-120}"
STOP_ON_CEILING="${STOP_ON_CEILING:-true}"

# Reset target: the commit to reset clones to (before any field changes exist)
# Default: current HEAD (testing current codebase capability)
RESET_TARGET="${RESET_TARGET:-$CURRENT_HEAD}"
RESET_TARGET_SHORT="$(git -C "$REPO_ROOT" rev-parse --short "$RESET_TARGET")"

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RESULTS_FILE="/tmp/pressure-test-${TIMESTAMP}.csv"
CLONE_BASE="/tmp/pressure-test"
LOG_BASE="/tmp/pressure-logs/${TIMESTAMP}"
BIN_PATH="/tmp/orchestra-pressure-${CURRENT_HEAD_SHORT}"
GOAL_GEN="$REPO_ROOT/scripts/generate-pressure-goal.sh"

# ── Pre-flight checks ───────────────────────────────────────────────

if [ ! -f "$GOAL_GEN" ]; then
    echo "ERROR: Goal generator not found: $GOAL_GEN" >&2
    exit 1
fi

if ! command -v sqlite3 &>/dev/null; then
    echo "ERROR: sqlite3 required for metrics extraction" >&2
    exit 1
fi

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         PRESSURE TEST — Package Release Readiness           ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Binary:        $CURRENT_HEAD_SHORT (current HEAD)"
echo "║  Reset target:  $RESET_TARGET_SHORT"
echo "║  Tiers:         $TIERS"
echo "║  Runs/tier:     $RUNS_PER_TIER"
echo "║  Max tasks:     $MAX_TASKS"
echo "║  Cooldown:      ${COOLDOWN}s"
echo "║  Stop on ceil:  $STOP_ON_CEILING"
echo "║  Results:       $RESULTS_FILE"
echo "║  Logs:          $LOG_BASE/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Phase 1: Build binary ───────────────────────────────────────────

if [ -f "$BIN_PATH" ]; then
    echo "[$(date +%H:%M:%S)] Reusing existing binary: $BIN_PATH"
else
    echo "[$(date +%H:%M:%S)] Building binary from $CURRENT_HEAD_SHORT..."
    cd "$REPO_ROOT"
    make build 2>&1 | tail -3
    cp bin/orchestra "$BIN_PATH"
    echo "[$(date +%H:%M:%S)] Binary ready: $BIN_PATH ($(wc -c < "$BIN_PATH") bytes)"
fi
echo ""

# ── Phase 2: Initialize CSV ─────────────────────────────────────────

echo "tier,run,field_name,field_type,files_target,files_correct,build_pass,merge_conflicts,wall_clock_s,task_count,done_count,failed_count,decomposer_reasoning_len,files_detail,verdict" > "$RESULTS_FILE"

# ── Phase 3: Run tiers ──────────────────────────────────────────────

CEILING_TIER=""
TIER_SUMMARY_FILE=$(mktemp)
trap 'rm -f "$TIER_SUMMARY_FILE"' EXIT

run_single() {
    local tier=$1
    local run_num=$2
    local run_id="tier-${tier}/run-${run_num}"

    echo ""
    echo "────────────────────────────────────────────────────────"
    echo "  Tier $tier, Run $run_num / $RUNS_PER_TIER"
    echo "────────────────────────────────────────────────────────"

    # Generate random goal for this run
    local goal_dir="$LOG_BASE/$run_id"
    mkdir -p "$goal_dir"
    echo "[$(date +%H:%M:%S)] Generating random goal for tier $tier..."
    bash "$GOAL_GEN" "$tier" "$goal_dir" 2>&1

    # Read generated files
    local goal_text
    goal_text=$(cat "$goal_dir/goal.txt")

    # Read metadata
    local field_name field_type go_field
    field_name=$(grep '^FIELD_NAME=' "$goal_dir/meta.env" | cut -d= -f2)
    field_type=$(grep '^SQL_TYPE=' "$goal_dir/meta.env" | cut -d= -f2)
    go_field=$(grep '^GO_FIELD=' "$goal_dir/meta.env" | cut -d= -f2)
    local file_count
    file_count=$(grep '^FILE_COUNT=' "$goal_dir/meta.env" | cut -d= -f2)

    echo "[$(date +%H:%M:%S)] Goal: add $field_name ($field_type) across $file_count files"

    # Create fresh clone
    local clone_dir="${CLONE_BASE}/${run_id}"
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Cloning repo to $clone_dir..."
    git clone --quiet "$REPO_ROOT" "$clone_dir" 2>&1
    cd "$clone_dir"
    git reset --hard "$RESET_TARGET" 2>&1 | tail -1
    echo "[$(date +%H:%M:%S)] Clone ready at $(git rev-parse --short HEAD)"

    # Initialize orchestra in the clone
    rm -rf "$clone_dir/.orchestra" "$clone_dir/.worktree"
    "$BIN_PATH" init --clean 2>&1 | tail -1
    echo "[$(date +%H:%M:%S)] Orchestra initialized in clone"

    local db="$clone_dir/.orchestra/orchestrator.db"

    # Run orchestra
    echo "[$(date +%H:%M:%S)] Starting orchestra go (max-tasks=$MAX_TASKS)..."
    local start_ts
    start_ts=$(date +%s)

    # Prevent nesting guard from affecting agents (G77/G83)
    unset CLAUDECODE

    # Capture stdout for later analysis
    "$BIN_PATH" go --goal "$goal_text" --max-tasks "$MAX_TASKS" --test-cmd 'go build ./...' --foreground 2>&1 | tee "$goal_dir/stdout.log"
    local exit_code=$?

    local end_ts
    end_ts=$(date +%s)
    local wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s (exit: $exit_code)"

    # ── Collect metrics ──

    # Run verification
    local verify_output
    verify_output=$(bash "$goal_dir/verify.sh" "$clone_dir" 2>&1)
    echo "$verify_output"

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

    # DB metrics
    local merge_conflicts=0
    local task_count=0
    local done_count=0
    local failed_count=0
    local decomposer_reasoning_len=0

    if [ -f "$db" ]; then
        merge_conflicts=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_conflict';" 2>/dev/null || echo 0)
        task_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks;" 2>/dev/null || echo 0)
        done_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks WHERE status = 'done';" 2>/dev/null || echo 0)
        failed_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks WHERE status = 'failed';" 2>/dev/null || echo 0)
        decomposer_reasoning_len=$(sqlite3 "$db" "SELECT COALESCE(LENGTH(payload), 0) FROM events WHERE event_type = 'decompose_reasoning' ORDER BY id DESC LIMIT 1;" 2>/dev/null || echo 0)
    fi

    local files_detail
    files_detail=$(echo "$verify_output" | grep '^FILES_DETAIL:' | sed 's/^FILES_DETAIL: //' || echo "")

    echo ""
    echo "--- Tier $tier, Run $run_num Results ---"
    echo "Field: $field_name ($field_type/$go_field)"
    echo "Files correct: $files_correct/$file_count | Build: $build_pass | Conflicts: $merge_conflicts"
    echo "Tasks: $task_count (done=$done_count, failed=$failed_count) | Wall clock: ${wall_clock}s"
    echo "Files: $files_detail"
    echo "Verdict: $verdict"
    echo ""

    # Append to CSV
    echo "$tier,$run_num,$field_name,$field_type,$file_count,$files_correct,$build_pass,$merge_conflicts,$wall_clock,$task_count,$done_count,$failed_count,$decomposer_reasoning_len,$files_detail,$verdict" >> "$RESULTS_FILE"

    # Preserve logs before cleanup
    if [ -d "$clone_dir/.orchestra/logs" ]; then
        cp -r "$clone_dir/.orchestra/logs" "$goal_dir/"
        echo "[$(date +%H:%M:%S)] Agent logs saved to $goal_dir/logs/"
    fi
    if [ -f "$db" ]; then
        cp "$db" "$goal_dir/orchestrator.db"
        echo "[$(date +%H:%M:%S)] DB saved to $goal_dir/orchestrator.db"
    fi

    # Generate case study template
    cat > "$goal_dir/findings.md" << FINDINGS_EOF
# Pressure Test: Tier ${tier}, Run ${run_num}

## Goal
**Field:** ${field_name} (${field_type})
**Go field:** ${go_field}
**Files targeted:** ${file_count}

## Result
**Verdict:** ${verdict}
**Files correct:** ${files_correct}/${file_count}
**Build passes:** ${build_pass}
**Wall clock:** ${wall_clock}s

## Decomposer
- Tasks created: ${task_count}
- Done: ${done_count}
- Failed: ${failed_count}
- Reasoning length: ${decomposer_reasoning_len} chars
- (check orchestrator.db events table, event_type='decompose_reasoning')

## File Results
${files_detail}

## Merge
- Conflicts: ${merge_conflicts}
- Auto-resolved: (check events for merge_conflict_resolved)

## Agent Behavior
- (trace from logs/*.jsonl — what did each agent read/edit/skip?)

## What Broke
- (fill in after log analysis — NEVER assume; read the actual agent logs)

## Root Cause
- (must be traced from logs/DB evidence, not assumed)
- (if logs are missing, that is the gap to fix first)

## Gaps Identified
- (only list gaps with evidence from this run — no speculative gaps)
- (each gap needs: file path, line number or event, what was expected vs actual)

## Improvement Suggestion
- (what Orchestra change would fix the traced root cause?)
FINDINGS_EOF
    echo "[$(date +%H:%M:%S)] Case study template: $goal_dir/findings.md"

    # Clean up clone
    cd "$REPO_ROOT"
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Clone cleaned up."

    # Return verdict via global
    _LAST_VERDICT="$verdict"
}

for tier in $TIERS; do
    # Skip tiers below START_TIER
    if [ "$tier" -lt "$START_TIER" ]; then
        echo "[$(date +%H:%M:%S)] Skipping tier $tier (below START_TIER=$START_TIER)"
        continue
    fi

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║  TIER $tier — ${tier}-file pressure test ($RUNS_PER_TIER runs)"
    echo "╚══════════════════════════════════════════════════════════════╝"

    tier_passes=0
    for run in $(seq 1 "$RUNS_PER_TIER"); do
        run_single "$tier" "$run"

        if [ "$_LAST_VERDICT" = "PASS" ]; then
            tier_passes=$((tier_passes + 1))
        fi

        # Cooldown between runs (not after last run of tier)
        if [ "$run" -lt "$RUNS_PER_TIER" ]; then
            echo "[$(date +%H:%M:%S)] Cooling down for ${COOLDOWN}s between runs..."
            sleep "$COOLDOWN"
        fi
    done

    # Calculate tier pass rate
    tier_rate=$((tier_passes * 100 / RUNS_PER_TIER))
    echo "${tier}|${tier_passes}/${RUNS_PER_TIER} (${tier_rate}%)" >> "$TIER_SUMMARY_FILE"

    echo ""
    echo "══════════════════════════════════════════════════════════════"
    echo "  Tier $tier summary: ${tier_passes}/${RUNS_PER_TIER} passed (${tier_rate}%)"
    echo "══════════════════════════════════════════════════════════════"

    # Stop if ceiling found
    if [ "$tier_rate" -lt 50 ] && [ "$STOP_ON_CEILING" = "true" ]; then
        CEILING_TIER="$tier"
        echo ""
        echo "!! CEILING FOUND at tier $tier (${tier_rate}% < 50%) — stopping."
        break
    fi

    # Cooldown between tiers
    local_next_tier=""
    for t in $TIERS; do
        if [ "$t" -gt "$tier" ]; then
            local_next_tier="$t"
            break
        fi
    done
    if [ -n "$local_next_tier" ]; then
        echo "[$(date +%H:%M:%S)] Cooling down for ${COOLDOWN}s between tiers..."
        sleep "$COOLDOWN"
    fi
done

# ── Phase 4: Summary ────────────────────────────────────────────────

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                 PRESSURE TEST COMPLETE                      ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Binary: $CURRENT_HEAD_SHORT"
echo "║  Reset:  $RESET_TARGET_SHORT"

while IFS='|' read -r t result; do
    printf "║  Tier %2d: %s\n" "$t" "$result"
done < "$TIER_SUMMARY_FILE"

if [ -n "$CEILING_TIER" ]; then
    echo "║"
    echo "║  >> CEILING: Tier $CEILING_TIER (first tier with <50% pass rate)"
else
    echo "║"
    echo "║  >> No ceiling found — all tiers passed ≥50%"
fi

echo "║"
echo "║  Results CSV:  $RESULTS_FILE"
echo "║  Run logs:     $LOG_BASE/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Print CSV
echo "=== Full Results CSV ==="
cat "$RESULTS_FILE"
echo ""

# Print log directory structure
echo "=== Log Directory ==="
if command -v tree &>/dev/null; then
    tree -L 3 "$LOG_BASE" 2>/dev/null || ls -R "$LOG_BASE"
else
    ls -R "$LOG_BASE"
fi
