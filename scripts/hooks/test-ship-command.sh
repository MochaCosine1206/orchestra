#!/bin/bash
# Tests for B-256: /ship bot command, dynamic buttons, and git action handlers
# Run: bash scripts/hooks/test-ship-command.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHIP_LIB="$SCRIPT_DIR/lib/ship.sh"
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

echo "=== B-256: Ship Command & Dynamic Buttons Tests ==="
echo ""

# --- Test 1: /ship command registered ---
echo "Test 1: /ship command in bot registration"
assert "register-commands.sh has /ship" 'grep -q "ship" "$SCRIPT_DIR/register-commands.sh"'
echo ""

# --- Test 2: telegram-check.sh routes /ship command ---
echo "Test 2: telegram-check.sh routes /ship command"
assert "Has /ship case" 'grep -q "/ship" "$SCRIPT_DIR/telegram-check.sh"'
assert "Has handle_ship_cmd" 'grep -q "handle_ship_cmd" "$SCRIPT_DIR/telegram-check.sh"'
assert "Sources lib/ship.sh" 'grep -q "lib/ship.sh" "$SCRIPT_DIR/telegram-check.sh"'
echo ""

# --- Test 3: Ship handler performs git operations (in lib/ship.sh) ---
echo "Test 3: Ship handler git operations"
assert "Runs git add" 'grep -q "git add" "$SHIP_LIB"'
assert "Runs git commit" 'grep -q "git commit" "$SHIP_LIB"'
assert "Runs git push" 'grep -q "git push" "$SHIP_LIB"'
assert "Runs gh pr create" 'grep -q "gh pr create" "$SHIP_LIB"'
echo ""

# --- Test 4: Ship handler sends status updates ---
echo "Test 4: Ship handler sends Telegram feedback"
assert "Sends progress message" 'grep -q "tg_send_message" "$SHIP_LIB"'
assert "Reports PR URL on success" 'grep -q "PR\|pull request\|pr create" "$SHIP_LIB"'
echo ""

# --- Test 5: Stop-notify has dynamic action button ---
echo "Test 5: Stop-notify has dynamic action button"
assert "Uses tg_send_with_keyboard for summaries" 'grep -q "tg_send_with_keyboard" "$SCRIPT_DIR/stop-notify.sh"'
assert "Uses git_action_keyboard" 'grep -q "git_action_keyboard" "$SCRIPT_DIR/stop-notify.sh"'
assert "Falls back to tg_send_message when no action" 'grep -q "tg_send_message.*TEXT.*TOPIC_ID" "$SCRIPT_DIR/stop-notify.sh"'
echo ""

# --- Test 6: Ship handler reads last-summary for commit message ---
echo "Test 6: Ship uses last-summary.md for commit message"
assert "Reads last-summary.md" 'grep -q "last-summary" "$SHIP_LIB"'
echo ""

# --- Test 7: Ship handler guards against main/dev direct push ---
echo "Test 7: Safety guards"
assert "Checks current branch" 'grep -q "branch\|BRANCH" "$SHIP_LIB"'
assert "Guards main" 'grep -q "main" "$SHIP_LIB"'
assert "Guards dev" 'grep -q "dev" "$SHIP_LIB"'
echo ""

# --- Test 8: Callback handlers for all action types ---
echo "Test 8: Callback handlers for all action types"
assert "telegram-check handles ship/merge/push/pr" 'grep -q "ship|merge|push|pr" "$SCRIPT_DIR/telegram-check.sh"'
assert "stop-notify handles ship/merge/push/pr" 'grep -q "ship|merge|push|pr" "$SCRIPT_DIR/stop-notify.sh"'
echo ""

# --- Test 9: Stop-notify loop-based polling ---
echo "Test 9: Stop-notify loop-based polling"
assert "Has polling loop" 'grep -q "MAX_POLLS\|for i in" "$SCRIPT_DIR/stop-notify.sh"'
assert "Checks callback_query" 'grep -q "callback_query\|CALLBACK_DATA" "$SCRIPT_DIR/stop-notify.sh"'
assert "Sources ship lib" 'grep -q "lib/ship.sh" "$SCRIPT_DIR/stop-notify.sh"'
echo ""

# --- Test 10: Dynamic keyboard function ---
echo "Test 10: git_action_keyboard context detection"
assert "Has git_action_keyboard function" 'grep -q "git_action_keyboard()" "$SHIP_LIB"'
assert "Detects trunk branches" 'grep -q "is_trunk" "$SHIP_LIB"'
assert "Detects uncommitted changes" 'grep -q "has_changes" "$SHIP_LIB"'
assert "Checks for existing PR" 'grep -q "gh pr view" "$SHIP_LIB"'
assert "Ship button for feature+changes" 'grep -q "Ship It" "$SHIP_LIB"'
assert "Merge button for feature+PR" 'grep -q "Merge" "$SHIP_LIB"'
assert "Push button for trunk+changes" 'grep -q "Push It" "$SHIP_LIB"'
assert "Create PR button for feature+pushed" 'grep -q "Create PR" "$SHIP_LIB"'
echo ""

# --- Test 11: Merge handler ---
echo "Test 11: Merge handler"
assert "Has handle_merge_cmd" 'grep -q "handle_merge_cmd" "$SHIP_LIB"'
assert "Uses gh pr merge" 'grep -q "gh pr merge" "$SHIP_LIB"'
assert "Uses --squash" 'grep -q "\-\-squash" "$SHIP_LIB"'
assert "Deletes branch after merge" 'grep -q "\-\-delete-branch" "$SHIP_LIB"'
echo ""

# --- Test 12: Push handler ---
echo "Test 12: Push handler"
assert "Has handle_push_cmd" 'grep -q "handle_push_cmd" "$SHIP_LIB"'
assert "Pushes to current branch" 'grep -A40 "handle_push_cmd" "$SHIP_LIB" | grep -q "git push"'
echo ""

# --- Test 13: Create PR handler ---
echo "Test 13: Create PR handler"
assert "Has handle_pr_cmd" 'grep -q "handle_pr_cmd" "$SHIP_LIB"'
assert "Creates PR via gh" 'grep -A40 "handle_pr_cmd" "$SHIP_LIB" | grep -q "gh pr create"'
assert "Checks for existing PR first" 'grep -A20 "handle_pr_cmd" "$SHIP_LIB" | grep -q "gh pr view"'
echo ""

# --- Test 14: Ship handler sends Merge keyboard after success (G90) ---
echo "Test 14: Ship handler offers Merge button after shipping"
assert "Ship handler calls tg_send_with_keyboard for Shipped msg" 'grep -A5 "Shipped" "$SHIP_LIB" | grep -q "tg_send_with_keyboard"'
assert "Ship handler builds merge keyboard after PR" 'grep -q "merge_kb\|Merge.*callback_data\|merge:.*slug" "$SHIP_LIB"'
echo ""

# --- Test 15: Stop-notify continues polling after ship for Merge button (G90) ---
echo "Test 15: Stop-notify keeps polling after ship action"
STOP_SCRIPT="$SCRIPT_DIR/stop-notify.sh"
assert "Ship case continues instead of exiting" 'grep -A3 "CB_ACTION.*=.*ship" "$STOP_SCRIPT" | grep -q "continue"'
echo ""

# --- Test 16: Merge handler runs checkout dev + pull + shows summary (G90) ---
echo "Test 16: Merge handler runs checkout + pull + shows summary"
assert "Merge handler runs git checkout dev" 'grep -q "git checkout dev" "$SHIP_LIB"'
assert "Merge handler runs git pull" 'grep -A50 "handle_merge_cmd" "$SHIP_LIB" | grep -q "git pull"'
assert "Merge handler reads last-summary" 'grep -A60 "handle_merge_cmd" "$SHIP_LIB" | grep -q "last-summary"'
echo ""

# --- Test 17: Push handler shows summary after push (G90) ---
echo "Test 17: Push handler shows summary after push"
assert "Push handler reads last-summary" 'grep -A60 "handle_push_cmd" "$SHIP_LIB" | grep -q "last-summary"'
echo ""

# --- Test 18: Reply-poller continues polling after callback (not exit 0) ---
echo "Test 18: Reply-poller keeps polling after handling callbacks"
POLLER_SCRIPT="$SCRIPT_DIR/reply-poller.sh"
assert "Reply-poller continues after callback" 'grep -A5 "Callback handled" "$POLLER_SCRIPT" | grep -q "continue"'
assert "Reply-poller does NOT exit after callback" '! grep -A5 "Callback handled" "$POLLER_SCRIPT" | grep -q "exit 0"'
assert "Reply-poller sends timer footer after callback" 'grep -A5 "Callback handled" "$POLLER_SCRIPT" | grep -q "Reply within"'
echo ""

# --- Test 19: All handlers include timer footer ---
echo "Test 19: All handler final messages include reply timer"
assert "Ship handler has timer footer" 'grep -A3 "Shipped! PR:" "$SHIP_LIB" | grep -q "Reply within"'
assert "Merge handler has timer footer" 'grep -B2 "handle_merge_cmd" "$SHIP_LIB" -A80 | grep "Checked out dev" | head -1 && grep -A3 "Checked out dev" "$SHIP_LIB" | grep -q "Reply within"'
assert "Push handler has timer footer" 'grep -A3 "Pushed to" "$SHIP_LIB" | grep -q "Reply within"'
assert "PR handler has timer footer" 'grep -A3 "PR created:" "$SHIP_LIB" | grep -q "Reply within"'
echo ""

echo "=== Results: $PASS/$TOTAL passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
else
  exit 0
fi
