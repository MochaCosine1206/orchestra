#!/bin/bash
# Topic management for Telegram forum supergroups
# Source this file after telegram-api.sh and state-helpers.sh
#
# Storage: ~/.claude-session-state/<slug>/telegram/
#   topic_id      — integer thread ID
#   topic_name    — display name
#   topic_closed  — marker file (mtime for 30-day cleanup)

# topic_create(slug, display_name) — create a new forum topic, cache and return ID
topic_create() {
  local slug="$1"
  local display_name="${2:-$slug}"
  local state_dir
  state_dir=$(ensure_state_dir "$slug")

  local response
  response=$(curl -s -X POST "${TG_API_URL}/createForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "name=${display_name}" \
    --data-urlencode "icon_color=9367192" \
    2>/dev/null || echo '{"ok":false}')

  local ok
  ok=$(echo "$response" | jq -r '.ok' 2>/dev/null || echo "false")

  if [ "$ok" = "true" ]; then
    local topic_id
    topic_id=$(echo "$response" | jq -r '.result.message_thread_id' 2>/dev/null || echo "")
    if [ -n "$topic_id" ] && [ "$topic_id" != "null" ]; then
      echo "$topic_id" > "$state_dir/telegram/topic_id"
      echo "$display_name" > "$state_dir/telegram/topic_name"
      echo "$topic_id"
      return 0
    fi
  fi

  return 1
}

# topic_lookup(slug) — read cached topic_id, empty if not cached
topic_lookup() {
  local slug="$1"
  local state_dir
  state_dir=$(get_state_dir "$slug")
  local id_file="$state_dir/telegram/topic_id"

  if [ -f "$id_file" ]; then
    cat "$id_file" 2>/dev/null || true
  fi
}

# tg_is_supergroup() — check if TELEGRAM_CHAT_ID is a supergroup (negative, starts with -100)
# Private chats have positive IDs; groups/supergroups have negative IDs.
# Forum topics only work in supergroups with topics enabled.
tg_is_supergroup() {
  [[ "${TELEGRAM_CHAT_ID:-0}" == -100* ]]
}

# topic_ensure(slug) — lookup cached → return, or create → cache → return
# Returns empty string for private chats (no topic routing).
# Falls back to empty string on API failure (messages sent without topic).
topic_ensure() {
  local slug="$1"

  # Private chats don't support forum topics — skip entirely
  if ! tg_is_supergroup; then
    echo ""
    return 0
  fi

  # Check cache first
  local cached
  cached=$(topic_lookup "$slug")
  if [ -n "$cached" ]; then
    echo "$cached"
    return 0
  fi

  # Format display name: hyphens to spaces, title case first word
  local display_name="$slug"

  # Try to create
  local created
  created=$(topic_create "$slug" "$display_name" 2>/dev/null) || true

  if [ -n "$created" ]; then
    echo "$created"
    return 0
  fi

  # Fallback: no topic (messages go to default chat)
  echo ""
}

# topic_ensure_named(slug, desired_name, [branch]) — ensure topic exists + rename if name changed
# Wraps topic_ensure with rename-if-changed logic. Backwards compatible.
# Stores current_branch in state dir for branch-change detection.
topic_ensure_named() {
  local slug="$1"
  local desired_name="${2:-}"
  local branch="${3:-}"

  # Get/create topic via existing logic
  local topic_id
  topic_id=$(topic_ensure "$slug")

  # Private chat or no topic — return early
  if [ -z "$topic_id" ]; then
    echo ""
    return 0
  fi

  # Store branch for change detection
  if [ -n "$branch" ]; then
    local state_dir
    state_dir=$(ensure_state_dir "$slug")
    echo "$branch" > "$state_dir/telegram/current_branch"
  fi

  # Rename if desired_name differs from cached name
  if [ -n "$desired_name" ]; then
    local state_dir
    state_dir=$(get_state_dir "$slug")
    local cached_name=""
    if [ -f "$state_dir/telegram/topic_name" ]; then
      cached_name=$(cat "$state_dir/telegram/topic_name" 2>/dev/null || true)
    fi

    if [ "$cached_name" != "$desired_name" ]; then
      topic_rename "$slug" "$desired_name"
    fi
  fi

  echo "$topic_id"
}

# topic_close(slug) — close a forum topic
topic_close() {
  local slug="$1"
  local topic_id
  topic_id=$(topic_lookup "$slug")
  [ -z "$topic_id" ] && return 1

  curl -s -X POST "${TG_API_URL}/closeForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_thread_id=${topic_id}" \
    > /dev/null 2>&1 || true

  local state_dir
  state_dir=$(get_state_dir "$slug")
  touch "$state_dir/telegram/topic_closed"
}

# topic_reopen(slug) — reopen a closed forum topic
topic_reopen() {
  local slug="$1"
  local topic_id
  topic_id=$(topic_lookup "$slug")
  [ -z "$topic_id" ] && return 1

  curl -s -X POST "${TG_API_URL}/reopenForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_thread_id=${topic_id}" \
    > /dev/null 2>&1 || true

  local state_dir
  state_dir=$(get_state_dir "$slug")
  rm -f "$state_dir/telegram/topic_closed"
}

# topic_rename(slug, new_name) — rename a forum topic
topic_rename() {
  local slug="$1"
  local new_name="$2"
  local topic_id
  topic_id=$(topic_lookup "$slug")
  [ -z "$topic_id" ] && return 1

  curl -s -X POST "${TG_API_URL}/editForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_thread_id=${topic_id}" \
    --data-urlencode "name=${new_name}" \
    > /dev/null 2>&1 || true

  local state_dir
  state_dir=$(get_state_dir "$slug")
  echo "$new_name" > "$state_dir/telegram/topic_name"
}

# topic_archive(slug, status) — rename to [Done]/[Paused] then close
topic_archive() {
  local slug="$1"
  local status="${2:-Done}"

  topic_rename "$slug" "[${status}] ${slug}"
  topic_close "$slug"
}

# topic_delete(slug) — delete forum topic and remove state files
topic_delete() {
  local slug="$1"
  local topic_id
  topic_id=$(topic_lookup "$slug")
  [ -z "$topic_id" ] && return 1

  curl -s -X POST "${TG_API_URL}/deleteForumTopic" \
    --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "message_thread_id=${topic_id}" \
    > /dev/null 2>&1 || true

  local state_dir
  state_dir=$(get_state_dir "$slug")
  rm -f "$state_dir/telegram/topic_id" \
        "$state_dir/telegram/topic_name" \
        "$state_dir/telegram/topic_closed"
}

# topic_find_slug_by_id(thread_id) — reverse lookup: find slug owning this topic
topic_find_slug_by_id() {
  local target_id="$1"

  for id_file in "$SESSION_STATE_BASE"/*/telegram/topic_id; do
    [ -f "$id_file" ] || continue
    local stored_id
    stored_id=$(cat "$id_file" 2>/dev/null || true)
    if [ "$stored_id" = "$target_id" ]; then
      # Extract slug from path: .../slug/telegram/topic_id
      local tg_dir
      tg_dir=$(dirname "$id_file")
      local slug_dir
      slug_dir=$(dirname "$tg_dir")
      basename "$slug_dir"
      return 0
    fi
  done
}
