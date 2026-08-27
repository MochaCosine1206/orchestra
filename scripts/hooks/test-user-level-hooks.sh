#!/bin/bash
# Tests for B-259: User-level hook promotion
# Verifies: symlinks exist, settings valid, hooks resolve, no duplication
# Run: bash scripts/hooks/test-user-level-hooks.sh
set -uo pipefail

PASS=0
FAIL=0
TOTAL=0

USER_HOOKS="$HOME/.claude/hooks"
USER_SETTINGS="$HOME/.claude/settings.json"
PROJECT_SETTINGS=".claude/settings.local.json"

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

echo "=== B-259: User-Level Hook Promotion Tests ==="
echo ""

# --- Test 1: Symlink structure ---
echo "Test 1: Symlink structure at ~/.claude/hooks/"
assert "hooks dir exists" '[ -d "$USER_HOOKS" ]'
assert "lib symlink exists" '[ -L "$USER_HOOKS/lib" ]'
assert "lib resolves to real dir" '[ -d "$USER_HOOKS/lib" ]'

TELEGRAM_SCRIPTS="stop-notify.sh telegram-check.sh question-relay.sh session-start-inject.sh pre-compact-state.sh askuser-relay.sh permission-relay.sh reply-poller.sh register-commands.sh cleanup-topics.sh"
for script in $TELEGRAM_SCRIPTS; do
  assert "$script symlink exists" '[ -L "$USER_HOOKS/'"$script"'" ]'
done
echo ""

# --- Test 2: Symlinks resolve to real files ---
echo "Test 2: Symlinks resolve to real executable files"
for script in $TELEGRAM_SCRIPTS; do
  assert "$script resolves to real file" '[ -f "$USER_HOOKS/'"$script"'" ]'
done
echo ""

# --- Test 3: Lib files accessible through symlink ---
echo "Test 3: Lib dependencies accessible"
assert "state-helpers.sh" '[ -f "$USER_HOOKS/lib/state-helpers.sh" ]'
assert "telegram-api.sh" '[ -f "$USER_HOOKS/lib/telegram-api.sh" ]'
assert "telegram-topics.sh" '[ -f "$USER_HOOKS/lib/telegram-topics.sh" ]'
assert "telegram-keyboards.sh" '[ -f "$USER_HOOKS/lib/telegram-keyboards.sh" ]'
echo ""

# --- Test 4: User settings has Telegram hooks ---
echo "Test 4: User settings.json has Telegram hooks"
assert "Valid JSON" 'python3 -c "import json; json.load(open(\"$USER_SETTINGS\"))" 2>/dev/null'
assert "Has hooks key" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"hooks\" in d" 2>/dev/null'
assert "Has SessionStart" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"SessionStart\" in d[\"hooks\"]" 2>/dev/null'
assert "Has PreCompact" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"PreCompact\" in d[\"hooks\"]" 2>/dev/null'
assert "Has PostToolUse" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"PostToolUse\" in d[\"hooks\"]" 2>/dev/null'
assert "Has PermissionRequest" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"PermissionRequest\" in d[\"hooks\"]" 2>/dev/null'
assert "Has Stop" 'python3 -c "import json; d=json.load(open(\"$USER_SETTINGS\")); assert \"Stop\" in d[\"hooks\"]" 2>/dev/null'
echo ""

# --- Test 5: User hooks use $HOME path ---
echo "Test 5: User hooks use \$HOME/.claude/hooks/ paths"
assert "stop-notify uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/stop-notify.sh" "$USER_SETTINGS"'
assert "telegram-check uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/telegram-check.sh" "$USER_SETTINGS"'
assert "session-start-inject uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/session-start-inject.sh" "$USER_SETTINGS"'
assert "pre-compact-state uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/pre-compact-state.sh" "$USER_SETTINGS"'
assert "askuser-relay uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/askuser-relay.sh" "$USER_SETTINGS"'
assert "permission-relay uses \$HOME" 'grep -q "\\\$HOME/.claude/hooks/permission-relay.sh" "$USER_SETTINGS"'
echo ""

# --- Test 6: Project settings has NO Telegram hooks ---
echo "Test 6: Project settings has only project-specific hooks"
assert "Valid JSON" 'python3 -c "import json; json.load(open(\"$PROJECT_SETTINGS\"))" 2>/dev/null'
assert "No stop-notify in project" '! grep -q "stop-notify" "$PROJECT_SETTINGS"'
assert "No telegram-check in project" '! grep -q "telegram-check" "$PROJECT_SETTINGS"'
assert "No question-relay in project" '! grep -q "question-relay" "$PROJECT_SETTINGS"'
assert "No session-start-inject in project" '! grep -q "session-start-inject" "$PROJECT_SETTINGS"'
assert "No pre-compact-state in project" '! grep -q "pre-compact-state" "$PROJECT_SETTINGS"'
assert "No askuser-relay in project" '! grep -q "askuser-relay" "$PROJECT_SETTINGS"'
assert "No permission-relay in project" '! grep -q "permission-relay" "$PROJECT_SETTINGS"'
assert "Has post-bash-check" 'grep -q "post-bash-check" "$PROJECT_SETTINGS"'
assert "Has verification-check" 'grep -q "verification-check" "$PROJECT_SETTINGS"'
echo ""

# --- Test 7: No duplicate hook events between user and project ---
echo "Test 7: No identical event+matcher duplication"
# User has PostToolUse matcher "" (telegram-check), project has PostToolUse matcher "Bash|Edit|Write" (post-bash-check)
# These are DIFFERENT matchers, so no duplication
USER_POST_MATCHER=$(python3 -c "import json; d=json.load(open('$USER_SETTINGS')); print(d['hooks']['PostToolUse'][0]['matcher'])" 2>/dev/null)
PROJECT_POST_MATCHER=$(python3 -c "import json; d=json.load(open('$PROJECT_SETTINGS')); print(d['hooks']['PostToolUse'][0]['matcher'])" 2>/dev/null)
assert "PostToolUse matchers differ" '[ "$USER_POST_MATCHER" != "$PROJECT_POST_MATCHER" ]'

# User has Stop matcher "" (stop-notify), project has Stop matcher "" (verification-check)
# Same matcher but DIFFERENT scripts — both should fire (Claude merges them)
USER_STOP_CMD=$(python3 -c "import json; d=json.load(open('$USER_SETTINGS')); print(d['hooks']['Stop'][0]['hooks'][0]['command'])" 2>/dev/null)
PROJECT_STOP_CMD=$(python3 -c "import json; d=json.load(open('$PROJECT_SETTINGS')); print(d['hooks']['Stop'][0]['hooks'][0]['command'])" 2>/dev/null)
assert "Stop hooks are different scripts" '[ "$USER_STOP_CMD" != "$PROJECT_STOP_CMD" ]'
echo ""

# --- Test 8: HOOK_DIR resolution works through symlinks ---
echo "Test 8: HOOK_DIR resolves correctly through symlinks"
RESOLVED_DIR=$(bash -c 'cd "$(dirname "'"$USER_HOOKS/stop-notify.sh"'")" && pwd')
assert "HOOK_DIR resolves to hooks dir" '[ "$RESOLVED_DIR" = "$USER_HOOKS" ]'
assert "lib accessible from resolved dir" '[ -f "$RESOLVED_DIR/lib/state-helpers.sh" ]'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
