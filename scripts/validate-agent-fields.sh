#!/usr/bin/env bash
# validate-agent-fields.sh — Verify all agent definition files have maxTurns and disallowedTools in YAML frontmatter
# Part of B-313 validation gate

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PASS=0
FAIL=0
TOTAL=0

check_file() {
    local file="$1"
    local rel_path="${file#$REPO_ROOT/}"
    TOTAL=$((TOTAL + 1))

    # Extract YAML frontmatter (between first two --- lines)
    local frontmatter
    frontmatter=$(awk '/^---$/{n++; next} n==1{print} n>=2{exit}' "$file")

    if [ -z "$frontmatter" ]; then
        echo "FAIL  $rel_path — no YAML frontmatter found"
        FAIL=$((FAIL + 1))
        return
    fi

    # Extract maxTurns value (always a single-line scalar)
    local max_turns
    max_turns=$(echo "$frontmatter" | grep -E '^maxTurns:' | sed 's/maxTurns:[[:space:]]*//' || true)

    # Check for disallowedTools key — may be inline [] or multi-line YAML list
    local has_disallowed
    has_disallowed=$(echo "$frontmatter" | grep -E '^disallowedTools:' || true)
    local disallowed_tools=""
    if [ -n "$has_disallowed" ]; then
        # Try inline format first: disallowedTools: [Bash, Write] or disallowedTools: []
        local inline_val
        inline_val=$(echo "$has_disallowed" | sed 's/disallowedTools:[[:space:]]*//')
        if [ -n "$inline_val" ]; then
            disallowed_tools="$inline_val"
        else
            # Multi-line YAML list: collect - items indented under disallowedTools
            disallowed_tools=$(echo "$frontmatter" | awk '
                /^disallowedTools:/ { capture=1; next }
                capture && /^[[:space:]]+-/ { gsub(/^[[:space:]]+-[[:space:]]*/, ""); items = items (items ? ", " : "") $0; next }
                capture { exit }
                END { print "[" items "]" }
            ')
        fi
    fi

    if [ -z "$max_turns" ]; then
        echo "FAIL  $rel_path — missing maxTurns"
        FAIL=$((FAIL + 1))
        return
    fi

    if [ -z "$has_disallowed" ]; then
        echo "FAIL  $rel_path — missing disallowedTools"
        FAIL=$((FAIL + 1))
        return
    fi

    echo "PASS  $rel_path — maxTurns=$max_turns disallowedTools=$disallowed_tools"
    PASS=$((PASS + 1))
}

echo "=== Agent Field Validation (B-313) ==="
echo ""

echo "--- .claude/agents/ ---"
for f in "$REPO_ROOT"/.claude/agents/*.md; do
    [ -f "$f" ] && check_file "$f"
done

echo ""
echo "--- internal/assets/agents/ ---"
for f in "$REPO_ROOT"/internal/assets/agents/*.md; do
    [ -f "$f" ] && check_file "$f"
done

echo ""
echo "=== Results: $PASS passed, $FAIL failed, $TOTAL total ==="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
