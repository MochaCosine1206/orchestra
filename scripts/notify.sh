#!/bin/bash
# Send notifications to iPhone via Telegram bot
# Usage: ./scripts/notify.sh "Title" "Message body"
#   or:  ./scripts/notify.sh "Simple message"
#
# Requires env vars:
#   TELEGRAM_BOT_TOKEN  — from @BotFather
#   TELEGRAM_CHAT_ID    — your user ID from @userinfobot
#
# Add to ~/.zshrc:
#   export TELEGRAM_BOT_TOKEN="your-token"
#   export TELEGRAM_CHAT_ID="your-chat-id"
set -euo pipefail

if [ -z "${TELEGRAM_BOT_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT_ID:-}" ]; then
  # Silent fail — don't break scripts that call this
  exit 0
fi

if [ $# -eq 0 ]; then
  echo "Usage: $0 \"title\" [\"body\"]" >&2
  exit 1
fi

TITLE="${1}"
BODY="${2:-}"

if [ -n "$BODY" ]; then
  TEXT="*${TITLE}*
${BODY}"
else
  TEXT="${TITLE}"
fi

curl -s -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
  -d chat_id="${TELEGRAM_CHAT_ID}" \
  -d parse_mode="Markdown" \
  -d text="${TEXT}" > /dev/null 2>&1

exit 0
