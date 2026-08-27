#!/bin/bash
# run-external-benchmark.sh — Run external benchmark tasks through Orchestra
#
# Adapts external benchmark tasks (SWE-bench, FeatureBench, etc.) to Orchestra's
# goal format, runs them with orchestra go, and collects results.
#
# ISOLATION: Each task uses a fresh clone. The main repo and benchmark repos
# are NEVER modified. Binary is built once from the main repo and reused.
#
# Usage:
#   ./scripts/run-external-benchmark.sh --benchmark swe-bench --tasks tasks.jsonl --repos /tmp/bench-repos
#   ./scripts/run-external-benchmark.sh --benchmark featurebench --tasks tasks.jsonl --repos /tmp/bench-repos
#
# Options:
#   --benchmark     Benchmark name (swe-bench, featurebench) — selects adapter
#   --tasks         Path to JSONL file with task specs (one JSON object per line)
#   --repos         Directory containing pre-cloned benchmark repos
#   --max-tasks     Max Orchestra decomposition tasks (default: 4)
#   --cooldown      Seconds between runs (default: 120)
#   --start-task    Skip to this task number (for resuming failed runs)
#   --limit         Max number of tasks to run (default: unlimited)
#
# Task JSONL format (each line):
#   {"id": "task-123", "repo": "owner/name", "base_commit": "abc123",
#    "description": "...", "test_cmd": "pytest tests/", "files_changed": [...]}
#
# Output:
#   /tmp/external-bench-<timestamp>/results.csv  — per-task results
#   /tmp/external-bench-<timestamp>/<task-id>/    — logs, goal, verify output
set -uo pipefail

# ── Parse arguments ──────────────────────────────────────────────────

BENCHMARK=""
TASKS_FILE=""
REPOS_DIR=""
MAX_TASKS="${MAX_TASKS:-8}"
MAX_FILES_PER_TASK="${MAX_FILES_PER_TASK:-15}"
COOLDOWN="${COOLDOWN:-120}"
START_TASK="${START_TASK:-1}"
LIMIT="${LIMIT:-0}"
RUNTIME="${RUNTIME:-}"
TEST_CMD_TIMEOUT="${TEST_CMD_TIMEOUT:-600}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --benchmark) BENCHMARK="$2"; shift 2 ;;
        --tasks) TASKS_FILE="$2"; shift 2 ;;
        --repos) REPOS_DIR="$2"; shift 2 ;;
        --max-tasks) MAX_TASKS="$2"; shift 2 ;;
        --max-files-per-task) MAX_FILES_PER_TASK="$2"; shift 2 ;;
        --cooldown) COOLDOWN="$2"; shift 2 ;;
        --start-task) START_TASK="$2"; shift 2 ;;
        --limit) LIMIT="$2"; shift 2 ;;
        --runtime) RUNTIME="$2"; shift 2 ;;
        --test-cmd-timeout) TEST_CMD_TIMEOUT="$2"; shift 2 ;;
        *) echo "ERROR: Unknown argument: $1" >&2; exit 1 ;;
    esac
done

# ── Validate inputs ──────────────────────────────────────────────────

if [ -z "$BENCHMARK" ] || [ -z "$TASKS_FILE" ] || [ -z "$REPOS_DIR" ]; then
    echo "ERROR: --benchmark, --tasks, and --repos are required" >&2
    echo "Usage: $0 --benchmark swe-bench --tasks tasks.jsonl --repos /tmp/bench-repos" >&2
    exit 1
fi

if [ ! -f "$TASKS_FILE" ]; then
    echo "ERROR: Tasks file not found: $TASKS_FILE" >&2
    exit 1
fi

if [ ! -d "$REPOS_DIR" ]; then
    echo "ERROR: Repos directory not found: $REPOS_DIR" >&2
    exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ADAPTER="$REPO_ROOT/scripts/adapters/${BENCHMARK}-adapter.sh"

if [ ! -f "$ADAPTER" ]; then
    echo "ERROR: No adapter found for benchmark '$BENCHMARK'" >&2
    echo "Expected: $ADAPTER" >&2
    echo "Available adapters:" >&2
    ls "$REPO_ROOT/scripts/adapters/" 2>/dev/null || echo "  (none)"
    exit 1
fi

if ! command -v jq &>/dev/null; then
    echo "ERROR: jq required for JSON parsing" >&2
    exit 1
fi

if ! command -v sqlite3 &>/dev/null; then
    echo "ERROR: sqlite3 required for metrics extraction" >&2
    exit 1
fi

# ── Setup ────────────────────────────────────────────────────────────

CURRENT_HEAD_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RESULTS_DIR="/tmp/external-bench-${TIMESTAMP}"
RESULTS_FILE="$RESULTS_DIR/results.csv"
CLONE_BASE="/tmp/external-bench-clone"
BIN_PATH="/tmp/orchestra-bench-${CURRENT_HEAD_SHORT}"

mkdir -p "$RESULTS_DIR"

TOTAL_TASKS=$(wc -l < "$TASKS_FILE" | tr -d ' ')

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║        EXTERNAL BENCHMARK — $BENCHMARK"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Binary:      $CURRENT_HEAD_SHORT"
echo "║  Adapter:     $(basename "$ADAPTER")"
echo "║  Tasks file:  $TASKS_FILE"
echo "║  Total tasks: $TOTAL_TASKS"
echo "║  Start task:  $START_TASK"
echo "║  Max tasks:   $MAX_TASKS"
echo "║  Max files/task: $MAX_FILES_PER_TASK"
echo "║  Test timeout: ${TEST_CMD_TIMEOUT}s"
echo "║  Cooldown:    ${COOLDOWN}s"
echo "║  Results:     $RESULTS_DIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Phase 1: Build binary ────────────────────────────────────────────

if [ -f "$BIN_PATH" ]; then
    echo "[$(date +%H:%M:%S)] Reusing existing binary: $BIN_PATH"
else
    echo "[$(date +%H:%M:%S)] Building binary from $CURRENT_HEAD_SHORT..."
    cd "$REPO_ROOT"
    make build 2>&1 | tail -3
    cp bin/orchestra "$BIN_PATH"
    echo "[$(date +%H:%M:%S)] Binary ready: $BIN_PATH"
fi
echo ""

# ── Phase 2: Pre-pull Docker images ──────────────────────────────────
# Pull unique Docker images referenced in task specs. Failed pulls are
# non-fatal — tasks fall back to host or uv+venv execution.

if command -v docker &>/dev/null && command -v jq &>/dev/null; then
    IMAGES=$(jq -r '.image // empty' "$TASKS_FILE" 2>/dev/null | sort -u)
    if [ -n "$IMAGES" ]; then
        IMAGE_COUNT=$(echo "$IMAGES" | wc -l | tr -d ' ')
        echo "[$(date +%H:%M:%S)] Pre-pulling $IMAGE_COUNT Docker images..."
        while IFS= read -r img; do
            [ -z "$img" ] && continue
            if docker image inspect "$img" &>/dev/null; then
                echo "  CACHED: $img"
            elif docker pull --platform linux/amd64 "$img" 2>/dev/null; then
                echo "  PULLED: $img"
            else
                echo "  WARN: Failed to pull $img (will fall back to host/venv)"
            fi
        done <<< "$IMAGES"
        echo ""
    fi
else
    echo "[$(date +%H:%M:%S)] Docker not available — tests will use host/venv execution"
    echo ""
fi

# ── Phase 3: Initialize CSV ──────────────────────────────────────────

echo "benchmark,task_id,repo,files_target,files_correct,build_pass,test_pass,merge_conflicts,wall_clock_s,task_count,done_count,failed_count,verdict" > "$RESULTS_FILE"

# ── Phase 4: Run tasks ───────────────────────────────────────────────

# B-271: Suppress post-bash-check.sh hook triggers during benchmark runs.
# Benchmark test output contains expected failures ("FAILED", "exit status 1")
# that would cause false positives on Trigger 1/2/3.
export ORCHESTRA_BENCHMARK=1

PASS_COUNT=0
FAIL_COUNT=0
TASK_NUM=0

while IFS= read -r task_json; do
    TASK_NUM=$((TASK_NUM + 1))

    # Skip tasks before START_TASK
    if [ "$TASK_NUM" -lt "$START_TASK" ]; then
        continue
    fi

    # Stop if LIMIT reached
    if [ "$LIMIT" -gt 0 ] && [ $((TASK_NUM - START_TASK + 1)) -gt "$LIMIT" ]; then
        echo "[$(date +%H:%M:%S)] Limit of $LIMIT tasks reached. Stopping."
        break
    fi

    # Parse task fields
    task_id=$(echo "$task_json" | jq -r '.id')
    task_repo=$(echo "$task_json" | jq -r '.repo')
    base_commit=$(echo "$task_json" | jq -r '.base_commit')
    test_cmd=$(echo "$task_json" | jq -r '.test_cmd // "true"')
    files_count=$(echo "$task_json" | jq -r '.files_changed | length')

    echo ""
    echo "────────────────────────────────────────────────────────"
    echo "  Task $TASK_NUM / $TOTAL_TASKS: $task_id"
    echo "  Repo: $task_repo | Files: $files_count | Tests: $test_cmd"
    echo "────────────────────────────────────────────────────────"

    task_dir="$RESULTS_DIR/$task_id"
    mkdir -p "$task_dir"

    # Save raw task spec
    echo "$task_json" > "$task_dir/task.json"

    # ── Call adapter to generate goal ──
    echo "[$(date +%H:%M:%S)] Generating Orchestra goal via adapter..."
    goal_text=$(bash "$ADAPTER" generate-goal "$task_dir/task.json" 2>"$task_dir/adapter-stderr.log")
    adapter_exit=$?

    if [ $adapter_exit -ne 0 ] || [ -z "$goal_text" ]; then
        echo "[$(date +%H:%M:%S)] SKIP: Adapter failed to generate goal (exit=$adapter_exit)"
        echo "$BENCHMARK,$task_id,$task_repo,$files_count,0,false,false,0,0,0,0,0,SKIP" >> "$RESULTS_FILE"
        continue
    fi
    echo "$goal_text" > "$task_dir/goal.txt"

    # ── Get additional orchestra flags from adapter (B-240) ──
    # Adapters can output flags like --test-failure-mode, --disable-action-expansion,
    # --read-only-files to enable environment-aware features instead of bypassing them.
    extra_flags=$(bash "$ADAPTER" orchestra-flags "$task_dir/task.json" 2>/dev/null || echo "")
    if [ -n "$extra_flags" ]; then
        echo "[$(date +%H:%M:%S)] Adapter flags: $extra_flags"
    fi

    # ── Prepare repo clone ──
    repo_source="$REPOS_DIR/$task_repo"
    if [ ! -d "$repo_source" ]; then
        # Try without owner prefix
        repo_name=$(basename "$task_repo")
        repo_source="$REPOS_DIR/$repo_name"
    fi

    if [ ! -d "$repo_source" ]; then
        echo "[$(date +%H:%M:%S)] SKIP: Repo not found: $REPOS_DIR/$task_repo"
        echo "$BENCHMARK,$task_id,$task_repo,$files_count,0,false,false,0,0,0,0,0,SKIP" >> "$RESULTS_FILE"
        continue
    fi

    clone_dir="${CLONE_BASE}/${task_id}"
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Cloning $task_repo to $clone_dir..."
    git clone --quiet "$repo_source" "$clone_dir" 2>&1
    cd "$clone_dir"
    # Create a local branch at the base commit instead of detached HEAD.
    # detectBaseBranch needs a local branch to find the merge-base;
    # detached HEAD clones only have remotes/origin/* refs.
    git checkout -b dev "$base_commit" 2>/dev/null || git reset --hard "$base_commit"
    echo "[$(date +%H:%M:%S)] Clone ready at $(git rev-parse --short HEAD) (branch: $(git branch --show-current 2>/dev/null || echo 'detached'))"

    # ── Apply test patch before orchestra (if adapter supports it) ──
    # SWE-EVO tasks include a test_patch that adds fail-to-pass tests.
    # Apply BEFORE orchestra so agents can implement against real tests.
    patch_result=$(bash "$ADAPTER" apply-test-patch "$task_dir/task.json" "$clone_dir" 2>&1 || true)
    echo "$patch_result"

    # ── Tier 1 fallback: uv+venv for simple repos without Docker image ──
    # If no Docker image is available and uv is on PATH, try creating a venv
    # with editable install. Works for simple repos (requests, pydantic, conan).
    task_image=$(echo "$task_json" | jq -r '.image // ""')
    if [ -z "$task_image" ] || [ "$task_image" = "null" ]; then
        if command -v uv &>/dev/null && [ -f "$clone_dir/pyproject.toml" ] || [ -f "$clone_dir/setup.py" ] || [ -f "$clone_dir/setup.cfg" ]; then
            echo "[$(date +%H:%M:%S)] No Docker image — trying uv+venv (Tier 1)..."
            if (cd "$clone_dir" && uv venv .venv 2>/dev/null && uv pip install -e ".[dev,test]" 2>/dev/null); then
                echo "[$(date +%H:%M:%S)] venv ready: $clone_dir/.venv"
                export PATH="$clone_dir/.venv/bin:$PATH"
            else
                echo "[$(date +%H:%M:%S)] venv setup failed (expected for complex repos)"
            fi
        fi
    fi

    # ── Get merge-time test command from adapter ──
    # Called after clone_dir exists so adapter can Docker-wrap with correct volume path.
    merge_test_cmd=$(bash "$ADAPTER" merge-test-cmd "$task_dir/task.json" "$clone_dir" 2>/dev/null || echo "$test_cmd")
    echo "[$(date +%H:%M:%S)] Merge-time test cmd: $merge_test_cmd"

    # Initialize orchestra
    rm -rf "$clone_dir/.orchestra" "$clone_dir/.worktree"
    "$BIN_PATH" init --clean 2>&1 | tail -1
    echo "[$(date +%H:%M:%S)] Orchestra initialized"

    db="$clone_dir/.orchestra/orchestrator.db"

    # ── Run orchestra ──
    # NOTE: orchestra go --foreground blocks through merge completion.
    # External monitoring (wrapper scripts, dashboards) should NOT check PIDs —
    # wait for this process to exit, or query the DB:
    #   sqlite3 $db "SELECT value FROM blackboard WHERE key='conductor:merge_complete'"
    # A non-empty result means the run finished (including merge). Checking PIDs
    # leads to false "all done" at ~60s when the Go process forks subagents.
    echo "[$(date +%H:%M:%S)] Starting orchestra go (max-tasks=$MAX_TASKS)..."
    start_ts=$(date +%s)

    # shellcheck disable=SC2086
    runtime_flag=""
    if [ -n "$RUNTIME" ]; then
        runtime_flag="--runtime $RUNTIME"
    fi
    "$BIN_PATH" go --goal "$goal_text" --max-tasks "$MAX_TASKS" --max-files-per-task "$MAX_FILES_PER_TASK" --test-cmd "$merge_test_cmd" --test-cmd-timeout "$TEST_CMD_TIMEOUT" $extra_flags $runtime_flag --foreground 2>&1 | tee "$task_dir/stdout.log"
    orch_exit=$?

    end_ts=$(date +%s)
    wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s (exit: $orch_exit)"

    # ── Log git state for debugging ──
    echo "[$(date +%H:%M:%S)] Git state after orchestra:"
    cd "$clone_dir"
    echo "  HEAD: $(git rev-parse --short HEAD)"
    echo "  Commits since base: $(git rev-list --count "$base_commit"..HEAD 2>/dev/null || echo '?')"
    git diff --stat "$base_commit"..HEAD 2>/dev/null | tail -5
    echo "  Modified files:"
    git diff --name-only "$base_commit"..HEAD 2>/dev/null | head -20
    cd "$REPO_ROOT"

    # ── Verify with adapter ──
    echo "[$(date +%H:%M:%S)] Running verification..."
    verify_output=$(bash "$ADAPTER" verify "$task_dir/task.json" "$clone_dir" 2>&1)
    echo "$verify_output" > "$task_dir/verify.log"
    echo "$verify_output"

    # Parse verification results
    files_correct=0
    build_pass="false"
    test_pass="false"
    verdict="FAIL"

    if echo "$verify_output" | grep -q 'FILES_CORRECT:'; then
        files_correct=$(echo "$verify_output" | grep 'FILES_CORRECT:' | awk '{print $2}')
    fi
    if echo "$verify_output" | grep -q 'BUILD: PASS'; then
        build_pass="true"
    fi
    if echo "$verify_output" | grep -q 'TESTS: PASS'; then
        test_pass="true"
    fi
    if echo "$verify_output" | grep -q 'VERDICT: PASS'; then
        verdict="PASS"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi

    # DB metrics
    merge_conflicts=0
    task_count=0
    done_count=0
    failed_count=0

    if [ -f "$db" ]; then
        merge_conflicts=$(sqlite3 "$db" "SELECT COUNT(*) FROM events WHERE event_type = 'merge_conflict';" 2>/dev/null || echo 0)
        task_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks;" 2>/dev/null || echo 0)
        done_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks WHERE status = 'done';" 2>/dev/null || echo 0)
        failed_count=$(sqlite3 "$db" "SELECT COUNT(*) FROM tasks WHERE status = 'failed';" 2>/dev/null || echo 0)
    fi

    echo ""
    echo "--- Task $task_id Results ---"
    echo "Files: $files_correct/$files_count | Build: $build_pass | Tests: $test_pass"
    echo "Tasks: $task_count (done=$done_count, failed=$failed_count) | Conflicts: $merge_conflicts"
    echo "Wall clock: ${wall_clock}s | Verdict: $verdict"

    # Append CSV
    echo "$BENCHMARK,$task_id,$task_repo,$files_count,$files_correct,$build_pass,$test_pass,$merge_conflicts,$wall_clock,$task_count,$done_count,$failed_count,$verdict" >> "$RESULTS_FILE"

    # Preserve logs
    if [ -d "$clone_dir/.orchestra/logs" ]; then
        cp -r "$clone_dir/.orchestra/logs" "$task_dir/"
        echo "[$(date +%H:%M:%S)] Agent logs saved to $task_dir/logs/"
    fi
    if [ -f "$db" ]; then
        cp "$db" "$task_dir/orchestrator.db"
    fi

    # Cleanup clone
    cd "$REPO_ROOT"
    rm -rf "$clone_dir"
    echo "[$(date +%H:%M:%S)] Clone cleaned up."

    # Cooldown
    if [ "$TASK_NUM" -lt "$TOTAL_TASKS" ]; then
        echo "[$(date +%H:%M:%S)] Cooling down for ${COOLDOWN}s..."
        sleep "$COOLDOWN"
    fi

done < "$TASKS_FILE"

# ── Phase 5: Summary ─────────────────────────────────────────────────

TOTAL_RUN=$((PASS_COUNT + FAIL_COUNT))
if [ "$TOTAL_RUN" -gt 0 ]; then
    PASS_RATE=$((PASS_COUNT * 100 / TOTAL_RUN))
else
    PASS_RATE=0
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║           EXTERNAL BENCHMARK COMPLETE                       ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Benchmark: $BENCHMARK"
echo "║  Results:   $PASS_COUNT/$TOTAL_RUN passed ($PASS_RATE%)"
echo "║  CSV:       $RESULTS_FILE"
echo "║  Logs:      $RESULTS_DIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

echo "=== Full Results CSV ==="
cat "$RESULTS_FILE"
echo ""

if command -v tree &>/dev/null; then
    echo "=== Results Directory ==="
    tree -L 2 "$RESULTS_DIR" 2>/dev/null || ls -R "$RESULTS_DIR"
fi
