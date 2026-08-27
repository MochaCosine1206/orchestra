#!/bin/bash
# PermissionRequest hook: relay permission prompts to Telegram with inline keyboard
# Sends an approval keyboard to the project topic, polls for callback_query response.
# On "approve": outputs allow JSON. On deny/skip/timeout: exits 0 (falls through to terminal).
# B-254: deny path broken per issue #19298 — allow-only for now.
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

# Parse tool information from stdin JSON
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // "Unknown"' 2>/dev/null || echo "Unknown")

# Skip AskUserQuestion — handled by askuser-relay.sh (B-258)
[ "$TOOL_NAME" = "AskUserQuestion" ] && exit 0

TOOL_INPUT=$(echo "$INPUT" | jq -r '.tool_input // {}' 2>/dev/null || echo "{}")

# Build human-readable permission description
DESCRIPTION=""
case "$TOOL_NAME" in
  AskUserQuestion)
    # Handled by askuser-relay.sh (B-258)
    exit 0
    ;;
  ExitPlanMode)
    # G89: Read actual plan file instead of dumping raw allowedPrompts JSON
    PLAN_FILE=$(ls -t "$HOME/.claude/plans/"*.md 2>/dev/null | head -1)
    if [ -n "$PLAN_FILE" ] && [ -f "$PLAN_FILE" ]; then
      PLAN_CONTENT=$(cat "$PLAN_FILE")
      SAFE_PLAN=$(tg_escape_html "$PLAN_CONTENT")
    else
      SAFE_PLAN="(plan file not found)"
    fi
    DESCRIPTION="<b>Plan Ready for Review</b>

${SAFE_PLAN}"
    ;;
  Bash)
    COMMAND=$(echo "$TOOL_INPUT" | jq -r '.command // "unknown command"' 2>/dev/null || echo "unknown command")
    # Truncate long commands
    if [ ${#COMMAND} -gt 300 ]; then
      COMMAND="${COMMAND:0:300}..."
    fi
    SAFE_CMD=$(tg_escape_html "$COMMAND")
    DESCRIPTION="<b>Run command:</b>
<code>${SAFE_CMD}</code>"
    ;;
  Edit)
    FILE=$(echo "$TOOL_INPUT" | jq -r '.file_path // "unknown file"' 2>/dev/null || echo "unknown file")
    SAFE_FILE=$(tg_escape_html "$FILE")
    DESCRIPTION="<b>Edit file:</b> <code>${SAFE_FILE}</code>"
    ;;
  Write)
    FILE=$(echo "$TOOL_INPUT" | jq -r '.file_path // "unknown file"' 2>/dev/null || echo "unknown file")
    SAFE_FILE=$(tg_escape_html "$FILE")
    DESCRIPTION="<b>Write file:</b> <code>${SAFE_FILE}</code>"
    ;;
  *)
    SAFE_TOOL=$(tg_escape_html "$TOOL_NAME")
    SAFE_INPUT=$(echo "$TOOL_INPUT" | head -c 200)
    SAFE_INPUT=$(tg_escape_html "$SAFE_INPUT")
    DESCRIPTION="<b>Tool:</b> ${SAFE_TOOL}
<code>${SAFE_INPUT}</code>"
    ;;
esac

# Generate request ID and build keyboard
REQUEST_ID=$(cb_generate_id)
KB_JSON=$(kb_approval "$REQUEST_ID")

# Send permission request with inline keyboard
MSG_TEXT="<b>Permission Request</b>

${DESCRIPTION}

<i>Approve to allow, or wait for terminal prompt.</i>"

SEND_RESP=$(tg_send_with_keyboard "$MSG_TEXT" "$KB_JSON" "$TOPIC_ID" "HTML" 2>/dev/null || echo '{"ok":false}')
SENT_MSG_ID=$(echo "$SEND_RESP" | jq -r '.result.message_id // empty' 2>/dev/null || true)

# --- Poll for callback response (up to 300s) ---
MAX_POLLS=10
POLL_TIMEOUT=30
OFFSET=$(tg_read_offset)
INBOX_FILE="$(get_state_dir "$PROJECT_SLUG")/telegram/inbox.jsonl"

for i in $(seq 1 "$MAX_POLLS"); do
  RESP=$(tg_get_updates "$POLL_TIMEOUT" "$OFFSET" 2>/dev/null || echo '{"ok":false}')

  OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
  [ "$OK" != "true" ] && continue

  # Check for callback_query matching our request_id
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

  # Save any text messages to inbox for later pickup by telegram-check.sh
  echo "$RESP" | jq -c --arg cid "$TELEGRAM_CHAT_ID" \
    '[.result[] | select(.message.text != null and .message.chat.id == ($cid | tonumber))] | .[]' \
    2>/dev/null >> "$INBOX_FILE" || true

  # Update offset
  tg_advance_offset "$RESP"
  OFFSET=$(tg_read_offset)

  if [ -n "$CALLBACK_DATA" ] && [[ "$CALLBACK_DATA" == *"$REQUEST_ID"* ]]; then
    # Answer callback (mandatory)
    PARSED=$(cb_parse "$CALLBACK_DATA")
    ACTION=$(echo "$PARSED" | head -1)

    tg_answer_callback "$CALLBACK_ID" "$(_cap "$ACTION")d!" > /dev/null 2>&1 || true

    # Edit original message to show result
    if [ -n "$SENT_MSG_ID" ]; then
      tg_edit_message "$SENT_MSG_ID" "${DESCRIPTION}

<b>Decision:</b> ${ACTION}" "HTML" > /dev/null 2>&1 || true
    fi

    if [ "$ACTION" = "approve" ]; then
      # Output allow decision to Claude Code
      echo '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}' >&3
      exit 0
    fi

    # deny/skip — fall through to terminal prompt
    exit 0
  fi
done

# Timeout — edit message to show timeout, fall through to terminal
if [ -n "$SENT_MSG_ID" ]; then
  tg_edit_message "$SENT_MSG_ID" "${DESCRIPTION}

<i>Timed out — prompting in terminal</i>" "HTML" > /dev/null 2>&1 || true
fi

exit 0
