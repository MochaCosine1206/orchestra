#!/bin/bash
# PermissionRequest hook: relay AskUserQuestion to Telegram with inline keyboard,
# poll for callback response, inject selected answer back into Claude.
#
# This hook fires because PermissionRequest triggers for AskUserQuestion (confirmed
# by Agent SDK docs and community projects). Returns decision.behavior="allow" with
# updatedInput.answers to bypass the CLI prompt entirely.
#
# Companion to question-relay.sh (PreToolUse), which sends a notification-only copy.
# This hook does the actual answer injection.
set -uo pipefail

# Safety: redirect all stdout to stderr so nothing accidentally reaches Claude Code's
# JSON parser. The original stdout (fd 3) is used ONLY for intentional JSON output.
exec 3>&1 1>&2

# Orchestra agent mode: agents use bypassPermissions, no Telegram relay needed
[ "${ORCHESTRA_AGENT:-}" = "1" ] && exit 0

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG"

[ -z "${TELEGRAM_BOT_TOKEN:-}" ] && exit 0
[ -z "${TELEGRAM_CHAT_ID:-}" ] && exit 0

# Read input from stdin
INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // ""' 2>/dev/null || echo "")

# Only handle AskUserQuestion — let other PermissionRequest events pass through
if [ "$TOOL_NAME" != "AskUserQuestion" ]; then
  exit 0
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Source shared libraries
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"
source "$HOOK_DIR/lib/telegram-api.sh"
source "$HOOK_DIR/lib/telegram-topics.sh"
source "$HOOK_DIR/lib/telegram-keyboards.sh"

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
BRANCH=$(cd "$PROJECT_DIR" && git branch --show-current 2>/dev/null || echo "")
REPO_NAME=$(get_repo_display_name "$PROJECT_SLUG")
TASK_NAME=$(branch_to_task_name "$BRANCH")
DISPLAY_NAME=$(compose_topic_name "$REPO_NAME" "$TASK_NAME")
TOPIC_ID=$(topic_ensure_named "$PROJECT_SLUG" "$DISPLAY_NAME" "$BRANCH")

# Extract questions from tool_input
QUESTIONS_JSON=$(echo "$INPUT" | jq -c '.tool_input.questions // []' 2>/dev/null || echo "[]")
NUM_QUESTIONS=$(echo "$QUESTIONS_JSON" | jq 'length' 2>/dev/null || echo 0)

if [ "$NUM_QUESTIONS" -eq 0 ]; then
  exit 0
fi

# Generate a request ID for this batch
REQUEST_ID=$(cb_generate_id)

# No timeout by default — wait until user taps a button in Telegram or answers in terminal.
# Terminal answer kills this hook process, so no orphan risk.
POLL_TIMEOUT="${TELEGRAM_ASKUSER_TIMEOUT:-86400}"

# Build answers map — process each question
ANSWERS_JSON='{}'

for i in $(seq 0 $((NUM_QUESTIONS - 1))); do
  Q_TEXT=$(echo "$QUESTIONS_JSON" | jq -r ".[$i].question // \"\"" 2>/dev/null)
  Q_HEADER=$(echo "$QUESTIONS_JSON" | jq -r ".[$i].header // \"\"" 2>/dev/null)
  Q_MULTI=$(echo "$QUESTIONS_JSON" | jq -r ".[$i].multiSelect // false" 2>/dev/null)
  OPTIONS_JSON=$(echo "$QUESTIONS_JSON" | jq -c ".[$i].options // []" 2>/dev/null)
  NUM_OPTIONS=$(echo "$OPTIONS_JSON" | jq 'length' 2>/dev/null || echo 0)

  [ -z "$Q_TEXT" ] && continue

  # Build inline keyboard — one button per option, one row each
  # Callback data format: "aq:{request_id}:{question_idx}:{option_idx}"
  # Fits within 64-byte Telegram limit (aq:abcd1234:0:0 = ~17 bytes)
  KEYBOARD_ROWS=""
  for j in $(seq 0 $((NUM_OPTIONS - 1))); do
    LABEL=$(echo "$OPTIONS_JSON" | jq -r ".[$j].label // \"\"" 2>/dev/null)
    DESC=$(echo "$OPTIONS_JSON" | jq -r ".[$j].description // \"\"" 2>/dev/null)

    # Show label + short description on button
    BTN_TEXT="$LABEL"
    if [ -n "$DESC" ] && [ ${#DESC} -le 30 ]; then
      BTN_TEXT="$LABEL — $DESC"
    fi

    CB_DATA="aq:${REQUEST_ID}:${i}:${j}"
    ROW="[{\"text\":\"${BTN_TEXT}\",\"callback_data\":\"${CB_DATA}\"}]"

    if [ -n "$KEYBOARD_ROWS" ]; then
      KEYBOARD_ROWS="${KEYBOARD_ROWS},${ROW}"
    else
      KEYBOARD_ROWS="$ROW"
    fi
  done

  # Add "Other" button
  OTHER_CB="aq:${REQUEST_ID}:${i}:other"
  OTHER_ROW="[{\"text\":\"Other (type reply)\",\"callback_data\":\"${OTHER_CB}\"}]"
  KEYBOARD_ROWS="${KEYBOARD_ROWS},${OTHER_ROW}"

  KB_JSON="{\"inline_keyboard\":[${KEYBOARD_ROWS}]}"

  # Format question message
  SAFE_Q=$(tg_escape_html "$Q_TEXT")
  MSG_TEXT="<b>${Q_HEADER}</b>
${SAFE_Q}"

  # Add option descriptions for context
  for j in $(seq 0 $((NUM_OPTIONS - 1))); do
    LABEL=$(echo "$OPTIONS_JSON" | jq -r ".[$j].label // \"\"" 2>/dev/null)
    DESC=$(echo "$OPTIONS_JSON" | jq -r ".[$j].description // \"\"" 2>/dev/null)
    SAFE_LABEL=$(tg_escape_html "$LABEL")
    SAFE_DESC=$(tg_escape_html "$DESC")
    if [ -n "$DESC" ]; then
      MSG_TEXT="${MSG_TEXT}
  <b>${SAFE_LABEL}</b>: ${SAFE_DESC}"
    fi
  done

  MSG_TEXT="${MSG_TEXT}

<i>Tap to select, or type a reply for Other.</i>"

  # Send to Telegram with keyboard
  SEND_RESP=$(tg_send_with_keyboard "$MSG_TEXT" "$KB_JSON" "$TOPIC_ID" "HTML" 2>/dev/null || echo '{"ok":false}')
  SENT_MSG_ID=$(echo "$SEND_RESP" | jq -r '.result.message_id // empty' 2>/dev/null || true)

  # Poll for callback response
  OFFSET=$(tg_read_offset)
  START_TIME=$(date +%s)
  SELECTED_LABEL=""

  while true; do
    ELAPSED=$(( $(date +%s) - START_TIME ))
    if [ "$ELAPSED" -ge "$POLL_TIMEOUT" ]; then
      # Timeout — pick first option or one marked (Recommended)
      SELECTED_LABEL=$(echo "$OPTIONS_JSON" | jq -r '
        (map(select(.label | test("Recommended"))) | first | .label) //
        (first | .label) // "Option 1"
      ' 2>/dev/null || echo "")
      # Edit message to show timeout
      if [ -n "$SENT_MSG_ID" ]; then
        tg_edit_message "$SENT_MSG_ID" "${MSG_TEXT}

<b>Timed out — auto-selected:</b> ${SELECTED_LABEL}" "HTML" > /dev/null 2>&1 || true
      fi
      break
    fi

    # Poll with 10s long-poll
    RESP=$(tg_get_updates 10 "$OFFSET" 2>/dev/null || echo '{"ok":false}')
    OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
    [ "$OK" != "true" ] && continue

    # Look for callback matching our request
    CALLBACK_DATA=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --arg prefix "aq:${REQUEST_ID}:${i}:" \
      '[.result[] | select(
        .callback_query != null and
        .callback_query.message.chat.id == ($cid | tonumber) and
        (.callback_query.data | startswith($prefix))
      )] | first | .callback_query.data // empty' \
      2>/dev/null || true)

    CALLBACK_ID=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --arg prefix "aq:${REQUEST_ID}:${i}:" \
      '[.result[] | select(
        .callback_query != null and
        .callback_query.message.chat.id == ($cid | tonumber) and
        (.callback_query.data | startswith($prefix))
      )] | first | .callback_query.id // empty' \
      2>/dev/null || true)

    # Also check for text messages (for "Other" free-text replies)
    TEXT_MSG=$(echo "$RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --argjson tid "${TOPIC_ID:-0}" \
      '[.result[] | select(
        .message.chat.id == ($cid | tonumber) and
        .message.text != null and
        (.message.text | startswith("/") | not) and
        (if $tid > 1 then .message.message_thread_id == $tid else true end)
      )] | first | .message.text // empty' \
      2>/dev/null || true)

    # Update offset
    tg_advance_offset "$RESP"
    OFFSET=$(tg_read_offset)

    if [ -n "$CALLBACK_DATA" ]; then
      # Parse option index from callback data: "aq:reqid:qidx:optidx"
      OPT_IDX="${CALLBACK_DATA##*:}"

      if [ "$OPT_IDX" = "other" ]; then
        # Acknowledge and wait for text reply
        tg_answer_callback "$CALLBACK_ID" "Type your answer..." > /dev/null 2>&1 || true

        # Edit message to prompt for text
        if [ -n "$SENT_MSG_ID" ]; then
          tg_edit_message "$SENT_MSG_ID" "${MSG_TEXT}

<i>Type your answer as a reply...</i>" "HTML" > /dev/null 2>&1 || true
        fi

        # Poll for text message (reuse remaining timeout)
        while true; do
          ELAPSED=$(( $(date +%s) - START_TIME ))
          [ "$ELAPSED" -ge "$POLL_TIMEOUT" ] && break

          TEXT_RESP=$(tg_get_updates 10 "$OFFSET" 2>/dev/null || echo '{"ok":false}')
          TEXT_OK=$(echo "$TEXT_RESP" | jq -r '.ok' 2>/dev/null || echo "false")
          [ "$TEXT_OK" != "true" ] && continue

          FREE_TEXT=$(echo "$TEXT_RESP" | jq -r --arg cid "$TELEGRAM_CHAT_ID" --argjson tid "${TOPIC_ID:-0}" \
            '[.result[] | select(
              .message.chat.id == ($cid | tonumber) and
              .message.text != null and
              (if $tid > 1 then .message.message_thread_id == $tid else true end)
            )] | first | .message.text // empty' \
            2>/dev/null || true)

          tg_advance_offset "$TEXT_RESP"
          OFFSET=$(tg_read_offset)

          if [ -n "$FREE_TEXT" ]; then
            SELECTED_LABEL="$FREE_TEXT"
            break
          fi
        done

        # If still empty after "Other" timeout, fall back to first option
        if [ -z "$SELECTED_LABEL" ]; then
          SELECTED_LABEL=$(echo "$OPTIONS_JSON" | jq -r '.[0].label // "Option 1"' 2>/dev/null)
        fi
      else
        # Direct option selection
        SELECTED_LABEL=$(echo "$OPTIONS_JSON" | jq -r ".[$OPT_IDX].label // \"\"" 2>/dev/null)
        tg_answer_callback "$CALLBACK_ID" "Selected: ${SELECTED_LABEL}" > /dev/null 2>&1 || true
      fi

      # Edit message to show selection
      if [ -n "$SENT_MSG_ID" ]; then
        SAFE_SEL=$(tg_escape_html "$SELECTED_LABEL")
        tg_edit_message "$SENT_MSG_ID" "${MSG_TEXT}

<b>Selected:</b> ${SAFE_SEL}" "HTML" > /dev/null 2>&1 || true
      fi

      break
    fi

    # Check for unsolicited text message as "Other" answer
    if [ -n "$TEXT_MSG" ]; then
      SELECTED_LABEL="$TEXT_MSG"
      if [ -n "$SENT_MSG_ID" ]; then
        SAFE_SEL=$(tg_escape_html "$SELECTED_LABEL")
        tg_edit_message "$SENT_MSG_ID" "${MSG_TEXT}

<b>Answered:</b> ${SAFE_SEL}" "HTML" > /dev/null 2>&1 || true
      fi
      break
    fi
  done

  # Add answer to map
  if [ -n "$SELECTED_LABEL" ]; then
    ANSWERS_JSON=$(echo "$ANSWERS_JSON" | jq --arg q "$Q_TEXT" --arg a "$SELECTED_LABEL" '. + {($q): $a}' 2>/dev/null)
  fi
done

# Build and output the PermissionRequest response
# This injects the answers and bypasses the CLI prompt
jq -n \
  --argjson questions "$QUESTIONS_JSON" \
  --argjson answers "$ANSWERS_JSON" \
  '{
    hookSpecificOutput: {
      hookEventName: "PermissionRequest",
      decision: {
        behavior: "allow",
        updatedInput: {
          questions: $questions,
          answers: $answers
        }
      }
    }
  }' >&3
