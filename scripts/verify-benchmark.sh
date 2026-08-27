#!/bin/bash
# verify-benchmark.sh — Checks if the 8-file priority_label benchmark was completed correctly
# Usage: ./scripts/verify-benchmark.sh [repo_root]
set -uo pipefail

REPO="${1:-.}"
PASS=0
FAIL=0
TOTAL=8

# Track per-file results for machine-readable output
declare -a FILE_NAMES=()
declare -a FILE_RESULTS=()

check() {
    local desc="$1"
    local short_name="$2"
    shift 2
    if "$@" >/dev/null 2>&1; then
        echo "  PASS: $desc"
        PASS=$((PASS + 1))
        FILE_RESULTS+=("PASS")
    else
        echo "  FAIL: $desc"
        FAIL=$((FAIL + 1))
        FILE_RESULTS+=("FAIL")
    fi
    FILE_NAMES+=("$short_name")
}

echo "=== 8-File Benchmark Verification ==="

# 1. Schema has priority_label column
check "schema/001_initial.sql: priority_label column" "schema" \
    grep -q 'priority_label' "$REPO/internal/db/schema/001_initial.sql"

# 2. Model struct has PriorityLabel field
check "models.go: PriorityLabel field" "models" \
    grep -q 'PriorityLabel' "$REPO/internal/db/models.go"

# 3. Queries include priority_label
check "queries.go: priority_label in queries" "queries" \
    grep -q 'priority_label' "$REPO/internal/db/queries.go"

# 4. Mutations include priority_label
check "mutations.go: priority_label in mutations" "mutations" \
    grep -q 'priority_label' "$REPO/internal/db/mutations.go"

# 5. Spawner/specgen references priority_label
# Agents may correctly place spec generation logic in specgen.go (where GenerateSpec lives)
check "spawner.go or specgen.go: priority_label handling" "spawner" \
    bash -c "grep -qi 'priority.label\|PriorityLabel' '$REPO/internal/agent/spawner.go' 2>/dev/null || grep -qi 'priority.label\|PriorityLabel' '$REPO/internal/agent/specgen.go' 2>/dev/null"

# 6. go.go references priority_label or uses updated Task struct
check "go.go: priority_label handling" "go" \
    grep -qi 'priority.label\|PriorityLabel' "$REPO/internal/orchestrator/go.go"

# 7. iterative.go references or is compatible
check "iterative.go: priority_label handling" "iterative" \
    grep -qi 'priority.label\|PriorityLabel' "$REPO/internal/orchestrator/iterative.go"

# 8. TUI displays priority_label
check "tui/: priority_label display" "tui" \
    grep -rqi 'priority.label\|PriorityLabel' "$REPO/internal/tui/"

# Emit machine-readable per-file detail
detail=""
for i in "${!FILE_NAMES[@]}"; do
    if [ -n "$detail" ]; then detail="${detail},"; fi
    detail="${detail}${FILE_NAMES[$i]}=${FILE_RESULTS[$i]}"
done
echo "FILES_DETAIL: $detail"

echo ""
echo "=== Build Verification ==="
cd "$REPO" || exit 1

if go build ./... 2>/dev/null; then
    echo "  PASS: go build ./..."
    BUILD_PASS=true
else
    echo "  FAIL: go build ./..."
    BUILD_PASS=false
fi

echo ""
echo "=== Results: $PASS/$TOTAL file checks passed ==="
echo "Build: $BUILD_PASS"

if [ "$PASS" -ge 6 ] && [ "$BUILD_PASS" = true ]; then
    echo "VERDICT: PASS (≥6/8 threshold met + build passes)"
    exit 0
else
    echo "VERDICT: FAIL"
    exit 1
fi
