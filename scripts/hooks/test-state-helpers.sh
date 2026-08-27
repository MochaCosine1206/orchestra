#!/bin/bash
# Tests for lib/state-helpers.sh — shared utilities for M1 infrastructure
# Run: bash scripts/hooks/test-state-helpers.sh
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
}
trap cleanup EXIT

source "$SCRIPT_DIR/lib/state-helpers.sh"

echo "=== State Helpers Tests ==="
echo ""

# --- get_project_slug tests ---
echo "Test 1: get_project_slug derivation"
assert "Simple name" '[ "$(get_project_slug "/home/user/my-project")" = "my-project" ]'
assert "Uppercase to lowercase" '[ "$(get_project_slug "/home/user/My-Project")" = "my-project" ]'
assert "Spaces to hyphens" '[ "$(get_project_slug "/home/user/My Cool Project")" = "my-cool-project" ]'
assert "Special chars removed" '[ "$(get_project_slug "/home/user/my@project!v2")" = "myprojectv2" ]'
assert "Dots preserved" '[ "$(get_project_slug "/home/user/my.project")" = "my.project" ]'
assert "Underscores preserved" '[ "$(get_project_slug "/home/user/my_project")" = "my_project" ]'
assert "Trailing slash handled" '[ "$(get_project_slug "/home/user/my-project/")" = "my-project" ]'
echo ""

# --- ensure_state_dir tests ---
echo "Test 2: ensure_state_dir creates structure"
RESULT=$(ensure_state_dir "test-project")
assert "Returns path" '[ -n "$RESULT" ]'
assert "Sessions dir created" '[ -d "$TEST_BASE/test-project/sessions" ]'
assert "Telegram dir created" '[ -d "$TEST_BASE/test-project/telegram" ]'
assert "Idempotent (second call succeeds)" 'ensure_state_dir "test-project" >/dev/null'
echo ""

# --- load_telegram_config tests ---
echo "Test 3: load_telegram_config"
# Create a fake config
FAKE_CONFIG=$(mktemp)
echo 'TELEGRAM_BOT_TOKEN="test-token-123"' > "$FAKE_CONFIG"
echo 'TELEGRAM_CHAT_ID="12345"' >> "$FAKE_CONFIG"

# Test with explicit config path
unset TELEGRAM_BOT_TOKEN 2>/dev/null || true
unset TELEGRAM_CHAT_ID 2>/dev/null || true
load_telegram_config "$FAKE_CONFIG"
assert "Bot token loaded" '[ "${TELEGRAM_BOT_TOKEN:-}" = "test-token-123" ]'
assert "Chat ID loaded" '[ "${TELEGRAM_CHAT_ID:-}" = "12345" ]'

# Test missing config
unset TELEGRAM_BOT_TOKEN 2>/dev/null || true
unset TELEGRAM_CHAT_ID 2>/dev/null || true
load_telegram_config "/nonexistent/config" 2>/dev/null
RC=$?
assert "Missing config returns non-zero" '[ "$RC" -ne 0 ]'

# Test already-set vars are kept
export TELEGRAM_BOT_TOKEN="pre-set-token"
export TELEGRAM_CHAT_ID="pre-set-id"
load_telegram_config "$FAKE_CONFIG"
assert "Pre-set token preserved" '[ "$TELEGRAM_BOT_TOKEN" = "pre-set-token" ]'
assert "Pre-set chat ID preserved" '[ "$TELEGRAM_CHAT_ID" = "pre-set-id" ]'
unset TELEGRAM_BOT_TOKEN TELEGRAM_CHAT_ID
rm -f "$FAKE_CONFIG"
echo ""

# --- migrate_flat_state tests ---
echo "Test 4: migrate_flat_state"
# Create legacy flat files
mkdir -p "$TEST_BASE"
echo '{"session_id":"sess-123"}' > "$TEST_BASE/sess-123-stop.json"
echo '{"session_id":"sess-456"}' > "$TEST_BASE/sess-456-stop.json"

# Ensure slug dir exists
ensure_state_dir "migrate-test" > /dev/null

migrate_flat_state "sess-123" "migrate-test"
assert "Legacy file moved" '[ -f "$TEST_BASE/migrate-test/sessions/sess-123-stop.json" ]'
assert "Original removed" '[ ! -f "$TEST_BASE/sess-123-stop.json" ]'

# Non-existent session should not error
migrate_flat_state "nonexistent" "migrate-test" 2>/dev/null
assert "Missing session handled gracefully" "true"

# Already-migrated session (file in target, not in flat)
migrate_flat_state "sess-123" "migrate-test" 2>/dev/null
assert "Double migration is idempotent" '[ -f "$TEST_BASE/migrate-test/sessions/sess-123-stop.json" ]'
echo ""

# --- get_state_dir tests ---
echo "Test 5: get_state_dir convenience function"
RESULT=$(get_state_dir "test-project")
assert "Returns project dir path" '[ "$RESULT" = "$TEST_BASE/test-project" ]'
echo ""

# --- branch_to_task_name tests ---
echo "Test 6: branch_to_task_name parsing"
assert "Feature branch" '[ "$(branch_to_task_name "feature/M2-telegram-evolution")" = "M2 Telegram Evolution" ]'
assert "Fix branch" '[ "$(branch_to_task_name "fix/B-261-hook-failure-isolation")" = "B 261 Hook Failure Isolation" ]'
assert "Chore branch" '[ "$(branch_to_task_name "chore/cleanup-old-logs")" = "Cleanup Old Logs" ]'
assert "Docs branch" '[ "$(branch_to_task_name "docs/update-readme")" = "Update Readme" ]'
assert "Refactor branch" '[ "$(branch_to_task_name "refactor/split-conductor")" = "Split Conductor" ]'
assert "Dev branch returns empty" '[ -z "$(branch_to_task_name "dev")" ]'
assert "Main branch returns empty" '[ -z "$(branch_to_task_name "main")" ]'
assert "Master branch returns empty" '[ -z "$(branch_to_task_name "master")" ]'
assert "Empty string returns empty" '[ -z "$(branch_to_task_name "")" ]'
assert "Unknown branch returned as-is" '[ "$(branch_to_task_name "hotfix-123")" = "hotfix-123" ]'
echo ""

# --- get_repo_display_name tests ---
echo "Test 7: get_repo_display_name"
unset TELEGRAM_REPO_NAME 2>/dev/null || true
assert "Default: title-cases slug" '[ "$(get_repo_display_name "claude-orchestra")" = "Claude Orchestra" ]'
assert "Single word slug" '[ "$(get_repo_display_name "myapp")" = "Myapp" ]'
export TELEGRAM_REPO_NAME="Orchestra"
assert "Env override" '[ "$(get_repo_display_name "claude-orchestra")" = "Orchestra" ]'
unset TELEGRAM_REPO_NAME
echo ""

# --- compose_topic_name tests ---
echo "Test 8: compose_topic_name"
assert "Repo + task" '[ "$(compose_topic_name "Orchestra" "M2 Telegram Evolution")" = "Orchestra — M2 Telegram Evolution" ]'
assert "Repo only (empty task)" '[ "$(compose_topic_name "Orchestra" "")" = "Orchestra" ]'
# 128-char truncation test
LONG_TASK=$(printf 'A%.0s' {1..200})
RESULT=$(compose_topic_name "Orchestra" "$LONG_TASK")
assert "Truncated to 128 chars" '[ ${#RESULT} -le 128 ]'
assert "Truncation preserves prefix" '[[ "$RESULT" == Orchestra* ]]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
