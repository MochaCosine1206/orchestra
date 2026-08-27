#!/usr/bin/env bash
# concurrency-probe.sh — Measure claude -p concurrency degradation curve
#
# Spawns increasing numbers of trivial concurrent `claude -p` sessions and
# measures startup latency, success rate, and error types at each level.
#
# macOS compatible. Matches orchestra's spawner.go invocation pattern.
#
# Usage: ./scripts/concurrency-probe.sh                     # default levels: 1,2,4,6,8
#        ./scripts/concurrency-probe.sh "1,2,4,6,8,12,16"   # custom levels
#        PAUSE=60 ./scripts/concurrency-probe.sh             # longer inter-level pause
set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────

LEVELS="${1:-1,2,4,6,8}"
OUTDIR="/tmp/concurrency-probe-$(date +%Y%m%d-%H%M%S)"
PAUSE="${PAUSE:-30}"

mkdir -p "$OUTDIR"

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         CONCURRENCY PROBE — Claude Max Session Limits       ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Levels:      $LEVELS"
echo "║  Pause:       ${PAUSE}s between levels"
echo "║  Output:      $OUTDIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Preflight ────────────────────────────────────────────────────────

for cmd in python3 claude; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd required" >&2; exit 1
    fi
done

# Prevent nesting guard (G77/G83)
unset CLAUDECODE 2>/dev/null || true

# Temporarily disable global hooks (stop-notify.sh does a Telegram long-poll
# that blocks each session for 10-30s, making measurements unreliable)
HOOKS_DIR="$HOME/.claude/hooks"
HOOKS_BACKUP="$HOME/.claude/hooks.probe-backup"
HOOKS_DISABLED=false

disable_hooks() {
    if [ -d "$HOOKS_DIR" ] && [ ! -d "$HOOKS_BACKUP" ]; then
        mv "$HOOKS_DIR" "$HOOKS_BACKUP"
        HOOKS_DISABLED=true
        echo "[hooks] Temporarily moved $HOOKS_DIR → $HOOKS_BACKUP"
    fi
}

restore_hooks() {
    if [ "$HOOKS_DISABLED" = "true" ] && [ -d "$HOOKS_BACKUP" ]; then
        # Remove any hooks dir that might have been recreated
        rm -rf "$HOOKS_DIR" 2>/dev/null || true
        mv "$HOOKS_BACKUP" "$HOOKS_DIR"
        echo "[hooks] Restored $HOOKS_DIR"
    fi
}

trap restore_hooks EXIT INT TERM
disable_hooks

# ── CSV header ───────────────────────────────────────────────────────

CSV="$OUTDIR/results.csv"
echo "level,session,spawn_ts,first_output_ts,complete_ts,exit_code,success,failure_type,startup_latency_ms,total_latency_ms" > "$CSV"

# ── Helpers ──────────────────────────────────────────────────────────

ms_now() {
    python3 -c 'import time; print(int(time.time() * 1000))'
}

file_mtime_ms() {
    python3 -c "import os; print(int(os.path.getmtime('$1') * 1000))" 2>/dev/null || echo "0"
}

classify_failure() {
    local stderr_file="$1"
    local exit_code="$2"

    if [ "$exit_code" = "0" ]; then echo "none"; return; fi
    if [ "$exit_code" = "137" ] || [ "$exit_code" = "143" ]; then echo "timeout"; return; fi

    local content=""
    [ -s "$stderr_file" ] && content=$(cat "$stderr_file")

    if [ -z "$content" ]; then echo "unknown"; return; fi

    if echo "$content" | grep -qiE 'rate.?limit|429|too many requests|rate_limit_error'; then
        echo "rate_limit"
    elif echo "$content" | grep -qiE 'limit will reset|usage.?limit|session.?limit|quota.?exceed'; then
        echo "session_limit"
    else
        echo "unknown"
    fi
}

check_success() {
    local jsonl_file="$1"
    local session_num="$2"
    [ -s "$jsonl_file" ] && grep -q "PING_OK_${session_num}" "$jsonl_file" 2>/dev/null && echo "true" || echo "false"
}

# ── Main loop ────────────────────────────────────────────────────────

IFS=',' read -ra LEVEL_ARR <<< "$LEVELS"
LEVEL_COUNT=${#LEVEL_ARR[@]}
LEVEL_IDX=0

for level in "${LEVEL_ARR[@]}"; do
    LEVEL_IDX=$((LEVEL_IDX + 1))
    level_dir="$OUTDIR/level-${level}"
    mkdir -p "$level_dir"

    echo ""
    echo "══════════════════════════════════════════════════════════════"
    echo "  Level $level — Spawning $level concurrent sessions ($LEVEL_IDX/$LEVEL_COUNT)"
    echo "══════════════════════════════════════════════════════════════"
    echo ""

    PIDS=()
    SPAWN_TS_ARR=()

    # Spawn all sessions simultaneously — matching spawner.go invocation
    for i in $(seq 1 "$level"); do
        spawn_ts=$(ms_now)
        SPAWN_TS_ARR+=("$spawn_ts")

        jsonl="$level_dir/session-${i}.jsonl"
        stderr="$level_dir/session-${i}.stderr"

        # Match orchestra's spawner.go: -p, --output-format stream-json, --verbose,
        # --permission-mode bypassPermissions, --max-turns 1
        # Run from /tmp to avoid project hooks (prevents Telegram spam)
        (cd /tmp && claude -p "Reply with exactly: PING_OK_${i}" \
            --output-format stream-json \
            --verbose \
            --max-turns 1 \
            --permission-mode bypassPermissions \
            > "$jsonl" \
            2> "$stderr") &
        PIDS+=($!)

        echo "[$(date +%H:%M:%S)] Spawned session $i (PID=${PIDS[${#PIDS[@]}-1]}, ts=$spawn_ts)"
    done

    echo ""
    echo "[$(date +%H:%M:%S)] Waiting for $level sessions..."

    # Wait for all — capture exit codes
    EXIT_CODES=()
    for pid in "${PIDS[@]}"; do
        set +e
        wait "$pid" 2>/dev/null
        EXIT_CODES+=("$?")
        set -e
    done

    echo "[$(date +%H:%M:%S)] All $level sessions completed."

    # ── Collect results ──

    SUCCESS_COUNT=0

    for i in $(seq 1 "$level"); do
        idx=$((i - 1))
        jsonl="$level_dir/session-${i}.jsonl"
        stderr="$level_dir/session-${i}.stderr"
        spawn_ts="${SPAWN_TS_ARR[$idx]}"
        exit_code="${EXIT_CODES[$idx]}"

        # Timestamps from file mtimes (best available on macOS)
        first_output_ts="0"
        complete_ts=$(ms_now)
        if [ -s "$jsonl" ]; then
            first_output_ts=$(file_mtime_ms "$jsonl")
        fi

        success=$(check_success "$jsonl" "$i")
        failure_type=$(classify_failure "$stderr" "$exit_code")

        # Latency
        startup_latency_ms=0
        total_latency_ms=0
        if [ "$first_output_ts" != "0" ]; then
            startup_latency_ms=$((first_output_ts - spawn_ts))
            [ "$startup_latency_ms" -lt 0 ] && startup_latency_ms=0
        fi
        total_latency_ms=$((complete_ts - spawn_ts))

        [ "$success" = "true" ] && SUCCESS_COUNT=$((SUCCESS_COUNT + 1))

        echo "$level,$i,$spawn_ts,$first_output_ts,$complete_ts,$exit_code,$success,$failure_type,$startup_latency_ms,$total_latency_ms" >> "$CSV"

        icon="+"
        [ "$success" = "true" ] || icon="x"
        echo "  [$icon] Session $i: exit=$exit_code success=$success type=$failure_type startup=${startup_latency_ms}ms total=${total_latency_ms}ms"
    done

    echo ""
    echo "  Level $level: ${SUCCESS_COUNT}/${level} success ($((SUCCESS_COUNT * 100 / level))%)"

    if [ "$SUCCESS_COUNT" -eq 0 ] && [ "$level" -gt 1 ]; then
        echo "  !! WARNING: 0% success at level $level"
    fi

    # Pause between levels (not after last)
    if [ "$LEVEL_IDX" -lt "$LEVEL_COUNT" ]; then
        echo ""
        echo "[$(date +%H:%M:%S)] Pausing ${PAUSE}s..."
        sleep "$PAUSE"
    fi
done

# ── Summary ──────────────────────────────────────────────────────────

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║              CONCURRENCY PROBE COMPLETE                     ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Results CSV:  $CSV"
echo "║  Session logs: $OUTDIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "=== Raw CSV ==="
cat "$CSV"
echo ""
echo "Next: ./scripts/analyze-concurrency.sh $CSV"
