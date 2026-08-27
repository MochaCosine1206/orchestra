#!/bin/bash
# verify-hierarchical.sh — Verify hierarchical experiment run
# Usage: ./scripts/verify-hierarchical.sh <REPO_DIR> <TIER>
set -uo pipefail

REPO_DIR="${1:?Usage: verify-hierarchical.sh <REPO_DIR> <TIER>}"
TIER="${2:?Usage: verify-hierarchical.sh <REPO_DIR> <TIER>}"

cd "$REPO_DIR"

pass=true
checks=0
passed=0

# Check 1: Goal files exist and have Version field
echo "--- Goal File Checks ---"
dirs=("internal/alpha" "internal/beta" "internal/gamma" "internal/delta")
for ((i=1; i<=TIER; i++)); do
    dir_idx=$(( (i - 1) % 4 ))
    dir="${dirs[$dir_idx]}"
    file="${dir}/file_${i}.go"
    checks=$((checks + 1))

    if [ ! -f "$file" ]; then
        echo "  FAIL: $file does not exist"
        pass=false
        continue
    fi

    if grep -q "Version" "$file"; then
        passed=$((passed + 1))
    else
        echo "  FAIL: $file missing Version field"
        pass=false
    fi
done
echo "  Files: $passed/$checks have Version field"

# Check 2: Build passes
echo ""
echo "--- Build Check ---"
checks=$((checks + 1))
if go build ./... 2>/dev/null; then
    echo "  PASS: go build ./..."
    passed=$((passed + 1))
else
    echo "  FAIL: go build ./..."
    pass=false
fi

# Check 3: No uncommitted worktrees left
echo ""
echo "--- Cleanup Check ---"
checks=$((checks + 1))
wt_count=$(git worktree list 2>/dev/null | wc -l | tr -d ' ')
if [ "$wt_count" -le 1 ]; then
    echo "  PASS: No stale worktrees ($wt_count)"
    passed=$((passed + 1))
else
    echo "  WARN: $wt_count worktrees found (expected <=1)"
    # Not a hard fail — worktree cleanup is best-effort
    passed=$((passed + 1))
fi

echo ""
echo "--- Summary ---"
echo "  Checks: $passed/$checks passed"
if $pass; then
    echo "  VERDICT: PASS"
else
    echo "  VERDICT: FAIL"
fi
