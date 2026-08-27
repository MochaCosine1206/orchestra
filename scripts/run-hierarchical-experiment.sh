#!/bin/bash
# run-hierarchical-experiment.sh — A/B experiment: flat vs hierarchical decomposition
# Usage: ./scripts/run-hierarchical-experiment.sh [TIER] [RUNS]
#   TIER: number of goal files (default: 16)
#   RUNS: total runs (default: 6, 3 per arm)
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TIER="${1:-16}"
RUNS="${2:-6}"
RESULTS_DIR="/tmp/hierarchical-experiment"
RESULTS_FILE="${RESULTS_DIR}/results-tier${TIER}.csv"
ORCHESTRA="${REPO_ROOT}/bin/orchestra"

mkdir -p "$RESULTS_DIR"

# Build binary once
echo "[$(date +%H:%M:%S)] Building orchestra..."
cd "$REPO_ROOT"
make build 2>&1 | tail -1

# Generate goal files for the given tier
generate_goal() {
    local tier=$1
    local files=()

    # Generate tier-many Go files spread across directories
    local dirs=("internal/alpha" "internal/beta" "internal/gamma" "internal/delta")
    for ((i=1; i<=tier; i++)); do
        local dir_idx=$(( (i - 1) % ${#dirs[@]} ))
        local dir="${dirs[$dir_idx]}"
        files+=("${dir}/file_${i}.go")
    done

    local file_list=""
    for f in "${files[@]}"; do
        file_list+="$f "
    done

    echo "Add a 'Version string' field to all Go structs in: ${file_list}. Each file should define a struct named Model_N (where N is the file number) with at least 3 fields including Version. Include a constructor function NewModel_N() that returns a properly initialized struct."
}

GOAL=$(generate_goal "$TIER")

# Create CSV header
if [ ! -f "$RESULTS_FILE" ]; then
    echo "run,arm,tier,pass,merge_conflicts,auto_resolved,wall_clock_s,task_count,cluster_count,verdict" > "$RESULTS_FILE"
fi

# Generate arm assignments (alternating A/B, randomized)
declare -a ARMS=()
for ((i=0; i<RUNS; i++)); do
    if (( i % 2 == 0 )); then
        ARMS+=("A")
    else
        ARMS+=("B")
    fi
done
# Shuffle arms (Fisher-Yates)
for ((i=${#ARMS[@]}-1; i>0; i--)); do
    j=$((RANDOM % (i+1)))
    tmp="${ARMS[$i]}"
    ARMS[$i]="${ARMS[$j]}"
    ARMS[$j]="$tmp"
done

echo ""
echo "========================================="
echo "  Hierarchical A/B Experiment"
echo "  Tier: $TIER files | Runs: $RUNS"
echo "  Arms: ${ARMS[*]}"
echo "  A = flat + FIFO | B = hierarchical + FIFO"
echo "========================================="
echo ""

START_RUN="${START_RUN:-1}"

run_experiment() {
    local run_num=$1
    local arm="${ARMS[$((run_num-1))]}"
    local clone_dir="/tmp/hier-exp-run${run_num}"

    echo ""
    echo "================================================================"
    echo "  RUN $run_num / $RUNS — Arm $arm (tier $TIER)"
    echo "  $([ "$arm" = "A" ] && echo "Flat + FIFO" || echo "Hierarchical + FIFO")"
    echo "================================================================"

    # Clone-based isolation
    rm -rf "$clone_dir"
    git clone --quiet "$REPO_ROOT" "$clone_dir" 2>/dev/null
    mkdir -p "$clone_dir/bin" "$clone_dir/.orchestra"
    cp "$ORCHESTRA" "$clone_dir/bin/orchestra"
    chmod +x "$clone_dir/bin/orchestra"

    cd "$clone_dir"

    # Create goal files so the decomposer has something to work with
    for dir in internal/alpha internal/beta internal/gamma internal/delta; do
        mkdir -p "$dir"
    done
    for ((i=1; i<=TIER; i++)); do
        local dir_idx=$(( (i - 1) % 4 ))
        local dirs=("internal/alpha" "internal/beta" "internal/gamma" "internal/delta")
        local dir="${dirs[$dir_idx]}"
        cat > "${dir}/file_${i}.go" <<GOFILE
package $(basename "$dir")

type Model_${i} struct {
    ID   int
    Name string
}
GOFILE
    done
    git add -A && git commit -m "seed tier-$TIER goal files" --quiet 2>/dev/null

    # Build command as array to avoid eval quoting issues (G121)
    local args=("$clone_dir/bin/orchestra" "go" "--goal" "$GOAL" "--merge-strategy" "fifo" "--test-cmd" "go build ./..." "--foreground")
    if [ "$arm" = "B" ]; then
        args+=("--hierarchical")
    fi

    # Run with timing
    echo "[$(date +%H:%M:%S)] Starting..."
    local start_ts=$(date +%s)
    "${args[@]}" 2>&1 || true
    local end_ts=$(date +%s)
    local wall_clock=$((end_ts - start_ts))
    echo "[$(date +%H:%M:%S)] Finished in ${wall_clock}s"

    # Collect metrics
    local db_path="$clone_dir/.orchestra/orchestrator.db"
    local task_count=0 cluster_count=0 merge_conflicts=0 auto_resolved=0

    if [ -f "$db_path" ]; then
        task_count=$(sqlite3 "$db_path" "SELECT COUNT(*) FROM tasks" 2>/dev/null || echo 0)
        cluster_count=$(sqlite3 "$db_path" "SELECT COUNT(DISTINCT feature_cluster) FROM tasks WHERE feature_cluster IS NOT NULL AND feature_cluster != ''" 2>/dev/null || echo 0)
        merge_conflicts=$(sqlite3 "$db_path" "SELECT COUNT(*) FROM events WHERE event_type LIKE '%conflict%'" 2>/dev/null || echo 0)
        auto_resolved=$(sqlite3 "$db_path" "SELECT COUNT(*) FROM events WHERE event_type LIKE '%auto_resolved%'" 2>/dev/null || echo 0)
    fi

    # Verify
    local pass="0"
    local verdict="FAIL"
    if bash "$REPO_ROOT/scripts/verify-hierarchical.sh" "$clone_dir" "$TIER" 2>&1 | grep -q "VERDICT: PASS"; then
        pass="1"
        verdict="PASS"
    fi

    echo "$run_num,$arm,$TIER,$pass,$merge_conflicts,$auto_resolved,$wall_clock,$task_count,$cluster_count,$verdict" >> "$RESULTS_FILE"
    echo "[$(date +%H:%M:%S)] Run $run_num: $verdict (tasks=$task_count, clusters=$cluster_count, ${wall_clock}s)"

    # Preserve logs
    local log_dir="${RESULTS_DIR}/run${run_num}-arm${arm}"
    mkdir -p "$log_dir"
    cp -r "$clone_dir/.orchestra/logs/" "$log_dir/" 2>/dev/null || true
    cp "$db_path" "$log_dir/orchestrator.db" 2>/dev/null || true

    # Cleanup clone
    cd "$REPO_ROOT"
    rm -rf "$clone_dir"
}

for ((r=START_RUN; r<=RUNS; r++)); do
    run_experiment "$r"
done

echo ""
echo "========================================="
echo "  Experiment Complete"
echo "  Results: $RESULTS_FILE"
echo "========================================="
echo ""

# Run analysis
bash "$REPO_ROOT/scripts/analyze-hierarchical.sh" "$RESULTS_FILE"
