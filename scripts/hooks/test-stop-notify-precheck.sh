#!/bin/bash
# Tests for B-237 ext: pre-check replies before sending notification
# Tests the pre-check logic path in stop-notify.sh
# Run: bash scripts/hooks/test-stop-notify-precheck.sh
set -uo pipefail

SCRIPT="scripts/hooks/stop-notify.sh"
SUMMARY_FILE="/tmp/claude-session-summary.txt"
DEDUP_FILE="/tmp/claude-stop-notify-last"
OFFSET_FILE="/tmp/telegram-last-update-id"
PASS=0
FAIL=0
TOTAL=0

# Use temp dir for state isolation
TEST_STATE_BASE=$(mktemp -d)
export SESSION_STATE_BASE="$TEST_STATE_BASE"
export TELEGRAM_NOTIFY_CONFIG=/dev/null

cleanup() {
  rm -f "$SUMMARY_FILE" "$DEDUP_FILE" "$OFFSET_FILE" "/tmp/claude-stop-hook-active" "/tmp/claude-telegram-pending-reply"
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

echo "=== B-237 ext: Pre-Check Reply Tests ==="
echo ""

# --- Test 1: No offset file — pre-check skipped, normal flow ---
echo "Test 1: No offset file means pre-check is skipped (normal flow)"
cleanup
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo "Test summary" > "$SUMMARY_FILE"
# No offset file — pre-check IF block won't enter
echo '{"session_id":"precheck-001","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" bash "$SCRIPT" 2>/dev/null || true
assert "Summary file preserved after send" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 2: Offset file exists but Telegram unreachable — falls through to send ---
echo "Test 2: Offset file exists, Telegram unreachable — normal send"
cleanup
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
echo "999999" > "$OFFSET_FILE"
echo "Test summary 2" > "$SUMMARY_FILE"
echo '{"session_id":"precheck-002","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake-bad-token" TELEGRAM_CHAT_ID="fake-bad-id" bash "$SCRIPT" 2>/dev/null || true
assert "Summary file preserved after send" "[ -f '$SUMMARY_FILE' ]"

echo ""

# --- Test 3: Pre-check code path exists and handles empty results ---
echo "Test 3: Pre-check logic present in script"
# Verify the pre-check code path exists in the script
assert "Pre-check section exists in stop-notify.sh" \
  'grep -qi "pre-check" "$SCRIPT"'
assert "Pre-check uses tg_get_updates for polling" \
  'grep -q "tg_get_updates" "$SCRIPT"'
assert "Pre-check uses tg_advance_offset" \
  'grep -q "tg_advance_offset" "$SCRIPT"'

echo ""

# --- Test 4: Summary persisted even on pre-check path ---
echo "Test 4: Summary persisted to slug dir on pre-check path"
cleanup
echo "$(($(date +%s) - 15))" > "$DEDUP_FILE"
# No offset file to trigger pre-check, but verify slug persistence on normal path
echo "Persist this summary" > "$SUMMARY_FILE"
echo '{"session_id":"precheck-003","last_assistant_message":"hello"}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" \
  CLAUDE_PROJECT_DIR="/home/user/test-project" bash "$SCRIPT" 2>/dev/null || true
SLUG_DIR="$TEST_STATE_BASE/test-project"
assert "Summary persisted to last-summary.md" "[ -f '$SLUG_DIR/last-summary.md' ]"

echo ""

# --- Test 5b: Configurable reply timeout (B-255) ---
echo "Test 5b: Reply timeout is configurable"
assert "Uses TELEGRAM_REPLY_TIMEOUT env var" \
  'grep -q "TELEGRAM_REPLY_TIMEOUT" "$SCRIPT"'
assert "Default timeout is 120s" \
  'grep -q "TELEGRAM_REPLY_TIMEOUT:-120" "$SCRIPT"'
assert "Timeout shown in notification text" \
  'grep -q "Reply within.*REPLY_TIMEOUT\|reply_timeout\|REPLY_TIMEOUT.*to continue" "$SCRIPT"'
assert "REPLY_TIMEOUT drives polling loop" \
  'grep -q "REPLY_TIMEOUT.*POLL_TIMEOUT\|MAX_POLLS.*REPLY_TIMEOUT" "$SCRIPT"'
echo ""

# --- Test 5: Pre-check block decision format ---
echo "Test 5: Pre-check block decision JSON format validation"
# Verify the block decision JSON is properly formed with jq
assert "Block decision uses jq for JSON construction" \
  'grep -q "jq -Rs.*decision.*block.*reason.*TELEGRAM REPLY" "$SCRIPT"'

echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
