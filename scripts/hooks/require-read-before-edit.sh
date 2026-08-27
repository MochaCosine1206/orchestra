#!/bin/bash
# PreToolUse hook: require Read before Edit/Write on existing source files (B-278)
#
# Tracks Read tool calls per session. Blocks Edit/Write on source files that
# haven't been Read in the current session. Enforces "trace before assuming" —
# understand code before changing it.
#
# Exemptions:
#   - Non-source files (docs, configs, summaries, images)
#   - New files being created (Write to non-existent path)
#   - Temporary files (/tmp/)
#   - Hook scripts themselves (scripts/hooks/)
#   - Test files that share a name with an already-read file (e.g. foo_test.go when foo.go was read)
set -uo pipefail

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

# Only process Read, Edit, Write tools
case "$TOOL_NAME" in
  Read|Edit|Write) ;;
  *) exit 0 ;;
esac

READS_LOG="/tmp/claude-reads-${SESSION_ID:-unknown}"

# Extract file path
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# --- Track Read calls ---
if [ "$TOOL_NAME" = "Read" ]; then
  mkdir -p "$(dirname "$READS_LOG")"
  echo "$FILE_PATH" >> "$READS_LOG"
  exit 0
fi

# --- Edit/Write: check if file was Read first ---

# Exempt non-source files (docs, configs, summaries, images, etc.)
if ! echo "$FILE_PATH" | grep -qE '\.(go|py|ts|tsx|js|jsx|sh|sql|rs|java|c|cpp|h|hpp|rb|php)$'; then
  exit 0
fi

# Exempt hook scripts (we're editing hooks right now)
if echo "$FILE_PATH" | grep -qE 'scripts/hooks/'; then
  exit 0
fi

# Exempt temporary files
if echo "$FILE_PATH" | grep -qE '^/tmp/'; then
  exit 0
fi

# Exempt new files being created (Write to non-existent path)
if [ "$TOOL_NAME" = "Write" ] && [ ! -f "$FILE_PATH" ]; then
  exit 0
fi

# Check if file was Read in this session
if [ -f "$READS_LOG" ] && grep -qF "$FILE_PATH" "$READS_LOG" 2>/dev/null; then
  exit 0
fi

# Also check if a related file was read (e.g. editing foo_test.go after reading foo.go)
# This handles the common TDD pattern of reading impl then editing test
BASE_PATH="${FILE_PATH%_test.go}"
if [ "$BASE_PATH" != "$FILE_PATH" ] && [ -f "$READS_LOG" ]; then
  # Editing a _test.go file — check if the base .go file was read
  if grep -qF "${BASE_PATH}.go" "$READS_LOG" 2>/dev/null; then
    exit 0
  fi
fi
# Reverse: editing impl after reading test
TEST_PATH="${FILE_PATH%.go}_test.go"
if [ "$TEST_PATH" != "$FILE_PATH" ] && [ -f "$READS_LOG" ]; then
  if grep -qF "$TEST_PATH" "$READS_LOG" 2>/dev/null; then
    exit 0
  fi
fi

# Block: file not Read
BASENAME=$(basename "$FILE_PATH")
cat <<HOOK_EOF
{"decision":"block","reason":"You are editing '${BASENAME}' without reading it first in this session. Read the file before editing to understand what you're changing. Run: Read ${FILE_PATH}"}
HOOK_EOF
exit 0
