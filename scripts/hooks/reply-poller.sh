#!/bin/bash
# Reply poller: long-poll loop for Telegram after stop-hook exits.
# Handles BOTH text replies (→ claude --resume) and callback queries
# (→ ship/merge/push/pr git actions).
# Usage: reply-poller.sh <session_id> <project_dir>
# Spawned by stop-notify.sh after its poll loop times out.
set -euo pipefail

SESSION_ID="${1:?Usage: reply-poller.sh <session_id> <project_dir>}"
PROJECT_DIR="${2:?Usage: reply-poller.sh <session_id> <project_dir>}"
WAIT="${TELEGRAM_POLLER_TIMEOUT:-1800}"  # Total polling duration (default 30 minutes)
POLL_INTERVAL=30   # Each long-poll timeout
PID_FILE="/tmp/telegram-reply-poller.pid"
LOG_FILE="/tmp/telegram-reply-poller.log"

# Clear nesting guard — this process runs detached from the parent Claude session
unset CLAUDECODE

# Kill any existing poller (only one should run at a time)
if [ -f "$PID_FILE" ]; then
  OLD_PID=$(cat "$PID_FILE" 2>/dev/null || echo 0)
  kill "$OLD_PID" 2>/dev/null || true
  rm -f "$PID_FILE"
fi
echo $$ > "$PID_FILE"

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
if [ -f "$CONFIG" ]; then
  source "$CONFIG"
fi
: "${TELEGRAM_BOT_TOKEN:?Missing TELEGRAM_BOT_TOKEN}"
: "${TELEGRAM_CHAT_ID:?Missing TELEGRAM_CHAT_ID}"

# Source shared libraries
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"
source "$HOOK_DIR/lib/ship.sh"

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")
TOPIC_ID=$(topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH")

log() { echo "[$(date '+%H:%M:%S')] $*" >> "$LOG_FILE"; }

cleanup() { rm -f "$PID_FILE"; }
trap cleanup EXIT

log "Poller started: session=$SESSION_ID project=$PROJECT_DIR topic=$TOPIC_ID wait=${WAIT}s"

# Read current offset — DON'T flush, stop-notify already advanced it
OFFSET=$(tg_read_offset)
log "Starting offset: $OFFSET"

MAX_POLLS=$(( (WAIT + POLL_INTERVAL - 1) / POLL_INTERVAL ))

for i in $(seq 1 "$MAX_POLLS"); do
  RESP=$(tg_get_updates "$POLL_INTERVAL" "$OFFSET" 2>/dev/null || echo '{"ok":false}')

  OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
  [ "$OK" != "true" ] && continue

  # --- Check for callback queries first ---
  CALLBACK_DATA=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(
      .callback_query != null and
      .callback_query.message.chat.id == ($cid | tonumber)
    )] | first | .callback_query.data // empty' \
    2>/dev/null || true)

  CALLBACK_ID=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(
      .callback_query != null and
      .callback_query.message.chat.id == ($cid | tonumber)
    )] | first | .callback_query.id // empty' \
    2>/dev/null || true)

  CALLBACK_MSG_ID=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(
      .callback_query != null and
      .callback_query.message.chat.id == ($cid | tonumber)
    )] | first | .callback_query.message.message_id // empty' \
    2>/dev/null || true)

  if [ -n "$CALLBACK_DATA" ]; then
    CB_ACTION="${CALLBACK_DATA%%:*}"
    case "$CB_ACTION" in
      ship|merge|push|pr)
        log "Callback received: $CB_ACTION"
        tg_answer_callback "$CALLBACK_ID" "$(_action_label "$CB_ACTION")" > /dev/null 2>&1 || true
        if [ -n "$CALLBACK_MSG_ID" ]; then
          tg_edit_message "$CALLBACK_MSG_ID" "$(_action_label "$CB_ACTION")" "HTML" > /dev/null 2>&1 || true
        fi
        tg_advance_offset "$RESP"
        cd "$PROJECT_DIR" 2>/dev/null || exit 0
        case "$CB_ACTION" in
          ship)  handle_ship_cmd "${TOPIC_ID:-0}" ;;
          merge) handle_merge_cmd "${TOPIC_ID:-0}" ;;
          push)  handle_push_cmd "${TOPIC_ID:-0}" ;;
          pr)    handle_pr_cmd "${TOPIC_ID:-0}" ;;
        esac
        log "Callback handled: $CB_ACTION"
        # Send reply prompt and keep polling for follow-up actions/messages
        REPLY_TIMEOUT="${TELEGRAM_REPLY_TIMEOUT:-120}"
        tg_send_message "<i>Reply within ${REPLY_TIMEOUT}s to continue...</i>" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
        OFFSET=$(tg_read_offset)
        continue
        ;;
      *)
        log "Unknown callback: $CALLBACK_DATA"
        tg_answer_callback "$CALLBACK_ID" "Unknown action" > /dev/null 2>&1 || true
        tg_advance_offset "$RESP"
        ;;
    esac
  fi

  # --- Check for text replies ---
  TEXT=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --argjson tid "${TOPIC_ID:-0}" \
    '[.result[] | select(
      .message.chat.id == ($cid | tonumber) and
      .message.text != null and
      (.message.text | startswith("/") | not) and
      (if $tid > 1 then .message.message_thread_id == $tid else true end)
    )] | first | .message.text // empty' \
    2>/dev/null || true)

  # Update offset to acknowledge all received updates
  tg_advance_offset "$RESP"
  OFFSET=$(tg_read_offset)

  if [ -n "$TEXT" ]; then
    log "Text reply received: \"$TEXT\""

    # Immediate acknowledgment to user
    tg_send_message "Sending to Claude..." "$TOPIC_ID" "" > /dev/null 2>&1 || true

    # Dispatch to Claude Code via --resume, capture full response
    log "Dispatching: claude --resume $SESSION_ID -p ..."
    cd "$PROJECT_DIR" 2>/dev/null || exit 0
    CLAUDE_OUTPUT=$(claude --resume "$SESSION_ID" -p \
      "The user is messaging you via Telegram (iPhone). Give a detailed, actionable response — it will be sent back to them as a Telegram message. Their message: ${TEXT}" \
      --output-format text 2>> "$LOG_FILE" || true)

    log "Dispatch complete, response length: ${#CLAUDE_OUTPUT}"

    # Send Claude's actual response back to Telegram (4096 char limit)
    if [ -n "$CLAUDE_OUTPUT" ]; then
      if [ ${#CLAUDE_OUTPUT} -gt 4000 ]; then
        CLAUDE_OUTPUT="${CLAUDE_OUTPUT:0:4000}..."
      fi
      tg_send_message "Claude Orchestra
${CLAUDE_OUTPUT}" "$TOPIC_ID" "" > /dev/null 2>&1 || true
    else
      tg_send_message "Reply dispatched but no response generated." "$TOPIC_ID" "" > /dev/null 2>&1 || true
    fi
    exit 0
  fi
done

log "Poller timed out after ${WAIT}s"
exit 0
