#!/bin/bash
# Tests for permission-relay.sh — PermissionRequest → Telegram relay (B-254)
# Run: bash scripts/hooks/test-permission-relay.sh
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
export TELEGRAM_BOT_TOKEN="test-token-123"
export TELEGRAM_CHAT_ID="-1003326289132"
CURL_LOG="$TEST_BASE/curl-calls.log"
export CURL_LOG
MOCK_RESPONSE_FILE="$TEST_BASE/mock-response.txt"
export MOCK_RESPONSE_FILE

# Set up mock curl — default returns topic creation success
echo '{"ok":true,"result":{"message_thread_id":42,"name":"test"}}' > "$MOCK_RESPONSE_FILE"
mkdir -p "$TEST_BASE/bin"
cat > "$TEST_BASE/bin/curl" << 'MOCKCURL'
#!/bin/bash
echo "$@" >> "${CURL_LOG}"
cat "${MOCK_RESPONSE_FILE}"
MOCKCURL
chmod +x "$TEST_BASE/bin/curl"
export PATH="$TEST_BASE/bin:$PATH"

cleanup() {
  rm -rf "$TEST_BASE"
  rm -f /tmp/telegram-decision-*
}
trap cleanup EXIT

SCRIPT="$SCRIPT_DIR/permission-relay.sh"

echo "=== Permission Relay Tests ==="
echo ""

# --- Test 1: Script exists and is executable ---
echo "Test 1: permission-relay.sh structure"
assert "Script exists" '[ -f "$SCRIPT" ]'
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT"'
assert "Sources telegram-keyboards.sh" 'grep -q "telegram-keyboards.sh" "$SCRIPT"'
assert "Uses topic_ensure" 'grep -q "topic_ensure" "$SCRIPT"'
assert "Uses kb_approval" 'grep -q "kb_approval" "$SCRIPT"'
assert "Uses tg_send_with_keyboard" 'grep -q "tg_send_with_keyboard" "$SCRIPT"'
echo ""

# --- Test 2: Output matches PermissionRequest schema ---
echo "Test 2: Allow decision output format"
assert "Outputs hookSpecificOutput JSON" 'grep -q "hookSpecificOutput" "$SCRIPT"'
assert "Outputs hookEventName PermissionRequest" 'grep -q "PermissionRequest" "$SCRIPT"'
assert "Decision behavior: allow" 'grep -q '"'"'allow'"'"' "$SCRIPT"'
echo ""

# --- Test 3: Reads stdin JSON for tool info ---
echo "Test 3: Input parsing"
assert "Reads tool_name from input" 'grep -q "tool_name" "$SCRIPT"'
assert "Reads tool_input from input" 'grep -q "tool_input" "$SCRIPT"'
echo ""

# --- Test 4: Timeout behavior ---
echo "Test 4: Timeout exits cleanly"
assert "Has timeout logic" 'grep -qE "timeout|TIMEOUT|max_poll" "$SCRIPT"'
assert "Exits 0 on timeout (fall through to terminal)" 'grep -q "exit 0" "$SCRIPT"'
echo ""

# --- Test 5: Callback parsing from updates ---
echo "Test 5: Callback query processing"
assert "Checks for callback_query in updates" 'grep -q "callback_query" "$SCRIPT"'
assert "Uses tg_answer_callback" 'grep -q "tg_answer_callback" "$SCRIPT"'
echo ""

# --- Test 6: Text messages saved to inbox during polling ---
echo "Test 6: Stashes text messages during permission poll"
assert "Saves text messages to inbox" 'grep -q "inbox" "$SCRIPT"'
echo ""

# --- Test 7: Message construction for different tools ---
echo "Test 7: Human-readable permission description"
assert "Formats Bash commands" 'grep -q "Bash\|command" "$SCRIPT"'
assert "Includes tool name in message" 'grep -q "tool_name\|TOOL_NAME" "$SCRIPT"'
echo ""

# --- Test 8: User-level settings has PermissionRequest hook (B-259) ---
echo "Test 8: Hook registered in settings"
SETTINGS="$HOME/.claude/settings.json"
assert "PermissionRequest in settings" 'grep -q "PermissionRequest" "$SETTINGS"'
assert "permission-relay.sh referenced" 'grep -q "permission-relay.sh" "$SETTINGS"'
assert "300s timeout" 'grep -q "300" "$SETTINGS"'
echo ""

# --- Test 9: Skips AskUserQuestion (handled by askuser-relay.sh) ---
echo "Test 9: AskUserQuestion skipped"
assert "Has AskUserQuestion skip guard" 'grep -q "AskUserQuestion.*exit 0" "$SCRIPT"'
# Functional test: feed AskUserQuestion input, should produce no output
AQ_OUTPUT=$(echo '{"tool_name":"AskUserQuestion","tool_input":{"questions":[]}}' | \
  TELEGRAM_BOT_TOKEN="fake" TELEGRAM_CHAT_ID="fake" \
  SESSION_STATE_BASE="$TEST_BASE" CLAUDE_PROJECT_DIR="/tmp" \
  bash "$SCRIPT" 3>&1 2>/dev/null || true)
assert "No output for AskUserQuestion" '[ -z "$AQ_OUTPUT" ]'
echo ""

# --- Test 10: ExitPlanMode reads plan file instead of dumping raw JSON (G89) ---
echo "Test 10: ExitPlanMode shows plan content"
assert "Has ExitPlanMode case in description builder" 'grep -q "ExitPlanMode)" "$SCRIPT"'
assert "Reads plan file from ~/.claude/plans/" 'grep -q "\.claude/plans/" "$SCRIPT"'
assert "Shows Plan Ready for Review header" 'grep -q "Plan Ready for Review" "$SCRIPT"'
assert "Does NOT skip ExitPlanMode" '! grep -q "ExitPlanMode.*&&.*exit 0" "$SCRIPT"'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
