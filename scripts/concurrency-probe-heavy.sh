#!/usr/bin/env bash
# concurrency-probe-heavy.sh — Measure claude -p concurrency degradation under sustained load
#
# Unlike the trivial probe (doc 181) which tested session count with ~10-token tasks,
# this probe tests whether sustained token consumption from multiple simultaneous
# heavy sessions triggers rate limiting, degrades quality, or increases latency.
#
# Each session receives the full source of decompose.go (1445 lines, 48KB) and must
# perform detailed analysis. Expected: ~12K input tokens, ~10-20K output tokens,
# 30-60 seconds per session.
#
# macOS compatible. Matches orchestra's spawner.go invocation pattern.
#
# Usage: ./scripts/concurrency-probe-heavy.sh                   # default levels: 1,2,4,8
#        ./scripts/concurrency-probe-heavy.sh "1,2,4,8"         # custom levels
#        PAUSE=120 ./scripts/concurrency-probe-heavy.sh         # longer inter-level pause
#        TIMEOUT=600 ./scripts/concurrency-probe-heavy.sh       # longer per-session timeout
#        ANALYSIS_FILE=path/to/file.go ./scripts/concurrency-probe-heavy.sh  # custom analysis target
set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────

LEVELS="${1:-1,2,4,8}"
OUTDIR="/tmp/concurrency-probe-heavy-$(date +%Y%m%d-%H%M%S)"
PAUSE="${PAUSE:-90}"
TIMEOUT="${TIMEOUT:-300}"
REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
ANALYSIS_FILE="${ANALYSIS_FILE:-$REPO_ROOT/internal/orchestrator/decompose.go}"

mkdir -p "$OUTDIR"

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     HEAVY CONCURRENCY PROBE — Sustained Load Testing        ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Levels:        $LEVELS"
echo "║  Pause:         ${PAUSE}s between levels"
echo "║  Timeout:       ${TIMEOUT}s per session"
echo "║  Analysis file: $ANALYSIS_FILE"
echo "║  Output:        $OUTDIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Preflight ────────────────────────────────────────────────────────

for cmd in python3 claude; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd required" >&2; exit 1
    fi
done

if [ ! -f "$ANALYSIS_FILE" ]; then
    echo "ERROR: Analysis file not found: $ANALYSIS_FILE" >&2; exit 1
fi

ANALYSIS_CONTENT=$(cat "$ANALYSIS_FILE")
ANALYSIS_SIZE=${#ANALYSIS_CONTENT}
ANALYSIS_LINES=$(wc -l < "$ANALYSIS_FILE" | tr -d ' ')

echo "[preflight] Analysis file: $ANALYSIS_LINES lines, $ANALYSIS_SIZE bytes"

if [ "$ANALYSIS_SIZE" -gt 900000 ]; then
    echo "ERROR: Analysis file too large ($ANALYSIS_SIZE bytes > 900KB limit)" >&2
    exit 1
fi

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
        rm -rf "$HOOKS_DIR" 2>/dev/null || true
        mv "$HOOKS_BACKUP" "$HOOKS_DIR"
        echo "[hooks] Restored $HOOKS_DIR"
    fi
}

trap restore_hooks EXIT INT TERM
disable_hooks

# ── CSV header ───────────────────────────────────────────────────────

CSV="$OUTDIR/results.csv"
echo "level,session,spawn_ts,first_output_ts,complete_ts,exit_code,success,failure_type,startup_latency_ms,total_latency_ms,duration_ms,duration_api_ms,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,utilization,rate_limit_type,rate_limit_status" > "$CSV"

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
    elif echo "$content" | grep -qiE 'context.?length|context_length|too.?long'; then
        echo "context_exhausted"
    else
        echo "unknown"
    fi
}

check_success() {
    local jsonl_file="$1"
    local session_num="$2"
    local level_num="$3"
    [ -s "$jsonl_file" ] && grep -q "HEAVY_ANALYSIS_OK_${session_num}_LEVEL_${level_num}" "$jsonl_file" 2>/dev/null && echo "true" || echo "false"
}

# Extract result event fields from jsonl (duration_ms, duration_api_ms)
extract_result_fields() {
    local jsonl_file="$1"
    python3 -c "
import json
found = False
with open('$jsonl_file', 'r') as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue
        if ev.get('type') == 'result':
            dm = ev.get('duration_ms', 0)
            dam = ev.get('duration_api_ms', 0)
            print(f'{dm},{dam}')
            found = True
            break
if not found:
    print('0,0')
" 2>/dev/null || echo "0,0"
}

# Extract token usage from jsonl (from result event's usage field)
extract_tokens() {
    local jsonl_file="$1"
    python3 -c "
import json
input_t = 0
output_t = 0
cache_read = 0
cache_create = 0
with open('$jsonl_file', 'r') as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue
        if ev.get('type') == 'result':
            usage = ev.get('usage', {})
            input_t = usage.get('input_tokens', 0)
            output_t = usage.get('output_tokens', 0)
            cache_read = usage.get('cache_read_input_tokens', 0)
            cache_create = usage.get('cache_creation_input_tokens', 0)
            break
print(f'{input_t},{output_t},{cache_read},{cache_create}')
" 2>/dev/null || echo "0,0,0,0"
}

# Extract utilization from rate_limit_event in jsonl
# Format: rate_limit_info.{utilization, rateLimitType, status}
extract_utilization() {
    local jsonl_file="$1"
    python3 -c "
import json
util = ''
rl_type = ''
rl_status = ''
with open('$jsonl_file', 'r') as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue
        if ev.get('type') == 'rate_limit_event':
            info = ev.get('rate_limit_info', {})
            util = str(info.get('utilization', ''))
            rl_type = str(info.get('rateLimitType', ''))
            rl_status = str(info.get('status', ''))
print(f'{util},{rl_type},{rl_status}')
" 2>/dev/null || echo ",,"
}

build_heavy_prompt() {
    local session_num="$1"
    local level_num="$2"

    cat <<PROMPT_EOF
You are a code analysis expert. Analyze the following Go source file in detail.

=== FILE: decompose.go ($ANALYSIS_LINES lines) ===
$ANALYSIS_CONTENT
=== END FILE ===

Perform this analysis:

1. **Types**: List every exported type (struct, interface, type alias) with a one-line description.
2. **Functions**: List every exported function with signature and a one-line purpose description.
3. **Control flow**: Describe the main execution flow of the Decompose function, including error handling paths.
4. **Error patterns**: Identify all error handling patterns used (sentinel errors, wrapping, custom types).
5. **Summary**: Write a 3-sentence summary of what this file does and its role in the system.

After your analysis, on the very last line of your response, output exactly this marker:
HEAVY_ANALYSIS_OK_${session_num}_LEVEL_${level_num}
PROMPT_EOF
}

# ── Preflight utilization capture ────────────────────────────────────

echo "[preflight] Capturing baseline utilization..."
PREFLIGHT_DIR="$OUTDIR/preflight"
mkdir -p "$PREFLIGHT_DIR"

(cd /tmp && claude -p "Reply with exactly: UTIL_CHECK" \
    --output-format stream-json \
    --verbose \
    --max-turns 1 \
    --permission-mode bypassPermissions \
    > "$PREFLIGHT_DIR/util-check.jsonl" \
    2> "$PREFLIGHT_DIR/util-check.stderr") || true

BASELINE_UTIL=$(extract_utilization "$PREFLIGHT_DIR/util-check.jsonl")
echo "[preflight] Baseline utilization: $BASELINE_UTIL"
echo ""

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
    echo "  Level $level — Spawning $level heavy sessions ($LEVEL_IDX/$LEVEL_COUNT)"
    echo "  Expected: ~${ANALYSIS_LINES} lines analysis, 30-60s each"
    echo "══════════════════════════════════════════════════════════════"
    echo ""

    PIDS=()
    WATCHDOG_PIDS=()
    SPAWN_TS_ARR=()

    # Spawn all sessions simultaneously
    for i in $(seq 1 "$level"); do
        spawn_ts=$(ms_now)
        SPAWN_TS_ARR+=("$spawn_ts")

        jsonl="$level_dir/session-${i}.jsonl"
        stderr="$level_dir/session-${i}.stderr"

        prompt=$(build_heavy_prompt "$i" "$level")

        # Run from /tmp to avoid project hooks
        (cd /tmp && claude -p "$prompt" \
            --output-format stream-json \
            --verbose \
            --max-turns 1 \
            --permission-mode bypassPermissions \
            > "$jsonl" \
            2> "$stderr") &
        session_pid=$!
        PIDS+=($session_pid)

        # Watchdog: kill session if it exceeds timeout
        (sleep "$TIMEOUT" && kill "$session_pid" 2>/dev/null) &
        WATCHDOG_PIDS+=($!)

        echo "[$(date +%H:%M:%S)] Spawned session $i (PID=$session_pid, ts=$spawn_ts)"
    done

    echo ""
    echo "[$(date +%H:%M:%S)] Waiting for $level heavy sessions (timeout=${TIMEOUT}s)..."

    # Wait for all — capture exit codes
    EXIT_CODES=()
    for pid in "${PIDS[@]}"; do
        set +e
        wait "$pid" 2>/dev/null
        EXIT_CODES+=("$?")
        set -e
    done

    # Kill any remaining watchdogs
    for wpid in "${WATCHDOG_PIDS[@]}"; do
        kill "$wpid" 2>/dev/null || true
        wait "$wpid" 2>/dev/null || true
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

        # Timestamps from file mtimes
        first_output_ts="0"
        complete_ts=$(ms_now)
        if [ -s "$jsonl" ]; then
            first_output_ts=$(file_mtime_ms "$jsonl")
        fi

        success=$(check_success "$jsonl" "$i" "$level")
        failure_type=$(classify_failure "$stderr" "$exit_code")

        # Latency
        startup_latency_ms=0
        total_latency_ms=0
        if [ "$first_output_ts" != "0" ]; then
            startup_latency_ms=$((first_output_ts - spawn_ts))
            [ "$startup_latency_ms" -lt 0 ] && startup_latency_ms=0
        fi
        total_latency_ms=$((complete_ts - spawn_ts))

        # Extract extended metrics
        result_fields=$(extract_result_fields "$jsonl")
        duration_ms=$(echo "$result_fields" | cut -d',' -f1)
        duration_api_ms=$(echo "$result_fields" | cut -d',' -f2)

        token_fields=$(extract_tokens "$jsonl")
        input_tokens=$(echo "$token_fields" | cut -d',' -f1)
        output_tokens=$(echo "$token_fields" | cut -d',' -f2)
        cache_read_tokens=$(echo "$token_fields" | cut -d',' -f3)
        cache_creation_tokens=$(echo "$token_fields" | cut -d',' -f4)

        util_fields=$(extract_utilization "$jsonl")
        utilization=$(echo "$util_fields" | cut -d',' -f1)
        rate_limit_type=$(echo "$util_fields" | cut -d',' -f2)
        rate_limit_status=$(echo "$util_fields" | cut -d',' -f3)

        [ "$success" = "true" ] && SUCCESS_COUNT=$((SUCCESS_COUNT + 1))

        echo "$level,$i,$spawn_ts,$first_output_ts,$complete_ts,$exit_code,$success,$failure_type,$startup_latency_ms,$total_latency_ms,$duration_ms,$duration_api_ms,$input_tokens,$output_tokens,$cache_read_tokens,$cache_creation_tokens,$utilization,$rate_limit_type,$rate_limit_status" >> "$CSV"

        icon="+"
        [ "$success" = "true" ] || icon="x"
        echo "  [$icon] Session $i: exit=$exit_code success=$success type=$failure_type startup=${startup_latency_ms}ms total=${total_latency_ms}ms duration_api=${duration_api_ms}ms in=${input_tokens} out=${output_tokens} cache_read=${cache_read_tokens} util=${utilization}"
    done

    echo ""
    echo "  Level $level: ${SUCCESS_COUNT}/${level} success ($((SUCCESS_COUNT * 100 / level))%)"

    if [ "$SUCCESS_COUNT" -eq 0 ] && [ "$level" -gt 1 ]; then
        echo "  !! WARNING: 0% success at level $level"
    fi

    # Post-level utilization capture
    echo ""
    echo "[$(date +%H:%M:%S)] Capturing post-level utilization..."
    post_util_jsonl="$level_dir/post-util-check.jsonl"
    post_util_stderr="$level_dir/post-util-check.stderr"
    (cd /tmp && claude -p "Reply with exactly: UTIL_POST_LEVEL_${level}" \
        --output-format stream-json \
        --verbose \
        --max-turns 1 \
        --permission-mode bypassPermissions \
        > "$post_util_jsonl" \
        2> "$post_util_stderr") || true

    POST_UTIL=$(extract_utilization "$post_util_jsonl")
    echo "[$(date +%H:%M:%S)] Post-level $level utilization: $POST_UTIL"

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
echo "║            HEAVY CONCURRENCY PROBE COMPLETE                  ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Results CSV:  $CSV"
echo "║  Session logs: $OUTDIR/"
echo "║  Baseline:     $BASELINE_UTIL"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "=== Raw CSV ==="
cat "$CSV"
echo ""
echo "Next: ./scripts/analyze-concurrency-heavy.sh $CSV"
