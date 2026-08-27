#!/bin/bash
# B-261: Verification auditor — command hook replacement for prompt hook.
# Checks last_assistant_message for unverified claims and untested edits.
# Always outputs valid JSON. Never returns exit code 2 (never blocks).
#
# Checks:
# 1. UNVERIFIED CAUSAL CLAIMS: "root cause is", "caused by", etc. without
#    citing file:line, error output, stack traces, or log evidence.
# 2. UNVERIFIED FIX CLAIMS: "fixed", "resolved", "corrected" without
#    showing test re-run or build verification.
# 3. UNTESTED EDITS: Mentions editing source files without running tests/build.
#
# Returns: {"ok": true} or {"ok": false, "reason": "VERIFICATION NEEDED: ..."}
set -uo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"

INPUT=$(cat)

# --- Hook mode detection (B-249) ---
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
HOOK_MODE=$(get_hook_mode "$PROJECT_SLUG")

# Research mode: skip all verification checks (B-249)
if [ "$HOOK_MODE" = "research" ]; then
  echo '{"ok": true}'
  exit 0
fi

# --- Fast exits ---

# If stop_hook_active is true, skip verification (reply continuation)
STOP_ACTIVE=$(echo "$INPUT" | jq -r '.stop_hook_active // false' 2>/dev/null || echo "false")
if [ "$STOP_ACTIVE" = "true" ]; then
  echo '{"ok": true}'
  exit 0
fi

# Extract last_assistant_message
MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // empty' 2>/dev/null || true)

# Empty or missing message — nothing to check
if [ -z "$MSG" ]; then
  echo '{"ok": true}'
  exit 0
fi

# --- Check 1: Unverified causal claims ---
# Look for diagnostic phrases WITHOUT evidence (file:line, error output, stack trace)
HAS_CAUSAL_CLAIM=false
if echo "$MSG" | grep -qiE '(the )?root cause (is|was)|caused by|the (issue|problem) (is|was) (that |the )'; then
  HAS_CAUSAL_CLAIM=true
fi

if [ "$HAS_CAUSAL_CLAIM" = "true" ]; then
  # Check for evidence: file:line patterns, error/stack output, log references
  HAS_EVIDENCE=false
  # file.go:123 or file.py:42 or similar file:line patterns
  if echo "$MSG" | grep -qE '[a-zA-Z0-9_/-]+\.(go|py|js|ts|sh|rs|java|rb|md):[0-9]+'; then
    HAS_EVIDENCE=true
  fi
  # Error output indicators
  if echo "$MSG" | grep -qiE '(error output|stack trace|traceback|panic:|goroutine|exception|stderr).*:'; then
    HAS_EVIDENCE=true
  fi
  # Quoted error messages (likely from actual output)
  if echo "$MSG" | grep -qE '(error|Error|ERROR):.*[a-z]'; then
    HAS_EVIDENCE=true
  fi

  if [ "$HAS_EVIDENCE" = "false" ]; then
    jq -n '{ok: false, reason: "VERIFICATION NEEDED: Causal claim (root cause/caused by/issue is) without citing specific error output, log files, or source code line numbers."}'
    exit 0
  fi
fi

# --- Check 2: Unverified fix claims ---
# Look for fix assertions WITHOUT verification (test re-run, build output)
HAS_FIX_CLAIM=false
if echo "$MSG" | grep -qiE '\bI (fixed|resolved|corrected)\b|\bI.{0,20}(fix|resolve|correct) (the|this|that|a)\b'; then
  HAS_FIX_CLAIM=true
fi

if [ "$HAS_FIX_CLAIM" = "true" ]; then
  # Check for verification: test/build re-run
  HAS_VERIFICATION=false
  if echo "$MSG" | grep -qiE '(go test|npm test|pytest|make test|make build|cargo test|running|re-run|PASS|pass|success|BUILD)'; then
    HAS_VERIFICATION=true
  fi
  # "all N tests pass" pattern
  if echo "$MSG" | grep -qiE '[0-9]+ tests? pass'; then
    HAS_VERIFICATION=true
  fi

  if [ "$HAS_VERIFICATION" = "false" ]; then
    jq -n '{ok: false, reason: "VERIFICATION NEEDED: Fix claim (fixed/resolved/corrected) without showing test re-run or build verification output."}'
    exit 0
  fi
fi

# --- Check 3: Untested edits ---
# Look for editing source files WITHOUT running tests/build
HAS_EDIT=false
if echo "$MSG" | grep -qiE '\b(I )?(edited|modified|updated|changed|rewrote) .{0,40}\.(go|py|js|ts|sh|rs|java|rb)\b'; then
  HAS_EDIT=true
fi

if [ "$HAS_EDIT" = "true" ]; then
  # Check if tests or build were also mentioned
  HAS_TEST_RUN=false
  if echo "$MSG" | grep -qiE '(go test|npm test|pytest|make test|make build|cargo test|running|PASS|pass|success|BUILD|test.*pass|build.*success)'; then
    HAS_TEST_RUN=true
  fi

  if [ "$HAS_TEST_RUN" = "false" ]; then
    jq -n '{ok: false, reason: "UNTESTED EDITS: Source files were edited but no tests or build verification was run."}'
    exit 0
  fi
fi

# --- All checks passed ---
echo '{"ok": true}'
exit 0
