#!/usr/bin/env bash
# visual-review-gate.sh — Gate script for visual review phase
# Parses .orchestra/visual-review/report.json and checks overall_score >= threshold.
# Usage: bash scripts/visual-review-gate.sh [threshold]
# Default threshold: 45 (out of 60 max)
# Exit 0 on pass, 1 on fail.

set -euo pipefail

REPORT=".orchestra/visual-review/report.json"
THRESHOLD="${1:-45}"

if [ ! -f "$REPORT" ]; then
    echo "FAIL: Visual review report not found at $REPORT"
    echo "The visual-reviewer agent must run before the gate can evaluate."
    exit 1
fi

# Validate JSON
if ! jq empty "$REPORT" 2>/dev/null; then
    echo "FAIL: $REPORT is not valid JSON"
    exit 1
fi

SCORE=$(jq -r '.overall_score // 0' "$REPORT")
MAX_SCORE=$(jq -r '.max_score // 60' "$REPORT")
CORRECTIONS=$(jq -r '.corrections_count // 0' "$REPORT")

echo "Visual Review Gate"
echo "=================="
echo "Score:       $SCORE / $MAX_SCORE"
echo "Threshold:   $THRESHOLD"
echo "Corrections: $CORRECTIONS"
echo ""

# Per-page breakdown
echo "Per-page scores:"
jq -r '.pages[] | "  \(.page): \(.score)/10 (\(.issues_found) issues)"' "$REPORT" 2>/dev/null || true
echo ""

if [ "$SCORE" -ge "$THRESHOLD" ]; then
    echo "PASS: Score $SCORE >= threshold $THRESHOLD"
    exit 0
else
    echo "FAIL: Score $SCORE < threshold $THRESHOLD"
    echo ""
    echo "Top issues:"
    jq -r '.pages[] | select(.score < 7) | "  \(.page): \(.dom_mismatches // [] | join(", "))"' "$REPORT" 2>/dev/null || true
    exit 1
fi
