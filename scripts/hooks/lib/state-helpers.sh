#!/bin/bash
# Shared utilities for M1 infrastructure hooks
# Sourced by stop-notify.sh, pre-compact-state.sh, session-start-inject.sh
#
# All functions use SESSION_STATE_BASE (default: ~/.claude-session-state) as root.
# Set SESSION_STATE_BASE to a temp dir for test isolation.

SESSION_STATE_BASE="${SESSION_STATE_BASE:-$HOME/.claude-session-state}"
# B-249: Hook mode functions (get_hook_mode, set_hook_mode, detect_mode_from_branch) added below

# get_project_slug(cwd) — derive a filesystem-safe slug from a directory path
# Strips trailing slash, takes basename, lowercases, spaces→hyphens, strips non-safe chars
get_project_slug() {
  local cwd="${1%/}"
  basename "$cwd" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[^a-z0-9._-]//g'
}

# ensure_state_dir(slug) — create project state directory structure, echo path
ensure_state_dir() {
  local slug="$1"
  local dir="$SESSION_STATE_BASE/$slug"
  mkdir -p "$dir/sessions" "$dir/telegram"
  echo "$dir"
}

# get_state_dir(slug) — return path without creating (for reads)
get_state_dir() {
  echo "$SESSION_STATE_BASE/$1"
}

# load_telegram_config([config_path]) — source telegram credentials if not already set
# Returns 0 if credentials available, 1 if missing
load_telegram_config() {
  local config="${1:-$HOME/.telegram-notify}"
  if [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${TELEGRAM_CHAT_ID:-}" ]; then
    return 0
  fi
  if [ -f "$config" ]; then
    source "$config"
    return 0
  fi
  return 1
}

# branch_to_task_name(branch) — convert git branch to human-readable task name
# feature/M2-telegram-evolution → M2 Telegram Evolution
# dev/main/master/empty → "" (trunk branches)
# unknown-format → returned as-is
branch_to_task_name() {
  local branch="$1"
  case "$branch" in
    feature/*|fix/*|chore/*|docs/*|refactor/*)
      local task="${branch#*/}"
      # Replace hyphens with spaces, title case each word
      echo "$task" | tr '-' ' ' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2)}1'
      ;;
    dev|main|master|"") echo "" ;;
    *) echo "$branch" ;;
  esac
}

# get_repo_display_name(slug) — human-readable repo name for topic display
# Title-cases the slug: "claude-orchestra" → "Claude Orchestra"
get_repo_display_name() {
  local slug="$1"
  echo "$slug" | tr '-' ' ' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2)}1'
}

# compose_topic_name(repo_name, task_name) — build full display name for Telegram topic
# "Orchestra" + "M2 Telegram Evolution" → "Orchestra — M2 Telegram Evolution"
# Truncates to 128 chars (Telegram topic name limit)
compose_topic_name() {
  local repo_name="$1"
  local task_name="$2"
  if [ -n "$task_name" ]; then
    local full="${repo_name} — ${task_name}"
    # Truncate to 128 chars (Telegram limit)
    echo "${full:0:128}"
  else
    echo "$repo_name"
  fi
}

# --- Hook Mode Functions (B-249) ---

# detect_mode_from_branch(branch) — pattern match branch name to hook mode
# Returns: default, implementation, research, or debug
detect_mode_from_branch() {
  local branch="$1"
  case "$branch" in
    feature/*|fix/*|chore/*|refactor/*)
      echo "implementation" ;;
    docs/*|research/*)
      echo "research" ;;
    debug/*|investigate/*)
      echo "debug" ;;
    dev|main|master|"")
      echo "default" ;;
    *research*)
      echo "research" ;;
    *)
      echo "default" ;;
  esac
}

# get_hook_mode(slug) — read current hook mode from state file
# Returns mode string, "default" if file missing or unreadable
get_hook_mode() {
  local slug="$1"
  local mode_file="$SESSION_STATE_BASE/$slug/hook-mode.json"
  if [ -f "$mode_file" ]; then
    local mode
    mode=$(jq -r '.mode // "default"' "$mode_file" 2>/dev/null || echo "default")
    echo "$mode"
  else
    echo "default"
  fi
}

# set_hook_mode(slug, mode, set_by, branch) — write hook mode to state file
# Respects user override: if set_by=auto and current is set_by=user on same branch, skip
set_hook_mode() {
  local slug="$1"
  local mode="$2"
  local set_by="$3"
  local branch="$4"
  local mode_file="$SESSION_STATE_BASE/$slug/hook-mode.json"

  # Respect user override precedence
  if [ "$set_by" = "auto" ] && [ -f "$mode_file" ]; then
    local current_set_by current_branch
    current_set_by=$(jq -r '.set_by // "auto"' "$mode_file" 2>/dev/null || echo "auto")
    current_branch=$(jq -r '.branch // ""' "$mode_file" 2>/dev/null || echo "")
    if [ "$current_set_by" = "user" ] && [ "$current_branch" = "$branch" ]; then
      return 0
    fi
  fi

  mkdir -p "$SESSION_STATE_BASE/$slug"
  local now
  now=$(date +%s)
  jq -n \
    --arg mode "$mode" \
    --arg set_by "$set_by" \
    --arg branch "$branch" \
    --argjson updated_at "$now" \
    '{mode: $mode, set_by: $set_by, branch: $branch, updated_at: $updated_at}' \
    > "$mode_file"
}

# get_parent_topic_id(slug) — read topic_id for a given project slug
# Used by orchestra agents to route notifications to the parent project's topic
get_parent_topic_id() {
  local slug="$1"
  local topic_file="$SESSION_STATE_BASE/$slug/telegram/topic_id"
  if [ -f "$topic_file" ]; then
    cat "$topic_file" 2>/dev/null || true
  fi
}

# migrate_flat_state(session_id, slug) — move legacy flat files to slug subfolder
# Moves ~/.claude-session-state/{session_id}-stop.json → slug/sessions/
migrate_flat_state() {
  local session_id="$1"
  local slug="$2"
  local flat_file="$SESSION_STATE_BASE/${session_id}-stop.json"
  local target_dir="$SESSION_STATE_BASE/$slug/sessions"

  if [ -f "$flat_file" ]; then
    mkdir -p "$target_dir"
    mv "$flat_file" "$target_dir/${session_id}-stop.json"
  fi
}
