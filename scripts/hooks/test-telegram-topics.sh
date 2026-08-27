#!/bin/bash
# Tests for lib/telegram-topics.sh — topic CRUD + lifecycle
# Run: bash scripts/hooks/test-telegram-topics.sh
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

# Set up mock curl ONCE — response controlled via file
MOCK_RESPONSE_FILE="$TEST_BASE/mock-response.txt"
export MOCK_RESPONSE_FILE
echo '{"ok":true,"result":{}}' > "$MOCK_RESPONSE_FILE"

mkdir -p "$TEST_BASE/bin"
cat > "$TEST_BASE/bin/curl" << 'MOCKCURL'
#!/bin/bash
echo "$@" >> "${CURL_LOG}"
cat "${MOCK_RESPONSE_FILE}"
MOCKCURL
chmod +x "$TEST_BASE/bin/curl"
export PATH="$TEST_BASE/bin:$PATH"

set_mock_response() {
  echo "$1" > "$MOCK_RESPONSE_FILE"
}

cleanup() {
  rm -rf "$TEST_BASE"
}
trap cleanup EXIT

# Source all libs after mock is on PATH
source "$SCRIPT_DIR/lib/state-helpers.sh"
source "$SCRIPT_DIR/lib/telegram-api.sh"
source "$SCRIPT_DIR/lib/telegram-topics.sh"

echo "=== Telegram Topics Library Tests ==="
echo ""

# --- Test 1: topic_create stores topic_id ---
echo "Test 1: topic_create calls API and caches result"
set_mock_response '{"ok":true,"result":{"message_thread_id":42,"name":"claude-orchestra"}}'
> "$CURL_LOG"
ensure_state_dir "claude-orchestra" > /dev/null
RESULT=$(topic_create "claude-orchestra" "Claude Orchestra")
assert "Returns topic ID" '[ "$RESULT" = "42" ]'
assert "createForumTopic called" 'grep -q "createForumTopic" "$CURL_LOG"'
assert "Topic name sent" 'grep -q "Claude Orchestra" "$CURL_LOG"'
assert "Icon color sent" 'grep -q "9367192" "$CURL_LOG"'
assert "topic_id file created" '[ -f "$TEST_BASE/claude-orchestra/telegram/topic_id" ]'
STORED=$(cat "$TEST_BASE/claude-orchestra/telegram/topic_id")
assert "topic_id file contains 42" '[ "$STORED" = "42" ]'
assert "topic_name file created" '[ -f "$TEST_BASE/claude-orchestra/telegram/topic_name" ]'
echo ""

# --- Test 2: topic_lookup reads cached ID ---
echo "Test 2: topic_lookup reads from cache"
LOOKUP=$(topic_lookup "claude-orchestra")
assert "Returns cached ID" '[ "$LOOKUP" = "42" ]'
MISSING=$(topic_lookup "nonexistent-project")
assert "Returns empty for missing slug" '[ -z "$MISSING" ]'
echo ""

# --- Test 3: topic_ensure creates on first call, caches on second ---
echo "Test 3: topic_ensure — create + cache behavior"
set_mock_response '{"ok":true,"result":{"message_thread_id":42,"name":"claude-orchestra"}}'
> "$CURL_LOG"
# Remove cached topic to force creation
rm -f "$TEST_BASE/claude-orchestra/telegram/topic_id"
ENSURED=$(topic_ensure "claude-orchestra")
assert "Returns topic ID" '[ "$ENSURED" = "42" ]'
assert "API was called (first call)" 'grep -q "createForumTopic" "$CURL_LOG"'

# Second call should use cache
> "$CURL_LOG"
ENSURED2=$(topic_ensure "claude-orchestra")
assert "Returns cached ID (second call)" '[ "$ENSURED2" = "42" ]'
assert "API NOT called (cached)" '! grep -q "createForumTopic" "$CURL_LOG"'
echo ""

# --- Test 4: topic_ensure falls back to empty on failure ---
echo "Test 4: topic_ensure fallback on API failure"
set_mock_response '{"ok":false,"description":"Forbidden"}'
rm -f "$TEST_BASE/new-project/telegram/topic_id"
ensure_state_dir "new-project" > /dev/null
FALLBACK=$(topic_ensure "new-project")
assert "Falls back to empty (no topic)" '[ -z "$FALLBACK" ]'
echo ""

# --- Test 4b: topic_ensure returns empty for private chats ---
echo "Test 4b: topic_ensure skips topic creation for private chats"
SAVED_CHAT_ID="$TELEGRAM_CHAT_ID"
export TELEGRAM_CHAT_ID="8544101708"  # Positive = private chat
> "$CURL_LOG"
rm -f "$TEST_BASE/private-project/telegram/topic_id"
ensure_state_dir "private-project" > /dev/null
PRIVATE_RESULT=$(topic_ensure "private-project")
assert "Returns empty for private chat" '[ -z "$PRIVATE_RESULT" ]'
assert "No API call for private chat" '! grep -q "createForumTopic" "$CURL_LOG"'
export TELEGRAM_CHAT_ID="$SAVED_CHAT_ID"
echo ""

# --- Test 5: topic_close / topic_reopen ---
echo "Test 5: topic_close and topic_reopen"
set_mock_response '{"ok":true,"result":true}'
echo "42" > "$TEST_BASE/claude-orchestra/telegram/topic_id"
> "$CURL_LOG"
topic_close "claude-orchestra"
assert "closeForumTopic called" 'grep -q "closeForumTopic" "$CURL_LOG"'
assert "topic_closed marker created" '[ -f "$TEST_BASE/claude-orchestra/telegram/topic_closed" ]'

> "$CURL_LOG"
topic_reopen "claude-orchestra"
assert "reopenForumTopic called" 'grep -q "reopenForumTopic" "$CURL_LOG"'
assert "topic_closed marker removed" '[ ! -f "$TEST_BASE/claude-orchestra/telegram/topic_closed" ]'
echo ""

# --- Test 6: topic_rename ---
echo "Test 6: topic_rename calls editForumTopic"
> "$CURL_LOG"
topic_rename "claude-orchestra" "New Name"
assert "editForumTopic called" 'grep -q "editForumTopic" "$CURL_LOG"'
assert "New name sent" 'grep -q "New Name" "$CURL_LOG"'
echo ""

# --- Test 7: topic_archive renames then closes ---
echo "Test 7: topic_archive with Done and Paused"
> "$CURL_LOG"
topic_archive "claude-orchestra" "Done"
assert "Renamed with [Done] prefix" 'grep -q "editForumTopic" "$CURL_LOG"'
assert "closeForumTopic also called" 'grep -q "closeForumTopic" "$CURL_LOG"'
assert "topic_closed marker exists" '[ -f "$TEST_BASE/claude-orchestra/telegram/topic_closed" ]'

> "$CURL_LOG"
rm -f "$TEST_BASE/claude-orchestra/telegram/topic_closed"
topic_archive "claude-orchestra" "Paused"
RENAME_CALL=$(grep "editForumTopic" "$CURL_LOG" || true)
assert "Renamed with [Paused] prefix" '[[ "$RENAME_CALL" == *"Paused"* ]]'
echo ""

# --- Test 8: topic_delete removes state ---
echo "Test 8: topic_delete cleans up"
> "$CURL_LOG"
topic_delete "claude-orchestra"
assert "deleteForumTopic called" 'grep -q "deleteForumTopic" "$CURL_LOG"'
assert "topic_id file removed" '[ ! -f "$TEST_BASE/claude-orchestra/telegram/topic_id" ]'
assert "topic_name file removed" '[ ! -f "$TEST_BASE/claude-orchestra/telegram/topic_name" ]'
echo ""

# --- Test 9: topic_find_slug_by_id reverse lookup ---
echo "Test 9: topic_find_slug_by_id"
mkdir -p "$TEST_BASE/project-a/telegram" "$TEST_BASE/project-b/telegram"
echo "100" > "$TEST_BASE/project-a/telegram/topic_id"
echo "200" > "$TEST_BASE/project-b/telegram/topic_id"

FOUND_A=$(topic_find_slug_by_id "100")
assert "Finds project-a by topic 100" '[ "$FOUND_A" = "project-a" ]'
FOUND_B=$(topic_find_slug_by_id "200")
assert "Finds project-b by topic 200" '[ "$FOUND_B" = "project-b" ]'
FOUND_NONE=$(topic_find_slug_by_id "999")
assert "Returns empty for unknown topic" '[ -z "$FOUND_NONE" ]'
echo ""

# --- Test 10: topic_ensure_named — rename triggered on name change ---
echo "Test 10: topic_ensure_named renames when display name changes"
set_mock_response '{"ok":true,"result":{"message_thread_id":42,"name":"claude-orchestra"}}'
# Set up cached topic
mkdir -p "$TEST_BASE/naming-test/telegram"
echo "42" > "$TEST_BASE/naming-test/telegram/topic_id"
echo "Old Name" > "$TEST_BASE/naming-test/telegram/topic_name"
> "$CURL_LOG"

RESULT=$(topic_ensure_named "naming-test" "Orchestra — M2 Evolution")
assert "Returns topic ID" '[ "$RESULT" = "42" ]'
assert "editForumTopic called for rename" 'grep -q "editForumTopic" "$CURL_LOG"'
assert "New name sent" 'grep -q "Orchestra" "$CURL_LOG"'
# Verify cached name updated
CACHED_NAME=$(cat "$TEST_BASE/naming-test/telegram/topic_name" 2>/dev/null)
assert "Cached name updated" '[ "$CACHED_NAME" = "Orchestra — M2 Evolution" ]'
echo ""

# --- Test 11: topic_ensure_named — no rename when cached matches ---
echo "Test 11: topic_ensure_named skips rename when name matches"
echo "Orchestra — M2 Evolution" > "$TEST_BASE/naming-test/telegram/topic_name"
> "$CURL_LOG"
RESULT=$(topic_ensure_named "naming-test" "Orchestra — M2 Evolution")
assert "Returns topic ID" '[ "$RESULT" = "42" ]'
assert "No API call when name matches" '! grep -q "editForumTopic" "$CURL_LOG"'
echo ""

# --- Test 12: topic_ensure_named — empty desired_name skips rename ---
echo "Test 12: topic_ensure_named with empty desired_name"
> "$CURL_LOG"
RESULT=$(topic_ensure_named "naming-test" "")
assert "Returns topic ID" '[ "$RESULT" = "42" ]'
assert "No rename for empty desired_name" '! grep -q "editForumTopic" "$CURL_LOG"'
echo ""

# --- Test 13: topic_ensure_named — stores current_branch ---
echo "Test 13: topic_ensure_named stores current_branch"
RESULT=$(topic_ensure_named "naming-test" "Orchestra — New Feature" "feature/new-feature")
STORED_BRANCH=$(cat "$TEST_BASE/naming-test/telegram/current_branch" 2>/dev/null)
assert "current_branch stored" '[ "$STORED_BRANCH" = "feature/new-feature" ]'
echo ""

# --- Test 14: topic_ensure_named — private chat skips ---
echo "Test 14: topic_ensure_named returns empty for private chat"
SAVED_CHAT_ID="$TELEGRAM_CHAT_ID"
export TELEGRAM_CHAT_ID="8544101708"
> "$CURL_LOG"
RESULT=$(topic_ensure_named "naming-test" "Orchestra — Task")
assert "Returns empty for private chat" '[ -z "$RESULT" ]'
assert "No API calls for private chat" '[ ! -s "$CURL_LOG" ]'
export TELEGRAM_CHAT_ID="$SAVED_CHAT_ID"
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
