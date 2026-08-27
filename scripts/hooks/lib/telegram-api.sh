#!/bin/bash
# Shared Telegram API functions — used by all hooks
# Source this file: source "$HOOK_DIR/lib/telegram-api.sh"
#
# Requires TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID to be set.
# Optional: TELEGRAM_FORUM_CHAT_ID — supergroup with Topics enabled.
#   When set, overrides TELEGRAM_CHAT_ID for topic-routed messages.
#   When unset, messages go to TELEGRAM_CHAT_ID (private chat, no topics).
# Call tg_init() after sourcing to set up TG_API_URL.

# tg_init() — set up API URL and validate credentials
tg_init() {
  # Prefer forum chat ID (supergroup) when available
  if [ -n "${TELEGRAM_FORUM_CHAT_ID:-}" ]; then
    TELEGRAM_CHAT_ID="$TELEGRAM_FORUM_CHAT_ID"
  fi
  TG_API_URL="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
  TG_OFFSET_FILE="${TG_OFFSET_FILE:-/tmp/telegram-last-update-id}"
}

# Auto-init if credentials are already set
if [ -n "${TELEGRAM_BOT_TOKEN:-}" ]; then
  tg_init
fi

# _cap(word) — capitalize first letter (bash 3.2 compatible, no ${var^})
_cap() {
  local w="$1"
  local first=$(printf '%s' "${w:0:1}" | tr '[:lower:]' '[:upper:]')
  printf '%s%s' "$first" "${w:1}"
}

# _action_label(action) — correct gerund for callback actions
# "merge" → "Merging..." (not "Mergeing..."), "pr" → "Creating PR..."
_action_label() {
  case "$1" in
    ship)  printf 'Shipping...' ;;
    merge) printf 'Merging...' ;;
    push)  printf 'Pushing...' ;;
    pr)    printf 'Creating PR...' ;;
    *)     printf '%sing...' "$(_cap "$1")" ;;
  esac
}

# tg_escape_html(text) — escape &, <, > for HTML parse_mode
tg_escape_html() {
  local text="${1:-}"
  echo "$text" | sed 's/&/\&amp;/g;s/</\&lt;/g;s/>/\&gt;/g'
}

# tg_send_message(text, topic_id, parse_mode) — send a message, optionally to a topic
# text: message text
# topic_id: forum topic ID (empty = no topic)
# parse_mode: HTML, Markdown, or empty
tg_send_message() {
  local text="$1"
  local topic_id="${2:-}"
  local parse_mode="${3:-}"

  local -a args=(
    -s -X POST "${TG_API_URL}/sendMessage"
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}"
    --data-urlencode "text=${text}"
  )

  if [ -n "$parse_mode" ]; then
    args+=(--data-urlencode "parse_mode=${parse_mode}")
  fi

  if [ -n "$topic_id" ] && [ "$topic_id" != "0" ]; then
    args+=(--data-urlencode "message_thread_id=${topic_id}")
  fi

  curl "${args[@]}" 2>/dev/null || echo '{"ok":false}'
}

# tg_send_with_keyboard(text, keyboard_json, topic_id, parse_mode) — send message with inline keyboard
tg_send_with_keyboard() {
  local text="$1"
  local keyboard_json="$2"
  local topic_id="${3:-}"
  local parse_mode="${4:-HTML}"

  local -a args=(
    -s -X POST "${TG_API_URL}/sendMessage"
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}"
    --data-urlencode "text=${text}"
    --data-urlencode "parse_mode=${parse_mode}"
    --data-urlencode "reply_markup=${keyboard_json}"
  )

  if [ -n "$topic_id" ] && [ "$topic_id" != "0" ]; then
    args+=(--data-urlencode "message_thread_id=${topic_id}")
  fi

  curl "${args[@]}" 2>/dev/null || echo '{"ok":false}'
}

# tg_edit_message(message_id, new_text, parse_mode) — edit an existing message
tg_edit_message() {
  local message_id="$1"
  local new_text="$2"
  local parse_mode="${3:-HTML}"

  curl -s -X POST "${TG_API_URL}/editMessageText" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_id=${message_id}" \
    --data-urlencode "text=${new_text}" \
    --data-urlencode "parse_mode=${parse_mode}" \
    2>/dev/null || echo '{"ok":false}'
}

# tg_answer_callback(callback_query_id, toast_text) — mandatory response to callback button press
tg_answer_callback() {
  local callback_query_id="$1"
  local toast_text="${2:-}"

  local -a args=(
    -s -X POST "${TG_API_URL}/answerCallbackQuery"
    --data-urlencode "callback_query_id=${callback_query_id}"
  )

  if [ -n "$toast_text" ]; then
    args+=(--data-urlencode "text=${toast_text}")
  fi

  curl "${args[@]}" 2>/dev/null || echo '{"ok":false}'
}

# tg_get_updates(timeout, offset) — poll for updates with message + callback_query
tg_get_updates() {
  local timeout="${1:-0}"
  local offset="${2:-0}"
  local max_time=$((timeout + 10))

  curl -s --max-time "$max_time" \
    "${TG_API_URL}/getUpdates?offset=${offset}&timeout=${timeout}&allowed_updates=%5B%22message%22%2C%22callback_query%22%5D" \
    2>/dev/null || echo '{"ok":false}'
}

# tg_advance_offset(response_json) — update offset file from API response
tg_advance_offset() {
  local response="$1"
  local new_offset
  new_offset=$(echo "$response" | jq '[.result[].update_id] | max // 0' 2>/dev/null || echo 0)

  if [ "$new_offset" -gt 0 ] 2>/dev/null; then
    echo "$((new_offset + 1))" > "$TG_OFFSET_FILE"
  fi
}

# tg_read_offset() — read current offset from file
tg_read_offset() {
  if [ -f "$TG_OFFSET_FILE" ]; then
    cat "$TG_OFFSET_FILE" 2>/dev/null || echo 0
  else
    echo 0
  fi
}

# tg_flush_offset() — flush old updates and set offset to latest+1
tg_flush_offset() {
  local flush
  flush=$(curl -s --max-time 5 "${TG_API_URL}/getUpdates?offset=-1&timeout=0" 2>/dev/null || echo '{}')
  local offset
  offset=$(echo "$flush" | jq '[.result[].update_id] | max // 0' 2>/dev/null || echo 0)
  if [ "$offset" -gt 0 ]; then
    echo "$((offset + 1))" > "$TG_OFFSET_FILE"
  else
    echo "0" > "$TG_OFFSET_FILE"
  fi
}
