#!/bin/bash
# PreToolUse hook: relay AskUserQuestion to Telegram
# Fires BEFORE the question is presented in the terminal,
# so the user sees it on their phone immediately.
set -euo pipefail

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

# Read tool input from stdin
INPUT=$(cat)

# Extract questions from the AskUserQuestion tool input
# tool_input.questions is an array of {question, options[{label, description}], ...}
QUESTIONS=$(echo "$INPUT" | jq -r '
  .tool_input.questions // [] | map(
    .question + "\n" +
    ([.options[] | "  - " + .label + (if .description then " (" + .description + ")" else "" end)] | join("\n"))
  ) | join("\n\n")
' 2>/dev/null || echo "")

if [ -z "$QUESTIONS" ]; then
  exit 0
fi

# Escape HTML entities for safe Telegram delivery
SAFE_QUESTIONS=$(tg_escape_html "$QUESTIONS")

TEXT="<b>Claude Orchestra</b>
Waiting for your input:

${SAFE_QUESTIONS}

<i>(Reply here or answer in terminal)</i>"

# Truncate if needed (Telegram 4096 char limit)
if [ ${#TEXT} -gt 4000 ]; then
  TEXT="${TEXT:0:4000}..."
fi

tg_send_message "$TEXT" "$TOPIC_ID" "HTML" > /dev/null 2>&1 || true

exit 0
