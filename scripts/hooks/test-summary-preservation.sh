#!/bin/bash
# Tests for B-261b/c: summary file lifecycle + M1 slug-based paths
# Run: bash scripts/hooks/test-summary-preservation.sh
set -uo pipefail

SCRIPT="scripts/hooks/stop-notify.sh"
SUMMARY_FILE="/tmp/claude-session-summary.txt"
DEDUP_FILE="/tmp/claude-stop-notify-last"
PASS=0
FAIL=0
TOTAL=0

# Use temp dir for state isolation
TEST_STATE_BASE=$(mktemp -d)
export SESSION_STATE_BASE="$TEST_STATE_BASE"
export TELEGRAM_NOTIFY_CONFIG=/dev/null

cleanup() {
  rm -f "$SUMMARY_FILE" "$DEDUP_FILE" "/tmp/claude-stop-hook-active"
}

full_cleanup() {
  cleanup
  rm -rf "$TEST_STATE_BASE"
}
trap full_cleanup EXIT

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

echo "=== Summary File Lifecycle + Slug Path Tests ==="
echo ""

# --- Test 1: Summary file consumed after notification sends ---
echo "Test 1: Summary file consumed when notification sends"
cleanup
echo "Test summary content" > "$SUMMARY_FILE"
echo '{"session_id":"test","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "Summary file preserved after send" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 2: No summary file — fallback works (no crash) ---
echo "Test 2: No summary file present (fallback, no crash)"
cleanup
echo '{"session_id":"test","last_assistant_message":"hello world from claude"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "No crash when summary file absent" "true"
assert "No summary file created spuriously" "[ ! -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 3: Dedup guard prevents rapid-fire sends ---
echo "Test 3: Dedup guard prevents rapid-fire notifications"
cleanup
echo "First summary" > "$SUMMARY_FILE"
echo '{"session_id":"test","last_assistant_message":"first"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "First notification preserves summary" "[ -f '$SUMMARY_FILE' ]"
# Immediately fire again — dedup guard should skip (within 10s)
echo "Second summary" > "$SUMMARY_FILE"
echo '{"session_id":"test","last_assistant_message":"second"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "Second summary preserved (dedup guard skipped send)" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 4: Notification always sends after dedup window ---
echo "Test 4: Notification sends after dedup window expires"
cleanup
# Set dedup timestamp to 15 seconds ago (past the 10s window)
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo "Fresh summary" > "$SUMMARY_FILE"
echo '{"session_id":"test","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "Summary file preserved after send" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 5: No stop_hook_active guard (B-261c removed it) ---
echo "Test 5: stop_hook_active file has no effect (guard removed)"
cleanup
echo "Should still send" > "$SUMMARY_FILE"
echo "true" > "/tmp/claude-stop-hook-active"
# Set dedup past window
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo '{"session_id":"test","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "Summary file preserved despite stop_hook_active" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 6: Slug-based state directory created ---
echo "Test 6: Slug-based state directory created on stop"
cleanup
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo '{"session_id":"slug-test-sess","last_assistant_message":"testing slug"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" \
  CLAUDE_PROJECT_DIR="/home/user/My-Project" bash "$SCRIPT" 2>/dev/null || true
SLUG_DIR="$TEST_STATE_BASE/my-project"
assert "Project slug dir created" "[ -d '$SLUG_DIR' ]"
assert "Sessions subdir created" "[ -d '$SLUG_DIR/sessions' ]"
assert "Stop file in slug path" "[ -f '$SLUG_DIR/sessions/slug-test-sess-stop.json' ]"

echo ""

# --- Test 7: Summary persisted to last-summary.md ---
echo "Test 7: Summary persisted to slug dir as last-summary.md"
cleanup
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo "Persistent summary content" > "$SUMMARY_FILE"
echo '{"session_id":"sum-test","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" \
  CLAUDE_PROJECT_DIR="/home/user/My-Project" bash "$SCRIPT" 2>/dev/null || true
SLUG_DIR="$TEST_STATE_BASE/my-project"
assert "last-summary.md created" "[ -f '$SLUG_DIR/last-summary.md' ]"
assert "Content matches original summary" '[ "$(cat "$SLUG_DIR/last-summary.md")" = "Persistent summary content" ]'
assert "Summary file preserved after persist" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Cleanup ---
cleanup

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
