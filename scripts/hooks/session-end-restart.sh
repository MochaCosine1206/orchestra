#!/bin/bash
# B-241: Auto-restart on abnormal session termination.
# Registered as SessionEnd hook with matcher "other" — only fires on non-user exits.
# User-initiated exits (clear, logout, prompt_input_exit) never reach this hook.
#
# IMPORTANT: "other" also fires when a user starts a new session in the same terminal
# (the old session is replaced, not crashed). We detect this by checking if another
# Claude process is already running in this project directory and skip the restart.
#
# The hook system's matcher filters by the structured "reason" field.
set -uo pipefail

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)
TRANSCRIPT=$(echo "$INPUT" | jq -r '.transcript_path // empty' 2>/dev/null || true)
PROJECT_DIR=$(echo "$INPUT" | jq -r '.cwd // empty' 2>/dev/null || true)

[ -z "$PROJECT_DIR" ] && PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Skip during benchmarks
[ "${ORCHESTRA_BENCHMARK:-0}" = "1" ] && exit 0
# Skip for orchestra agents (they have their own lifecycle)
[ "${ORCHESTRA_AGENT:-}" = "1" ] && exit 0
# Skip for headless sessions — walk up the process tree looking for orchestra
PID_CHECK=$$
for _ in 1 2 3 4 5; do
  PID_CHECK=$(ps -o ppid= -p "$PID_CHECK" 2>/dev/null | tr -d ' ')
  [ -z "$PID_CHECK" ] || [ "$PID_CHECK" = "1" ] && break
  ANCESTOR_CMD=$(ps -o comm= -p "$PID_CHECK" 2>/dev/null || true)
  [[ "$ANCESTOR_CMD" == *orchestra* ]] && exit 0
done

# G98: Skip if another Claude session is already running in this project.
# When a user starts a new session, the old one gets "other" exit reason.
# Detect this by checking for live Claude processes whose cwd is this project.
# `lsof -a -d cwd -c claude` is fast and reliable on macOS (~5ms).
CLAUDE_IN_PROJECT=$(lsof -a -d cwd -c claude 2>/dev/null \
  | awk -v dir="$PROJECT_DIR" '$NF == dir {print $2}' \
  || true)
if [ -n "$CLAUDE_IN_PROJECT" ]; then
  # Another Claude process has this project as its cwd — session was replaced, not crashed
  exit 0
fi

# Source credentials
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG"

# Source shared libraries
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
PROJECT_STATE_DIR=$(ensure_state_dir "$PROJECT_SLUG")
NOW=$(date +%s)

# --- Save session state ---
if [ -n "$SESSION_ID" ]; then
  migrate_flat_state "$SESSION_ID" "$PROJECT_SLUG"
  mkdir -p "${PROJECT_STATE_DIR}/sessions"
  jq -n \
    --arg sid "$SESSION_ID" \
    --arg reason "abnormal" \
    --arg cwd "$PROJECT_DIR" \
    --argjson ts "$NOW" \
    '{session_id:$sid, reason:$reason, cwd:$cwd, timestamp:$ts}' \
    > "${PROJECT_STATE_DIR}/sessions/${SESSION_ID}-stop.json" 2>/dev/null || true
fi

# --- Determine Telegram topic ---
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")
TOPIC_ID=$(topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH")

# --- Send limit notification ---
if [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${TELEGRAM_CHAT_ID:-}" ]; then
  LIMIT_TEXT="<b>Session ended abnormally</b>
<b>Project:</b> $(basename "$PROJECT_DIR")
<b>Session:</b> ${SESSION_ID:-unknown}
<i>Resume: <code>claude --continue</code></i>"

  tg_send_message "$LIMIT_TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
fi

# --- Circuit breaker: max 3 restarts per project within the same hour ---
RESTART_COUNT_FILE="${PROJECT_STATE_DIR}/watchdog-restart-count"
RESTART_HOUR_FILE="${PROJECT_STATE_DIR}/watchdog-restart-hour"
CURRENT_HOUR=$(date +%Y%m%d%H)
RESTART_COUNT=0

if [ -f "$RESTART_HOUR_FILE" ] && [ "$(cat "$RESTART_HOUR_FILE" 2>/dev/null)" = "$CURRENT_HOUR" ]; then
  RESTART_COUNT=$(cat "$RESTART_COUNT_FILE" 2>/dev/null || echo 0)
else
  echo "$CURRENT_HOUR" > "$RESTART_HOUR_FILE"
  echo 0 > "$RESTART_COUNT_FILE"
fi

if [ "$RESTART_COUNT" -ge 3 ]; then
  [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && \
    tg_send_message "Circuit breaker: 3 restarts this hour. Manual: <code>claude --continue</code>" \
      "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
  exit 0
fi

# --- Cooldown: default 30s for unknown abnormal exits ---
COOLDOWN=30

[ -n "${TELEGRAM_BOT_TOKEN:-}" ] && \
  tg_send_message "Auto-restarting in ${COOLDOWN}s... (attempt $((RESTART_COUNT + 1))/3)" \
    "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true

# Update circuit breaker counter
echo "$CURRENT_HOUR" > "$RESTART_HOUR_FILE"
echo $((RESTART_COUNT + 1)) > "$RESTART_COUNT_FILE"

# Persist summary before restart
SUMMARY_FILE="/tmp/claude-session-summary.txt"
if [ -f "$SUMMARY_FILE" ]; then
  cp "$SUMMARY_FILE" "${PROJECT_STATE_DIR}/last-summary.md" 2>/dev/null || true
  rm -f "$SUMMARY_FILE"
fi

sleep "$COOLDOWN"

# exec replaces this process — takes over the terminal
exec claude --continue
