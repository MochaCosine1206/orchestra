#!/bin/bash
# swe-evo-adapter.sh — Convert SWE-EVO task specs to Orchestra goals
#
# SWE-EVO tasks represent version evolution: upgrading a codebase from
# start_version to end_version based on release notes. Each task may
# touch 1-107 files and bundle multiple PRs, features, and bug fixes.
#
# Key differences from SWE-bench:
#   - problem_statement is release notes (multi-item), not a single issue
#   - test_cmds contains real pytest commands (not "true")
#   - FAIL_TO_PASS is already a list (not a JSON string)
#   - Tasks span version upgrades, not single issue fixes
#
# Commands:
#   generate-goal <task_json>           — output Orchestra goal text
#   verify <task_json> <repo_dir>       — run verification checks (Docker-aware)
#   merge-test-cmd <task_json> [repo]   — emit merge-time test cmd (Docker-wrapped if available)
#   extract-tasks <dataset_path> [n]    — extract n tasks from dataset as JSONL
set -uo pipefail

COMMAND="${1:?Usage: swe-evo-adapter.sh <command> <args...>}"
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

# Helper: resolve Docker image for a task, trying multiple registries.
# Returns the image name if found, empty string if none available.
# Fallback chain:
#   1. "image" field from dataset (e.g. xingyaoww/...)
#   2. Official SWE-bench namespace (swebench/...)
#   3. Epoch AI registry (ghcr.io/epoch-research/...)
resolve_docker_image() {
    local task_json="$1"
    local image

    # 1. Explicit image field from dataset
    image=$(echo "$task_json" | jq -r '.image // ""')
    if [ -n "$image" ] && [ "$image" != "null" ] && docker image inspect "$image" &>/dev/null; then
        echo "$image"
        return
    fi
    # Try pulling if not local
    if [ -n "$image" ] && [ "$image" != "null" ] && docker pull "$image" &>/dev/null; then
        echo "$image"
        return
    fi

    # 2. Try SWE-bench namespace: derive from instance_id
    local instance_id
    instance_id=$(echo "$task_json" | jq -r '.id // .instance_id // ""')
    if [ -n "$instance_id" ]; then
        # instance_id like "scikit-learn__scikit-learn-12345" → "swebench/scikit-learn__scikit-learn:12345"
        local swe_image
        swe_image="swebench/${instance_id}"
        if docker image inspect "$swe_image" &>/dev/null; then
            echo "$swe_image"
            return
        fi
    fi

    # 3. Try Epoch AI registry
    if [ -n "$instance_id" ]; then
        local epoch_image="ghcr.io/epoch-research/swe-evo/${instance_id}"
        if docker image inspect "$epoch_image" &>/dev/null; then
            echo "$epoch_image"
            return
        fi
    fi

    # No image found
    echo ""
}

# Helper: wrap a test command with Docker if image is available.
# Usage: docker_wrap_cmd <test_cmd> <image> <repo_dir>
# If image is empty, returns the raw test_cmd.
docker_wrap_cmd() {
    local test_cmd="$1"
    local image="$2"
    local repo_dir="$3"

    if [ -z "$image" ]; then
        echo "$test_cmd"
        return
    fi

    # SWE-bench/SWE-EVO images use conda with a 'testbed' environment.
    # Activate it before running the test command. Use bash (not sh) since
    # conda activation requires bash features.
    # --platform linux/amd64: SWE-bench images are x86_64 only.
    # git safe.directory: bind-mounted /testbed is owned by host user, not
    # the container user. GitPython (used by DVC etc.) calls git internally
    # and fails with "dubious ownership" without this config.
    local safe_dir="git config --global --add safe.directory /testbed 2>/dev/null;"
    local conda_activate="source /opt/miniconda3/etc/profile.d/conda.sh && conda activate testbed &&"
    echo "docker run --rm --platform linux/amd64 -v ${repo_dir}:/testbed -w /testbed ${image} bash -c '${safe_dir} ${conda_activate} ${test_cmd}'"
}

# Helper: run a test command, using Docker if image is available, raw bash otherwise.
# Usage: run_test <test_cmd> <image> <repo_dir>
# Returns exit code from test execution.
run_test() {
    local test_cmd="$1"
    local image="$2"
    local repo_dir="$3"

    if [ -n "$image" ]; then
        docker run --rm --platform linux/amd64 \
          -v "${repo_dir}":/testbed -w /testbed "$image" \
          bash -c "git config --global --add safe.directory /testbed 2>/dev/null; source /opt/miniconda3/etc/profile.d/conda.sh && conda activate testbed && $test_cmd" 2>&1
    else
        cd "$repo_dir" || return 1
        bash -c "$test_cmd" 2>&1
    fi
}

case "$COMMAND" in
    generate-goal)
        TASK_JSON="$(resolve_task_json "${1:?generate-goal requires task JSON or file path}")"

        # Extract fields
        instance_id=$(echo "$TASK_JSON" | jq -r '.id // .instance_id')
        problem_statement=$(echo "$TASK_JSON" | jq -r '.description // .problem_statement')
        start_version=$(echo "$TASK_JSON" | jq -r '.start_version // ""')
        end_version=$(echo "$TASK_JSON" | jq -r '.end_version // ""')
        # Separate source files from test files — agents should only modify source files.
        # Test files are provided via test_patch applied before orchestra.
        source_files=$(echo "$TASK_JSON" | jq -r '[.files_changed // [] | .[] | select(test("/tests?/") | not)] | join(", ")')
        source_count=$(echo "$TASK_JSON" | jq -r '[.files_changed // [] | .[] | select(test("/tests?/") | not)] | length')
        files_changed="$source_files"
        files_count="$source_count"
        fail_to_pass=$(echo "$TASK_JSON" | jq -r '.fail_to_pass // .FAIL_TO_PASS // [] | if type == "array" then join("\n") else . end')
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // .test_cmds // "true"')

        # Build goal text — frame as version evolution
        goal="Evolve this Python repository from version ${start_version} to ${end_version}.

## Release Notes (Changes Required)
${problem_statement}

## Files That Must Be Modified (${files_count} files)
${files_changed}

## Requirements
- Read and understand the release notes thoroughly — each bullet point describes a change that must be implemented
- Modify ALL ${files_count} files listed above with the specific changes described in the release notes
- Each file modification must implement the corresponding feature, bug fix, or improvement from the release notes
- Ensure existing functionality is preserved while adding the new changes
- Every file listed above MUST be modified with specific logic — do not skip any file"

        if [ -n "$fail_to_pass" ] && [ "$fail_to_pass" != "null" ] && [ "$fail_to_pass" != "" ]; then
            # B-240: Keep full pytest node IDs (no longer strip file paths).
            # Read-only file filtering is handled by --read-only-files flag
            # instead of stripping paths from the goal text.
            goal="${goal}

## Tests That Must Pass After Changes
These test functions already exist in the repo and currently fail. Your code changes must make them pass.
Do NOT modify any test files — only modify the source files listed above:
${fail_to_pass}"
        fi

        if [ -n "$test_cmd" ] && [ "$test_cmd" != "true" ] && [ "$test_cmd" != "null" ]; then
            goal="${goal}

## Test Command
Run tests with: ${test_cmd}"
        fi

        echo "$goal"
        ;;

    verify)
        TASK_JSON="$(resolve_task_json "${1:?verify requires task JSON or file path}")"
        REPO_DIR="${2:?verify requires repo directory}"

        files_changed=$(echo "$TASK_JSON" | jq -r '.files_changed // []')
        files_count=$(echo "$files_changed" | jq -r 'length')
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // .test_cmds // "true"')
        fail_to_pass=$(echo "$TASK_JSON" | jq -r '.fail_to_pass // .FAIL_TO_PASS // []')
        fail_to_pass_count=$(echo "$fail_to_pass" | jq -r 'if type == "array" then length else 0 end')

        echo "=== SWE-EVO Verification ==="
        echo "Instance: $(echo "$TASK_JSON" | jq -r '.id // .instance_id')"
        echo "Version: $(echo "$TASK_JSON" | jq -r '.start_version') → $(echo "$TASK_JSON" | jq -r '.end_version')"

        # Check if expected files were modified
        base_commit=$(echo "$TASK_JSON" | jq -r '.base_commit // ""')
        files_correct=0
        if [ "$files_count" -gt 0 ]; then
            cd "$REPO_DIR" || exit 1
            if [ -n "$base_commit" ] && [ "$base_commit" != "null" ]; then
                modified_files=$(git diff --name-only "$base_commit"..HEAD 2>/dev/null || echo "")
            else
                modified_files=$(git diff --name-only HEAD~1..HEAD 2>/dev/null || echo "")
            fi

            echo ""
            echo "--- File Coverage ---"
            while IFS= read -r expected_file; do
                if [ -z "$expected_file" ]; then continue; fi
                if echo "$modified_files" | grep -qF "$expected_file"; then
                    echo "  PASS: $expected_file"
                    files_correct=$((files_correct + 1))
                else
                    echo "  MISS: $expected_file"
                fi
            done < <(echo "$files_changed" | jq -r '.[]')
            echo "FILES_CORRECT: $files_correct"
            echo "FILES_TOTAL: $files_count"
            echo "FILE_COVERAGE: $(( files_correct * 100 / files_count ))%"
        fi

        # Build check (Python syntax)
        cd "$REPO_DIR" || exit 1
        build_ok=true
        echo ""
        echo "--- Build Check ---"
        while IFS= read -r pyfile; do
            if [ -z "$pyfile" ]; then continue; fi
            if [ -f "$pyfile" ] && [[ "$pyfile" == *.py ]]; then
                if ! python3 -m py_compile "$pyfile" 2>/dev/null; then
                    echo "  FAIL: $pyfile has syntax errors"
                    build_ok=false
                fi
            fi
        done < <(echo "$files_changed" | jq -r '.[]' | head -20)
        if [ "$build_ok" = true ]; then
            echo "BUILD: PASS"
        else
            echo "BUILD: FAIL"
        fi

        # Apply test_patch — adds the FAIL_TO_PASS tests to the repo.
        # May already be applied (pre-orchestra), so try and skip if already present.
        test_patch=$(echo "$TASK_JSON" | jq -r '.test_patch // ""')
        echo ""
        echo "--- Applying Test Patch ---"
        if [ -n "$test_patch" ] && [ "$test_patch" != "null" ]; then
            cd "$REPO_DIR" || exit 1
            # Check if already applied (pre-orchestra step committed it)
            if echo "$test_patch" | git apply --allow-empty --check 2>/dev/null; then
                echo "$test_patch" | git apply --allow-empty 2>/dev/null
                echo "TEST_PATCH: APPLIED (clean)"
            elif echo "$test_patch" | git apply --allow-empty --reverse --check 2>/dev/null; then
                echo "TEST_PATCH: ALREADY APPLIED (skipping)"
            elif echo "$test_patch" | git apply --allow-empty --3way 2>/dev/null; then
                echo "TEST_PATCH: APPLIED (3way)"
            elif echo "$test_patch" | patch -p1 --fuzz=10 --force --no-backup-if-mismatch >/dev/null 2>&1; then
                echo "TEST_PATCH: APPLIED (fuzz)"
            else
                echo "TEST_PATCH: FAILED"
                echo "  Patch apply error:"
                echo "$test_patch" | git apply --allow-empty --check 2>&1 | head -10
            fi
        else
            echo "TEST_PATCH: NONE (no test patch provided)"
        fi

        # Run test command
        echo ""
        echo "--- Test Verification ---"
        test_pass="false"
        if [ -n "$test_cmd" ] && [ "$test_cmd" != "true" ] && [ "$test_cmd" != "null" ]; then
            cd "$REPO_DIR" || exit 1

            # Resolve Docker image for sandboxed test execution
            docker_image=""
            if command -v docker &>/dev/null; then
                docker_image=$(resolve_docker_image "$TASK_JSON")
                if [ -n "$docker_image" ]; then
                    echo "TEST_ENV: Docker ($docker_image)"
                else
                    echo "TEST_ENV: host (no Docker image found)"
                fi
            else
                echo "TEST_ENV: host (Docker not available)"
            fi

            # Check if the project's test environment is actually available.
            # Docker images have deps by definition — skip probe.
            first_test=$(echo "$fail_to_pass" | jq -r '.[0]' 2>/dev/null)
            can_test=false
            if [ -n "$docker_image" ]; then
                can_test=true
            elif [ -n "$first_test" ] && [ "$first_test" != "null" ]; then
                cd "$REPO_DIR" || exit 1
                collect_out=$(bash -c "$test_cmd --collect-only $first_test" 2>&1)
                if [ $? -eq 0 ]; then
                    can_test=true
                else
                    echo "TESTS: SKIP (test environment not available)"
                    echo "$collect_out" | grep -i 'error\|import\|module\|unrecognized' | head -3 | sed 's/^/  /'
                fi
            fi
            if [ "$can_test" = false ]; then
                echo "F2P_PASSED: 0"
                echo "F2P_FAILED: 0"
                echo "F2P_TOTAL: $fail_to_pass_count"
                test_pass="skip"
            elif [ "$fail_to_pass_count" -gt 0 ]; then
                # Use python3 -m pytest as fallback if pytest not on PATH (host only)
                if [ -z "$docker_image" ]; then
                    if ! command -v "${test_cmd%% *}" &>/dev/null; then
                        test_cmd="python3 -m pytest ${test_cmd#pytest}"
                    fi
                fi
                echo "Running $fail_to_pass_count FAIL_TO_PASS tests..."
                f2p_passed=0
                f2p_failed=0
                while IFS= read -r test_name; do
                    if [ -z "$test_name" ]; then continue; fi
                    test_output=$(run_test "$test_cmd $test_name" "$docker_image" "$REPO_DIR" 2>&1)
                    test_exit=$?
                    if [ "$test_exit" -eq 0 ]; then
                        f2p_passed=$((f2p_passed + 1))
                    else
                        echo "  FAIL: $test_name"
                        echo "$test_output" | tail -15 | sed 's/^/    /'
                        f2p_failed=$((f2p_failed + 1))
                    fi
                done < <(echo "$fail_to_pass" | jq -r '.[]' 2>/dev/null)

                echo "F2P_PASSED: $f2p_passed"
                echo "F2P_FAILED: $f2p_failed"
                echo "F2P_TOTAL: $fail_to_pass_count"

                if [ "$f2p_failed" -eq 0 ] && [ "$f2p_passed" -gt 0 ]; then
                    echo "TESTS: PASS"
                    test_pass="true"
                else
                    echo "TESTS: FAIL ($f2p_failed of $fail_to_pass_count tests failing)"
                fi
            else
                echo "TESTS: PASS (no FAIL_TO_PASS tests specified)"
                test_pass="true"
            fi
        else
            echo "TESTS: PASS (no test command specified)"
            test_pass="true"
        fi

        # Verdict
        echo ""
        if [ "$files_correct" -eq 0 ] && [ "$files_count" -gt 0 ]; then
            echo "VERDICT: FAIL (no expected files modified)"
        elif [ "$build_ok" != true ]; then
            echo "VERDICT: FAIL (build errors)"
        elif [ "$test_pass" = "true" ]; then
            echo "VERDICT: PASS"
        elif [ "$test_pass" = "skip" ] && [ "$build_ok" = true ]; then
            echo "VERDICT: PASS (file coverage + build only — no test env)"
        else
            echo "VERDICT: FAIL (tests failed)"
        fi
        ;;

    apply-test-patch)
        # Apply test_patch to the repo BEFORE orchestra runs, so agents
        # can implement against the real fail-to-pass tests instead of
        # writing their own (which causes patch conflicts at verify time).
        TASK_JSON="$(resolve_task_json "${1:?apply-test-patch requires task JSON or file path}")"
        REPO_DIR="${2:?apply-test-patch requires repo directory}"

        test_patch=$(echo "$TASK_JSON" | jq -r '.test_patch // ""')
        if [ -n "$test_patch" ] && [ "$test_patch" != "null" ]; then
            cd "$REPO_DIR" || exit 1
            if echo "$test_patch" | git apply --allow-empty 2>/dev/null; then
                echo "PRE_TEST_PATCH: APPLIED (clean)"
                git add -A && git commit -q -m "Apply test patch (pre-orchestra)" 2>/dev/null
            elif echo "$test_patch" | git apply --allow-empty --3way 2>/dev/null; then
                echo "PRE_TEST_PATCH: APPLIED (3way)"
                git add -A && git commit -q -m "Apply test patch (pre-orchestra)" 2>/dev/null
            else
                echo "PRE_TEST_PATCH: SKIPPED (will apply at verify time)"
            fi
        else
            echo "PRE_TEST_PATCH: NONE"
        fi
        ;;

    merge-test-cmd)
        # B-240: Return the actual test command, Docker-wrapped when possible.
        # The probe system (probeTestEnvironment) detects Docker-prefixed commands
        # and skips host probing — container image has deps by definition.
        TASK_JSON="$(resolve_task_json "${1:?merge-test-cmd requires task JSON or file path}")"
        REPO_DIR="${2:-\$REPO_DIR}"  # Caller passes repo dir, or placeholder for substitution
        test_cmd=$(echo "$TASK_JSON" | jq -r '.test_cmd // .test_cmds // "true"')

        if [ "$test_cmd" = "true" ] || [ "$test_cmd" = "null" ]; then
            echo "true"
        elif command -v docker &>/dev/null; then
            image=$(resolve_docker_image "$TASK_JSON")
            if [ -n "$image" ]; then
                docker_wrap_cmd "$test_cmd" "$image" "$REPO_DIR"
            else
                echo "$test_cmd"
            fi
        else
            echo "$test_cmd"
        fi
        ;;

    orchestra-flags)
        # B-240: Output Orchestra flags that enable environment-aware features
        # instead of bypassing them. Called by experiment scripts to get the
        # flags to pass to `orchestra go`.
        TASK_JSON="$(resolve_task_json "${1:?orchestra-flags requires task JSON or file path}")"

        # Collect test file paths from fail_to_pass for --read-only-files
        fail_to_pass=$(echo "$TASK_JSON" | jq -r '.fail_to_pass // .FAIL_TO_PASS // []')
        test_files=$(echo "$fail_to_pass" | jq -r 'if type == "array" then [.[] | split("::")[0]] | unique | join(",") else "" end' 2>/dev/null)

        flags="--test-failure-mode warn_only --disable-action-expansion"
        if [ -n "$test_files" ] && [ "$test_files" != "" ]; then
            flags="$flags --read-only-files $test_files"
        fi
        echo "$flags"
        ;;

    extract-tasks)
        DATASET="${1:?extract-tasks requires dataset path}"
        MAX="${2:-10}"
        MIN_FILES="${3:-1}"

        echo "Extracting up to $MAX tasks with >= $MIN_FILES files from $DATASET" >&2

        if [[ "$DATASET" == *.jsonl ]]; then
            jq -c "select(.files_changed | length >= $MIN_FILES)" "$DATASET" | head -n "$MAX"
        else
            echo "ERROR: Only JSONL format supported." >&2
            exit 1
        fi
        ;;

    *)
        echo "ERROR: Unknown command: $COMMAND" >&2
        echo "Commands: generate-goal, verify, extract-tasks, merge-test-cmd, orchestra-flags, apply-test-patch" >&2
        exit 1
        ;;
esac
