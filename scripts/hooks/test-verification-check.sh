#!/bin/bash
# Tests for verification-check.sh (B-261)
# TDD: these tests define the expected behavior before implementation.
# Run: bash scripts/hooks/test-verification-check.sh

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOK="$SCRIPT_DIR/verification-check.sh"
PASS=0
FAIL=0
TOTAL=0

assert_ok() {
  local test_name="$1"
  local input="$2"
  TOTAL=$((TOTAL + 1))
  local result
  result=$(echo "$input" | bash "$HOOK" 2>/dev/null)
  local exit_code=$?
  local ok
  ok=$(echo "$result" | jq -r '.ok' 2>/dev/null)
  if [ "$ok" = "true" ] && [ "$exit_code" -eq 0 ]; then
    echo "  PASS: $test_name"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $test_name (ok=$ok, exit=$exit_code, output=$result)"
    FAIL=$((FAIL + 1))
  fi
}

assert_not_ok() {
  local test_name="$1"
  local input="$2"
  local expected_pattern="${3:-}"
  TOTAL=$((TOTAL + 1))
  local result
  result=$(echo "$input" | bash "$HOOK" 2>/dev/null)
  local exit_code=$?
  local ok
  ok=$(echo "$result" | jq -r '.ok' 2>/dev/null)
  local reason
  reason=$(echo "$result" | jq -r '.reason // empty' 2>/dev/null)
  if [ "$ok" = "false" ] && [ "$exit_code" -eq 0 ]; then
    if [ -n "$expected_pattern" ]; then
      if echo "$reason" | grep -qi "$expected_pattern"; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
      else
        echo "  FAIL: $test_name (reason missing pattern '$expected_pattern': $reason)"
        FAIL=$((FAIL + 1))
      fi
    else
      echo "  PASS: $test_name"
      PASS=$((PASS + 1))
    fi
  else
    echo "  FAIL: $test_name (ok=$ok, exit=$exit_code, output=$result)"
    FAIL=$((FAIL + 1))
  fi
}

assert_valid_json() {
  local test_name="$1"
  local input="$2"
  TOTAL=$((TOTAL + 1))
  local result
  result=$(echo "$input" | bash "$HOOK" 2>/dev/null)
  if echo "$result" | jq . >/dev/null 2>&1; then
    echo "  PASS: $test_name"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $test_name (invalid JSON: $result)"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== B-261: verification-check.sh Tests ==="
echo ""

# --- Group 1: Always returns valid JSON ---
echo "Group 1: JSON validity"

assert_valid_json "Empty input produces valid JSON" '{}'
assert_valid_json "Missing last_assistant_message produces valid JSON" '{"session_id":"test"}'
assert_valid_json "Normal message produces valid JSON" '{"last_assistant_message":"I updated the file."}'
assert_valid_json "Message with special chars produces valid JSON" '{"last_assistant_message":"Fixed the \"quotes\" and <html> & stuff"}'
assert_valid_json "Very long message produces valid JSON" "{\"last_assistant_message\":\"$(python3 -c "print('a' * 5000)")\"}"

echo ""

# --- Group 2: Conversational / normal responses → ok:true ---
echo "Group 2: Normal responses (should pass)"

assert_ok "Simple Q&A response" \
  '{"last_assistant_message":"The file is located at internal/orchestrator/merge.go on line 42."}'

assert_ok "Planning response" \
  '{"last_assistant_message":"Here is the plan: first we will create the tests, then implement the feature."}'

assert_ok "Research response" \
  '{"last_assistant_message":"Based on the documentation, forum topics require supergroup mode."}'

assert_ok "Empty last_assistant_message" \
  '{"last_assistant_message":""}'

assert_ok "No last_assistant_message field" \
  '{"session_id":"test-123"}'

assert_ok "stop_hook_active is true" \
  '{"stop_hook_active":true,"last_assistant_message":"The root cause is unknown."}'

echo ""

# --- Group 3: Causal claims WITH evidence → ok:true ---
echo "Group 3: Causal claims with evidence (should pass)"

assert_ok "Root cause with file:line citation" \
  '{"last_assistant_message":"The root cause is the missing nil check at internal/db/queries.go:142."}'

assert_ok "Caused by with error output citation" \
  '{"last_assistant_message":"This was caused by a nil pointer dereference. The error output shows: panic: runtime error: invalid memory address."}'

assert_ok "Issue identified with stack trace" \
  '{"last_assistant_message":"The issue is in the timeout handling. The stack trace shows: goroutine 1 [running]: main.go:55."}'

echo ""

# --- Group 4: Causal claims WITHOUT evidence → ok:false ---
echo "Group 4: Causal claims without evidence (should fail)"

assert_not_ok "Root cause without evidence" \
  '{"last_assistant_message":"The root cause is that the database connection pool is exhausted."}' \
  "VERIFICATION"

assert_not_ok "Caused by without evidence" \
  '{"last_assistant_message":"This was caused by a race condition in the scheduler."}' \
  "VERIFICATION"

assert_not_ok "The problem was without evidence" \
  '{"last_assistant_message":"The problem was that the config file was not being read properly."}' \
  "VERIFICATION"

echo ""

# --- Group 5: Fix claims WITH verification → ok:true ---
echo "Group 5: Fix claims with verification (should pass)"

assert_ok "Fixed with test re-run" \
  '{"last_assistant_message":"I fixed the nil check. Re-running the tests: go test ./... — all 42 tests pass."}'

assert_ok "Resolved with command output" \
  '{"last_assistant_message":"Resolved the issue by adding the missing import. Running make build: BUILD SUCCESSFUL."}'

echo ""

# --- Group 6: Fix claims WITHOUT verification → ok:false ---
echo "Group 6: Fix claims without verification (should fail)"

assert_not_ok "Fixed without re-running tests" \
  '{"last_assistant_message":"I fixed the bug by updating the query in internal/db/queries.go."}' \
  "VERIFICATION"

assert_not_ok "Resolved without verification" \
  '{"last_assistant_message":"I resolved the race condition by adding a mutex lock."}' \
  "VERIFICATION"

echo ""

# --- Group 7: Edited files without testing → ok:false ---
echo "Group 7: Edits without tests (should fail)"

assert_not_ok "Edited Go files, no test run mentioned" \
  '{"last_assistant_message":"I edited internal/orchestrator/merge.go to add the timeout parameter and updated internal/cmd/go_cmd.go to expose the flag."}' \
  "UNTESTED"

echo ""

# --- Group 8: Edited files WITH testing → ok:true ---
echo "Group 8: Edits with tests (should pass)"

assert_ok "Edited files and ran tests" \
  '{"last_assistant_message":"I edited merge.go and go_cmd.go. Running go test ./internal/orchestrator/... — PASS. All 15 tests pass."}'

assert_ok "Edited and built" \
  '{"last_assistant_message":"Updated the handler in api.go. Running make build — success, no errors."}'

echo ""

# --- Group 9: Edge cases ---
echo "Group 9: Edge cases"

assert_ok "Message mentions fix in quoted context (not a claim)" \
  '{"last_assistant_message":"The user said they fixed it yesterday. I will investigate further."}'

assert_ok "Message discusses root cause analysis approach (not a claim)" \
  '{"last_assistant_message":"To find the root cause, we should check the logs and trace the request flow."}'

assert_valid_json "Malformed JSON in last_assistant_message field" \
  '{"last_assistant_message":"Here is some {broken json"}'

echo ""

# --- Summary ---
echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
