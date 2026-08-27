#!/bin/bash
# Synchronous PostToolUse hook: check for pending Telegram messages mid-session
# AND deliver pending Claude responses back to Telegram.
# Rate-limited (15s) to avoid slowing down every tool call.
# Synchronous hooks can't double-fire (only one runs at a time), so no lock needed.
# Shares offset file /tmp/telegram-last-update-id with stop-notify.sh.
set -uo pipefail

NOW=$(date +%s)

# Orchestra agent mode: agents don't receive messages — exit immediately
if [ "${ORCHESTRA_AGENT:-}" = "1" ]; then
  exit 0
fi

# B-271: Skip during benchmark runs — prevents reconciliation/agent sessions
# from having unwanted Telegram conversations with the user.
if [ "${ORCHESTRA_BENCHMARK:-0}" = "1" ]; then
  exit 0
fi

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG"

[ -z "${TELEGRAM_BOT_TOKEN:-}" ] && exit 0
[ -z "${TELEGRAM_CHAT_ID:-}" ] && exit 0

# Source shared libraries
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")
TOPIC_ID=$(topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH")

OFFSET_FILE="/tmp/telegram-last-update-id"
PENDING_REPLY="/tmp/claude-telegram-pending-reply"
SUMMARY_FILE="/tmp/claude-session-summary.txt"

# --- Bot command handlers (B-244) ---
# All handlers must be fast (<10s hook timeout).

handle_status_cmd() {
  local thread_id="${1:-0}"
  local slug="$PROJECT_SLUG"
  local state_dir
  state_dir=$(get_state_dir "$slug")
  local status_text="<b>Project Status: ${slug}</b>"

  # Check for active session
  local latest_stop
  latest_stop=$(ls -t "$state_dir/sessions/"*-stop.json 2>/dev/null | head -1 || true)
  if [ -n "$latest_stop" ]; then
    local reason ts
    reason=$(jq -r '.reason // "unknown"' "$latest_stop" 2>/dev/null || echo "unknown")
    ts=$(jq -r '.timestamp // 0' "$latest_stop" 2>/dev/null || echo 0)
    if [ "$ts" -gt 0 ] 2>/dev/null; then
      local age=$(( $(date +%s) - ts ))
      local mins=$(( age / 60 ))
      status_text="${status_text}
Last stop: ${mins}m ago (${reason})"
    fi
  fi

  # Check for last summary
  if [ -f "$state_dir/last-summary.md" ]; then
    local summary
    summary=$(head -3 "$state_dir/last-summary.md" | cut -c1-200)
    local safe_summary
    safe_summary=$(tg_escape_html "$summary")
    status_text="${status_text}
<b>Last summary:</b> ${safe_summary}"
  fi

  tg_send_message "$status_text" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

handle_list_cmd() {
  local projects=""
  for id_file in "$SESSION_STATE_BASE"/*/telegram/topic_id; do
    [ -f "$id_file" ] || continue
    local slug_dir
    slug_dir=$(dirname "$(dirname "$id_file")")
    local slug
    slug=$(basename "$slug_dir")
    local tid
    tid=$(cat "$id_file" 2>/dev/null || true)
    local closed=""
    [ -f "$slug_dir/telegram/topic_closed" ] && closed=" [closed]"
    projects="${projects}
- <b>${slug}</b> (topic ${tid})${closed}"
  done

  if [ -z "$projects" ]; then
    projects="
No projects with topics found."
  fi

  # Send to General topic (id=1) since /list is cross-project
  tg_send_message "<b>Active Projects</b>${projects}" "1" "HTML" > /dev/null 2>&1 || true
}

handle_newproject_cmd() {
  local text="$1"
  local thread_id="${2:-0}"
  # Extract project name: "/newproject my-project" → "my-project"
  local name
  name=$(echo "$text" | sed 's|^/newproject[^ ]* *||' | tr -d '\n' | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g')

  if [ -z "$name" ]; then
    tg_send_message "Usage: /newproject &lt;name&gt;" "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  local new_tid
  new_tid=$(topic_create "$name" "$name" 2>/dev/null) || true

  if [ -n "$new_tid" ]; then
    tg_send_message "Created topic <b>${name}</b> (thread ${new_tid})" "$thread_id" "HTML" > /dev/null 2>&1 || true
  else
    tg_send_message "Failed to create topic for ${name}" "$thread_id" "" > /dev/null 2>&1 || true
  fi
}

handle_close_cmd() {
  local thread_id="${1:-0}"
  local slug
  slug=$(topic_find_slug_by_id "$thread_id")

  if [ -z "$slug" ]; then
    tg_send_message "No project found for this topic. Use in a project topic." "$thread_id" "" > /dev/null 2>&1 || true
    return
  fi

  topic_archive "$slug" "Done"
  tg_send_message "Project <b>${slug}</b> archived as [Done]" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

handle_pause_cmd() {
  local thread_id="${1:-0}"
  local slug
  slug=$(topic_find_slug_by_id "$thread_id")

  if [ -z "$slug" ]; then
    tg_send_message "No project found for this topic. Use in a project topic." "$thread_id" "" > /dev/null 2>&1 || true
    return
  fi

  topic_archive "$slug" "Paused"
  tg_send_message "Project <b>${slug}</b> marked as [Paused]" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

handle_resume_cmd() {
  local thread_id="${1:-0}"
  local slug
  slug=$(topic_find_slug_by_id "$thread_id")

  if [ -z "$slug" ]; then
    tg_send_message "No project found for this topic. Use in a project topic." "$thread_id" "" > /dev/null 2>&1 || true
    return
  fi

  topic_reopen "$slug"
  tg_send_message "Project <b>${slug}</b> resumed" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

handle_mode_cmd() {
  local text="$1"
  local thread_id="${2:-0}"
  # Extract mode name: "/mode research" → "research", "/mode" → ""
  local requested_mode
  requested_mode=$(echo "$text" | sed 's|^/mode[^ ]* *||' | tr -d '\n' | tr '[:upper:]' '[:lower:]')

  if [ -z "$requested_mode" ]; then
    # Show current mode
    local current_mode
    current_mode=$(get_hook_mode "$PROJECT_SLUG")
    local mode_file="$SESSION_STATE_BASE/$PROJECT_SLUG/hook-mode.json"
    local set_by="auto"
    local mode_branch=""
    if [ -f "$mode_file" ]; then
      set_by=$(jq -r '.set_by // "auto"' "$mode_file" 2>/dev/null || echo "auto")
      mode_branch=$(jq -r '.branch // ""' "$mode_file" 2>/dev/null || echo "")
    fi
    tg_send_message "<b>Hook Mode:</b> ${current_mode} (set by: ${set_by}, branch: ${mode_branch})" "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  # Validate mode name
  case "$requested_mode" in
    default|implementation|research|debug)
      set_hook_mode "$PROJECT_SLUG" "$requested_mode" "user" "$BRANCH"
      tg_send_message "Hook mode set to <b>${requested_mode}</b> (user override)" "$thread_id" "HTML" > /dev/null 2>&1 || true
      ;;
    *)
      tg_send_message "Unknown mode: ${requested_mode}. Valid modes: default, implementation, research, debug" "$thread_id" "" > /dev/null 2>&1 || true
      ;;
  esac
}

handle_help_cmd() {
  local thread_id="${1:-0}"
  local help_text="<b>Claude Orchestra Bot Commands</b>

/status — Show current session status
/list — List active project topics
/newproject &lt;name&gt; — Create a new project topic
/close — Close current project topic
/pause — Mark project as paused
/resume — Resume paused project
/ship — Commit, push, and create PR
/mode [name] — Show/set hook mode (default, implementation, research, debug)
/approve — Approve pending operation
/help — Show this help message

Send any other text to relay to the active Claude session."

  tg_send_message "$help_text" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

# Ship handler — sourced from shared lib (B-256)
source "$HOOK_DIR/lib/ship.sh"

# --- Deliver pending reply: if Claude wrote a summary after a Telegram message, send it back ---
# This runs BEFORE rate limiting so replies are delivered promptly.
if [ -f "$PENDING_REPLY" ]; then
  PENDING_TS=$(cat "$PENDING_REPLY" 2>/dev/null || echo 0)
  PENDING_AGE=$((NOW - PENDING_TS))
  if [ "$PENDING_AGE" -gt 600 ]; then
    # Stale (>10 min) — discard
    rm -f "$PENDING_REPLY"
  elif [ -f "$SUMMARY_FILE" ]; then
    REPLY_CONTENT=$(cat "$SUMMARY_FILE")
    rm -f "$PENDING_REPLY"
    # Leave $SUMMARY_FILE for stop-notify.sh to send with action buttons
    if [ -n "$REPLY_CONTENT" ]; then
      SAFE_CONTENT=$(tg_escape_html "$REPLY_CONTENT")
      REPLY_TIMEOUT="${TELEGRAM_REPLY_TIMEOUT:-120}"
      STATUS_TEXT="<b>Status Update</b>
${SAFE_CONTENT}

<i>Reply within ${REPLY_TIMEOUT}s to continue...</i>"
      tg_send_message "$STATUS_TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
      # Write summary hash so stop-notify.sh dedup guard prevents duplicate send.
      # stop-notify.sh checks last-summary-hash before sending — if we already
      # delivered the content here, it will skip the duplicate.
      PROJECT_STATE_DIR=$(ensure_state_dir "$PROJECT_SLUG")
      SUMMARY_HASH=$(echo "$REPLY_CONTENT" | md5 -q 2>/dev/null || echo "$REPLY_CONTENT" | md5sum | cut -d' ' -f1)
      echo "$SUMMARY_HASH" > "${PROJECT_STATE_DIR}/last-summary-hash" 2>/dev/null || true
    fi
  fi
fi

# --- Rate limit: only poll Telegram every 15 seconds ---
RATE_FILE="/tmp/telegram-check-last"
if [ -f "$RATE_FILE" ]; then
  LAST=$(cat "$RATE_FILE" 2>/dev/null || echo 0)
  if [ $((NOW - LAST)) -lt 15 ]; then
    exit 0
  fi
fi
echo "$NOW" > "$RATE_FILE"

# Read stored offset
if [ -f "$OFFSET_FILE" ]; then
  OFFSET=$(cat "$OFFSET_FILE")
else
  # First run: flush old updates so we only see new messages
  tg_flush_offset
  OFFSET=$(cat "$OFFSET_FILE" 2>/dev/null || echo 0)
fi

# Quick check — 0 timeout, instant return (includes message + callback_query)
RESP=$(tg_get_updates 0 "$OFFSET" 2>/dev/null || echo '{"ok":false}')

OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
[ "$OK" != "true" ] && exit 0

# --- Process callback queries first ---
CALLBACK_ID=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
  '[.result[] | select(.callback_query != null and .callback_query.message.chat.id == ($cid | tonumber))] | first | .callback_query.id // empty' \
  2>/dev/null || true)

if [ -n "$CALLBACK_ID" ]; then
  CALLBACK_DATA=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(.callback_query != null and .callback_query.message.chat.id == ($cid | tonumber))] | first | .callback_query.data // empty' \
    2>/dev/null || true)
  CALLBACK_MSG_ID=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(.callback_query != null and .callback_query.message.chat.id == ($cid | tonumber))] | first | .callback_query.message.message_id // empty' \
    2>/dev/null || true)

  if [ -n "$CALLBACK_DATA" ]; then
    # Source keyboard lib for callback parsing
    source "$HOOK_DIR/lib/telegram-keyboards.sh"

    PARSED=$(cb_parse "$CALLBACK_DATA")
    ACTION=$(echo "$PARSED" | head -1)
    REQ_ID=$(echo "$PARSED" | tail -1)

    # Handle git action callbacks: ship:/merge:/push:/pr: (B-256)
    CB_ACTION="${CALLBACK_DATA%%:*}"
    case "$CB_ACTION" in
      ship|merge|push|pr)
        tg_answer_callback "$CALLBACK_ID" "$(_action_label "$CB_ACTION")" > /dev/null 2>&1 || true
        if [ -n "$CALLBACK_MSG_ID" ]; then
          tg_edit_message "$CALLBACK_MSG_ID" "$(_action_label "$CB_ACTION")" "HTML" > /dev/null 2>&1 || true
        fi
        case "$CB_ACTION" in
          ship)  handle_ship_cmd "${TOPIC_ID:-0}" ;;
          merge) handle_merge_cmd "${TOPIC_ID:-0}" ;;
          push)  handle_push_cmd "${TOPIC_ID:-0}" ;;
          pr)    handle_pr_cmd "${TOPIC_ID:-0}" ;;
        esac
        tg_advance_offset "$RESP"
        exit 0
        ;;
    esac

    # Answer callback (mandatory — suppresses loading spinner)
    tg_answer_callback "$CALLBACK_ID" "$(_cap "$ACTION")d" > /dev/null 2>&1 || true

    # Write decision for permission-relay.sh to pick up
    cb_write_decision "$REQ_ID" "$ACTION" "$PROJECT_SLUG"

    # Edit original message to show result
    if [ -n "$CALLBACK_MSG_ID" ]; then
      tg_edit_message "$CALLBACK_MSG_ID" "Decision: ${ACTION}" "HTML" > /dev/null 2>&1 || true
    fi
  fi

  # Update offset
  tg_advance_offset "$RESP"
  exit 0
fi

# --- Process ALL text messages (not just first) ---
# Extract all messages from our chat, process commands first, then aggregate regular messages.
ALL_MESSAGES=$(echo "$RESP" | jq -c --arg cid "$TELEGRAM_CHAT_ID" \
  '[.result[] | select(
    .message.chat.id == ($cid | tonumber) and
    .message.text != null
  ) | {text: .message.text, thread_id: (.message.message_thread_id // 0)}]' \
  2>/dev/null || echo '[]')

MSG_COUNT=$(echo "$ALL_MESSAGES" | jq 'length' 2>/dev/null || echo 0)

# Update offset to acknowledge all received updates
tg_advance_offset "$RESP"

# --- Bot command routing (B-244) ---
# Process commands first (any message starting with /)
REGULAR_TEXTS=""
for idx in $(seq 0 $((MSG_COUNT - 1))); do
  TEXT=$(echo "$ALL_MESSAGES" | jq -r ".[$idx].text" 2>/dev/null || true)
  MSG_THREAD_ID=$(echo "$ALL_MESSAGES" | jq -r ".[$idx].thread_id" 2>/dev/null || echo 0)

  case "$TEXT" in
    /status|/status@*)
      handle_status_cmd "$MSG_THREAD_ID"
      ;;
    /list|/list@*)
      handle_list_cmd
      ;;
    /newproject*)
      handle_newproject_cmd "$TEXT" "$MSG_THREAD_ID"
      ;;
    /close|/close@*)
      handle_close_cmd "$MSG_THREAD_ID"
      ;;
    /pause|/pause@*)
      handle_pause_cmd "$MSG_THREAD_ID"
      ;;
    /resume|/resume@*)
      handle_resume_cmd "$MSG_THREAD_ID"
      ;;
    /ship|/ship@*)
      handle_ship_cmd "$MSG_THREAD_ID"
      ;;
    /mode*)
      handle_mode_cmd "$TEXT" "$MSG_THREAD_ID"
      ;;
    /help|/help@*)
      handle_help_cmd "$MSG_THREAD_ID"
      ;;
    /*)
      # Unknown command — ignore
      ;;
    *)
      # Regular message — filter by topic, then aggregate
      if [ "${TOPIC_ID:-0}" -gt 1 ] && [ "$MSG_THREAD_ID" != "$TOPIC_ID" ]; then
        continue
      fi
      if [ -n "$REGULAR_TEXTS" ]; then
        REGULAR_TEXTS="${REGULAR_TEXTS}
---
${TEXT}"
      else
        REGULAR_TEXTS="$TEXT"
      fi
      ;;
  esac
done

# --- Deliver aggregated regular messages to Claude ---
if [ -n "$REGULAR_TEXTS" ]; then
  # Acknowledge on Telegram
  tg_send_message "Received mid-task, Claude will see this shortly..." "$TOPIC_ID" "" > /dev/null 2>&1 || true

  # Mark pending reply so next invocation sends Claude's summary back to Telegram
  echo "$NOW" > "$PENDING_REPLY"

  # Deliver ALL messages to Claude via block decision
  ESCAPED=$(echo "$REGULAR_TEXTS" | jq -Rs '.' | sed 's/^"//;s/"$//')
  cat <<HOOK_EOF
{"decision":"block","reason":"TELEGRAM MESSAGE from user: ${ESCAPED}\n\nIMPORTANT: Write your full response to /tmp/claude-session-summary.txt so it gets delivered back to Telegram. Do not condense or shorten — write the same text you would show in the conversation."}
HOOK_EOF
  exit 0
fi

exit 0
