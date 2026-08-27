#!/bin/bash
# download-swe-bench.sh — Download SWE-bench Lite and filter multi-file tasks
#
# Downloads the SWE-bench Lite dataset from HuggingFace, converts to JSONL,
# filters for multi-file tasks (3+ files in gold patch), and pre-clones repos.
#
# Requirements: python3, pip (will install datasets and unidiff if needed)
#
# Usage: ./scripts/download-swe-bench.sh [--min-files N] [--max-tasks N] [--output-dir DIR]
#
# Output:
#   <output-dir>/swe-bench-lite-all.jsonl      — full dataset as JSONL
#   <output-dir>/swe-bench-lite-multifile.jsonl — filtered to >=min-files
#   <output-dir>/repos/                        — pre-cloned repos
set -euo pipefail

MIN_FILES="${MIN_FILES:-3}"
MAX_TASKS="${MAX_TASKS:-20}"
OUTPUT_DIR="${OUTPUT_DIR:-/tmp/swe-bench-data}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --min-files) MIN_FILES="$2"; shift 2 ;;
        --max-tasks) MAX_TASKS="$2"; shift 2 ;;
        --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
        *) echo "ERROR: Unknown argument: $1" >&2; exit 1 ;;
    esac
done

mkdir -p "$OUTPUT_DIR/repos"

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║        SWE-bench Lite Download & Filter                     ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Min files:    $MIN_FILES"
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

ALL_JSONL="$OUTPUT_DIR/swe-bench-lite-all.jsonl"
FILTERED_JSONL="$OUTPUT_DIR/swe-bench-lite-multifile.jsonl"

if [ -f "$ALL_JSONL" ]; then
    echo "[$(date +%H:%M:%S)] Dataset already downloaded: $ALL_JSONL"
else
    echo "[$(date +%H:%M:%S)] Downloading SWE-bench Lite from HuggingFace..."
    python3 << 'PYEOF'
import json
from datasets import load_dataset
from unidiff import PatchSet

ds = load_dataset("SWE-bench/SWE-bench_Lite", split="test")
output_path = "$OUTPUT_DIR/swe-bench-lite-all.jsonl".replace("$OUTPUT_DIR", "${OUTPUT_DIR}")

# Can't use shell vars in heredoc python — use env
import os
output_path = os.environ.get("OUTPUT_DIR", "/tmp/swe-bench-data") + "/swe-bench-lite-all.jsonl"

with open(output_path, "w") as f:
    for item in ds:
        record = dict(item)

        # Extract files_changed from the gold patch
        try:
            patch = PatchSet(record.get("patch", ""))
            record["files_changed"] = [p.path for p in patch]
        except Exception:
            record["files_changed"] = []

        # Normalize field names for our adapter
        record["id"] = record.get("instance_id", "")
        record["description"] = record.get("problem_statement", "")
        record["test_cmd"] = "true"  # SWE-bench uses custom test harness

        # Parse FAIL_TO_PASS from string to list if needed
        ftp = record.get("FAIL_TO_PASS", "")
        if isinstance(ftp, str) and ftp.startswith("["):
            try:
                record["fail_to_pass"] = json.loads(ftp)
            except json.JSONDecodeError:
                record["fail_to_pass"] = []

        f.write(json.dumps(record) + "\n")

print(f"Wrote {len(ds)} instances to {output_path}")
PYEOF
    echo "[$(date +%H:%M:%S)] Dataset downloaded."
fi

# ── Phase 3: Filter for multi-file tasks ───────────────────────────

echo "[$(date +%H:%M:%S)] Filtering for tasks with >= $MIN_FILES files changed..."

python3 << PYEOF
import json
import os

input_path = os.environ.get("OUTPUT_DIR", "/tmp/swe-bench-data") + "/swe-bench-lite-all.jsonl"
output_path = os.environ.get("OUTPUT_DIR", "/tmp/swe-bench-data") + "/swe-bench-lite-multifile.jsonl"
min_files = int(os.environ.get("MIN_FILES", "3"))
max_tasks = int(os.environ.get("MAX_TASKS", "20"))

count = 0
with open(input_path) as fin, open(output_path, "w") as fout:
    for line in fin:
        record = json.loads(line)
        files = record.get("files_changed", [])
        if len(files) >= min_files:
            fout.write(line)
            count += 1
            if count >= max_tasks:
                break

print(f"Filtered: {count} tasks with >= {min_files} files (max {max_tasks})")
PYEOF

TASK_COUNT=$(wc -l < "$FILTERED_JSONL" | tr -d ' ')
echo "[$(date +%H:%M:%S)] Found $TASK_COUNT multi-file tasks."

# ── Phase 4: Pre-clone repos ──────────────────────────────────────

echo ""
echo "[$(date +%H:%M:%S)] Pre-cloning repos..."

# Extract unique repos from filtered tasks
REPOS=$(python3 -c "
import json, os
seen = set()
path = os.environ.get('OUTPUT_DIR', '/tmp/swe-bench-data') + '/swe-bench-lite-multifile.jsonl'
with open(path) as f:
    for line in f:
        r = json.loads(line).get('repo', '')
        if r and r not in seen:
            seen.add(r)
            print(r)
")

for repo in $REPOS; do
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
echo "Multi-file (>=$MIN_FILES): $TASK_COUNT"
echo "Repos cloned:    $(ls -d "$OUTPUT_DIR/repos/"*/* 2>/dev/null | wc -l | tr -d ' ')"
echo ""
echo "To run benchmarks:"
echo "  ./scripts/run-external-benchmark.sh \\"
echo "    --benchmark swe-bench \\"
echo "    --tasks $FILTERED_JSONL \\"
echo "    --repos $OUTPUT_DIR/repos \\"
echo "    --limit 5"
