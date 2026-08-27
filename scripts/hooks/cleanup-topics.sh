#!/bin/bash
# Cleanup old closed Telegram forum topics (>30 days)
# Usage: bash scripts/hooks/cleanup-topics.sh
# Safe for weekly cron: 0 0 * * 0 /path/to/cleanup-topics.sh
set -uo pipefail

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

NOW=$(date +%s)
THIRTY_DAYS=$((30 * 24 * 60 * 60))
CLEANED=0

# Scan all project state dirs for closed topics
for closed_file in "$SESSION_STATE_BASE"/*/telegram/topic_closed; do
  [ -f "$closed_file" ] || continue

  # Check age of topic_closed marker (mtime)
  if [[ "$OSTYPE" == "darwin"* ]]; then
    CLOSED_TIME=$(stat -f %m "$closed_file" 2>/dev/null || echo "$NOW")
  else
    CLOSED_TIME=$(stat -c %Y "$closed_file" 2>/dev/null || echo "$NOW")
  fi

  AGE=$((NOW - CLOSED_TIME))
  if [ "$AGE" -lt "$THIRTY_DAYS" ]; then
    continue
  fi

  # Extract slug from path
  local_tg_dir=$(dirname "$closed_file")
  local_slug_dir=$(dirname "$local_tg_dir")
  local_slug=$(basename "$local_slug_dir")

  # Get topic_id
  local_topic_id=""
  if [ -f "$local_tg_dir/topic_id" ]; then
    local_topic_id=$(cat "$local_tg_dir/topic_id" 2>/dev/null || true)
  fi

  if [ -z "$local_topic_id" ]; then
    # No topic_id — just clean up state files
    rm -f "$closed_file" "$local_tg_dir/topic_name"
    continue
  fi

  # Attempt API deletion
  RESP=$(curl -s -X POST "${TG_API_URL}/deleteForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_thread_id=${local_topic_id}" \
    2>/dev/null || echo '{"ok":false}')

  OK=$(echo "$RESP" | jq -r '.ok' 2>/dev/null || echo "false")
  if [ "$OK" = "true" ]; then
    # Clean up state files
    rm -f "$local_tg_dir/topic_id" "$local_tg_dir/topic_name" "$closed_file"
    CLEANED=$((CLEANED + 1))
    echo "Cleaned: $local_slug (topic $local_topic_id)"
  else
    echo "Failed to delete topic $local_topic_id for $local_slug — state preserved"
  fi
done

if [ "$CLEANED" -gt 0 ]; then
  echo "Cleaned $CLEANED old topic(s)"
else
  echo "No topics to clean up"
fi
