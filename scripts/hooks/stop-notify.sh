#!/bin/bash
# Stop hook: send Telegram notification with session summary + inline poll for reply
# If user replies within 30s, returns {"decision":"block"} to continue in same session.
# Shares offset file /tmp/telegram-last-update-id with telegram-check.sh.
# Uses -uo (not -euo) so individual command failures don't kill the hook silently.
set -uo pipefail

# Safety: redirect all stdout to stderr so nothing accidentally reaches Claude Code's
# JSON parser. The original stdout (fd 3) is used ONLY for intentional JSON output.
exec 3>&1 1>&2

# Dedup guard: skip if last notification was <10 seconds ago
DEDUP_FILE="/tmp/claude-stop-notify-last"
NOW=$(date +%s)
if [ -f "$DEDUP_FILE" ]; then
  LAST=$(cat "$DEDUP_FILE" 2>/dev/null || echo 0)
  if [ $((NOW - LAST)) -lt 10 ]; then
    exit 0
  fi
fi

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
# Tests can set TELEGRAM_NOTIFY_CONFIG=/dev/null to prevent real API calls
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG"

[ -z "${TELEGRAM_BOT_TOKEN:-}" ] && exit 0
[ -z "${TELEGRAM_CHAT_ID:-}" ] && exit 0

# B-271: Skip Telegram notifications during benchmark runs.
# Benchmark spawns multiple claude -p sessions (decompose, agents, reconcile)
# that would each trigger stop-notify and spam the user's Telegram.
[ "${ORCHESTRA_BENCHMARK:-0}" = "1" ] && exit 0

INPUT=$(cat)
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
OFFSET_FILE="/tmp/telegram-last-update-id"

# Source shared libraries
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"

# --- Orchestra agent mode: brief status to parent topic, no buttons/polling ---
if [ "${ORCHESTRA_AGENT:-}" = "1" ]; then
  # Use agent's own last message, not the shared summary file (which belongs to the main session)
  SUMMARY=$(echo "$INPUT" | jq -r '.last_assistant_message // empty' 2>/dev/null | head -5 | cut -c1-500)
  [ -z "$SUMMARY" ] && SUMMARY="(no output)"

  PARENT_SLUG="${ORCHESTRA_PARENT_SLUG:-}"
  TASK_ID="${ORCHESTRA_TASK_ID:-unknown}"
  if [ -n "$PARENT_SLUG" ]; then
    PARENT_TOPIC_ID=$(get_parent_topic_id "$PARENT_SLUG")
    if [ -n "$PARENT_TOPIC_ID" ] && [ -n "${TELEGRAM_BOT_TOKEN:-}" ]; then
      SAFE_SUMMARY=$(tg_escape_html "$SUMMARY")
      # Brief one-line agent status — no buttons, no reply prompt
      AGENT_STATUS="Agent ${TASK_ID} done — ${SAFE_SUMMARY}"
      tg_send_message "$AGENT_STATUS" "$PARENT_TOPIC_ID" "HTML" > /dev/null 2>&1 || true
    fi
  fi
  # No polling, no reply-poller, no buttons — just exit
  exit 0
fi

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
PROJECT_STATE_DIR=$(ensure_state_dir "$PROJECT_SLUG")
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")
TOPIC_ID=$(topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH")

# Clean up mid-session pending reply marker (stop hook sends its own notification)
rm -f /tmp/claude-telegram-pending-reply

LAST_MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // empty' 2>/dev/null || true)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)

# --- Build summary ---
# Primary: last_assistant_message from hook input (always available).
# Override: summary file, when model writes one to signal "work complete, ready for action."
# The summary file doubles as the "ready to push" signal — action buttons only appear when it exists.
# Per-project path prevents cross-session contamination when multiple sessions run concurrently.
SUMMARY_FILE="${PROJECT_STATE_DIR}/session-summary.txt"
LEGACY_SUMMARY_FILE="/tmp/claude-session-summary.txt"
READY_FOR_ACTION=false

if [ -f "$SUMMARY_FILE" ]; then
  SUMMARY=$(cat "$SUMMARY_FILE")
  READY_FOR_ACTION=true
elif [ -f "$LEGACY_SUMMARY_FILE" ]; then
  # Legacy fallback: global /tmp/ path (remove after all CLAUDE.md files updated)
  SUMMARY=$(cat "$LEGACY_SUMMARY_FILE")
  READY_FOR_ACTION=true
else
  # Fallback: use last assistant message (truncated). Always send something.
  SUMMARY=$(echo "$LAST_MSG" | tail -c 2000)
  [ -z "$SUMMARY" ] && exit 0  # truly nothing to send
fi

# --- Deduplicate: skip if summary content hasn't changed since last send ---
SUMMARY_HASH=$(echo "$SUMMARY" | md5 -q 2>/dev/null || echo "$SUMMARY" | md5sum | cut -d' ' -f1)
HASH_FILE="${PROJECT_STATE_DIR}/last-summary-hash"
if [ -f "$HASH_FILE" ] && [ "$(cat "$HASH_FILE")" = "$SUMMARY_HASH" ]; then
  exit 0
fi

# --- Pre-check: if user already sent a TEXT message, continue session immediately ---
# Prevents "notification then immediate reply" race condition.
# Only triggers on text messages — callback_query (button presses) are handled in the poll loop.
if [ -f "$OFFSET_FILE" ]; then
  PRE_OFFSET=$(cat "$OFFSET_FILE")
  PRE_CHECK=$(tg_get_updates 0 "$PRE_OFFSET" 2>/dev/null || echo '{"ok":false}')

  PRE_OK=$(echo "$PRE_CHECK" | jq -r '.ok' 2>/dev/null || echo "false")
  if [ "$PRE_OK" = "true" ]; then
    # --- Handle pending callback queries (e.g. user pressed Ship on a Status Update) ---
    PRE_CB_DATA=$(echo "$PRE_CHECK" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
      '[.result[] | select(
        .callback_query != null and
        .callback_query.message.chat.id == ($cid | tonumber)
      )] | first | .callback_query.data // empty' \
      2>/dev/null || true)

    PRE_CB_ID=$(echo "$PRE_CHECK" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
      '[.result[] | select(
        .callback_query != null and
        .callback_query.message.chat.id == ($cid | tonumber)
      )] | first | .callback_query.id // empty' \
      2>/dev/null || true)

    PRE_CB_MSG_ID=$(echo "$PRE_CHECK" | jq -r --arg cid "$TELEGRAM_CHAT_ID" \
      '[.result[] | select(
        .callback_query != null and
        .callback_query.message.chat.id == ($cid | tonumber)
      )] | first | .callback_query.message.message_id // empty' \
      2>/dev/null || true)

    if [ -n "$PRE_CB_DATA" ]; then
      PRE_CB_ACTION="${PRE_CB_DATA%%:*}"
      case "$PRE_CB_ACTION" in
        ship|merge|push|pr)
          tg_answer_callback "$PRE_CB_ID" "$(_action_label "$PRE_CB_ACTION")" > /dev/null 2>&1 || true
          if [ -n "$PRE_CB_MSG_ID" ]; then
            tg_edit_message "$PRE_CB_MSG_ID" "$(_action_label "$PRE_CB_ACTION")" "HTML" > /dev/null 2>&1 || true
          fi
          tg_advance_offset "$PRE_CHECK"
          # Persist summary before executing action
          if [ -f "$SUMMARY_FILE" ]; then
            cp "$SUMMARY_FILE" "${PROJECT_STATE_DIR}/last-summary.md" 2>/dev/null || true
            echo "$SUMMARY_HASH" > "$HASH_FILE" 2>/dev/null || true
          fi
          source "$HOOK_DIR/lib/ship.sh"
          case "$PRE_CB_ACTION" in
            ship)  handle_ship_cmd "${TOPIC_ID:-0}" ;;
            merge) handle_merge_cmd "${TOPIC_ID:-0}" ;;
            push)  handle_push_cmd "${TOPIC_ID:-0}" ;;
            pr)    handle_pr_cmd "${TOPIC_ID:-0}" ;;
          esac
          # Ship sends a follow-up Merge button — fall through to polling loop
          [ "$PRE_CB_ACTION" != "ship" ] && exit 0
          ;;
        *)
          # Unknown callback — advance past it
          tg_advance_offset "$PRE_CHECK"
          ;;
      esac
    fi

    # --- Handle pending text messages ---
    PRE_REPLY=$(echo "$PRE_CHECK" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --argjson tid "${TOPIC_ID:-0}" \
      '[.result[] | select(
        .message.chat.id == ($cid | tonumber) and
        .message.text != null and
        (.message.text | startswith("/") | not) and
        (if $tid > 1 then .message.message_thread_id == $tid else true end)
      )] | first | .message.text // empty' \
      2>/dev/null || true)

    if [ -n "$PRE_REPLY" ]; then
      tg_advance_offset "$PRE_CHECK"

      # Persist summary before skipping
      if [ -f "$SUMMARY_FILE" ]; then
        cp "$SUMMARY_FILE" "${PROJECT_STATE_DIR}/last-summary.md" 2>/dev/null || true
        echo "$SUMMARY_HASH" > "$HASH_FILE" 2>/dev/null || true
      fi

      printf '%s' "$PRE_REPLY" | jq -Rs '{decision: "block", reason: ("TELEGRAM REPLY from user (iPhone): " + .)}' >&3
      exit 0
    fi

    # No actionable updates — advance offset so poll loop starts clean
    tg_advance_offset "$PRE_CHECK"
  fi
fi

# --- Send notification (dedup guard at top prevents rapid-fire) ---
echo "$NOW" > "$DEDUP_FILE"

SAFE_SUMMARY=$(tg_escape_html "$SUMMARY")

# Configurable reply timeout (default 120s)
REPLY_TIMEOUT="${TELEGRAM_REPLY_TIMEOUT:-120}"

TEXT="<b>${DISPLAY_NAME}</b>
${SAFE_SUMMARY}

<i>Reply within ${REPLY_TIMEOUT}s to continue...</i>"

# Action buttons only appear when model explicitly wrote a summary file (ready-for-action signal).
# Without the file, this is just an informational notification — no Ship/Push/PR buttons.
if [ "$READY_FOR_ACTION" = true ]; then
  # G96: Don't show action buttons if conductor merge is still in progress.
  MERGE_GATED=false
  ORCH_DB="${PROJECT_DIR}/.orchestra/orchestrator.db"
  if [ -f "$ORCH_DB" ] && command -v sqlite3 &>/dev/null; then
    CONDUCTOR_ACTIVE=$(sqlite3 "$ORCH_DB" "SELECT value FROM blackboard WHERE key='conductor:active'" 2>/dev/null || echo "")
    MERGE_COMPLETE=$(sqlite3 "$ORCH_DB" "SELECT value FROM blackboard WHERE key='conductor:merge_complete'" 2>/dev/null || echo "")
    if [ "$CONDUCTOR_ACTIVE" = "1" ] && [ "$MERGE_COMPLETE" != "true" ]; then
      MERGE_GATED=true
    fi
  fi

  if [ "$MERGE_GATED" = true ]; then
    TEXT="${TEXT}

<i>Merge in progress — buttons will appear when merge completes.</i>"
    tg_send_message "$TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
  else
    source "$HOOK_DIR/lib/ship.sh"
    ACTION_KB=$(git_action_keyboard "$PROJECT_DIR" "$PROJECT_SLUG")
    if [ -n "$ACTION_KB" ]; then
      tg_send_with_keyboard "$TEXT" "$ACTION_KB" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
    else
      tg_send_message "$TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
    fi
  fi
else
  tg_send_message "$TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true
fi

# Persist summary and hash to slug dir
if [ "$READY_FOR_ACTION" = true ]; then
  cp "$SUMMARY_FILE" "${PROJECT_STATE_DIR}/last-summary.md" 2>/dev/null || true
  rm -f "$SUMMARY_FILE"  # consumed — next stop falls back to last_assistant_message
  rm -f "$LEGACY_SUMMARY_FILE"  # clean up legacy path too
fi
echo "$SUMMARY_HASH" > "$HASH_FILE" 2>/dev/null || true

# --- Ensure offset is current before polling ---
if [ -f "$OFFSET_FILE" ]; then
  OFFSET=$(cat "$OFFSET_FILE")
else
  tg_flush_offset
  OFFSET=$(cat "$OFFSET_FILE" 2>/dev/null || echo 0)
fi

# --- Kill any existing reply-poller before we start polling ---
# Prevents duplicate callback handling between stop-notify and a stale poller.
POLLER_PID_FILE="/tmp/telegram-reply-poller.pid"
if [ -f "$POLLER_PID_FILE" ]; then
  kill "$(cat "$POLLER_PID_FILE" 2>/dev/null)" 2>/dev/null || true
  rm -f "$POLLER_PID_FILE"
fi

# --- Loop-based poll: check for text replies AND action button callbacks ---
# Lock file prevents duplicate poll loops (e.g., two stop-notify instances from rapid stops)
POLL_LOCK="/tmp/claude-stop-notify-poll.lock"
if [ -f "$POLL_LOCK" ]; then
  LOCK_PID=$(cat "$POLL_LOCK" 2>/dev/null || echo 0)
  if kill -0 "$LOCK_PID" 2>/dev/null; then
    # Another poll loop is running — exit to avoid duplicate callback handling
    exit 0
  fi
  rm -f "$POLL_LOCK"
fi
echo $$ > "$POLL_LOCK"
trap 'rm -f "$POLL_LOCK"' EXIT

# 10s intervals (matching askuser-relay pattern) for snappy button response.
POLL_TIMEOUT=10
MAX_POLLS=$(( (REPLY_TIMEOUT + POLL_TIMEOUT - 1) / POLL_TIMEOUT ))

# Source ship handler for callback execution
source "$HOOK_DIR/lib/ship.sh"

for i in $(seq 1 "$MAX_POLLS"); do
  RESP=$(tg_get_updates "$POLL_TIMEOUT" "$OFFSET" 2>/dev/null || echo '{"ok":false}')

  OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
  [ "$OK" != "true" ] && continue

  # --- Check for ship: callback query ---
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
        tg_answer_callback "$CALLBACK_ID" "$(_action_label "$CB_ACTION")" > /dev/null 2>&1 || true
        if [ -n "$CALLBACK_MSG_ID" ]; then
          tg_edit_message "$CALLBACK_MSG_ID" "$(_action_label "$CB_ACTION")" "HTML" > /dev/null 2>&1 || true
        fi
        tg_advance_offset "$RESP"
        case "$CB_ACTION" in
          ship)  handle_ship_cmd "${TOPIC_ID:-0}" ;;
          merge) handle_merge_cmd "${TOPIC_ID:-0}" ;;
          push)  handle_push_cmd "${TOPIC_ID:-0}" ;;
          pr)    handle_pr_cmd "${TOPIC_ID:-0}" ;;
        esac
        # Ship sends a follow-up Merge button — keep polling to catch it
        if [ "$CB_ACTION" = "ship" ]; then
          OFFSET=$(tg_read_offset)
          continue
        fi
        exit 0
        ;;
    esac
  fi

  # --- Check for text reply ---
  REPLY=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --argjson tid "${TOPIC_ID:-0}" \
    '[.result[] | select(
      .message.chat.id == ($cid | tonumber) and
      .message.text != null and
      (if $tid > 1 then .message.message_thread_id == $tid else true end)
    )] | first | .message.text // empty' \
    2>/dev/null || true)

  # Update offset to acknowledge all received updates
  tg_advance_offset "$RESP"
  OFFSET=$(tg_read_offset)

  if [ -n "$REPLY" ]; then
    # Acknowledge on Telegram
    tg_send_message "Continuing in session..." "$TOPIC_ID" "" > /dev/null 2>&1 || true

    # Build valid JSON entirely via jq — avoids fragile string interpolation with escaped content
    printf '%s' "$REPLY" | jq -Rs '{decision: "block", reason: ("TELEGRAM REPLY from user (iPhone): " + .)}' >&3
    exit 0
  fi
done

# No reply or callback — spawn background poller for between-session button presses.
# reply-poller.sh handles both text replies (→ claude --resume) and
# callback queries (→ ship.sh handlers) after this hook exits.
if [ -n "${SESSION_ID:-}" ]; then
  nohup "$HOOK_DIR/reply-poller.sh" "$SESSION_ID" "$PROJECT_DIR" \
    > /dev/null 2>&1 &
  disown
fi

exit 0
