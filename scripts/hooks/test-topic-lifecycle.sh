#!/bin/bash
# Tests for cleanup-topics.sh — 30-day topic cleanup (B-245)
# Run: bash scripts/hooks/test-topic-lifecycle.sh
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
export TELEGRAM_NOTIFY_CONFIG=/dev/null
CURL_LOG="$TEST_BASE/curl-calls.log"
export CURL_LOG
MOCK_RESPONSE_FILE="$TEST_BASE/mock-response.txt"
export MOCK_RESPONSE_FILE

# Set up mock curl
echo '{"ok":true,"result":true}' > "$MOCK_RESPONSE_FILE"
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

# Source libs
source "$SCRIPT_DIR/lib/state-helpers.sh"
source "$SCRIPT_DIR/lib/telegram-api.sh"
source "$SCRIPT_DIR/lib/telegram-topics.sh"

echo "=== Topic Lifecycle & Cleanup Tests ==="
echo ""

# --- Test 1: cleanup-topics.sh exists ---
echo "Test 1: cleanup-topics.sh structure"
assert "Script exists" '[ -f "$SCRIPT_DIR/cleanup-topics.sh" ]'
assert "Sources state-helpers.sh" 'grep -q "state-helpers.sh" "$SCRIPT_DIR/cleanup-topics.sh"'
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/cleanup-topics.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/cleanup-topics.sh"'
echo ""

# --- Test 2: Old closed topics get cleaned up ---
echo "Test 2: Cleanup deletes old closed topics (>30 days)"
# Create old-project with topic_closed mtime >30 days ago
mkdir -p "$TEST_BASE/old-project/telegram"
echo "100" > "$TEST_BASE/old-project/telegram/topic_id"
echo "Old Project" > "$TEST_BASE/old-project/telegram/topic_name"
touch -t "202501010000" "$TEST_BASE/old-project/telegram/topic_closed"  # Jan 2025

> "$CURL_LOG"
bash "$SCRIPT_DIR/cleanup-topics.sh" 2>/dev/null
assert "deleteForumTopic called for old topic" 'grep -q "deleteForumTopic" "$CURL_LOG"'
assert "topic_id removed" '[ ! -f "$TEST_BASE/old-project/telegram/topic_id" ]'
assert "topic_name removed" '[ ! -f "$TEST_BASE/old-project/telegram/topic_name" ]'
assert "topic_closed removed" '[ ! -f "$TEST_BASE/old-project/telegram/topic_closed" ]'
echo ""

# --- Test 3: Recent closed topics are NOT cleaned up ---
echo "Test 3: Recent closed topics preserved"
mkdir -p "$TEST_BASE/recent-project/telegram"
echo "200" > "$TEST_BASE/recent-project/telegram/topic_id"
echo "Recent Project" > "$TEST_BASE/recent-project/telegram/topic_name"
touch "$TEST_BASE/recent-project/telegram/topic_closed"  # now = recent

> "$CURL_LOG"
bash "$SCRIPT_DIR/cleanup-topics.sh" 2>/dev/null
assert "No deleteForumTopic for recent topic" '! grep -q "deleteForumTopic" "$CURL_LOG"'
assert "topic_id preserved" '[ -f "$TEST_BASE/recent-project/telegram/topic_id" ]'
echo ""

# --- Test 4: Topics without topic_closed marker are NOT touched ---
echo "Test 4: Active topics (no topic_closed) untouched"
mkdir -p "$TEST_BASE/active-project/telegram"
echo "300" > "$TEST_BASE/active-project/telegram/topic_id"
echo "Active Project" > "$TEST_BASE/active-project/telegram/topic_name"

> "$CURL_LOG"
bash "$SCRIPT_DIR/cleanup-topics.sh" 2>/dev/null
assert "No API call for active topic" '! grep -q "300" "$CURL_LOG"'
assert "Active topic_id preserved" '[ -f "$TEST_BASE/active-project/telegram/topic_id" ]'
echo ""

# --- Test 5: API failure leaves state intact ---
echo "Test 5: API failure preserves state"
echo '{"ok":false,"description":"Forbidden"}' > "$MOCK_RESPONSE_FILE"
mkdir -p "$TEST_BASE/api-fail-project/telegram"
echo "400" > "$TEST_BASE/api-fail-project/telegram/topic_id"
echo "API Fail Project" > "$TEST_BASE/api-fail-project/telegram/topic_name"
touch -t "202501010000" "$TEST_BASE/api-fail-project/telegram/topic_closed"

bash "$SCRIPT_DIR/cleanup-topics.sh" 2>/dev/null
assert "State files preserved on API failure" '[ -f "$TEST_BASE/api-fail-project/telegram/topic_id" ]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
