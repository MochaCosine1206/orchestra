#!/bin/zsh
# validate-phase1-gate.sh — End-to-end validation of Phase 1 gate features
#
# Validates three Phase 1 features:
#   1. Stall detection (stall_scores table populated)
#   2. Plan cache (store on first run, hit on second run)
#   3. Agent kill + respawn (agent_respawning event after PID kill)
#
# ISOLATION: Uses a fresh git clone in /tmp. The main repo is NEVER modified.
# Binary is built once from the main repo and reused.
#
# Usage: ./scripts/validate-phase1-gate.sh
#        TIMEOUT=600 ./scripts/validate-phase1-gate.sh    # custom gtimeout (seconds)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CURRENT_HEAD="$(git -C "$REPO_ROOT" rev-parse HEAD)"
CURRENT_HEAD_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"

# ── Configuration ────────────────────────────────────────────────────

TIMEOUT="${TIMEOUT:-300}"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
CLONE_DIR="/tmp/phase1-gate-${TIMESTAMP}"
BIN_PATH="/tmp/orchestra-phase1-${CURRENT_HEAD_SHORT}"
REPORT_FILE="/tmp/phase1-gate-report-${TIMESTAMP}.txt"
DB_NAME=".orchestra/orchestrator.db"

# Deterministic goal for decomposition (touches 3-5 files)
GOAL="Add a new 'version' field (string) to the Agent struct in internal/db/models.go, include it in InsertAgent and GetAgent queries in internal/db/mutations.go and internal/db/queries.go, and add a unit test in internal/db/queries_test.go that verifies the field round-trips correctly."

# Second goal for respawn test (different to avoid plan cache interference)
RESPAWN_GOAL="Refactor internal/agent/spawner.go to add structured slog logging to all log.Printf calls. For each log.Printf, convert to slog.Info or slog.Error with key-value pairs. Also add slog logging to internal/agent/validator.go and internal/agent/completion.go. Write a test in internal/agent/spawner_test.go that verifies the spawner initializes correctly."

# ── Result tracking ──────────────────────────────────────────────────

typeset -A RESULTS
ALL_PASS=true

record() {
    local name="$1" result="$2" evidence="${3:-}"
    RESULTS[$name]="$result"
    if [ "$result" = "FAIL" ]; then
        ALL_PASS=false
    fi
    echo "[$(date +%H:%M:%S)] $name: $result${evidence:+ ($evidence)}"
}

# ── Cleanup trap ─────────────────────────────────────────────────────

cleanup() {
    echo ""
    echo "[$(date +%H:%M:%S)] Cleaning up..."
    # Kill any orchestra processes we may have started in the clone
    if [ -d "${CLONE_DIR}/pids" ]; then
        for pidfile in "${CLONE_DIR}"/pids/*.pid; do
            [ -f "$pidfile" ] || continue
            local pid
            pid="$(cat "$pidfile" 2>/dev/null)" || continue
            kill "$pid" 2>/dev/null || true
        done
    fi
    rm -rf "$CLONE_DIR"
    echo "[$(date +%H:%M:%S)] Cleanup complete."
}
trap cleanup EXIT

# ── Helper: run orchestra in clone with gtimeout ──────────────────────

run_orchestra() {
    local goal="$1"
    local clone_timeout="${2:-$TIMEOUT}"
    cd "$CLONE_DIR"
    gtimeout "${clone_timeout}" "$BIN_PATH" go --goal "$goal" --foreground 2>&1 || true
    cd - > /dev/null
}

# ── Helper: query DB in clone ────────────────────────────────────────

query_db() {
    sqlite3 "${CLONE_DIR}/${DB_NAME}" "$1"
}

# ── Pre-flight checks ───────────────────────────────────────────────

if ! command -v sqlite3 &>/dev/null; then
    echo "ERROR: sqlite3 required" >&2
    exit 1
fi

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         PHASE 1 GATE VALIDATION                            ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Binary:   $CURRENT_HEAD_SHORT"
echo "║  Timeout:  ${TIMEOUT}s per run"
echo "║  Clone:    $CLONE_DIR"
echo "║  Report:   $REPORT_FILE"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Step 1: Setup ────────────────────────────────────────────────────

echo "═══ Step 1: Setup ═══"

# Build binary if needed
if [ -f "$BIN_PATH" ]; then
    echo "[$(date +%H:%M:%S)] Reusing existing binary: $BIN_PATH"
else
    echo "[$(date +%H:%M:%S)] Building binary from $CURRENT_HEAD_SHORT..."
    cd "$REPO_ROOT"
    go build -o "$BIN_PATH" ./cmd/orchestra
    cd - > /dev/null
    echo "[$(date +%H:%M:%S)] Binary built: $BIN_PATH"
fi

# Create fresh clone
echo "[$(date +%H:%M:%S)] Cloning repo to $CLONE_DIR..."
git clone --quiet "$REPO_ROOT" "$CLONE_DIR"
cd "$CLONE_DIR"
git reset --hard "$CURRENT_HEAD" --quiet
"$BIN_PATH" init 2>/dev/null || true
cd - > /dev/null
echo "[$(date +%H:%M:%S)] Clone ready."

# ── Step 2: First run (stall detection + plan cache store) ───────────

echo ""
echo "═══ Step 2: First run — stall detection + plan cache store ═══"

run_orchestra "$GOAL"

# Check stall_scores
STALL_COUNT="$(query_db "SELECT COUNT(*) FROM stall_scores;" 2>/dev/null || echo "0")"
if [ "$STALL_COUNT" -gt 0 ] 2>/dev/null; then
    record "stall_detection" "PASS" "stall_scores rows: $STALL_COUNT"
else
    record "stall_detection" "FAIL" "stall_scores rows: $STALL_COUNT"
fi

# Check plan_cache
CACHE_COUNT="$(query_db "SELECT COUNT(*) FROM plan_cache;" 2>/dev/null || echo "0")"
if [ "$CACHE_COUNT" -gt 0 ] 2>/dev/null; then
    record "plan_cache_store" "PASS" "plan_cache rows: $CACHE_COUNT"
else
    record "plan_cache_store" "FAIL" "plan_cache rows: $CACHE_COUNT"
fi

# ── Step 3: Second run (plan cache hit) ──────────────────────────────

echo ""
echo "═══ Step 3: Second run — plan cache hit ═══"

# Clear session state but KEEP plan_cache so the hit can be tested
cd "$CLONE_DIR"
query_db "DELETE FROM tasks;" 2>/dev/null || true
query_db "DELETE FROM agents;" 2>/dev/null || true
query_db "DELETE FROM conductors;" 2>/dev/null || true
query_db "DELETE FROM events;" 2>/dev/null || true
echo "[$(date +%H:%M:%S)] Cleared session state (preserved plan_cache)"
cd - > /dev/null

run_orchestra "$GOAL"

# Check hit_count
HIT_COUNT="$(query_db "SELECT COALESCE(MAX(hit_count), 0) FROM plan_cache;" 2>/dev/null || echo "0")"
if [ "$HIT_COUNT" -ge 1 ] 2>/dev/null; then
    record "plan_cache_hit" "PASS" "max hit_count: $HIT_COUNT"
else
    record "plan_cache_hit" "FAIL" "max hit_count: $HIT_COUNT"
fi

# ── Step 4: Agent kill + respawn ─────────────────────────────────────

echo ""
echo "═══ Step 4: Agent kill + respawn ═══"

# Reset clone state for respawn test
cd "$CLONE_DIR"
git reset --hard "$CURRENT_HEAD" --quiet
# Clear events from prior runs so we only see new respawn events
query_db "DELETE FROM events;" 2>/dev/null || true
cd - > /dev/null

# Launch orchestra in background
cd "$CLONE_DIR"
gtimeout "$TIMEOUT" "$BIN_PATH" go --goal "$RESPAWN_GOAL" --foreground 2>&1 &
ORCH_PID=$!
cd - > /dev/null

# Wait for TASK agents to spawn (up to 120s, checking every 5s)
# Must wait for decomposition to finish and actual task agents to start.
# Check .orchestra/pids/ for task PID files (t-*.pid), NOT the conductor's
# own decompose claude call. Also exclude our own session PID.
echo "[$(date +%H:%M:%S)] Waiting for task agents to spawn (up to 120s)..."
AGENT_PID=""
for i in $(seq 1 24); do
    sleep 5
    # Check .orchestra/pids/ for task PID files first (most reliable)
    if [ -d "${CLONE_DIR}/.orchestra/pids" ]; then
        for pidfile in "${CLONE_DIR}"/.orchestra/pids/t-*.pid; do
            [ -f "$pidfile" ] || continue
            local candidate="$(cat "$pidfile" 2>/dev/null)" || continue
            if [ -n "$candidate" ] && kill -0 "$candidate" 2>/dev/null; then
                AGENT_PID="$candidate"
                echo "[$(date +%H:%M:%S)] Found task agent PID from pidfile: $AGENT_PID ($(basename $pidfile))"
                break 2
            fi
        done
    fi
    # Fallback: look for claude processes that are NOT the conductor's decompose call
    # Task agents have longer prompts with "# Implementer Agent" or task spec content
    local candidates="$(pgrep -f 'claude.*-p.*Implementer\|claude.*-p.*task_id' 2>/dev/null || echo "")"
    if [ -n "$candidates" ]; then
        AGENT_PID="$(echo "$candidates" | head -1)"
        echo "[$(date +%H:%M:%S)] Found task agent PID via pgrep: $AGENT_PID"
        break
    fi
done

if [ -n "$AGENT_PID" ]; then
    echo "[$(date +%H:%M:%S)] Killing agent PID $AGENT_PID..."
    kill "$AGENT_PID" 2>/dev/null || true

    # Wait for orchestra to finish (it should detect the kill and respawn)
    echo "[$(date +%H:%M:%S)] Waiting for respawn detection (up to ${TIMEOUT}s)..."
    wait "$ORCH_PID" 2>/dev/null || true

    # Check events table for respawn
    RESPAWN_COUNT="$(query_db "SELECT COUNT(*) FROM events WHERE event_type IN ('agent_respawning', 'agent_respawning_refinement');" 2>/dev/null || echo "0")"
    if [ "$RESPAWN_COUNT" -gt 0 ] 2>/dev/null; then
        record "agent_respawn" "PASS" "respawn events: $RESPAWN_COUNT"
    else
        record "agent_respawn" "FAIL" "respawn events: $RESPAWN_COUNT"
    fi
else
    # No agent found to kill — orchestra may have finished too fast
    wait "$ORCH_PID" 2>/dev/null || true
    record "agent_respawn" "FAIL" "no running agent PID found to kill"
fi

# ── Step 5: Structured report ────────────────────────────────────────

echo ""
echo "═══ Step 5: Report ═══"

{
    echo "Phase 1 Gate Validation Report"
    echo "=============================="
    echo "Timestamp:  $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "Commit:     $CURRENT_HEAD_SHORT ($CURRENT_HEAD)"
    echo "Clone:      $CLONE_DIR"
    echo ""
    echo "Results:"
    echo "--------"
    for check in stall_detection plan_cache_store plan_cache_hit agent_respawn; do
        result="${RESULTS[$check]:-SKIP}"
        printf "  %-20s %s\n" "$check" "$result"
    done
    echo ""
    if [ "$ALL_PASS" = true ]; then
        echo "Overall: ALL PASS"
    else
        echo "Overall: SOME FAILED"
    fi
} | tee "$REPORT_FILE"

echo ""
echo "[$(date +%H:%M:%S)] Report written to: $REPORT_FILE"

# ── Step 6: Exit code ────────────────────────────────────────────────

if [ "$ALL_PASS" = true ]; then
    echo "[$(date +%H:%M:%S)] All Phase 1 gate checks PASSED."
    exit 0
else
    echo "[$(date +%H:%M:%S)] Some Phase 1 gate checks FAILED."
    exit 1
fi
