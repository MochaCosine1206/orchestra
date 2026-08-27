#!/bin/bash
# Tests for pre-compact-state.sh — PreCompact checkpoint hook
# Run: bash scripts/hooks/test-pre-compact-state.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/pre-compact-state.sh"
PASS=0
FAIL=0
TOTAL=0

# Use temp dir for state isolation
TEST_STATE_BASE=$(mktemp -d)
export SESSION_STATE_BASE="$TEST_STATE_BASE"
export TELEGRAM_NOTIFY_CONFIG=/dev/null

# Create a temp git repo for git command tests
TEST_GIT_DIR=$(mktemp -d)
git -C "$TEST_GIT_DIR" init -q
git -C "$TEST_GIT_DIR" commit --allow-empty -m "Initial commit" -q
git -C "$TEST_GIT_DIR" commit --allow-empty -m "Second commit" -q

cleanup() {
  rm -rf "$TEST_STATE_BASE" "$TEST_GIT_DIR" /tmp/claude-session-summary-precompact-test.txt
}
trap cleanup EXIT

assert() {
  local test_name="$1"
  local condition="$2"
  TOTAL=$((TOTAL + 1))
  if eval "$condition"; then
    echo "  PASS: $test_name"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $test_name"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== PreCompact State Hook Tests ==="
echo ""

# --- Test 1: Valid JSON output ---
echo "Test 1: Produces valid JSON checkpoint"
INPUT='{"session_id":"sess-001","trigger":"auto"}'
OUTPUT=$(echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" bash "$SCRIPT" 2>/dev/null)
assert "Output is valid JSON" 'echo "$OUTPUT" | jq -e . >/dev/null 2>&1'
assert "Contains session_id" '[ "$(echo "$OUTPUT" | jq -r .session_id)" = "sess-001" ]'
assert "Contains trigger" '[ "$(echo "$OUTPUT" | jq -r .trigger)" = "auto" ]'
assert "Contains timestamp" 'echo "$OUTPUT" | jq -e .timestamp >/dev/null 2>&1'
assert "Contains git_branch" 'echo "$OUTPUT" | jq -e .git_branch >/dev/null 2>&1'
assert "Contains recent_commits array" 'echo "$OUTPUT" | jq -e ".recent_commits | type == \"array\"" >/dev/null 2>&1'

echo ""

# --- Test 2: Checkpoint file written to slug path ---
echo "Test 2: Checkpoint file written correctly"
INPUT='{"session_id":"sess-002","trigger":"manual"}'
echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" bash "$SCRIPT" >/dev/null 2>&1
SLUG=$(basename "$TEST_GIT_DIR" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g')
CHECKPOINT="$TEST_STATE_BASE/$SLUG/sessions/sess-002-compact.json"
assert "Checkpoint file created" "[ -f '$CHECKPOINT' ]"
assert "Checkpoint is valid JSON" 'jq -e . "$CHECKPOINT" >/dev/null 2>&1'

echo ""

# --- Test 3: Compaction count increments ---
echo "Test 3: Compaction count increments"
# First compaction
INPUT='{"session_id":"sess-003","trigger":"auto"}'
echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" bash "$SCRIPT" >/dev/null 2>&1
SLUG=$(basename "$TEST_GIT_DIR" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g')
CHECKPOINT="$TEST_STATE_BASE/$SLUG/sessions/sess-003-compact.json"
COUNT1=$(jq -r '.compaction_count' "$CHECKPOINT")
assert "First compaction count is 1" '[ "$COUNT1" = "1" ]'

# Second compaction (same session)
echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" bash "$SCRIPT" >/dev/null 2>&1
COUNT2=$(jq -r '.compaction_count' "$CHECKPOINT")
assert "Second compaction count is 2" '[ "$COUNT2" = "2" ]'

# Third compaction
echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" bash "$SCRIPT" >/dev/null 2>&1
COUNT3=$(jq -r '.compaction_count' "$CHECKPOINT")
assert "Third compaction count is 3" '[ "$COUNT3" = "3" ]'

echo ""

# --- Test 4: Summary file copied ---
echo "Test 4: Summary file copied to last-summary.md"
SUMMARY_FILE="/tmp/claude-session-summary-precompact-test.txt"
echo "Pre-compact summary" > "$SUMMARY_FILE"
INPUT='{"session_id":"sess-004","trigger":"auto"}'
echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" SUMMARY_FILE_OVERRIDE="$SUMMARY_FILE" bash "$SCRIPT" >/dev/null 2>&1
SLUG=$(basename "$TEST_GIT_DIR" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g')
LAST_SUMMARY="$TEST_STATE_BASE/$SLUG/last-summary.md"
assert "last-summary.md created" "[ -f '$LAST_SUMMARY' ]"
assert "Content matches" '[ "$(cat "$LAST_SUMMARY")" = "Pre-compact summary" ]'
# Original NOT deleted (PreCompact only copies, stop-notify deletes)
assert "Original summary preserved" "[ -f '$SUMMARY_FILE' ]"
rm -f "$SUMMARY_FILE"

echo ""

# --- Test 5: Handles missing git gracefully ---
echo "Test 5: Handles non-git directory gracefully"
NON_GIT_DIR=$(mktemp -d)
INPUT='{"session_id":"sess-005","trigger":"auto"}'
OUTPUT=$(echo "$INPUT" | CLAUDE_PROJECT_DIR="$NON_GIT_DIR" bash "$SCRIPT" 2>/dev/null)
assert "Still produces valid JSON" 'echo "$OUTPUT" | jq -e . >/dev/null 2>&1'
assert "git_branch is empty or null" '[ "$(echo "$OUTPUT" | jq -r ".git_branch // empty")" = "" ] || [ "$(echo "$OUTPUT" | jq -r .git_branch)" = "null" ]'
rm -rf "$NON_GIT_DIR"

echo ""

# --- Test 6: Handles missing summary gracefully ---
echo "Test 6: Handles missing summary file gracefully"
INPUT='{"session_id":"sess-006","trigger":"auto"}'
OUTPUT=$(echo "$INPUT" | CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" SUMMARY_FILE_OVERRIDE="/nonexistent/file.txt" bash "$SCRIPT" 2>/dev/null)
assert "Still produces valid JSON without summary" 'echo "$OUTPUT" | jq -e . >/dev/null 2>&1'

echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
