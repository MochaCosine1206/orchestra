#!/bin/bash
# Tests for mode-aware verification-check.sh (B-249)
# Verifies all 3 checks are skipped in research mode, active in debug mode.
# Run: bash scripts/hooks/test-verification-mode.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOK="$SCRIPT_DIR/verification-check.sh"
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

echo "=== Verification Mode Tests (B-249) ==="
echo ""

# --- Research mode: all 3 checks skipped ---
echo "Test 1: Research mode — all checks skipped"

set_hook_mode "$SLUG" "research" "auto" "docs/readme"

# Check 1: Causal claim without evidence → should be skipped in research
RESULT=$(run_hook '{"last_assistant_message":"The root cause is a race condition in the scheduler"}')
assert "Research: causal claim passes (skipped)" 'echo "$RESULT" | jq -e ".ok == true" > /dev/null 2>&1'

# Check 2: Fix claim without verification → should be skipped in research
RESULT=$(run_hook '{"last_assistant_message":"I fixed the authentication bug in the login handler"}')
assert "Research: fix claim passes (skipped)" 'echo "$RESULT" | jq -e ".ok == true" > /dev/null 2>&1'

# Check 3: Untested edits → should be skipped in research
RESULT=$(run_hook '{"last_assistant_message":"I edited the main.go file to add error handling"}')
assert "Research: untested edit passes (skipped)" 'echo "$RESULT" | jq -e ".ok == true" > /dev/null 2>&1'
echo ""

# --- Debug mode: all 3 checks active ---
echo "Test 2: Debug mode — all checks active"

set_hook_mode "$SLUG" "debug" "auto" "debug/flaky"

# Check 1: Causal claim without evidence → should fire in debug
RESULT=$(run_hook '{"last_assistant_message":"The root cause is a race condition in the scheduler"}')
assert "Debug: causal claim fires" 'echo "$RESULT" | jq -e ".ok == false" > /dev/null 2>&1'

# Check 2: Fix claim without verification → should fire in debug
RESULT=$(run_hook '{"last_assistant_message":"I fixed the authentication bug in the login handler"}')
assert "Debug: fix claim fires" 'echo "$RESULT" | jq -e ".ok == false" > /dev/null 2>&1'

# Check 3: Untested edits → should fire in debug
RESULT=$(run_hook '{"last_assistant_message":"I edited the main.go file to add error handling"}')
assert "Debug: untested edit fires" 'echo "$RESULT" | jq -e ".ok == false" > /dev/null 2>&1'
echo ""

# --- Default mode: all checks active (baseline) ---
echo "Test 3: Default mode — all checks active (baseline)"

set_hook_mode "$SLUG" "default" "auto" "dev"

RESULT=$(run_hook '{"last_assistant_message":"The root cause is a race condition in the scheduler"}')
assert "Default: causal claim fires" 'echo "$RESULT" | jq -e ".ok == false" > /dev/null 2>&1'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
