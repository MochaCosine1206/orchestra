#!/bin/bash
# featurebench-adapter.sh — Convert FeatureBench task specs to Orchestra goals
#
# FeatureBench (ICLR 2026) focuses on feature addition tasks requiring
# multi-file changes (avg 15.7 files). Tasks include:
#   - Feature descriptions in natural language
#   - Test suites for verification
#   - Base repository state
#
# Task JSON format:
#   {"id": "feat-123", "repo": "owner/name", "base_commit": "abc123",
#    "description": "Add user authentication...", "test_cmd": "pytest tests/",
#    "files_changed": [...], "feature_spec": "..."}
#
# Commands:
#   generate-goal <task_json>       — output Orchestra goal text
#   verify <task_json> <repo_dir>   — run verification checks
set -uo pipefail

COMMAND="${1:?Usage: featurebench-adapter.sh <command> <args...>}"
shift

case "$COMMAND" in
    generate-goal)
        TASK_JSON="${1:?generate-goal requires task JSON}"

        instance_id=$(echo "$TASK_JSON" | jq -r '.id // .instance_id')
        description=$(echo "$TASK_JSON" | jq -r '.description // .feature_spec')
        files_changed=$(echo "$TASK_JSON" | jq -r '.files_changed // [] | join("\n  - ")')
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // "pytest"')

        goal="Implement the following feature in this repository.

## Feature Description
${description}"

        if [ -n "$files_changed" ] && [ "$files_changed" != "null" ] && [ "$files_changed" != "" ]; then
            goal="${goal}

## Files that need changes
  - ${files_changed}"
        fi

        goal="${goal}

## Requirements
- Read the codebase to understand the existing architecture
- Implement the feature across all necessary files
- Follow existing code patterns and conventions
- All existing tests must continue to pass
- The feature must work end-to-end
- Every file listed above MUST be modified with specific logic for this feature

## Verification
Run: ${test_cmd}"

        echo "$goal"
        ;;

    verify)
        TASK_JSON="${1:?verify requires task JSON}"
        REPO_DIR="${2:?verify requires repo directory}"

        files_changed=$(echo "$TASK_JSON" | jq -r '.files_changed // []')
        files_count=$(echo "$files_changed" | jq -r 'length')
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // "pytest"')

        echo "=== FeatureBench Verification ==="

        # Check modified files
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

        # Build check
        cd "$REPO_DIR" || exit 1
        if [ -f "$REPO_DIR/go.mod" ]; then
            if go build ./... 2>/dev/null; then
                echo "BUILD: PASS"
            else
                echo "BUILD: FAIL"
            fi
        elif [ -f "$REPO_DIR/Cargo.toml" ]; then
            if cargo build 2>/dev/null; then
                echo "BUILD: PASS"
            else
                echo "BUILD: FAIL"
            fi
        else
            echo "BUILD: PASS (no compilation step)"
        fi

        # Run test suite
        echo ""
        echo "=== Test Verification ==="
        cd "$REPO_DIR" || exit 1
        if bash -c "$test_cmd" 2>&1 | tail -30; then
            echo "TESTS: PASS"
        else
            echo "TESTS: FAIL"
        fi

        # Verdict
        echo ""
        cd "$REPO_DIR" || exit 1
        if bash -c "$test_cmd" >/dev/null 2>&1; then
            echo "VERDICT: PASS"
        else
            echo "VERDICT: FAIL (tests failed)"
        fi
        ;;

    *)
        echo "ERROR: Unknown command: $COMMAND" >&2
        echo "Commands: generate-goal, verify" >&2
        exit 1
        ;;
esac
