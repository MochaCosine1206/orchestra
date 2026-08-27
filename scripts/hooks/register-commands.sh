#!/bin/bash
# One-time bot command registration for Telegram
# Run: bash scripts/hooks/register-commands.sh
# Registers menu commands so users see them when typing "/" in the chat.
set -euo pipefail

# Source credentials (always source to pick up TELEGRAM_FORUM_CHAT_ID)
CONFIG="${TELEGRAM_NOTIFY_CONFIG:-$HOME/.telegram-notify}"
[ -f "$CONFIG" ] && source "$CONFIG" || { echo "Missing config"; exit 1; }

: "${TELEGRAM_BOT_TOKEN:?Missing TELEGRAM_BOT_TOKEN}"

API="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"

COMMANDS='[
  {"command":"status","description":"Show current session status"},
  {"command":"list","description":"List active project topics"},
  {"command":"newproject","description":"Create a new project topic"},
  {"command":"close","description":"Close current project topic"},
  {"command":"pause","description":"Mark project as paused"},
  {"command":"resume","description":"Resume paused project"},
  {"command":"ship","description":"Commit, push, and create PR"},
  {"command":"approve","description":"Approve pending operation (text fallback)"},
  {"command":"help","description":"Show available commands"}
]'

RESP=$(curl -s -X POST "${API}/setMyCommands" \
  -H "Content-Type: application/json" \
  -d "{\"commands\": ${COMMANDS}}" \
  2>/dev/null)

OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
if [ "$OK" = "true" ]; then
  echo "Commands registered successfully."
else
  echo "Failed to register commands:"
  echo "$RESP" | jq . 2>/dev/null || echo "$RESP"
  exit 1
fi
