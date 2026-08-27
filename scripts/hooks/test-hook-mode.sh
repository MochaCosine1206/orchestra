#!/bin/bash
# Tests for hook mode helpers in lib/state-helpers.sh (B-249)
# Tests: get_hook_mode, set_hook_mode, detect_mode_from_branch
# Run: bash scripts/hooks/test-hook-mode.sh
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

echo "=== Hook Mode Tests (B-249) ==="
echo ""

# --- detect_mode_from_branch tests ---
echo "Test 1: detect_mode_from_branch — pattern matching"
assert "feature/* → implementation" '[ "$(detect_mode_from_branch "feature/B-249-hook-profiles")" = "implementation" ]'
assert "fix/* → implementation" '[ "$(detect_mode_from_branch "fix/broken-tests")" = "implementation" ]'
assert "chore/* → implementation" '[ "$(detect_mode_from_branch "chore/cleanup")" = "implementation" ]'
assert "refactor/* → implementation" '[ "$(detect_mode_from_branch "refactor/split-module")" = "implementation" ]'
assert "docs/* → research" '[ "$(detect_mode_from_branch "docs/update-readme")" = "research" ]'
assert "research/* → research" '[ "$(detect_mode_from_branch "research/llm-patterns")" = "research" ]'
assert "*research* → research" '[ "$(detect_mode_from_branch "spike-research-gaps")" = "research" ]'
assert "debug/* → debug" '[ "$(detect_mode_from_branch "debug/flaky-test")" = "debug" ]'
assert "investigate/* → debug" '[ "$(detect_mode_from_branch "investigate/memory-leak")" = "debug" ]'
assert "dev → default" '[ "$(detect_mode_from_branch "dev")" = "default" ]'
assert "main → default" '[ "$(detect_mode_from_branch "main")" = "default" ]'
assert "master → default" '[ "$(detect_mode_from_branch "master")" = "default" ]'
assert "empty → default" '[ "$(detect_mode_from_branch "")" = "default" ]'
echo ""

# --- get_hook_mode tests ---
echo "Test 2: get_hook_mode — read mode from state file"
ensure_state_dir "mode-test" > /dev/null
assert "Missing file → default" '[ "$(get_hook_mode "mode-test")" = "default" ]'

# Write a mode file
mkdir -p "$TEST_BASE/mode-test"
echo '{"mode":"research","set_by":"auto","branch":"docs/readme","updated_at":1740672000}' \
  > "$TEST_BASE/mode-test/hook-mode.json"
assert "Reads research mode" '[ "$(get_hook_mode "mode-test")" = "research" ]'
echo ""

# --- set_hook_mode tests ---
echo "Test 3: set_hook_mode — write mode state"
set_hook_mode "mode-test" "debug" "auto" "debug/flaky-test"
assert "Mode written" '[ "$(get_hook_mode "mode-test")" = "debug" ]'

# Verify JSON fields
STORED=$(cat "$TEST_BASE/mode-test/hook-mode.json")
assert "set_by stored" '[ "$(echo "$STORED" | jq -r .set_by)" = "auto" ]'
assert "branch stored" '[ "$(echo "$STORED" | jq -r .branch)" = "debug/flaky-test" ]'
assert "updated_at is numeric" '[ "$(echo "$STORED" | jq -r ".updated_at | type")" = "number" ]'
echo ""

# --- Precedence: user override sticky ---
echo "Test 4: set_hook_mode — user override precedence"
set_hook_mode "mode-test" "research" "user" "debug/flaky-test"
assert "User override written" '[ "$(get_hook_mode "mode-test")" = "research" ]'

# Auto-detect on same branch should NOT overwrite user override
set_hook_mode "mode-test" "debug" "auto" "debug/flaky-test"
assert "Auto skipped: user override sticky on same branch" '[ "$(get_hook_mode "mode-test")" = "research" ]'

# Auto-detect on different branch SHOULD overwrite
set_hook_mode "mode-test" "implementation" "auto" "feature/new-thing"
assert "Auto applied: branch changed" '[ "$(get_hook_mode "mode-test")" = "implementation" ]'

# User override always wins regardless
set_hook_mode "mode-test" "debug" "user" "feature/new-thing"
assert "User always overrides" '[ "$(get_hook_mode "mode-test")" = "debug" ]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
