#!/bin/bash
# PreCompact hook: capture session state checkpoint before context compaction
# Fires async on PreCompact event. Writes checkpoint JSON to slug-based state dir.
# Reads session_id and trigger from stdin JSON (hook input).
set -uo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"

INPUT=$(cat)
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
SUMMARY_FILE="${SUMMARY_FILE_OVERRIDE:-/tmp/claude-session-summary.txt}"

SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)
TRIGGER=$(echo "$INPUT" | jq -r '.trigger // "unknown"' 2>/dev/null || true)

[ -z "$SESSION_ID" ] && exit 0

PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
STATE_DIR=$(ensure_state_dir "$PROJECT_SLUG")
CHECKPOINT_FILE="$STATE_DIR/sessions/${SESSION_ID}-compact.json"

# Increment compaction count from prior checkpoint
PREV_COUNT=0
if [ -f "$CHECKPOINT_FILE" ]; then
  PREV_COUNT=$(jq -r '.compaction_count // 0' "$CHECKPOINT_FILE" 2>/dev/null || echo 0)
fi
COMPACTION_COUNT=$((PREV_COUNT + 1))

# Gather git state (fail gracefully if not a git repo)
GIT_BRANCH=""
RECENT_COMMITS="[]"
UNCOMMITTED_FILES="[]"
STAGED_FILES="[]"

if cd "$PROJECT_DIR" 2>/dev/null && git rev-parse --git-dir >/dev/null 2>&1; then
  GIT_BRANCH=$(git branch --show-current 2>/dev/null || true)

  # Last 10 commits as JSON array
  RECENT_COMMITS=$(git log --oneline -10 2>/dev/null | jq -R -s 'split("\n") | map(select(length > 0))' 2>/dev/null || echo '[]')

  # Uncommitted files
  UNCOMMITTED_FILES=$(git diff --name-only 2>/dev/null | jq -R -s 'split("\n") | map(select(length > 0))' 2>/dev/null || echo '[]')

  # Staged files
  STAGED_FILES=$(git diff --cached --name-only 2>/dev/null | jq -R -s 'split("\n") | map(select(length > 0))' 2>/dev/null || echo '[]')
fi

# Orchestra agents: auto-commit uncommitted work before compaction.
# Context loss from compaction + worktree cleanup = permanent data loss.
if [ "${ORCHESTRA_AGENT:-}" = "1" ] && cd "$PROJECT_DIR" 2>/dev/null; then
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    git add -A 2>/dev/null || true
    git commit -m "checkpoint: pre-compact (round $COMPACTION_COUNT)" \
      --allow-empty-message 2>/dev/null || true
  fi
fi

# Copy summary to last-summary.md (don't delete — stop-notify owns deletion)
if [ -f "$SUMMARY_FILE" ]; then
  cp "$SUMMARY_FILE" "$STATE_DIR/last-summary.md" 2>/dev/null || true
fi

# Build checkpoint JSON
NOW=$(date +%s)
jq -n \
  --arg sid "$SESSION_ID" \
  --arg trigger "$TRIGGER" \
  --argjson ts "$NOW" \
  --arg branch "$GIT_BRANCH" \
  --argjson commits "$RECENT_COMMITS" \
  --argjson uncommitted "$UNCOMMITTED_FILES" \
  --argjson staged "$STAGED_FILES" \
  --argjson count "$COMPACTION_COUNT" \
  '{
    session_id: $sid,
    trigger: $trigger,
    timestamp: $ts,
    git_branch: $branch,
    recent_commits: $commits,
    uncommitted_files: $uncommitted,
    staged_files: $staged,
    compaction_count: $count
  }' > "$CHECKPOINT_FILE" 2>/dev/null || true

# Also echo to stdout (for hook output)
cat "$CHECKPOINT_FILE" 2>/dev/null || true
