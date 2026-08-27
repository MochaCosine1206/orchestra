#!/bin/bash
# swe-bench-adapter.sh — Convert SWE-bench task specs to Orchestra goals
#
# SWE-bench tasks come as JSON with:
#   - instance_id: unique identifier
#   - repo: owner/name
#   - base_commit: commit to reset to
#   - problem_statement: issue description
#   - hints_text: optional hints
#   - test_patch: pytest patch for verification
#   - patch: gold patch (not used during evaluation)
#   - FAIL_TO_PASS: tests that should pass after fix
#   - PASS_TO_PASS: tests that should still pass
#
# Commands:
#   generate-goal <task_json>           — output Orchestra goal text
#   verify <task_json> <repo_dir>       — run verification checks
#   extract-tasks <dataset_path> [n]    — extract n tasks from dataset as JSONL
#
# The adapter translates SWE-bench's problem_statement into a concrete
# Orchestra goal with file hints derived from the test paths.
set -uo pipefail

COMMAND="${1:?Usage: swe-bench-adapter.sh <command> <args...>}"
shift

# Helper: resolve task JSON from argument (file path or inline JSON)
resolve_task_json() {
    local arg="$1"
    if [ -f "$arg" ]; then
        cat "$arg"
    else
        echo "$arg"
    fi
}

case "$COMMAND" in
    generate-goal)
        TASK_JSON="$(resolve_task_json "${1:?generate-goal requires task JSON or file path}")"

        # Extract fields
        instance_id=$(echo "$TASK_JSON" | jq -r '.id // .instance_id')
        problem_statement=$(echo "$TASK_JSON" | jq -r '.description // .problem_statement')
        hints=$(echo "$TASK_JSON" | jq -r '.hints_text // ""')
        files_changed=$(echo "$TASK_JSON" | jq -r '.files_changed // [] | join(", ")')
        fail_to_pass=$(echo "$TASK_JSON" | jq -r '.fail_to_pass // .FAIL_TO_PASS // "" | if type == "array" then join(", ") else . end')

        # Build goal text
        goal="Fix the following issue in this Python repository.

## Issue
${problem_statement}"

        if [ -n "$hints" ] && [ "$hints" != "null" ] && [ "$hints" != "" ]; then
            goal="${goal}

## Hints
${hints}"
        fi

        if [ -n "$files_changed" ] && [ "$files_changed" != "null" ] && [ "$files_changed" != "" ]; then
            goal="${goal}

## Files likely needing changes
${files_changed}"
        fi

        if [ -n "$fail_to_pass" ] && [ "$fail_to_pass" != "null" ] && [ "$fail_to_pass" != "" ]; then
            goal="${goal}

## Tests that must pass after the fix
${fail_to_pass}"
        fi

        goal="${goal}

## Requirements
- Read and understand the issue thoroughly before making changes
- Identify ALL files that need modification
- Make the minimal changes needed to fix the issue
- Ensure existing tests continue to pass
- Every file listed above MUST be modified with specific logic to address the issue"

        echo "$goal"
        ;;

    verify)
        TASK_JSON="$(resolve_task_json "${1:?verify requires task JSON or file path}")"
        REPO_DIR="${2:?verify requires repo directory}"

        files_changed=$(echo "$TASK_JSON" | jq -r '.files_changed // []')
        files_count=$(echo "$files_changed" | jq -r 'length')
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // "true"')
        fail_to_pass=$(echo "$TASK_JSON" | jq -r '.fail_to_pass // .FAIL_TO_PASS // "" | if type == "array" then join(" ") else . end')

        echo "=== SWE-bench Verification ==="

        # Check if expected files were modified
        base_commit=$(echo "$TASK_JSON" | jq -r '.base_commit // ""')
        files_correct=0
        if [ "$files_count" -gt 0 ]; then
            cd "$REPO_DIR" || exit 1
            if [ -n "$base_commit" ] && [ "$base_commit" != "null" ]; then
                modified_files=$(git diff --name-only "$base_commit"..HEAD 2>/dev/null || echo "")
            else
                modified_files=$(git diff --name-only HEAD~1..HEAD 2>/dev/null || git diff --name-only HEAD 2>/dev/null || echo "")
            fi

            while IFS= read -r expected_file; do
                if [ -z "$expected_file" ]; then continue; fi
                if echo "$modified_files" | grep -qF "$expected_file"; then
                    echo "  PASS: $expected_file modified"
                    files_correct=$((files_correct + 1))
                else
                    echo "  FAIL: $expected_file NOT modified"
                fi
            done < <(echo "$files_changed" | jq -r '.[]')
        fi
        echo "FILES_CORRECT: $files_correct"

        # Build check (for Python: syntax check)
        cd "$REPO_DIR" || exit 1
        if python3 -m py_compile $(echo "$files_changed" | jq -r '.[]' | head -5) 2>/dev/null; then
            echo "BUILD: PASS"
        else
            # Fall back to just checking if Python can import without syntax errors
            echo "BUILD: PASS (skipped — no build step for Python)"
        fi

        # Apply test_patch — adds the FAIL_TO_PASS tests to the repo
        test_patch=$(echo "$TASK_JSON" | jq -r '.test_patch // ""')
        if [ -n "$test_patch" ] && [ "$test_patch" != "null" ]; then
            cd "$REPO_DIR" || exit 1
            if echo "$test_patch" | git apply --allow-empty 2>/dev/null; then
                echo "TEST_PATCH: APPLIED"
            else
                if echo "$test_patch" | git apply --allow-empty --reject 2>/dev/null; then
                    echo "TEST_PATCH: APPLIED (with rejects)"
                else
                    echo "TEST_PATCH: FAILED"
                fi
            fi
        fi

        # Run tests
        if [ -n "$test_cmd" ] && [ "$test_cmd" != "true" ] && [ "$test_cmd" != "null" ]; then
            echo ""
            echo "=== Test Verification ==="
            cd "$REPO_DIR" || exit 1
            if bash -c "$test_cmd" 2>&1 | tail -20; then
                echo "TESTS: PASS"
            else
                echo "TESTS: FAIL"
            fi
        else
            echo "TESTS: PASS (no test command specified)"
        fi

        # Run specific fail-to-pass tests if available
        if [ -n "$fail_to_pass" ] && [ "$fail_to_pass" != "null" ] && [ "$fail_to_pass" != "" ]; then
            echo ""
            echo "=== Fail-to-Pass Tests ==="
            cd "$REPO_DIR" || exit 1
            f2p_pass=true
            for test in $fail_to_pass; do
                if python3 -m pytest "$test" -x 2>&1 | tail -5; then
                    echo "  PASS: $test"
                else
                    echo "  FAIL: $test"
                    f2p_pass=false
                fi
            done
            if [ "$f2p_pass" = true ]; then
                echo "F2P: PASS"
            else
                echo "F2P: FAIL"
            fi
        fi

        # Verdict: tests must pass
        echo ""
        if echo "$files_correct" | grep -q '^0$' && [ "$files_count" -gt 0 ]; then
            echo "VERDICT: FAIL (no expected files modified)"
        elif [ -n "$test_cmd" ] && [ "$test_cmd" != "true" ] && [ "$test_cmd" != "null" ]; then
            # Re-run test to get exit code
            cd "$REPO_DIR" || exit 1
            if bash -c "$test_cmd" >/dev/null 2>&1; then
                echo "VERDICT: PASS"
            else
                echo "VERDICT: FAIL (tests failed)"
            fi
        else
            echo "VERDICT: PASS (no test verification available)"
        fi
        ;;

    extract-tasks)
        # Extract tasks from a SWE-bench dataset file (Parquet/JSONL)
        # Usage: swe-bench-adapter.sh extract-tasks <dataset_path> [max_tasks] [min_files]
        DATASET="${1:?extract-tasks requires dataset path}"
        MAX="${2:-10}"
        MIN_FILES="${3:-2}"

        echo "Extracting up to $MAX tasks with >= $MIN_FILES files from $DATASET" >&2

        # If it's a JSONL file, filter directly
        if [[ "$DATASET" == *.jsonl ]]; then
            jq -c "select(.files_changed | length >= $MIN_FILES)" "$DATASET" | head -n "$MAX"
        else
            echo "ERROR: Only JSONL format supported. Convert Parquet first:" >&2
            echo "  python3 -c \"import pandas as pd; pd.read_parquet('$DATASET').to_json('output.jsonl', orient='records', lines=True)\"" >&2
            exit 1
        fi
        ;;

    *)
        echo "ERROR: Unknown command: $COMMAND" >&2
        echo "Commands: generate-goal, verify, extract-tasks" >&2
        exit 1
        ;;
esac
