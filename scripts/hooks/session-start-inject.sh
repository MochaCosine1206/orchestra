#!/bin/bash
# SessionStart hook: inject context on session startup, resume, or post-compaction
# Outputs plain text to stdout (most reliable method — JSON additionalContext has known bugs).
# Handles all matchers via the "source" field from stdin JSON: startup, resume, compact.
set -uo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG"

source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"

# Kill any lingering reply-poller from a previous session
POLLER_PID_FILE="/tmp/telegram-reply-poller.pid"
if [ -f "$POLLER_PID_FILE" ]; then
  kill "$(cat "$POLLER_PID_FILE" 2>/dev/null)" 2>/dev/null || true
  rm -f "$POLLER_PID_FILE"
fi

INPUT=$(cat)
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
SOURCE=$(echo "$INPUT" | jq -r '.source // "startup"' 2>/dev/null || echo "startup")
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
STATE_DIR=$(get_state_dir "$PROJECT_SLUG")
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")

# --- Hook mode auto-detection (B-249) ---
DETECTED_MODE=$(detect_mode_from_branch "$BRANCH")
set_hook_mode "$PROJECT_SLUG" "$DETECTED_MODE" "auto" "$BRANCH"
HOOK_MODE=$(get_hook_mode "$PROJECT_SLUG")

# --- Helper: gather git state ---
git_state() {
  if cd "$PROJECT_DIR" 2>/dev/null && git rev-parse --git-dir >/dev/null 2>&1; then
    local branch
    branch=$(git branch --show-current 2>/dev/null || echo "unknown")
    echo "Git branch: $branch"

    local uncommitted
    uncommitted=$(git diff --name-only 2>/dev/null | head -5)
    if [ -n "$uncommitted" ]; then
      local count
      count=$(echo "$uncommitted" | wc -l | tr -d ' ')
      echo "Uncommitted files ($count): $(echo "$uncommitted" | paste -sd', ' -)"
    fi

    local staged
    staged=$(git diff --cached --name-only 2>/dev/null | head -5)
    if [ -n "$staged" ]; then
      echo "Staged files: $(echo "$staged" | paste -sd', ' -)"
    fi
  fi
}

# --- Helper: ensure the project's Telegram topic exists ---
# Inbound polling is now owned by the telegram-forum daemon (single getUpdates
# consumer); the per-session MCP client delivers messages from queue/<slug>/.
# This only creates/renames the topic so replies land in the right place.
telegram_messages() {
  load_telegram_config 2>/dev/null || return
  [ -z "${TELEGRAM_BOT_TOKEN:-}" ] && return
  [ -z "${TELEGRAM_CHAT_ID:-}" ] && return

  topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH" >/dev/null 2>&1
}

# --- Helper: inject user preferences (B-258 Phase 0) ---
preferences_inject() {
  local global_prefs="$HOME/.claude/PREFERENCES.md"
  local project_prefs="${CLAUDE_PROJECT_DIR:-.}/.claude/preferences.md"

  if [ -f "$global_prefs" ]; then
    echo ""
    echo "--- User Preferences (Global) ---"
    cat "$global_prefs"
    echo "--- End User Preferences ---"
  fi

  if [ -f "$project_prefs" ]; then
    echo ""
    echo "--- User Preferences (Project) ---"
    cat "$project_prefs"
    echo "--- End Project Preferences ---"
  fi
}

# --- Helper: rules reminder ---
rules_reminder() {
  echo ""
  echo "RULES REMINDER: 1) Trace before assuming — never claim root cause without evidence. 2) Verify before claiming fixed — re-run failing commands. 3) Write session summaries to /tmp/claude-session-summary.txt. 4) Follow CLAUDE.md workflow guidelines."
}

# --- Orchestra agent mode: minimal context, no topic creation ---
if [ "${ORCHESTRA_AGENT:-}" = "1" ]; then
  echo "=== Session Context (Orchestra Agent Mode) ==="
  echo "Task: ${ORCHESTRA_TASK_ID:-unknown}"
  echo "Parent project: ${ORCHESTRA_PARENT_SLUG:-unknown}"
  echo "Branch: $BRANCH"
  git_state
  rules_reminder
  exit 0
fi

# --- Source-specific injection ---
case "$SOURCE" in
  startup)
    echo "=== Session Context (startup) ==="
    echo "Hook mode: $HOOK_MODE (from branch: $BRANCH)"

    # Prior summary
    if [ -f "$STATE_DIR/last-summary.md" ]; then
      echo ""
      echo "--- Previous Session Summary ---"
      cat "$STATE_DIR/last-summary.md"
      echo ""
      echo "--- End Previous Summary ---"
    fi

    # User preferences
    preferences_inject

    # Git state
    echo ""
    git_state

    # Telegram check
    telegram_messages

    # Rules
    rules_reminder
    ;;

  resume)
    echo "=== Session Context (resume) ==="
    echo "Hook mode: $HOOK_MODE (from branch: $BRANCH)"

    # Minimal: just git state + telegram
    git_state
    telegram_messages
    ;;

  compact)
    echo "=== Session Context (post-compaction) ==="
    echo "Hook mode: $HOOK_MODE (from branch: $BRANCH)"

    # Read checkpoint data
    CHECKPOINT="$STATE_DIR/sessions/${SESSION_ID}-compact.json"
    if [ -f "$CHECKPOINT" ]; then
      COMP_COUNT=$(jq -r '.compaction_count // "?"' "$CHECKPOINT" 2>/dev/null || echo "?")
      COMP_BRANCH=$(jq -r '.git_branch // "unknown"' "$CHECKPOINT" 2>/dev/null || echo "unknown")
      COMP_COMMITS=$(jq -r '.recent_commits // [] | join("\n  ")' "$CHECKPOINT" 2>/dev/null || true)
      COMP_UNCOMMITTED=$(jq -r '.uncommitted_files // [] | join(", ")' "$CHECKPOINT" 2>/dev/null || true)

      echo "Compaction #${COMP_COUNT} for this session"
      echo "Branch: $COMP_BRANCH"
      if [ -n "$COMP_COMMITS" ]; then
        echo "Recent commits:"
        echo "  $COMP_COMMITS"
      fi
      if [ -n "$COMP_UNCOMMITTED" ]; then
        echo "Uncommitted files: $COMP_UNCOMMITTED"
      fi
    fi

    # Prior summary
    if [ -f "$STATE_DIR/last-summary.md" ]; then
      echo ""
      echo "--- Last Summary ---"
      cat "$STATE_DIR/last-summary.md"
      echo "--- End Summary ---"
    fi

    # User preferences
    preferences_inject

    # Telegram check
    telegram_messages

    # Rules
    rules_reminder
    ;;

  *)
    # Unknown source — provide minimal context
    echo "=== Session Context ==="
    git_state
    rules_reminder
    ;;
esac
