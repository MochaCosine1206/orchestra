#!/bin/bash
# Tests for subagent-quality-gate.sh — SubagentStop per-role quality checks
# Run: bash scripts/hooks/test-subagent-quality-gate.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/subagent-quality-gate.sh"
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

# Helper: run the script with given JSON, capture stdout
run_gate() {
  echo "$1" | bash "$SCRIPT" 2>/dev/null
}

echo "=== SubagentStop Quality Gate Tests ==="
echo ""

# --- Test 1: Implementer with test results passes ---
echo "Test 1: Implementer with passing tests → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":false,"last_assistant_message":"All 12 tests pass. Changes committed to branch."}')
assert "No block output (empty)" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 2: Implementer without test results blocks ---
echo "Test 2: Implementer without test mention → block"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":false,"last_assistant_message":"I have implemented the feature and edited main.go."}')
assert "Outputs block decision" 'echo "$OUTPUT" | grep -q "decision.*block"'
assert "Mentions running tests" 'echo "$OUTPUT" | grep -qi "test"'

echo ""

# --- Test 3: Implementer with stop_hook_active=true passes regardless ---
echo "Test 3: Implementer with stop_hook_active=true → always allow"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":true,"last_assistant_message":"I could not get tests to pass."}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 4: Researcher with sources passes ---
echo "Test 4: Researcher with source URLs → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Researcher","stop_hook_active":false,"last_assistant_message":"Research complete. Sources: https://example.com/paper1, https://arxiv.org/abs/2401.12345"}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 5: Researcher without sources blocks ---
echo "Test 5: Researcher without sources → block"
OUTPUT=$(run_gate '{"agent_type":"Researcher","stop_hook_active":false,"last_assistant_message":"I found that Redis is faster than SQLite for high-write workloads. The community prefers file-based solutions."}')
assert "Outputs block decision" 'echo "$OUTPUT" | grep -q "decision.*block"'
assert "Mentions citing sources" 'echo "$OUTPUT" | grep -qi "source"'

echo ""

# --- Test 6: Reviewer with verdict passes ---
echo "Test 6: Reviewer with APPROVE verdict → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Reviewer","stop_hook_active":false,"last_assistant_message":"## Review\n### Verdict: APPROVE\nCode looks good, all tests pass."}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 7: Reviewer without verdict blocks ---
echo "Test 7: Reviewer without verdict → block"
OUTPUT=$(run_gate '{"agent_type":"Reviewer","stop_hook_active":false,"last_assistant_message":"I looked at the code and it seems fine. There are no major issues."}')
assert "Outputs block decision" 'echo "$OUTPUT" | grep -q "decision.*block"'
assert "Mentions verdict" 'echo "$OUTPUT" | grep -qi "verdict"'

echo ""

# --- Test 8: Architect passes through (no checks) ---
echo "Test 8: Architect → always allow (no quality gate)"
OUTPUT=$(run_gate '{"agent_type":"Architect","stop_hook_active":false,"last_assistant_message":"Here is the decomposition plan."}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 9: Scout passes through (no checks) ---
echo "Test 9: Scout → always allow (no quality gate)"
OUTPUT=$(run_gate '{"agent_type":"Scout","stop_hook_active":false,"last_assistant_message":"Found 15 files matching the pattern."}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 10: Empty last_assistant_message passes ---
echo "Test 10: Empty message → graceful pass"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":false,"last_assistant_message":""}')
assert "No block output (empty msg guard)" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 11: Missing agent_type passes ---
echo "Test 11: Missing agent_type → graceful pass"
OUTPUT=$(run_gate '{"stop_hook_active":false,"last_assistant_message":"Some output here."}')
assert "No block output (unknown type)" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 12: Implementer with "PASS" in output passes ---
echo "Test 12: Implementer with PASS keyword → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":false,"last_assistant_message":"=== Results: 26/26 passed, 0 failed ==="}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 13: Researcher with "Sources:" section passes ---
echo "Test 13: Researcher with Sources: section → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Researcher","stop_hook_active":false,"last_assistant_message":"### Sources:\n- Paper A\n- Paper B"}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 14: Reviewer with REQUEST CHANGES passes ---
echo "Test 14: Reviewer with REQUEST CHANGES → allow stop"
OUTPUT=$(run_gate '{"agent_type":"Reviewer","stop_hook_active":false,"last_assistant_message":"### Verdict: REQUEST CHANGES\nNeed to fix SQL injection on line 42."}')
assert "No block output" '[ -z "$OUTPUT" ]'

echo ""

# --- Test 15: Lowercase agent_type works ---
echo "Test 15: Lowercase agent_type → same behavior"
OUTPUT=$(run_gate '{"agent_type":"implementer","stop_hook_active":false,"last_assistant_message":"Done editing files."}')
assert "Blocks lowercase implementer too" 'echo "$OUTPUT" | grep -q "decision.*block"'

echo ""

# --- Test 16: Valid JSON output format ---
echo "Test 16: Block output is valid JSON"
OUTPUT=$(run_gate '{"agent_type":"Implementer","stop_hook_active":false,"last_assistant_message":"Done."}')
assert "Output is valid JSON" 'echo "$OUTPUT" | jq . > /dev/null 2>&1'
assert "Has decision field" 'echo "$OUTPUT" | jq -r ".decision" 2>/dev/null | grep -q "block"'
assert "Has reason field" 'echo "$OUTPUT" | jq -r ".reason" 2>/dev/null | grep -q "."'

echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
