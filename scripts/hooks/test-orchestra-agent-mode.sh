#!/bin/bash
# Tests for orchestra agent mode behavior in hooks
# When ORCHESTRA_AGENT=1, hooks should suppress interactive Telegram features
# (topic creation, polling, buttons, reply-poller) while keeping quality gates.
# Run: bash scripts/hooks/test-orchestra-agent-mode.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0
FAIL=0
TOTAL=0

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

# Use temp dir for test isolation
TEST_BASE=$(mktemp -d)
export SESSION_STATE_BASE="$TEST_BASE"
# Prevent real Telegram API calls
export TELEGRAM_NOTIFY_CONFIG=/dev/null
export TELEGRAM_BOT_TOKEN=""
export TELEGRAM_CHAT_ID=""

cleanup() {
  rm -rf "$TEST_BASE"
  unset ORCHESTRA_AGENT ORCHESTRA_TASK_ID ORCHESTRA_PARENT_SLUG
}
trap cleanup EXIT

source "$SCRIPT_DIR/lib/state-helpers.sh"

echo "=== Orchestra Agent Mode Tests ==="
echo ""

# --- Test 1: get_parent_topic_id helper ---
echo "Test 1: get_parent_topic_id — reads topic_id from parent slug state"

# Set up parent project state with topic ID
mkdir -p "$TEST_BASE/claude-orchestra/telegram"
echo "12345" > "$TEST_BASE/claude-orchestra/telegram/topic_id"

assert "Reads parent topic_id" '[ "$(get_parent_topic_id "claude-orchestra")" = "12345" ]'
assert "Missing slug returns empty" '[ -z "$(get_parent_topic_id "nonexistent-project")" ]'
echo ""

# --- Test 2: telegram-check.sh — agent mode exits immediately ---
echo "Test 2: telegram-check.sh — agent mode early exit"

export ORCHESTRA_AGENT=1
export ORCHESTRA_TASK_ID="t-20260227-abc123"
export ORCHESTRA_PARENT_SLUG="claude-orchestra"

# telegram-check.sh should exit 0 immediately without doing anything
# We verify by checking that it exits cleanly and quickly (< 2s)
BEFORE=$(date +%s)
echo '{}' | bash "$SCRIPT_DIR/telegram-check.sh" 2>/dev/null
TC_EXIT=$?
AFTER=$(date +%s)
ELAPSED=$((AFTER - BEFORE))

assert "telegram-check exits 0 in agent mode" '[ "$TC_EXIT" -eq 0 ]'
assert "telegram-check exits fast (< 2s)" '[ "$ELAPSED" -lt 2 ]'
echo ""

# --- Test 3: session-start-inject.sh — agent mode skips topic creation ---
echo "Test 3: session-start-inject.sh — agent mode output"

# Create a minimal git repo for session-start to work with
FAKE_PROJECT=$(mktemp -d)
cd "$FAKE_PROJECT" && git init -q && git commit --allow-empty -m "init" -q
export CLAUDE_PROJECT_DIR="$FAKE_PROJECT"

OUTPUT=$(echo '{"source":"startup"}' | bash "$SCRIPT_DIR/session-start-inject.sh" 2>/dev/null)

assert "Contains orchestra agent context" 'echo "$OUTPUT" | grep -q "Orchestra Agent Mode"'
assert "Contains task ID" 'echo "$OUTPUT" | grep -q "t-20260227-abc123"'
assert "Does NOT contain topic creation" '! echo "$OUTPUT" | grep -qi "topic_ensure"'

rm -rf "$FAKE_PROJECT"
unset CLAUDE_PROJECT_DIR
echo ""

# --- Test 4: stop-notify.sh — agent mode sends brief status, no buttons ---
echo "Test 4: stop-notify.sh — agent mode brief status"

# Set up parent topic so stop-notify can route to it
mkdir -p "$TEST_BASE/claude-orchestra/telegram"
echo "12345" > "$TEST_BASE/claude-orchestra/telegram/topic_id"

# Create a summary file for the agent
SUMMARY_FILE="/tmp/claude-session-summary.txt"
echo "Added B-249 comment to state-helpers.sh" > "$SUMMARY_FILE"

# stop-notify should exit quickly without polling when ORCHESTRA_AGENT=1
# Since we have no TELEGRAM_BOT_TOKEN it won't actually send, but we test the logic path
BEFORE=$(date +%s)
echo '{"session_id":"test-session","last_assistant_message":"Done."}' | bash "$SCRIPT_DIR/stop-notify.sh" 2>/dev/null
SN_EXIT=$?
AFTER=$(date +%s)
ELAPSED=$((AFTER - BEFORE))

assert "stop-notify exits 0 in agent mode" '[ "$SN_EXIT" -eq 0 ]'
assert "stop-notify exits fast — no polling (< 5s)" '[ "$ELAPSED" -lt 5 ]'

# Cleanup
rm -f "$SUMMARY_FILE"
echo ""

# --- Test 5: Normal mode (non-agent) is unaffected ---
echo "Test 5: Normal mode unaffected — no ORCHESTRA_AGENT"
unset ORCHESTRA_AGENT
unset ORCHESTRA_TASK_ID
unset ORCHESTRA_PARENT_SLUG

# telegram-check should not exit immediately (it will exit due to missing token though)
echo '{}' | bash "$SCRIPT_DIR/telegram-check.sh" 2>/dev/null
NORMAL_EXIT=$?
assert "telegram-check normal mode exits 0 (no token)" '[ "$NORMAL_EXIT" -eq 0 ]'
echo ""

# --- Test 6: askuser-relay and permission-relay — agent mode early exit ---
echo "Test 6: askuser-relay.sh and permission-relay.sh — agent mode early exit"
export ORCHESTRA_AGENT=1

echo '{"tool_name":"AskUserQuestion"}' | bash "$SCRIPT_DIR/askuser-relay.sh" 2>/dev/null
AU_EXIT=$?
assert "askuser-relay exits 0 in agent mode" '[ "$AU_EXIT" -eq 0 ]'

echo '{"tool_name":"Bash"}' | bash "$SCRIPT_DIR/permission-relay.sh" 2>/dev/null
PR_EXIT=$?
assert "permission-relay exits 0 in agent mode" '[ "$PR_EXIT" -eq 0 ]'

unset ORCHESTRA_AGENT
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
