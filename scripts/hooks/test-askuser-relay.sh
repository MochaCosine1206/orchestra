#!/bin/bash
# Tests for askuser-relay.sh — AskUserQuestion answer injection via PermissionRequest
# Run: bash scripts/hooks/test-askuser-relay.sh
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
export TELEGRAM_NOTIFY_CONFIG="/dev/null"
CURL_LOG="$TEST_BASE/curl-calls.log"
export CURL_LOG

# Mock response control
MOCK_RESPONSE_FILE="$TEST_BASE/mock-response.txt"
export MOCK_RESPONSE_FILE
echo '{"ok":true,"result":{}}' > "$MOCK_RESPONSE_FILE"

# Track call count
MOCK_CALL_COUNT="$TEST_BASE/mock-call-count"
echo "0" > "$MOCK_CALL_COUNT"

# Callback response to return on getUpdates calls
MOCK_CALLBACK_FILE="$TEST_BASE/mock-callback.txt"
export MOCK_CALLBACK_FILE
echo '{"ok":true,"result":[]}' > "$MOCK_CALLBACK_FILE"

mkdir -p "$TEST_BASE/bin"
# File for capturing the request ID from the first sendMessage call
CAPTURED_REQ_ID="$TEST_BASE/captured-req-id"
export CAPTURED_REQ_ID

cat > "$TEST_BASE/bin/curl" << 'MOCKCURL'
#!/bin/bash
echo "$@" >> "${CURL_LOG}"
# Count calls
COUNT=$(cat "$MOCK_CALL_COUNT" 2>/dev/null || echo 0)
COUNT=$((COUNT + 1))
echo "$COUNT" > "$MOCK_CALL_COUNT"

# Detect if this is a getUpdates call
if echo "$@" | grep -q "getUpdates"; then
  # Extract the captured request ID and build a matching callback
  REQ_ID=$(cat "$CAPTURED_REQ_ID" 2>/dev/null || echo "")
  if [ -n "$REQ_ID" ]; then
    # Return a callback selecting option index 1
    echo "{\"ok\":true,\"result\":[{\"update_id\":1001,\"callback_query\":{\"id\":\"cb123\",\"data\":\"aq:${REQ_ID}:0:1\",\"message\":{\"chat\":{\"id\":-1003326289132},\"message_id\":99}}}]}"
  else
    echo '{"ok":true,"result":[]}'
  fi
elif echo "$@" | grep -q "sendMessage"; then
  # Capture the request ID from callback_data in the keyboard
  REQ_ID=$(echo "$@" | grep -o 'aq:[a-f0-9]*' | head -1 | sed 's/aq://')
  if [ -n "$REQ_ID" ]; then
    echo "$REQ_ID" > "$CAPTURED_REQ_ID"
  fi
  echo '{"ok":true,"result":{"message_id":99}}'
elif echo "$@" | grep -q "answerCallbackQuery\|editMessageText"; then
  echo '{"ok":true,"result":true}'
else
  cat "${MOCK_RESPONSE_FILE}"
fi
MOCKCURL
chmod +x "$TEST_BASE/bin/curl"
export PATH="$TEST_BASE/bin:$PATH"

# Mock git for branch detection
cat > "$TEST_BASE/bin/git" << 'MOCKGIT'
#!/bin/bash
if [ "$1" = "branch" ] && [ "$2" = "--show-current" ]; then
  echo "feature/test-branch"
elif [ "$1" = "rev-parse" ]; then
  echo "/tmp/fake-git-dir"
  exit 0
else
  /usr/bin/git "$@"
fi
MOCKGIT
chmod +x "$TEST_BASE/bin/git"

set_mock_response() {
  echo "$1" > "$MOCK_RESPONSE_FILE"
}

set_mock_callback() {
  echo "$1" > "$MOCK_CALLBACK_FILE"
}

cleanup() {
  rm -rf "$TEST_BASE"
}
trap cleanup EXIT

echo "=== AskUserQuestion Relay Tests ==="
echo ""

# --- Test 1: Non-AskUserQuestion tool is passed through ---
echo "Test 1: Non-AskUserQuestion tool passes through"
> "$CURL_LOG"
echo "0" > "$MOCK_CALL_COUNT"
OUTPUT=$(echo '{"tool_name":"Bash","tool_input":{"command":"ls"}}' | \
  bash "$SCRIPT_DIR/askuser-relay.sh" 2>/dev/null || true)
assert "No Telegram API calls" '[ "$(cat "$MOCK_CALL_COUNT")" = "0" ]'
assert "No output (pass through)" '[ -z "$OUTPUT" ]'
echo ""

# --- Test 2: Empty questions array passes through ---
echo "Test 2: Empty questions array"
> "$CURL_LOG"
echo "0" > "$MOCK_CALL_COUNT"
OUTPUT=$(echo '{"tool_name":"AskUserQuestion","tool_input":{"questions":[]}}' | \
  bash "$SCRIPT_DIR/askuser-relay.sh" 2>/dev/null || true)
assert "No API calls for empty questions" '[ "$(cat "$MOCK_CALL_COUNT")" = "0" ]'
echo ""

# --- Test 3: Script sources correct libraries ---
echo "Test 3: Script sources shared libraries"
assert "Sources state-helpers.sh" 'grep -q "state-helpers.sh" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Sources telegram-keyboards.sh" 'grep -q "telegram-keyboards.sh" "$SCRIPT_DIR/askuser-relay.sh"'
echo ""

# --- Test 4: Output format is correct PermissionRequest JSON ---
echo "Test 4: Output format is valid PermissionRequest JSON"
> "$CURL_LOG"
echo "0" > "$MOCK_CALL_COUNT"
echo "" > "$CAPTURED_REQ_ID"

# Set up topic state
mkdir -p "$TEST_BASE/claude-orchestra/telegram"
echo "42" > "$TEST_BASE/claude-orchestra/telegram/topic_id"
echo "Orchestra" > "$TEST_BASE/claude-orchestra/telegram/topic_name"

export CLAUDE_PROJECT_DIR="/Users/user/claude-orchestra"
OUTPUT=$(echo '{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which approach?","header":"Approach","multiSelect":false,"options":[{"label":"Option A","description":"First approach"},{"label":"Option B","description":"Second approach"}]}]}}' | \
  bash "$SCRIPT_DIR/askuser-relay.sh" 2>/dev/null || true)

# Validate output structure
assert "Output is valid JSON" 'echo "$OUTPUT" | jq . >/dev/null 2>&1'
assert "Has hookSpecificOutput" 'echo "$OUTPUT" | jq -e ".hookSpecificOutput" >/dev/null 2>&1'
assert "Has hookEventName=PermissionRequest" '[ "$(echo "$OUTPUT" | jq -r ".hookSpecificOutput.hookEventName")" = "PermissionRequest" ]'
assert "Has decision.behavior=allow" '[ "$(echo "$OUTPUT" | jq -r ".hookSpecificOutput.decision.behavior")" = "allow" ]'
assert "Has updatedInput.questions" 'echo "$OUTPUT" | jq -e ".hookSpecificOutput.decision.updatedInput.questions" >/dev/null 2>&1'
assert "Has updatedInput.answers" 'echo "$OUTPUT" | jq -e ".hookSpecificOutput.decision.updatedInput.answers" >/dev/null 2>&1'
# Option B is index 1
ANSWER=$(echo "$OUTPUT" | jq -r '.hookSpecificOutput.decision.updatedInput.answers["Which approach?"]' 2>/dev/null)
assert "Answer is Option B (index 1)" '[ "$ANSWER" = "Option B" ]'
echo ""

# --- Test 5: Keyboard is sent to Telegram ---
echo "Test 5: Inline keyboard sent to Telegram"
assert "sendMessage called" 'grep -q "sendMessage" "$CURL_LOG"'
assert "Reply markup included" 'grep -q "reply_markup" "$CURL_LOG"'
assert "Callback data includes aq: prefix" 'grep -q "aq:" "$CURL_LOG"'
echo ""

# --- Test 6: Script uses fd 3 for output (stdout safety) ---
echo "Test 6: Script safety — fd 3 redirect"
assert "Redirects stdout to stderr (exec 3>&1 1>&2)" 'grep -q "exec 3>&1 1>&2" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Final output goes to fd 3" 'grep -q ">&3" "$SCRIPT_DIR/askuser-relay.sh"'
echo ""

# --- Test 7: Uses topic_ensure_named ---
echo "Test 7: Uses branch-based topic naming"
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Detects branch" 'grep -q "git branch --show-current" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/askuser-relay.sh"'
echo ""

# --- Test 8: User-level settings has AskUserQuestion matcher (B-259) ---
echo "Test 8: Hook configuration"
SETTINGS="$HOME/.claude/settings.json"
assert "PermissionRequest has AskUserQuestion matcher" 'grep -q "\"matcher\": \"AskUserQuestion\"" "$SETTINGS" || grep -q "\"matcher\":\"AskUserQuestion\"" "$SETTINGS"'
assert "askuser-relay.sh registered" 'grep -q "askuser-relay.sh" "$SETTINGS"'
assert "No timeout (86400s)" 'grep -A5 "askuser-relay" "$SETTINGS" | grep -q "86400"'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
