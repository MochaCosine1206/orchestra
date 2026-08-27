#!/bin/bash
# Tests for topic routing in all 5 hooks
# Verifies: messages go to project topic, wrong-topic messages filtered,
# topic_ensure called, allowed_updates includes callback_query
# Run: bash scripts/hooks/test-topic-routing.sh
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

echo "=== Topic Routing Tests ==="
echo ""

# --- Test 1: stop-notify.sh sources shared libs ---
echo "Test 1: stop-notify.sh sources telegram-api.sh and telegram-topics.sh"
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/stop-notify.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/stop-notify.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/stop-notify.sh"'
assert "Uses tg_send_message" 'grep -q "tg_send_message" "$SCRIPT_DIR/stop-notify.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/stop-notify.sh"'
assert "Detects branch" 'grep -q "git branch --show-current" "$SCRIPT_DIR/stop-notify.sh"'
echo ""

# --- Test 2: telegram-check.sh sources shared libs ---
echo "Test 2: telegram-check.sh sources shared libs and routes to topic"
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/telegram-check.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/telegram-check.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/telegram-check.sh"'
assert "Uses tg_send_message" 'grep -q "tg_send_message" "$SCRIPT_DIR/telegram-check.sh"'
assert "Includes callback_query in allowed_updates" 'grep -q "callback_query" "$SCRIPT_DIR/telegram-check.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/telegram-check.sh"'
echo ""

# --- Test 3: question-relay.sh routes to topic ---
echo "Test 3: question-relay.sh sends to project topic"
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/question-relay.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/question-relay.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/question-relay.sh"'
assert "Uses HTML parse_mode" 'grep -q "HTML" "$SCRIPT_DIR/question-relay.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/question-relay.sh"'
echo ""

# --- Test 4: session-start-inject.sh routes to topic ---
echo "Test 4: session-start-inject.sh filters by topic"
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/session-start-inject.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/session-start-inject.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/session-start-inject.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/session-start-inject.sh"'
echo ""

# --- Test 5: reply-poller.sh routes to topic ---
echo "Test 5: reply-poller.sh sends to project topic"
assert "Sources telegram-api.sh" 'grep -q "telegram-api.sh" "$SCRIPT_DIR/reply-poller.sh"'
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/reply-poller.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/reply-poller.sh"'
assert "Uses message_thread_id or tg_send_message" \
  'grep -qE "message_thread_id|tg_send_message" "$SCRIPT_DIR/reply-poller.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/reply-poller.sh"'
echo ""

# --- Test 6: No hook uses old inline curl for sendMessage ---
echo "Test 6: Hooks use shared API functions instead of inline curl sendMessage"
# stop-notify and telegram-check may still have inline curl for offset/getUpdates compat,
# but sendMessage should go through tg_send_message
for hook in stop-notify.sh telegram-check.sh question-relay.sh; do
  # Count curl sendMessage calls that are NOT in comments
  INLINE_COUNT=$(grep -v '^#' "$SCRIPT_DIR/$hook" | grep -c 'curl.*sendMessage' || true)
  assert "$hook: no inline curl sendMessage" '[ "${INLINE_COUNT:-0}" -eq 0 ]'
done
echo ""

# --- Test 7: topic_ensure_named is called BEFORE any message send ---
echo "Test 7: topic_ensure_named precedes message sending"
for hook in stop-notify.sh telegram-check.sh question-relay.sh; do
  ENSURE_LINE=$(grep -n "topic_ensure_named" "$SCRIPT_DIR/$hook" | head -1 | cut -d: -f1)
  SEND_LINE=$(grep -n "tg_send_message\|tg_send_with_keyboard" "$SCRIPT_DIR/$hook" | head -1 | cut -d: -f1)
  if [ -n "$ENSURE_LINE" ] && [ -n "$SEND_LINE" ]; then
    assert "$hook: topic_ensure_named before first send" '[ "$ENSURE_LINE" -lt "$SEND_LINE" ]'
  else
    assert "$hook: topic_ensure_named before first send" 'false'
  fi
done
echo ""

# --- Test 8: permission-relay.sh uses topic_ensure_named ---
echo "Test 8: permission-relay.sh uses branch-based naming"
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/permission-relay.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/permission-relay.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/permission-relay.sh"'
assert "Detects branch" 'grep -q "git branch --show-current" "$SCRIPT_DIR/permission-relay.sh"'
echo ""

# --- Test 8b: askuser-relay.sh uses branch-based naming ---
echo "Test 8b: askuser-relay.sh uses branch-based naming and PermissionRequest output"
assert "Sources telegram-topics.sh" 'grep -q "telegram-topics.sh" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Calls topic_ensure_named" 'grep -q "topic_ensure_named" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Composes display name" 'grep -q "compose_topic_name" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Detects branch" 'grep -q "git branch --show-current" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Returns PermissionRequest hookEventName" 'grep -q "PermissionRequest" "$SCRIPT_DIR/askuser-relay.sh"'
assert "Uses updatedInput.answers" 'grep -q "updatedInput" "$SCRIPT_DIR/askuser-relay.sh"'
echo ""

# --- Test 9: No hook uses bare topic_ensure (all should use topic_ensure_named) ---
echo "Test 9: All hooks use topic_ensure_named (not bare topic_ensure)"
for hook in stop-notify.sh telegram-check.sh question-relay.sh reply-poller.sh permission-relay.sh askuser-relay.sh; do
  # Count topic_ensure calls that are NOT topic_ensure_named (exclude lib source lines)
  BARE_COUNT=$(grep -v '^#' "$SCRIPT_DIR/$hook" | grep -v 'topic_ensure_named' | grep -c 'topic_ensure' || true)
  assert "$hook: no bare topic_ensure" '[ "${BARE_COUNT:-0}" -eq 0 ]'
done
# session-start-inject.sh uses topic_ensure_named inside telegram_messages() helper
BARE_SSI=$(grep -v '^#' "$SCRIPT_DIR/session-start-inject.sh" | grep -v 'topic_ensure_named' | grep -c 'topic_ensure' || true)
assert "session-start-inject.sh: no bare topic_ensure" '[ "${BARE_SSI:-0}" -eq 0 ]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
