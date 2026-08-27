#!/bin/bash
# PostToolUse hook: six triggers across Bash, Edit, and Write
#   1. Test/build failure → inject trace-before-assume feedback + set failure state
#   2. Unexpected/empty results → inject follow-up-immediately feedback
#   3. Partial failures (mixed PASS/FAIL) → inject trace-before-interpreting feedback
#   4. Git commit → inject doc-alignment check
#   5. Edit/Write during unresolved failure → block until failure is verified fixed
#   6. Hardcoded path detection in Edit/Write (B-233)
set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HOOK_DIR/lib/state-helpers.sh"

# B-271: Skip all hook triggers during benchmark execution.
# Benchmark test output (e.g. "FAILED" in pytest) is expected and would
# cause false positives on Trigger 1/2/3.
if [ "${ORCHESTRA_BENCHMARK:-0}" = "1" ]; then
    exit 0
fi

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
FAILURE_FILE="/tmp/claude-failure-pending-${SESSION_ID:-unknown}"

# --- Hook mode detection (B-249) ---
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
PROJECT_SLUG=$(get_project_slug "$PROJECT_DIR")
HOOK_MODE=$(get_hook_mode "$PROJECT_SLUG")

# --- Trigger 5: Edit/Write during unresolved test failure ---
if [ "$TOOL_NAME" = "Edit" ] || [ "$TOOL_NAME" = "Write" ]; then
  if [ -f "$FAILURE_FILE" ]; then
    FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
    # Skip non-source files (docs, configs, hooks, summaries)
    if echo "$FILE_PATH" | grep -qE '\.(go|py|ts|js|sh|sql|rs|java)$'; then
      FAILED_CMD=$(cat "$FAILURE_FILE" 2>/dev/null || echo "unknown command")
      cat <<HOOK_EOF
{"decision":"block","reason":"EDITING SOURCE CODE WITH UNRESOLVED FAILURE. A test/build failure was detected from: ${FAILED_CMD}. You are editing source files before verifying you understand the root cause. Before editing: 1) READ the specific error output — what file, what line, what message? 2) READ the relevant source code at the failing location 3) Only THEN edit with a targeted fix based on traced evidence. After editing, RE-RUN the failing command to verify the fix works."}
HOOK_EOF
      exit 0
    fi
  fi

  # --- Trigger 6: Hardcoded path detection (B-233) ---
  # Catch bare "orchestrator.db" without .orchestra/ prefix — caused G84.
  # Also catch bare logs/ and pids/ paths that should use .orchestra/ prefix.
  # Skip hooks, tests, docs, and research files (they legitimately reference these paths).
  EDIT_FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
  SKIP_PATH_CHECK=false
  if echo "$EDIT_FILE_PATH" | grep -qE '(scripts/hooks/|test|_test\.go|research/|\.md$|BACKLOG|GAPS|CHANGELOG|CLAUDE\.md|SPEC\.md)'; then
    SKIP_PATH_CHECK=true
  fi
  EDIT_CONTENT=""
  if [ "$SKIP_PATH_CHECK" = "false" ]; then
    if [ "$TOOL_NAME" = "Edit" ]; then
      EDIT_CONTENT=$(echo "$INPUT" | jq -r '.tool_input.new_string // empty' 2>/dev/null)
    else
      EDIT_CONTENT=$(echo "$INPUT" | jq -r '.tool_input.content // empty' 2>/dev/null)
    fi
  fi
  if [ -n "$EDIT_CONTENT" ]; then
    # Match orchestrator.db NOT preceded by .orchestra/ (negative lookbehind via grep -P)
    # Exclude comments and string literals that document the pattern
    BAD_PATH=""
    if echo "$EDIT_CONTENT" | grep -qE '(^|[^/])orchestrator\.db' 2>/dev/null; then
      # Verify it's not already qualified: .orchestra/orchestrator.db OR
      # .orchestra + orchestrator.db in same content (e.g. filepath.Join args)
      if ! echo "$EDIT_CONTENT" | grep -q '\.orchestra' 2>/dev/null; then
        BAD_PATH="orchestrator.db"
      fi
    fi
    if [ -z "$BAD_PATH" ] && echo "$EDIT_CONTENT" | grep -qE '"(logs|pids)/[^"]*"' 2>/dev/null; then
      if ! echo "$EDIT_CONTENT" | grep -q '\.orchestra/' 2>/dev/null; then
        BAD_PATH="logs/ or pids/ without .orchestra/ prefix"
      fi
    fi
    if [ -n "$BAD_PATH" ]; then
      cat <<HOOK_EOF
{"decision":"block","reason":"HARDCODED PATH detected: ${BAD_PATH}. The orchestra database and directories live under .orchestra/ in the repo root. Use filepath.Join(repoRoot, \".orchestra\", \"orchestrator.db\") or equivalent. Bare 'orchestrator.db' without the .orchestra/ prefix caused G84 (enforcement hook queried wrong DB path). Fix the path to use the .orchestra/ prefix."}
HOOK_EOF
      exit 0
    fi
  fi

  exit 0
fi

[ "$TOOL_NAME" != "Bash" ] && exit 0

COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)
RESPONSE=$(echo "$INPUT" | jq -r '.tool_response | tostring // empty' 2>/dev/null)

# --- Git tag manipulation block (G102) ---
# Agents must not create, move, or delete git tags. Tags are version snapshots
# managed by release processes. An agent force-moved a tag to satisfy version
# acceptance criteria, corrupting benchmark repo state.
if echo "$COMMAND" | grep -qE '^git tag\b'; then
  cat <<'HOOK_EOF'
{"decision":"block","reason":"GIT TAG MANIPULATION BLOCKED (G102). Agents must NOT create, move, or delete git tags. Tags are version snapshots managed by release processes. To update a version, modify source files (pyproject.toml, __init__.py, version.go, etc.) instead."}
HOOK_EOF
  exit 0
fi

# --- False positive filter (B-278) ---
# Strip lines that contain known non-failure patterns before checking for real failures.
# Prevents: CSV fields like "F2P_FAILED: 0", commit messages "FAIL→PASS",
# zero-count lines "failed=0", benchmark summaries reporting results.
filter_false_positives() {
  grep -vE '(FAIL[A-Z_]*:\s*0|failed[=:]\s*0\b|FAIL→|→FAIL|_FAILED:\s*0|FAIL→PASS|tests_failed.*\b0\b)' || true
}

# --- Clear failure state if tests/build pass ---
if [ -n "$COMMAND" ] && [ -n "$RESPONSE" ]; then
  if echo "$COMMAND" | grep -qE '(go test|pytest|npm test|vitest|jest|cargo test|make test|make build)'; then
    FILTERED=$(echo "$RESPONSE" | filter_false_positives)
    if [ -z "$FILTERED" ] || ! echo "$FILTERED" | grep -qE '(^--- FAIL|^FAIL\s|FAILED|panic:|fatal error:|build failed|exit status [1-9])'; then
      rm -f "$FAILURE_FILE"
    fi
  fi
fi

# --- Trigger 1: Test/build failure detection ---
if [ -n "$RESPONSE" ]; then
  FILTERED_T1=$(echo "$RESPONSE" | filter_false_positives)
  if [ -n "$FILTERED_T1" ] && echo "$FILTERED_T1" | grep -qE '(^--- FAIL|^FAIL\s|FAILED|panic:|fatal error:|build failed|cannot build|compilation error|exit status [1-9])'; then
    # Record the failure for cross-tool-call tracking
    echo "$COMMAND" > "$FAILURE_FILE"
    cat <<'HOOK_EOF'
{"decision":"block","reason":"FAILURE DETECTED in command output. Do NOT assume or speculate about the cause. Instead: 1) READ the actual error output and identify the specific failing file/line/message 2) TRACE the root cause by reading relevant source files and logs 3) FIX the underlying issue based on traced evidence 4) VERIFY by rerunning the failing command. Never use phrases like 'known weakness' or 'expected failure' without log evidence."}
HOOK_EOF
    exit 0
  fi
fi

# --- Trigger 2: Unexpected/empty results → follow up immediately ---
# Skipped in research/debug modes (B-249). Skipped for test runners (G86 fix).
if [ "$HOOK_MODE" != "research" ] && [ "$HOOK_MODE" != "debug" ]; then
  if [ -n "$RESPONSE" ]; then
    # G86: skip trigger 2 for test runner commands — their output often contains
    # "not found", "0 matches" etc. as normal test assertions, not real empty results
    IS_TEST_RUNNER=false
    if echo "$COMMAND" | grep -qE '(go test|pytest|npm test|vitest|jest|cargo test|make test|bash .*test|\.\/test)'; then
      IS_TEST_RUNNER=true
    fi

    if [ "$IS_TEST_RUNNER" = "false" ]; then
      if echo "$RESPONSE" | grep -qE '(Filtered: 0 |Found 0 |No .* found|not found|0 results|0 matches|0 tasks|empty result)'; then
        cat <<'HOOK_EOF'
{"decision":"block","reason":"UNEXPECTED EMPTY RESULT detected. An assumption may have been invalidated. Do NOT pivot to a different task. Instead: 1) INVESTIGATE why the result was empty — trace the data, check the source 2) IDENTIFY alternatives that achieve the ORIGINAL goal (e.g., different dataset, different approach, different source) 3) RESEARCH those alternatives immediately before moving on 4) Only pivot if no viable alternative exists after investigation. The goal is to follow through on discoveries, not abandon them."}
HOOK_EOF
        exit 0
      fi
    fi
  fi
fi

# --- Trigger 3: Partial failure detection (results with mixed PASS/FAIL) ---
if [ -n "$RESPONSE" ]; then
  # Detect result summaries that contain FAIL entries alongside PASS entries
  # This catches: CSV results with FAIL fields, merge failures, file coverage misses
  FILTERED_T3=$(echo "$RESPONSE" | filter_false_positives)
  if [ -n "$FILTERED_T3" ] && echo "$FILTERED_T3" | grep -qE '(=FAIL|MISS:|merge failed|Merge failed|FAIL \(|files_correct.*[0-9]+.*files_target|[0-9]+/[0-9]+ files)'; then
    # Only fire if there are also PASS entries (partial failure, not total failure — total failures are caught by trigger 1)
    if echo "$FILTERED_T3" | grep -qE '(=PASS|PASS|passed|files_correct)'; then
      cat <<'HOOK_EOF'
{"decision":"block","reason":"PARTIAL FAILURE detected — results contain both PASS and FAIL entries. Do NOT interpret aggregate results without tracing individual failures. Instead: 1) IDENTIFY each failing item specifically 2) TRACE the root cause of each failure by reading logs and agent output 3) DETERMINE if failures share a common cause (task sizing, merge issue, agent crash) 4) REPORT traced evidence, not assumptions about why things failed."}
HOOK_EOF
      exit 0
    fi
  fi
fi

# --- Trigger 4: Git commit doc-alignment check ---
# Skipped in research/debug modes (B-249)
if [ "$HOOK_MODE" != "research" ] && [ "$HOOK_MODE" != "debug" ] && echo "$COMMAND" | grep -qE '^git commit'; then
  # A commit just succeeded — check actual committed files via git, not commit output text
  COMMITTED_FILES=$(git diff --name-only HEAD~1 HEAD 2>/dev/null || true)
  # Exclude hook-only and config-only changes from doc alignment check
  CODE_FILES=$(echo "$COMMITTED_FILES" | grep -E '\.(go|sql|sh)$' | grep -vE '^(scripts/hooks/|\.claude/)' || true)
  if [ -n "$CODE_FILES" ]; then
    # Code files were committed — check if any doc files were too
    if ! echo "$COMMITTED_FILES" | grep -qE '(CHANGELOG|GAPS|BACKLOG|research/)'; then
      cat <<'HOOK_EOF'
{"decision":"block","reason":"Code files were committed without documentation updates. Check if any of these need alignment: CHANGELOG.md (new features/fixes), GAPS.md (new/closed gaps), BACKLOG.md (new/completed items), research/ docs (technique findings). Update the relevant docs now while context is fresh, then commit them."}
HOOK_EOF
      exit 0
    fi
  fi
fi

exit 0
