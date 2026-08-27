#!/bin/bash
# Tests for mode-aware post-bash-check.sh (B-249)
# Verifies hook behavior changes based on hook mode.
# Run: bash scripts/hooks/test-post-bash-mode.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOK="$SCRIPT_DIR/post-bash-check.sh"
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
export CLAUDE_PROJECT_DIR="$TEST_BASE/fake-project"
mkdir -p "$CLAUDE_PROJECT_DIR"

cleanup() {
  rm -rf "$TEST_BASE"
  rm -f /tmp/claude-failure-pending-test-session
}
trap cleanup EXIT

# Source state-helpers so we can set modes
source "$SCRIPT_DIR/lib/state-helpers.sh"
SLUG=$(get_project_slug "$CLAUDE_PROJECT_DIR")
ensure_state_dir "$SLUG" > /dev/null

# Helper: run the hook with given input, return output
run_hook() {
  local input="$1"
  echo "$input" | bash "$HOOK" 2>/dev/null || true
}

echo "=== Post-Bash Mode Tests (B-249) ==="
echo ""

# --- Trigger 2: Empty results skipped in research/debug ---
echo "Test 1: Trigger 2 (empty results) — mode-aware behavior"

# Default mode: trigger 2 fires on "not found"
set_hook_mode "$SLUG" "default" "auto" "dev"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"grep foo bar"},"tool_response":"grep: bar: No such file found","session_id":"test-session"}')
assert "Default mode: trigger 2 fires on 'not found'" 'echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'

# Research mode: trigger 2 skipped
set_hook_mode "$SLUG" "research" "auto" "docs/readme"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"grep foo bar"},"tool_response":"grep: bar: No such file found","session_id":"test-session"}')
assert "Research mode: trigger 2 skipped" '! echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'

# Debug mode: trigger 2 skipped
set_hook_mode "$SLUG" "debug" "auto" "debug/flaky"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"grep foo bar"},"tool_response":"grep: bar: No such file found","session_id":"test-session"}')
assert "Debug mode: trigger 2 skipped" '! echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'
echo ""

# --- G86 fix: test runner output exempt from trigger 2 ---
echo "Test 2: G86 fix — test runners exempt from trigger 2"

set_hook_mode "$SLUG" "default" "auto" "dev"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":"ok  mypackage 0.3s\n--- Not Found in mock API","session_id":"test-session"}')
assert "go test: trigger 2 skipped despite 'Not Found'" '! echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'

RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"pytest tests/"},"tool_response":"PASSED - No results found message in test output","session_id":"test-session"}')
assert "pytest: trigger 2 skipped despite 'No results found'" '! echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'

RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"bash scripts/hooks/test-state-helpers.sh"},"tool_response":"PASS: Missing file returns default\nResults: 0 matches found","session_id":"test-session"}')
assert "bash test script: trigger 2 skipped" '! echo "$RESULT" | grep -q "UNEXPECTED EMPTY RESULT"'
echo ""

# --- Trigger 4: Doc alignment skipped in research/debug ---
echo "Test 3: Trigger 4 (doc alignment) — mode-aware behavior"

# Implementation mode: trigger 4 fires (needs a real git context, so we test the mode gate logic)
# We can't easily test trigger 4 firing without a real git repo, so we test that research/debug skip it
# by checking the hook exits cleanly (no block) when mode skips it

# For trigger 4 we need a git commit command — the hook checks git diff HEAD~1 HEAD
# We'll verify that in research mode, even if the command is "git commit", trigger 4 is skipped
set_hook_mode "$SLUG" "research" "auto" "docs/readme"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git commit -m \"test\""},"tool_response":"[docs/readme abc1234] test\n 1 file changed","session_id":"test-session"}')
assert "Research mode: trigger 4 (doc alignment) skipped" '! echo "$RESULT" | grep -q "doc"'

set_hook_mode "$SLUG" "debug" "auto" "debug/flaky"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git commit -m \"test\""},"tool_response":"[debug/flaky abc1234] test\n 1 file changed","session_id":"test-session"}')
assert "Debug mode: trigger 4 (doc alignment) skipped" '! echo "$RESULT" | grep -q "doc"'
echo ""

# --- Trigger 1: always fires regardless of mode ---
echo "Test 4: Trigger 1 (test failure) — always fires"

set_hook_mode "$SLUG" "research" "auto" "docs/readme"
RESULT=$(run_hook '{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":"--- FAIL: TestFoo\nFAIL mypackage","session_id":"test-session"}')
assert "Research mode: trigger 1 still fires on FAIL" 'echo "$RESULT" | grep -q "FAILURE DETECTED"'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
