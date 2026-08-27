#!/bin/bash
# download-swe-evo.sh — Download SWE-EVO dataset and convert to JSONL
#
# Downloads the SWE-EVO dataset from HuggingFace (Fsoft-AIC/SWE-EVO),
# converts to JSONL, optionally filters by file count, and pre-clones repos.
#
# Requirements: python3, pip (will install datasets and unidiff if needed)
#
# Usage: ./scripts/download-swe-evo.sh [--min-files N] [--max-files N] [--max-tasks N] [--output-dir DIR]
#
# Output:
#   <output-dir>/swe-evo-all.jsonl         — full dataset as JSONL (48 tasks)
#   <output-dir>/swe-evo-filtered.jsonl    — filtered by file count
#   <output-dir>/repos/                    — pre-cloned repos
set -euo pipefail

MIN_FILES="${MIN_FILES:-1}"
MAX_FILES="${MAX_FILES:-999}"
MAX_TASKS="${MAX_TASKS:-48}"
OUTPUT_DIR="${OUTPUT_DIR:-/tmp/swe-evo-data}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --min-files) MIN_FILES="$2"; shift 2 ;;
        --max-files) MAX_FILES="$2"; shift 2 ;;
        --max-tasks) MAX_TASKS="$2"; shift 2 ;;
        --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
        *) echo "ERROR: Unknown argument: $1" >&2; exit 1 ;;
    esac
done

mkdir -p "$OUTPUT_DIR/repos"

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║        SWE-EVO Download & Filter                            ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Min files:    $MIN_FILES"
echo "║  Max files:    $MAX_FILES"
echo "║  Max tasks:    $MAX_TASKS"
echo "║  Output:       $OUTPUT_DIR/"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Phase 1: Install dependencies ──────────────────────────────────

echo "[$(date +%H:%M:%S)] Checking Python dependencies..."
python3 -c "import datasets" 2>/dev/null || pip3 install datasets --quiet
python3 -c "import unidiff" 2>/dev/null || pip3 install unidiff --quiet
echo "[$(date +%H:%M:%S)] Dependencies ready."

# ── Phase 2: Download and convert to JSONL ─────────────────────────

ALL_JSONL="$OUTPUT_DIR/swe-evo-all.jsonl"
FILTERED_JSONL="$OUTPUT_DIR/swe-evo-filtered.jsonl"

if [ -f "$ALL_JSONL" ]; then
    echo "[$(date +%H:%M:%S)] Dataset already downloaded: $ALL_JSONL"
else
    echo "[$(date +%H:%M:%S)] Downloading SWE-EVO from HuggingFace..."
    OUTPUT_DIR="$OUTPUT_DIR" python3 << 'PYEOF'
import json, os
from datasets import load_dataset
from unidiff import PatchSet

output_dir = os.environ["OUTPUT_DIR"]
ds = load_dataset("Fsoft-AIC/SWE-EVO", split="test")
output_path = os.path.join(output_dir, "swe-evo-all.jsonl")

with open(output_path, "w") as f:
    for item in ds:
        record = dict(item)

        # Extract files_changed from gold patch
        try:
            patch = PatchSet(record.get("patch", ""))
            record["files_changed"] = [p.path for p in patch]
        except Exception:
            record["files_changed"] = []

        # Normalize field names for adapter compatibility
        record["id"] = record.get("instance_id", "")
        record["description"] = record.get("problem_statement", "")

        # FAIL_TO_PASS may already be a list
        ftp = record.get("FAIL_TO_PASS", [])
        if isinstance(ftp, str):
            try:
                record["fail_to_pass"] = json.loads(ftp)
            except json.JSONDecodeError:
                record["fail_to_pass"] = []
        else:
            record["fail_to_pass"] = ftp

        # PASS_TO_PASS
        ptp = record.get("PASS_TO_PASS", [])
        if isinstance(ptp, str):
            try:
                record["pass_to_pass"] = json.loads(ptp)
            except json.JSONDecodeError:
                record["pass_to_pass"] = []
        else:
            record["pass_to_pass"] = ptp

        # test_cmds is the real test command
        record["test_cmd"] = record.get("test_cmds", "true")

        f.write(json.dumps(record) + "\n")

print(f"Wrote {len(ds)} instances to {output_path}")
PYEOF
    echo "[$(date +%H:%M:%S)] Dataset downloaded."
fi

# ── Phase 3: Filter by file count ──────────────────────────────────

echo "[$(date +%H:%M:%S)] Filtering: $MIN_FILES <= files <= $MAX_FILES (max $MAX_TASKS tasks)..."

MIN_FILES="$MIN_FILES" MAX_FILES="$MAX_FILES" MAX_TASKS="$MAX_TASKS" OUTPUT_DIR="$OUTPUT_DIR" python3 << 'PYEOF'
import json, os

input_path = os.path.join(os.environ["OUTPUT_DIR"], "swe-evo-all.jsonl")
output_path = os.path.join(os.environ["OUTPUT_DIR"], "swe-evo-filtered.jsonl")
min_files = int(os.environ["MIN_FILES"])
max_files = int(os.environ["MAX_FILES"])
max_tasks = int(os.environ["MAX_TASKS"])

count = 0
with open(input_path) as fin, open(output_path, "w") as fout:
    for line in fin:
        record = json.loads(line)
        fc = len(record.get("files_changed", []))
        if min_files <= fc <= max_files:
            fout.write(line)
            count += 1
            if count >= max_tasks:
                break

print(f"Filtered: {count} tasks with {min_files}-{max_files} files (max {max_tasks})")
PYEOF

TASK_COUNT=$(wc -l < "$FILTERED_JSONL" | tr -d ' ')
echo "[$(date +%H:%M:%S)] Found $TASK_COUNT tasks after filtering."

# ── Phase 4: Pre-clone repos ──────────────────────────────────────

echo ""
echo "[$(date +%H:%M:%S)] Pre-cloning repos..."

OUTPUT_DIR="$OUTPUT_DIR" python3 -c "
import json, os
seen = set()
path = os.path.join(os.environ['OUTPUT_DIR'], 'swe-evo-filtered.jsonl')
with open(path) as f:
    for line in f:
        r = json.loads(line).get('repo', '')
        if r and r not in seen:
            seen.add(r)
            print(r)
" | while read -r repo; do
    repo_dir="$OUTPUT_DIR/repos/$repo"
    if [ -d "$repo_dir" ]; then
        echo "  Skip: $repo (already cloned)"
    else
        echo "  Cloning: $repo..."
        mkdir -p "$(dirname "$repo_dir")"
        git clone --quiet "https://github.com/$repo.git" "$repo_dir" 2>&1 || {
            echo "  WARN: Failed to clone $repo"
        }
    fi
done

echo ""
echo "[$(date +%H:%M:%S)] Done."
echo ""
echo "=== Summary ==="
echo "All tasks:       $(wc -l < "$ALL_JSONL" | tr -d ' ')"
echo "Filtered:        $TASK_COUNT"
echo "Repos cloned:    $(ls -d "$OUTPUT_DIR/repos/"*/* 2>/dev/null | wc -l | tr -d ' ')"
echo ""
echo "To run benchmarks:"
echo "  ./scripts/run-external-benchmark.sh \\"
echo "    --benchmark swe-evo \\"
echo "    --tasks $FILTERED_JSONL \\"
echo "    --repos $OUTPUT_DIR/repos \\"
echo "    --limit 3"
