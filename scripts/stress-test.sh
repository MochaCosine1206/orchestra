#!/usr/bin/env bash
# stress-test.sh — Dark Factory Phase 1 validation stress test
# Launches an orchestra run, injects failures at scheduled intervals,
# and monitors recovery. Designed for overnight unattended execution.
#
# Usage: bash scripts/stress-test.sh [--duration 6h] [--goal "..."]
# Output: .orchestra/stress-test/report.json
#
# Failure injections:
#   1. Kill agent (30min) — SIGKILL a random agent PID
#   2. Corrupt worktree (60min) — remove .git from an active worktree
#   3. Simulate stall (90min) — touch agent PID file but kill process
#
# All recovery paths are already built in Orchestra:
#   - Monitor detects dead agents → re-spawn
#   - Worktree corruption → salvage + re-create
#   - Stall detection → composite score → timeout + re-spawn

set -euo pipefail

DURATION="${DURATION:-6h}"
GOAL="${GOAL:-Build comprehensive test suite for internal/dashboard/ package: handler tests, template rendering tests, SSE streaming tests, and HTMX polling tests. Target 80% coverage.}"
MAX_TASKS="${MAX_TASKS:-8}"
MAX_PARALLEL="${MAX_PARALLEL:-4}"
REPORT_DIR=".orchestra/stress-test"
REPORT_FILE="$REPORT_DIR/report.json"
LOG_FILE="$REPORT_DIR/stress-test.log"

# Parse flags
while [[ $# -gt 0 ]]; do
    case $1 in
        --duration) DURATION="$2"; shift 2 ;;
        --goal) GOAL="$2"; shift 2 ;;
        --max-tasks) MAX_TASKS="$2"; shift 2 ;;
        --max-parallel) MAX_PARALLEL="$2"; shift 2 ;;
        *) echo "Unknown flag: $1"; exit 1 ;;
    esac
done

# Convert duration to seconds
duration_seconds() {
    local d="$1"
    if [[ "$d" =~ ^([0-9]+)h$ ]]; then
        echo $(( ${BASH_REMATCH[1]} * 3600 ))
    elif [[ "$d" =~ ^([0-9]+)m$ ]]; then
        echo $(( ${BASH_REMATCH[1]} * 60 ))
    else
        echo "$d"
    fi
}

DURATION_SEC=$(duration_seconds "$DURATION")
INJECT_1_SEC=$(( DURATION_SEC / 12 ))   # ~30min for 6h
INJECT_2_SEC=$(( DURATION_SEC / 6 ))    # ~60min for 6h
INJECT_3_SEC=$(( DURATION_SEC / 4 ))    # ~90min for 6h

mkdir -p "$REPORT_DIR"

log() {
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] $*"
    echo "$msg" | tee -a "$LOG_FILE"
}

# Initialize report
cat > "$REPORT_FILE" << EOF
{
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "duration_target": "$DURATION",
  "goal": "$(echo "$GOAL" | head -c 200)",
  "injections": [],
  "recoveries": [],
  "status": "running"
}
EOF

log "=== Dark Factory Stress Test ==="
log "Duration: $DURATION ($DURATION_SEC seconds)"
log "Goal: $(echo "$GOAL" | head -c 100)..."
log "Injections at: ${INJECT_1_SEC}s, ${INJECT_2_SEC}s, ${INJECT_3_SEC}s"
log ""

# Reset and build fresh
log "Building fresh binary..."
make build 2>&1 | tail -1
bin/orchestra reset 2>&1 | tail -3
log "Reset complete."

# Launch orchestra in auto mode
# `orchestra go` spawns a detached conductor-run process and exits immediately.
# We need to find the real conductor PID from the DB after launch.
log "Launching orchestra go (auto mode)..."
bin/orchestra go \
    -g "$GOAL" \
    --max-tasks "$MAX_TASKS" \
    --max-parallel "$MAX_PARALLEL" \
    --iterative \
    --reconcile \
    > "$REPORT_DIR/conductor.log" 2>&1 || true

# Wait for conductor to register in DB, then extract its PID
sleep 10
CONDUCTOR_PID=$(sqlite3 .orchestra/orchestrator.db \
    "SELECT pid FROM conductors WHERE status='active' ORDER BY started_at DESC LIMIT 1" 2>/dev/null || echo "")
if [ -z "$CONDUCTOR_PID" ]; then
    log "WARNING: No active conductor found in DB, checking processes..."
    CONDUCTOR_PID=$(pgrep -f "conductor-run" | head -1 || echo "")
fi

if [ -z "$CONDUCTOR_PID" ]; then
    log "ERROR: Could not find conductor process!"
    log "Check $REPORT_DIR/conductor.log and .orchestra/logs/"
    exit 1
fi
log "Conductor PID: $CONDUCTOR_PID"

# Verify it's actually running
sleep 5
if ! kill -0 "$CONDUCTOR_PID" 2>/dev/null; then
    log "ERROR: Conductor PID $CONDUCTOR_PID is not running!"
    log "Check .orchestra/logs/ for conductor log"
    exit 1
fi

log "Conductor running. Starting injection schedule..."

# Track injection results
INJECT_COUNT=0
INJECT_SUCCESS=0

inject_kill_agent() {
    log "--- INJECTION 1: Kill random agent ---"
    local agent_pid
    agent_pid=$(ls .orchestra/pids/*.pid 2>/dev/null | head -1)
    if [ -z "$agent_pid" ]; then
        log "  No agent PIDs found, skipping injection"
        return
    fi
    local pid
    pid=$(cat "$agent_pid" 2>/dev/null)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        log "  Killing agent PID $pid (file: $agent_pid)"
        kill -9 "$pid" 2>/dev/null || true
        INJECT_COUNT=$((INJECT_COUNT + 1))
        # Update report
        python3 -c "
import json, sys
with open('$REPORT_FILE') as f: r = json.load(f)
r['injections'].append({'type':'kill_agent','pid':$pid,'time':'$(date -u +%Y-%m-%dT%H:%M:%SZ)'})
with open('$REPORT_FILE','w') as f: json.dump(r, f, indent=2)
" 2>/dev/null || true
    else
        log "  Agent PID $pid not running, skipping"
    fi
}

inject_corrupt_worktree() {
    log "--- INJECTION 2: Corrupt worktree ---"
    local wt
    wt=$(ls -d .worktree/t-* 2>/dev/null | head -1)
    if [ -z "$wt" ]; then
        log "  No active worktrees found, skipping injection"
        return
    fi
    if [ -f "$wt/.git" ]; then
        log "  Corrupting worktree: $wt (removing .git pointer)"
        mv "$wt/.git" "$wt/.git.corrupted"
        INJECT_COUNT=$((INJECT_COUNT + 1))
        python3 -c "
import json
with open('$REPORT_FILE') as f: r = json.load(f)
r['injections'].append({'type':'corrupt_worktree','path':'$wt','time':'$(date -u +%Y-%m-%dT%H:%M:%SZ)'})
with open('$REPORT_FILE','w') as f: json.dump(r, f, indent=2)
" 2>/dev/null || true
    else
        log "  Worktree $wt has no .git pointer, skipping"
    fi
}

inject_stall_simulation() {
    log "--- INJECTION 3: Simulate stalled agent ---"
    local agent_pid
    agent_pid=$(ls .orchestra/pids/*.pid 2>/dev/null | tail -1)
    if [ -z "$agent_pid" ]; then
        log "  No agent PIDs found, skipping injection"
        return
    fi
    local pid
    pid=$(cat "$agent_pid" 2>/dev/null)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        log "  Stopping agent PID $pid with SIGSTOP (simulating stall)"
        kill -STOP "$pid" 2>/dev/null || true
        INJECT_COUNT=$((INJECT_COUNT + 1))
        python3 -c "
import json
with open('$REPORT_FILE') as f: r = json.load(f)
r['injections'].append({'type':'stall_simulation','pid':$pid,'time':'$(date -u +%Y-%m-%dT%H:%M:%SZ)'})
with open('$REPORT_FILE','w') as f: json.dump(r, f, indent=2)
" 2>/dev/null || true
    else
        log "  Agent PID $pid not running, skipping"
    fi
}

# Background injection scheduler
(
    sleep "$INJECT_1_SEC"
    inject_kill_agent

    sleep $(( INJECT_2_SEC - INJECT_1_SEC ))
    inject_corrupt_worktree

    sleep $(( INJECT_3_SEC - INJECT_2_SEC ))
    inject_stall_simulation
) &
INJECTOR_PID=$!

# Monitor loop — check conductor health every 60s
ELAPSED=0
CHECK_INTERVAL=60
while [ "$ELAPSED" -lt "$DURATION_SEC" ]; do
    sleep "$CHECK_INTERVAL"
    ELAPSED=$((ELAPSED + CHECK_INTERVAL))

    if ! kill -0 "$CONDUCTOR_PID" 2>/dev/null; then
        log "Conductor exited after ${ELAPSED}s"
        break
    fi

    # Periodic status check (direct SQLite query — avoids orchestra CLI issues)
    if (( ELAPSED % 300 == 0 )); then
        local_status=$(sqlite3 .orchestra/orchestrator.db \
            "SELECT 'done=' || COUNT(CASE WHEN status='done' THEN 1 END) || ' failed=' || COUNT(CASE WHEN status='failed' THEN 1 END) || ' in_progress=' || COUNT(CASE WHEN status='in_progress' THEN 1 END) || ' pending=' || COUNT(CASE WHEN status='pending' THEN 1 END) FROM tasks" 2>/dev/null || echo "DB unavailable")
        log "Status at ${ELAPSED}s: $local_status"
    fi
done

# Wait for conductor to finish (give it 5 extra minutes after duration)
log "Duration reached. Waiting up to 5 minutes for conductor to finish..."
EXTRA_WAIT=0
while kill -0 "$CONDUCTOR_PID" 2>/dev/null && [ "$EXTRA_WAIT" -lt 300 ]; do
    sleep 10
    EXTRA_WAIT=$((EXTRA_WAIT + 10))
done

# Kill conductor if still running
if kill -0 "$CONDUCTOR_PID" 2>/dev/null; then
    log "Conductor still running after grace period, sending SIGTERM"
    kill "$CONDUCTOR_PID" 2>/dev/null || true
    sleep 5
    kill -9 "$CONDUCTOR_PID" 2>/dev/null || true
fi

# Kill injector if still running
kill "$INJECTOR_PID" 2>/dev/null || true

# Collect results
log ""
log "=== Stress Test Complete ==="

FINAL_STATUS=$(bin/orchestra status --json 2>/dev/null || echo '{}')
DONE_COUNT=$(echo "$FINAL_STATUS" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('tasks',{}).get('done',0))" 2>/dev/null || echo 0)
FAILED_COUNT=$(echo "$FINAL_STATUS" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('tasks',{}).get('failed',0))" 2>/dev/null || echo 0)
TOTAL_COUNT=$(echo "$FINAL_STATUS" | python3 -c "import json,sys; d=json.load(sys.stdin); t=d.get('tasks',{}); print(sum(t.get(k,0) for k in t))" 2>/dev/null || echo 0)

log "Tasks: $DONE_COUNT done, $FAILED_COUNT failed, $TOTAL_COUNT total"
log "Injections attempted: $INJECT_COUNT"

# Check for recovery evidence
RECOVERY_COUNT=0
if sqlite3 .orchestra/orchestrator.db "SELECT COUNT(*) FROM events WHERE event_type='agent_respawned'" 2>/dev/null | grep -q '[1-9]'; then
    RECOVERY_COUNT=$((RECOVERY_COUNT + 1))
    log "Recovery evidence: agent respawns detected"
fi
if sqlite3 .orchestra/orchestrator.db "SELECT COUNT(*) FROM events WHERE event_type LIKE '%heal%'" 2>/dev/null | grep -q '[1-9]'; then
    RECOVERY_COUNT=$((RECOVERY_COUNT + 1))
    log "Recovery evidence: healing events detected"
fi
if sqlite3 .orchestra/orchestrator.db "SELECT COUNT(*) FROM events WHERE event_type LIKE '%stall%'" 2>/dev/null | grep -q '[1-9]'; then
    RECOVERY_COUNT=$((RECOVERY_COUNT + 1))
    log "Recovery evidence: stall detection events detected"
fi

# Determine pass/fail
PASSED=false
if [ "$DONE_COUNT" -gt 0 ] && [ "$INJECT_COUNT" -gt 0 ]; then
    PASSED=true
fi

# Finalize report
python3 -c "
import json
with open('$REPORT_FILE') as f: r = json.load(f)
r['completed_at'] = '$(date -u +%Y-%m-%dT%H:%M:%SZ)'
r['tasks_done'] = $DONE_COUNT
r['tasks_failed'] = $FAILED_COUNT
r['tasks_total'] = $TOTAL_COUNT
r['injections_count'] = $INJECT_COUNT
r['recovery_evidence'] = $RECOVERY_COUNT
r['passed'] = $PASSED
r['status'] = 'passed' if $PASSED else 'failed'
with open('$REPORT_FILE', 'w') as f: json.dump(r, f, indent=2)
" 2>/dev/null || true

log ""
if [ "$PASSED" = true ]; then
    log "RESULT: PASSED — $DONE_COUNT tasks completed despite $INJECT_COUNT injected failures"
else
    log "RESULT: FAILED — $DONE_COUNT tasks done, $INJECT_COUNT injections, check logs"
fi
log "Report: $REPORT_FILE"
log "Conductor log: $REPORT_DIR/conductor.log"
log "Orchestra logs: .orchestra/logs/"
