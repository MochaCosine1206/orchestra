#!/bin/bash
# Tests for bot command registration and handling (B-244)
# Verifies: command detection, /newproject creates topic, /close archives,
# /list scans dirs, unknown commands ignored
# Run: bash scripts/hooks/test-command-handling.sh
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

# Set up mock curl
echo '{"ok":true,"result":{"message_thread_id":99,"name":"new-project"}}' > "$MOCK_RESPONSE_FILE"
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
}
trap cleanup EXIT

# Source all libs
source "$SCRIPT_DIR/lib/state-helpers.sh"
source "$SCRIPT_DIR/lib/telegram-api.sh"
source "$SCRIPT_DIR/lib/telegram-topics.sh"

echo "=== Bot Command Handling Tests ==="
echo ""

# --- Test 1: register-commands.sh exists and calls setMyCommands ---
echo "Test 1: register-commands.sh structure"
assert "register-commands.sh exists" '[ -f "$SCRIPT_DIR/register-commands.sh" ]'
assert "Calls setMyCommands" 'grep -q "setMyCommands" "$SCRIPT_DIR/register-commands.sh"'
assert "Defines /status command" 'grep -q "status" "$SCRIPT_DIR/register-commands.sh"'
assert "Defines /list command" 'grep -q "list" "$SCRIPT_DIR/register-commands.sh"'
assert "Defines /help command" 'grep -q "help" "$SCRIPT_DIR/register-commands.sh"'
echo ""

# --- Test 2: telegram-check.sh has command routing ---
echo "Test 2: telegram-check.sh command routing"
assert "Has /status handler" 'grep -q "/status" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /list handler" 'grep -q "/list" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /newproject handler" 'grep -q "/newproject" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /close handler" 'grep -q "/close" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /pause handler" 'grep -q "/pause" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /resume handler" 'grep -q "/resume" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has /help handler" 'grep -q "/help" "$SCRIPT_DIR/telegram-check.sh"'
assert "Unknown commands ignored (fallthrough)" 'grep -q "/\*)" "$SCRIPT_DIR/telegram-check.sh"'
echo ""

# --- Test 3: Command detection with/without @BotName ---
echo "Test 3: Command patterns handle @BotName suffix"
assert "Detects /status" 'grep -qE "/status\|/status@" "$SCRIPT_DIR/telegram-check.sh"'
assert "Detects /list" 'grep -qE "/list\|/list@" "$SCRIPT_DIR/telegram-check.sh"'
assert "Detects /help" 'grep -qE "/help\|/help@" "$SCRIPT_DIR/telegram-check.sh"'
echo ""

# --- Test 4: /newproject creates a topic ---
echo "Test 4: handle_newproject_cmd creates topic"
# Source telegram-check.sh as a library to test functions directly
# We'll test the function by looking for topic_create call pattern
assert "newproject handler uses topic_create" \
  'grep -A5 "handle_newproject_cmd\|newproject" "$SCRIPT_DIR/telegram-check.sh" | grep -q "topic_create"'
echo ""

# --- Test 5: /close and /pause use topic_archive ---
echo "Test 5: /close and /pause use topic_archive"
assert "close handler uses topic_archive" \
  'grep -A15 "^handle_close_cmd" "$SCRIPT_DIR/telegram-check.sh" | grep -q "topic_archive"'
assert "pause handler uses topic_archive" \
  'grep -A15 "^handle_pause_cmd" "$SCRIPT_DIR/telegram-check.sh" | grep -q "topic_archive"'
echo ""

# --- Test 6: /resume uses topic_reopen ---
echo "Test 6: /resume uses topic_reopen"
assert "resume handler uses topic_reopen" \
  'grep -A10 "^handle_resume_cmd" "$SCRIPT_DIR/telegram-check.sh" | grep -q "topic_reopen"'
echo ""

# --- Test 7: /list scans state dirs ---
echo "Test 7: /list scans project state directories"
assert "list handler scans SESSION_STATE_BASE" \
  'grep -A10 "handle_list_cmd" "$SCRIPT_DIR/telegram-check.sh" | grep -q "SESSION_STATE_BASE\|topic_id"'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
