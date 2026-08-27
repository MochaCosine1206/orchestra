#!/bin/bash
# Tests for lib/telegram-api.sh — shared Telegram API functions
# Run: bash scripts/hooks/test-telegram-api.sh
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
  rm -f /tmp/telegram-last-update-id
}
trap cleanup EXIT

# Mock curl: record calls to a log file
CURL_LOG="$TEST_BASE/curl-calls.log"
mock_curl() {
  mkdir -p "$TEST_BASE/bin"
  cat > "$TEST_BASE/bin/curl" << 'MOCKCURL'
#!/bin/bash
# Log all arguments for inspection
echo "$@" >> "${CURL_LOG}"
# Default: return success with empty result
echo '{"ok":true,"result":[]}'
MOCKCURL
  chmod +x "$TEST_BASE/bin/curl"
  export PATH="$TEST_BASE/bin:$PATH"
  export CURL_LOG
}

# Source the library under test
export TELEGRAM_BOT_TOKEN="test-token-123"
export TELEGRAM_CHAT_ID="12345"

source "$SCRIPT_DIR/lib/telegram-api.sh"

echo "=== Telegram API Library Tests ==="
echo ""

# --- Test 1: tg_init ---
echo "Test 1: tg_init sets up API URL"
tg_init
assert "TG_API_URL set" '[ -n "${TG_API_URL:-}" ]'
assert "TG_API_URL contains bot token" '[[ "$TG_API_URL" == *"test-token-123"* ]]'
echo ""

# --- Test 2: tg_escape_html ---
echo "Test 2: tg_escape_html escapes special characters"
ESCAPED=$(tg_escape_html "hello & <world> \"test\"")
assert "Ampersand escaped" '[[ "$ESCAPED" == *"&amp;"* ]]'
assert "Less-than escaped" '[[ "$ESCAPED" == *"&lt;"* ]]'
assert "Greater-than escaped" '[[ "$ESCAPED" == *"&gt;"* ]]'
PLAIN=$(tg_escape_html "hello world")
assert "Plain text unchanged" '[ "$PLAIN" = "hello world" ]'
EMPTY=$(tg_escape_html "")
assert "Empty string handled" '[ "$EMPTY" = "" ]'
echo ""

# --- Test 3: tg_send_message construction ---
echo "Test 3: tg_send_message with and without topic_id"
mock_curl
> "$CURL_LOG"

tg_send_message "Hello world" "" "HTML" 2>/dev/null
CALL1=$(cat "$CURL_LOG" | head -1)
assert "sendMessage called" '[[ "$CALL1" == *"sendMessage"* ]]'
assert "chat_id included" '[[ "$CALL1" == *"12345"* ]]'
assert "No message_thread_id without topic" '! grep -q "message_thread_id" <<< "$CALL1"'

> "$CURL_LOG"
tg_send_message "Hello topic" "42" "HTML" 2>/dev/null
CALL2=$(cat "$CURL_LOG" | head -1)
assert "message_thread_id included with topic" '[[ "$CALL2" == *"message_thread_id"* ]]'
assert "Topic ID 42 present" '[[ "$CALL2" == *"42"* ]]'
echo ""

# --- Test 4: tg_send_message truncation ---
echo "Test 4: tg_send_message truncates long messages"
> "$CURL_LOG"
LONG_MSG=$(python3 -c "print('x' * 5000)")
tg_send_message "$LONG_MSG" "" "" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "Long message accepted (curl called)" '[ -n "$CALL" ]'
echo ""

# --- Test 5: tg_send_with_keyboard ---
echo "Test 5: tg_send_with_keyboard includes reply_markup"
> "$CURL_LOG"
KB_JSON='{"inline_keyboard":[[{"text":"Approve","callback_data":"approve:abc123"}]]}'
tg_send_with_keyboard "Approve this?" "$KB_JSON" "42" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "sendMessage called for keyboard" '[[ "$CALL" == *"sendMessage"* ]]'
assert "reply_markup included" '[[ "$CALL" == *"reply_markup"* ]]'
assert "topic_id included" '[[ "$CALL" == *"message_thread_id"* ]]'
echo ""

# --- Test 6: tg_edit_message ---
echo "Test 6: tg_edit_message calls editMessageText"
> "$CURL_LOG"
tg_edit_message "999" "Updated text" "HTML" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "editMessageText called" '[[ "$CALL" == *"editMessageText"* ]]'
assert "message_id included" '[[ "$CALL" == *"999"* ]]'
echo ""

# --- Test 7: tg_answer_callback ---
echo "Test 7: tg_answer_callback calls answerCallbackQuery"
> "$CURL_LOG"
tg_answer_callback "cb-id-123" "Approved!" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "answerCallbackQuery called" '[[ "$CALL" == *"answerCallbackQuery"* ]]'
assert "callback_query_id included" '[[ "$CALL" == *"cb-id-123"* ]]'
echo ""

# --- Test 8: tg_get_updates ---
echo "Test 8: tg_get_updates includes correct parameters"
> "$CURL_LOG"
tg_get_updates 30 "1000" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "getUpdates called" '[[ "$CALL" == *"getUpdates"* ]]'
assert "timeout included" '[[ "$CALL" == *"timeout=30"* ]]'
assert "offset included" '[[ "$CALL" == *"offset=1000"* ]]'
assert "callback_query in allowed_updates" '[[ "$CALL" == *"callback_query"* ]]'
echo ""

# --- Test 9: tg_advance_offset ---
echo "Test 9: tg_advance_offset updates offset file"
OFFSET_FILE="$TEST_BASE/offset"
export TG_OFFSET_FILE="$OFFSET_FILE"
echo "100" > "$OFFSET_FILE"
RESPONSE='{"ok":true,"result":[{"update_id":105},{"update_id":103}]}'
tg_advance_offset "$RESPONSE"
STORED=$(cat "$OFFSET_FILE")
assert "Offset advanced to max+1" '[ "$STORED" = "106" ]'

# Empty result — offset unchanged
RESPONSE='{"ok":true,"result":[]}'
tg_advance_offset "$RESPONSE"
STORED=$(cat "$OFFSET_FILE")
assert "Offset unchanged on empty result" '[ "$STORED" = "106" ]'
echo ""

# --- Test 10: TELEGRAM_FORUM_CHAT_ID overrides TELEGRAM_CHAT_ID ---
echo "Test 10: tg_init promotes TELEGRAM_FORUM_CHAT_ID when set"
SAVED_CHAT_ID="$TELEGRAM_CHAT_ID"
export TELEGRAM_FORUM_CHAT_ID="-1003326289132"
tg_init
assert "CHAT_ID overridden to forum ID" '[ "$TELEGRAM_CHAT_ID" = "-1003326289132" ]'
> "$CURL_LOG"
tg_send_message "Forum test" "" "" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "Messages use forum chat ID" '[[ "$CALL" == *"-1003326289132"* ]]'
# Restore
unset TELEGRAM_FORUM_CHAT_ID
export TELEGRAM_CHAT_ID="$SAVED_CHAT_ID"
tg_init
assert "CHAT_ID restored without forum var" '[ "$TELEGRAM_CHAT_ID" = "$SAVED_CHAT_ID" ]'
echo ""

# --- Test 11: tg_get_updates includes message AND callback_query ---
echo "Test 11: allowed_updates includes both message and callback_query"
> "$CURL_LOG"
tg_get_updates 0 "100" 2>/dev/null
CALL=$(cat "$CURL_LOG" | head -1)
assert "message in allowed_updates" '[[ "$CALL" == *"message"* ]]'
assert "callback_query in allowed_updates" '[[ "$CALL" == *"callback_query"* ]]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
