#!/bin/bash
# Tests for lib/telegram-keyboards.sh — inline keyboard construction + callbacks
# Run: bash scripts/hooks/test-inline-keyboards.sh
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

cleanup() {
  rm -rf "$TEST_BASE"
  rm -f /tmp/telegram-decision-*
}
trap cleanup EXIT

# Source libs
source "$SCRIPT_DIR/lib/state-helpers.sh"
export TELEGRAM_BOT_TOKEN="test-token"
export TELEGRAM_CHAT_ID="12345"
source "$SCRIPT_DIR/lib/telegram-api.sh"
source "$SCRIPT_DIR/lib/telegram-keyboards.sh"

echo "=== Inline Keyboard Library Tests ==="
echo ""

# --- Test 1: kb_approval generates valid JSON ---
echo "Test 1: kb_approval keyboard structure"
KB=$(kb_approval "a1b2c3d4")
assert "Valid JSON" 'echo "$KB" | jq . > /dev/null 2>&1'
assert "Has inline_keyboard key" 'echo "$KB" | jq -e ".inline_keyboard" > /dev/null 2>&1'
assert "Approve button exists" '[[ "$KB" == *"Approve"* ]]'
assert "Deny button exists" '[[ "$KB" == *"Deny"* ]]'
assert "Skip button exists" '[[ "$KB" == *"Skip"* ]]'
assert "Callback data: approve:a1b2c3d4" '[[ "$KB" == *"approve:a1b2c3d4"* ]]'
assert "Callback data: deny:a1b2c3d4" '[[ "$KB" == *"deny:a1b2c3d4"* ]]'
assert "Callback data: skip:a1b2c3d4" '[[ "$KB" == *"skip:a1b2c3d4"* ]]'

# Callback data under 64 bytes
APPROVE_DATA=$(echo "$KB" | jq -r '.inline_keyboard[0][0].callback_data')
assert "Callback data under 64 bytes" '[ ${#APPROVE_DATA} -lt 64 ]'
echo ""

# --- Test 2: kb_options generates per-option buttons ---
echo "Test 2: kb_options dynamic buttons"
KB=$(kb_options "f0f0f0f0" "Option A" "Option B" "Option C")
assert "Valid JSON" 'echo "$KB" | jq . > /dev/null 2>&1'
assert "Has 3 rows" '[ "$(echo "$KB" | jq ".inline_keyboard | length")" = "3" ]'
assert "opt:f0f0f0f0:0 for first" '[[ "$KB" == *"opt:f0f0f0f0:0"* ]]'
assert "opt:f0f0f0f0:1 for second" '[[ "$KB" == *"opt:f0f0f0f0:1"* ]]'
assert "opt:f0f0f0f0:2 for third" '[[ "$KB" == *"opt:f0f0f0f0:2"* ]]'
echo ""

# --- Test 3: kb_yesno ---
echo "Test 3: kb_yesno keyboard"
KB=$(kb_yesno "deadbeef")
assert "Valid JSON" 'echo "$KB" | jq . > /dev/null 2>&1'
assert "Yes button" '[[ "$KB" == *"\"Yes\""* ]]'
assert "No button" '[[ "$KB" == *"\"No\""* ]]'
assert "yes:deadbeef callback" '[[ "$KB" == *"yes:deadbeef"* ]]'
assert "no:deadbeef callback" '[[ "$KB" == *"no:deadbeef"* ]]'
echo ""

# --- Test 4: cb_parse extracts action and request_id ---
echo "Test 4: cb_parse"
PARSED=$(cb_parse "approve:a1b2c3d4")
ACTION=$(echo "$PARSED" | head -1)
REQ_ID=$(echo "$PARSED" | tail -1)
assert "Action is approve" '[ "$ACTION" = "approve" ]'
assert "Request ID is a1b2c3d4" '[ "$REQ_ID" = "a1b2c3d4" ]'

PARSED2=$(cb_parse "opt:f0f0f0f0:2")
ACTION2=$(echo "$PARSED2" | head -1)
REST2=$(echo "$PARSED2" | tail -1)
assert "Action is opt" '[ "$ACTION2" = "opt" ]'
assert "Rest contains request_id and index" '[ "$REST2" = "f0f0f0f0:2" ]'
echo ""

# --- Test 5: cb_generate_id produces 8-char hex ---
echo "Test 5: cb_generate_id"
ID=$(cb_generate_id)
assert "8 chars long" '[ ${#ID} -eq 8 ]'
assert "Hex chars only" '[[ "$ID" =~ ^[0-9a-f]{8}$ ]]'
ID2=$(cb_generate_id)
assert "Different each time" '[ "$ID" != "$ID2" ]'
echo ""

# --- Test 6: cb_write_decision + cb_read_decision lifecycle ---
echo "Test 6: Decision file lifecycle"
ensure_state_dir "test-project" > /dev/null
cb_write_decision "abc12345" "approve" "test-project"
assert "/tmp file created" '[ -f "/tmp/telegram-decision-abc12345" ]'
assert "State dir file created" '[ -f "$TEST_BASE/test-project/telegram/decisions/abc12345" ]'
CONTENT=$(cat "/tmp/telegram-decision-abc12345")
assert "Contains 'approve'" '[ "$CONTENT" = "approve" ]'

# Read removes the file
DECISION=$(cb_read_decision "abc12345" 1)
assert "Read returns approve" '[ "$DECISION" = "approve" ]'
assert "/tmp file removed after read" '[ ! -f "/tmp/telegram-decision-abc12345" ]'
echo ""

# --- Test 7: cb_read_decision timeout ---
echo "Test 7: Decision timeout"
TIMEOUT_RESULT=$(cb_read_decision "nonexistent" 1 2>/dev/null || true)
assert "Returns timeout" '[ "$TIMEOUT_RESULT" = "timeout" ]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
