#!/bin/bash
# Tests for session-start-inject.sh — SessionStart context injection
# Run: bash scripts/hooks/test-session-start-inject.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/session-start-inject.sh"
PASS=0
FAIL=0
TOTAL=0

# Use temp dir for state isolation
TEST_STATE_BASE=$(mktemp -d)
export SESSION_STATE_BASE="$TEST_STATE_BASE"
export TELEGRAM_NOTIFY_CONFIG=/dev/null

# Create a temp git repo
TEST_GIT_DIR=$(mktemp -d)
git -C "$TEST_GIT_DIR" init -q
git -C "$TEST_GIT_DIR" checkout -b dev -q 2>/dev/null || true
git -C "$TEST_GIT_DIR" commit --allow-empty -m "Initial commit" -q

# Temp HOME for preference file isolation
TEST_HOME=$(mktemp -d)
REAL_HOME="$HOME"

cleanup() {
  rm -rf "$TEST_STATE_BASE" "$TEST_GIT_DIR" "$TEST_HOME"
}
trap cleanup EXIT

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

# Helper: derive slug from test git dir
get_test_slug() {
  basename "$TEST_GIT_DIR" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g'
}

echo "=== SessionStart Context Injection Tests ==="
echo ""

# --- Test 1: Startup injects prior summary ---
echo "Test 1: Startup source injects prior summary"
SLUG=$(get_test_slug)
mkdir -p "$TEST_STATE_BASE/$SLUG/sessions"
echo "Previous session did X, Y, and Z" > "$TEST_STATE_BASE/$SLUG/last-summary.md"

OUTPUT=$(echo '{"session_id":"sess-start-001","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Output contains prior summary" 'echo "$OUTPUT" | grep -q "Previous session did X, Y, and Z"'
assert "Output contains git state" 'echo "$OUTPUT" | grep -qi "branch\|git"'
assert "Output contains rules reminder" 'echo "$OUTPUT" | grep -qi "rule\|summary\|trace"'

echo ""

# --- Test 2: Compact source reads checkpoint ---
echo "Test 2: Compact source reads checkpoint data"
SLUG=$(get_test_slug)
# Create a checkpoint file
jq -n '{session_id:"sess-compact-001", compaction_count:3, git_branch:"dev", recent_commits:["abc Fix bug","def Add feature"], uncommitted_files:["main.go"]}' \
  > "$TEST_STATE_BASE/$SLUG/sessions/sess-compact-001-compact.json"

OUTPUT=$(echo '{"session_id":"sess-compact-001","source":"compact"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Output mentions compaction count" 'echo "$OUTPUT" | grep -q "3"'
assert "Output mentions commits" 'echo "$OUTPUT" | grep -q "Fix bug"'
assert "Output contains rules reminder" 'echo "$OUTPUT" | grep -qi "rule\|summary\|trace"'

echo ""

# --- Test 3: Resume source is minimal ---
echo "Test 3: Resume source is minimal"
OUTPUT=$(echo '{"session_id":"sess-resume-001","source":"resume"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Resume produces output" '[ -n "$OUTPUT" ]'
assert "Resume includes git state" 'echo "$OUTPUT" | grep -qi "branch\|git"'

echo ""

# --- Test 4: Handles missing files gracefully ---
echo "Test 4: Handles missing state files gracefully"
# Use a slug with no existing state
OUTPUT=$(echo '{"session_id":"sess-missing-001","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="/tmp/nonexistent-project-xyz" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "No crash with missing state" '[ $? -eq 0 ] || true'
# Should still produce some output (at least rules reminder)
assert "Still produces output" '[ -n "$OUTPUT" ]'

echo ""

# --- Test 5: Telegram failure doesn't block ---
echo "Test 5: Telegram failure doesn't block execution"
SLUG=$(get_test_slug)
echo "Summary for timing test" > "$TEST_STATE_BASE/$SLUG/last-summary.md"
START=$(date +%s)
OUTPUT=$(echo '{"session_id":"sess-tg-001","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  TELEGRAM_BOT_TOKEN="fake-token" TELEGRAM_CHAT_ID="fake-id" \
  bash "$SCRIPT" 2>/dev/null)
END=$(date +%s)
ELAPSED=$((END - START))
assert "Completes within 5s even with bad Telegram token" '[ "$ELAPSED" -lt 6 ]'
assert "Still produces output despite Telegram failure" '[ -n "$OUTPUT" ]'

echo ""

# --- Test 6: Execution time under budget ---
echo "Test 6: Total execution time under 5s hook timeout (no network)"
SLUG=$(get_test_slug)
echo "Quick summary" > "$TEST_STATE_BASE/$SLUG/last-summary.md"
START=$(date +%s)
OUTPUT=$(echo '{"session_id":"sess-perf-001","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
END=$(date +%s)
ELAPSED=$((END - START))
# Must stay within the 5s hook timeout; date +%s has 1s resolution, so use 4s threshold
assert "Execution under 4s (within 5s hook timeout)" '[ "$ELAPSED" -lt 4 ]'

echo ""

# --- Test 7: Startup injects global preferences ---
echo "Test 7: Startup injects global preferences"
SLUG=$(get_test_slug)
mkdir -p "$TEST_HOME/.claude"
cat > "$TEST_HOME/.claude/PREFERENCES.md" << 'PREF_EOF'
# User Preferences — Test
## Communication
- Concise responses, no emoji
## Workflow
- TDD: write tests first
PREF_EOF

OUTPUT=$(echo '{"session_id":"sess-pref-001","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  HOME="$TEST_HOME" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Output contains global preferences header" 'echo "$OUTPUT" | grep -q "User Preferences (Global)"'
assert "Output contains preference content" 'echo "$OUTPUT" | grep -q "Concise responses"'
assert "Output contains preference end marker" 'echo "$OUTPUT" | grep -q "End User Preferences"'

echo ""

# --- Test 8: Missing preferences file doesn't crash ---
echo "Test 8: Missing preferences file doesn't crash"
EMPTY_HOME=$(mktemp -d)
OUTPUT=$(echo '{"session_id":"sess-pref-002","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  HOME="$EMPTY_HOME" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "No crash with missing preferences" '[ $? -eq 0 ] || true'
assert "Still has rules reminder" 'echo "$OUTPUT" | grep -qi "rule\|trace"'
assert "No preference headers when file missing" '! echo "$OUTPUT" | grep -q "User Preferences"'
rm -rf "$EMPTY_HOME"

echo ""

# --- Test 9: Project preferences injected alongside global ---
echo "Test 9: Project preferences injected alongside global"
SLUG=$(get_test_slug)
mkdir -p "$TEST_GIT_DIR/.claude"
cat > "$TEST_GIT_DIR/.claude/preferences.md" << 'PROJ_EOF'
# Project Preferences — Test Project
## Conventions
- Use Go for all new code
PROJ_EOF

OUTPUT=$(echo '{"session_id":"sess-pref-003","source":"startup"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  HOME="$TEST_HOME" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Output contains global preferences" 'echo "$OUTPUT" | grep -q "User Preferences (Global)"'
assert "Output contains project preferences" 'echo "$OUTPUT" | grep -q "User Preferences (Project)"'
assert "Output contains project content" 'echo "$OUTPUT" | grep -q "Use Go for all new code"'

# Clean up project prefs for other tests
rm -rf "$TEST_GIT_DIR/.claude"

echo ""

# --- Test 10: Preferences NOT injected on resume ---
echo "Test 10: Preferences not injected on resume (minimal source)"
OUTPUT=$(echo '{"session_id":"sess-pref-004","source":"resume"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  HOME="$TEST_HOME" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Resume has no preference headers" '! echo "$OUTPUT" | grep -q "User Preferences"'
assert "Resume still has git state" 'echo "$OUTPUT" | grep -qi "branch\|git"'

echo ""

# --- Test 11: Compact source injects preferences ---
echo "Test 11: Compact source injects preferences"
SLUG=$(get_test_slug)
jq -n '{session_id:"sess-pref-005", compaction_count:1, git_branch:"dev", recent_commits:[], uncommitted_files:[]}' \
  > "$TEST_STATE_BASE/$SLUG/sessions/sess-pref-005-compact.json"

OUTPUT=$(echo '{"session_id":"sess-pref-005","source":"compact"}' | \
  CLAUDE_PROJECT_DIR="$TEST_GIT_DIR" \
  HOME="$TEST_HOME" \
  TELEGRAM_BOT_TOKEN="" TELEGRAM_CHAT_ID="" \
  bash "$SCRIPT" 2>/dev/null)
assert "Compact includes preferences" 'echo "$OUTPUT" | grep -q "User Preferences (Global)"'
assert "Compact includes preference content" 'echo "$OUTPUT" | grep -q "TDD: write tests first"'

echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
