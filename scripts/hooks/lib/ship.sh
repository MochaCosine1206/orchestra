#!/bin/bash
# Shared ship/merge/push handlers — git actions from Telegram
# Source this file after state-helpers.sh and telegram-api.sh
#
# Requires: PROJECT_DIR, PROJECT_SLUG, state-helpers.sh, telegram-api.sh
# Handlers: handle_ship_cmd, handle_merge_cmd, handle_push_cmd
# Context: git_action_keyboard — returns dynamic keyboard JSON based on git state

# git_action_keyboard(project_dir, slug) — detect git state and return appropriate keyboard JSON
# Returns empty string if no action is available.
git_action_keyboard() {
  local project_dir="$1"
  local slug="$2"

  local branch
  branch=$(cd "$project_dir" && git branch --show-current 2>/dev/null || echo "")
  local is_trunk=false
  case "$branch" in dev|main|master|"") is_trunk=true ;; esac

  local has_changes=false
  cd "$project_dir" 2>/dev/null || return
  if ! git diff --quiet HEAD 2>/dev/null || ! git diff --cached --quiet 2>/dev/null || [ -n "$(git ls-files --others --exclude-standard 2>/dev/null)" ]; then
    has_changes=true
  fi

  if [ "$is_trunk" = true ]; then
    if [ "$has_changes" = true ]; then
      # dev/main with changes → Push It
      echo "{\"inline_keyboard\":[[{\"text\":\"📤 Push It\",\"callback_data\":\"push:${slug}\"}]]}"
    fi
    # trunk + no changes → no button
    return
  fi

  # Feature branch
  local pr_url=""
  pr_url=$(gh pr view --json url -q '.url' 2>/dev/null || true)

  if [ "$has_changes" = true ]; then
    # Feature branch + uncommitted → Ship It
    echo "{\"inline_keyboard\":[[{\"text\":\"🚀 Ship It\",\"callback_data\":\"ship:${slug}\"}]]}"
  elif [ -n "$pr_url" ]; then
    # Feature branch + PR exists → Merge
    echo "{\"inline_keyboard\":[[{\"text\":\"🔀 Merge\",\"callback_data\":\"merge:${slug}\"}]]}"
  else
    # Feature branch + pushed, no PR → Create PR
    echo "{\"inline_keyboard\":[[{\"text\":\"📋 Create PR\",\"callback_data\":\"pr:${slug}\"}]]}"
  fi
}

# handle_ship_cmd — commit, push, create PR (feature branches only)
handle_ship_cmd() {
  local thread_id="${1:-0}"
  local slug="$PROJECT_SLUG"
  local project_dir="$PROJECT_DIR"

  # Safety guard: refuse to ship from trunk branches
  local branch
  branch=$(cd "$project_dir" && git branch --show-current 2>/dev/null || echo "")
  if [ -z "$branch" ] || [ "$branch" = "main" ] || [ "$branch" = "master" ] || [ "$branch" = "dev" ]; then
    tg_send_message "Cannot ship from <b>${branch:-detached HEAD}</b>. Switch to a feature branch first." "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  tg_send_message "Shipping <b>${branch}</b>..." "$thread_id" "HTML" > /dev/null 2>&1 || true

  # Build commit message from last-summary.md
  local state_dir
  state_dir=$(get_state_dir "$slug")
  local commit_msg="Ship ${branch}"
  if [ -f "$state_dir/last-summary.md" ]; then
    commit_msg=$(head -20 "$state_dir/last-summary.md" | cut -c1-500)
  fi

  # Stage, commit, push
  local output=""
  cd "$project_dir" || { tg_send_message "Failed to cd into project" "$thread_id" "" > /dev/null 2>&1; return; }

  # Check for changes to commit
  if git diff --quiet HEAD 2>/dev/null && git diff --cached --quiet 2>/dev/null && [ -z "$(git ls-files --others --exclude-standard)" ]; then
    tg_send_message "No uncommitted changes. Pushing and creating PR..." "$thread_id" "HTML" > /dev/null 2>&1 || true
  else
    output=$(git add -A 2>&1) || {
      tg_send_message "git add failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
      return
    }
    output=$(git commit -m "$commit_msg" 2>&1) || {
      tg_send_message "git commit failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
      return
    }
  fi

  output=$(git push -u origin "$branch" 2>&1) || {
    tg_send_message "git push failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }

  # Create PR (skip if one already exists)
  local pr_url=""
  pr_url=$(gh pr view --json url -q '.url' 2>/dev/null || true)

  if [ -z "$pr_url" ]; then
    local pr_title
    pr_title=$(branch_to_task_name "$branch")
    [ -z "$pr_title" ] && pr_title="$branch"

    local pr_body="Shipped from Telegram /ship command."
    if [ -f "$state_dir/last-summary.md" ]; then
      pr_body=$(cat "$state_dir/last-summary.md")
    fi

    local pr_output
    pr_output=$(gh pr create --title "$pr_title" --body "$pr_body" --base dev 2>&1) || {
      tg_send_message "gh pr create failed: $(tg_escape_html "$pr_output")" "$thread_id" "HTML" > /dev/null 2>&1
      return
    }
    pr_url=$(echo "$pr_output" | grep -o 'https://github.com/[^ ]*' | head -1)
  fi

  local safe_url
  safe_url=$(tg_escape_html "${pr_url:-no URL}")

  # G90: offer Merge button after successful ship
  # Reviewed: G92 verification — ship events MUST keep action buttons (independent of mid-session fix)
  local reply_timeout="${TELEGRAM_REPLY_TIMEOUT:-120}"
  local merge_kb="{\"inline_keyboard\":[[{\"text\":\"🔀 Merge\",\"callback_data\":\"merge:${slug}\"}]]}"
  tg_send_with_keyboard "Shipped! PR: ${safe_url}

<i>Reply within ${reply_timeout}s to continue...</i>" "$merge_kb" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

# handle_merge_cmd — merge existing PR (feature branches only)
handle_merge_cmd() {
  local thread_id="${1:-0}"
  local project_dir="$PROJECT_DIR"

  local branch
  branch=$(cd "$project_dir" && git branch --show-current 2>/dev/null || echo "")

  tg_send_message "Merging PR for <b>${branch}</b>..." "$thread_id" "HTML" > /dev/null 2>&1 || true

  cd "$project_dir" || { tg_send_message "Failed to cd into project" "$thread_id" "" > /dev/null 2>&1; return; }

  local pr_url=""
  pr_url=$(gh pr view --json url -q '.url' 2>/dev/null || true)

  if [ -z "$pr_url" ]; then
    tg_send_message "No open PR found for <b>${branch}</b>." "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  local output
  output=$(gh pr merge --squash --delete-branch 2>&1) || {
    tg_send_message "Merge failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }

  # Switch to dev and sync with remote.
  # After squash-merge, local dev may have diverged from origin/dev
  # (local has individual commits, remote has the squash). Use --rebase
  # to cleanly replay any local-only commits on top of the squash.
  local checkout_output
  checkout_output=$(git checkout dev 2>&1) || {
    tg_send_message "Merged <b>${branch}</b> but checkout dev failed: $(tg_escape_html "$checkout_output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }
  git fetch origin dev > /dev/null 2>&1 || true
  local pull_output
  pull_output=$(git pull --rebase origin dev 2>&1) || {
    # Rebase failed — abort and try hard reset as fallback
    git rebase --abort 2>/dev/null || true
    pull_output=$(git reset --hard origin/dev 2>&1) || {
      tg_send_message "Merged <b>${branch}</b>, on dev, but sync failed: $(tg_escape_html "$pull_output")" "$thread_id" "HTML" > /dev/null 2>&1
      return
    }
  }

  # Show summary of what was merged
  local slug="$PROJECT_SLUG"
  local state_dir
  state_dir=$(get_state_dir "$slug")
  local summary_text=""
  if [ -f "$state_dir/last-summary.md" ]; then
    local raw_summary
    raw_summary=$(cat "$state_dir/last-summary.md")
    summary_text="

$(tg_escape_html "$raw_summary")"
  fi

  local reply_timeout="${TELEGRAM_REPLY_TIMEOUT:-120}"
  tg_send_message "Merged <b>${branch}</b> → dev. Checked out dev and pulled.${summary_text}

<i>Reply within ${reply_timeout}s to continue...</i>" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

# handle_push_cmd — commit and push on trunk branches (dev/main)
handle_push_cmd() {
  local thread_id="${1:-0}"
  local slug="$PROJECT_SLUG"
  local project_dir="$PROJECT_DIR"

  # G96: Block push if conductor merge is still in progress.
  local orch_db="${project_dir}/.orchestra/orchestrator.db"
  if [ -f "$orch_db" ] && command -v sqlite3 &>/dev/null; then
    local conductor_active merge_complete
    conductor_active=$(sqlite3 "$orch_db" "SELECT value FROM blackboard WHERE key='conductor:active'" 2>/dev/null || echo "")
    merge_complete=$(sqlite3 "$orch_db" "SELECT value FROM blackboard WHERE key='conductor:merge_complete'" 2>/dev/null || echo "")
    if [ "$conductor_active" = "1" ] && [ "$merge_complete" != "true" ]; then
      tg_send_message "Push blocked — merge still in progress." "$thread_id" "HTML" > /dev/null 2>&1 || true
      return
    fi
  fi

  local branch
  branch=$(cd "$project_dir" && git branch --show-current 2>/dev/null || echo "")

  tg_send_message "Pushing <b>${branch}</b>..." "$thread_id" "HTML" > /dev/null 2>&1 || true

  local state_dir
  state_dir=$(get_state_dir "$slug")
  local commit_msg="Update ${branch}"
  if [ -f "$state_dir/last-summary.md" ]; then
    commit_msg=$(head -20 "$state_dir/last-summary.md" | cut -c1-500)
  fi

  local output=""
  cd "$project_dir" || { tg_send_message "Failed to cd into project" "$thread_id" "" > /dev/null 2>&1; return; }

  if git diff --quiet HEAD 2>/dev/null && git diff --cached --quiet 2>/dev/null && [ -z "$(git ls-files --others --exclude-standard)" ]; then
    tg_send_message "Nothing to push — working tree clean." "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  output=$(git add -A 2>&1) || {
    tg_send_message "git add failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }
  output=$(git commit -m "$commit_msg" 2>&1) || {
    tg_send_message "git commit failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }
  output=$(git push 2>&1) || {
    tg_send_message "git push failed: $(tg_escape_html "$output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }

  # Show summary of what was pushed
  local summary_text=""
  if [ -f "$state_dir/last-summary.md" ]; then
    local raw_summary
    raw_summary=$(cat "$state_dir/last-summary.md")
    summary_text="

$(tg_escape_html "$raw_summary")"
  fi

  local reply_timeout="${TELEGRAM_REPLY_TIMEOUT:-120}"
  tg_send_message "Pushed to <b>${branch}</b>.${summary_text}

<i>Reply within ${reply_timeout}s to continue...</i>" "$thread_id" "HTML" > /dev/null 2>&1 || true
}

# handle_pr_cmd — create PR without committing (already pushed)
handle_pr_cmd() {
  local thread_id="${1:-0}"
  local slug="$PROJECT_SLUG"
  local project_dir="$PROJECT_DIR"

  local branch
  branch=$(cd "$project_dir" && git branch --show-current 2>/dev/null || echo "")

  tg_send_message "Creating PR for <b>${branch}</b>..." "$thread_id" "HTML" > /dev/null 2>&1 || true

  cd "$project_dir" || { tg_send_message "Failed to cd into project" "$thread_id" "" > /dev/null 2>&1; return; }

  # Check if PR already exists
  local existing
  existing=$(gh pr view --json url -q '.url' 2>/dev/null || true)
  if [ -n "$existing" ]; then
    tg_send_message "PR already exists: $(tg_escape_html "$existing")" "$thread_id" "HTML" > /dev/null 2>&1 || true
    return
  fi

  local state_dir
  state_dir=$(get_state_dir "$slug")
  local pr_title
  pr_title=$(branch_to_task_name "$branch")
  [ -z "$pr_title" ] && pr_title="$branch"

  local pr_body="Created from Telegram."
  if [ -f "$state_dir/last-summary.md" ]; then
    pr_body=$(cat "$state_dir/last-summary.md")
  fi

  local pr_output
  pr_output=$(gh pr create --title "$pr_title" --body "$pr_body" --base dev 2>&1) || {
    tg_send_message "gh pr create failed: $(tg_escape_html "$pr_output")" "$thread_id" "HTML" > /dev/null 2>&1
    return
  }
  local pr_url
  pr_url=$(echo "$pr_output" | grep -o 'https://github.com/[^ ]*' | head -1)

  local reply_timeout="${TELEGRAM_REPLY_TIMEOUT:-120}"
  tg_send_message "PR created: $(tg_escape_html "${pr_url:-no URL}")

<i>Reply within ${reply_timeout}s to continue...</i>" "$thread_id" "HTML" > /dev/null 2>&1 || true
}
