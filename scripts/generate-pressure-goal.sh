#!/bin/bash
# generate-pressure-goal.sh — Non-deterministic goal generator for pressure testing
#
# Produces randomized but verifiable multi-file goals for Orchestra pressure tests.
# Each invocation picks a random field name/type pair and generates tier-appropriate
# instructions with CONCRETE per-file logic (not "ensure it flows through").
#
# Usage: ./scripts/generate-pressure-goal.sh <tier> [output_dir]
#   tier:       4, 6, 8, 10, 12, 14, 16, 18, 20, 25, 30, 35, 40, 45, or 50 (number of files to target)
#   output_dir: directory for goal.txt + verify.sh (default: /tmp/pressure-goal)
#
# Output files:
#   <output_dir>/goal.txt    — the goal text for orchestra go --goal
#   <output_dir>/verify.sh   — verification script (per-file grep + build check)
#   <output_dir>/meta.env    — machine-readable metadata (FIELD_NAME, GO_FIELD, etc.)

set -euo pipefail

TIER="${1:?Usage: generate-pressure-goal.sh <tier> [output_dir]}"
OUTPUT_DIR="${2:-/tmp/pressure-goal}"

# Validate tier
case "$TIER" in
    4|6|8|10|12|14|16|18|20|25|30|35|40|45|50) ;;
    *) echo "ERROR: tier must be 4, 6, 8, 10, 12, 14, 16, 18, 20, 25, 30, 35, 40, 45, or 50 (got: $TIER)" >&2; exit 1 ;;
esac

mkdir -p "$OUTPUT_DIR"

# ── Field pools ──────────────────────────────────────────────────────

FIELD_NAMES=(
    estimated_hours
    cost_budget
    deadline_minutes
    risk_score
    complexity_rating
    review_priority
    max_tokens_budget
    retry_limit
)

# Matched pairs: SQL_TYPE / GO_TYPE / DEFAULT_VALUE / THRESHOLD_DESC
# Each entry: "SQL_TYPE|GO_TYPE|DEFAULT_VAL|THRESHOLD_DESC|FORMAT_HINT"
TYPE_PAIRS=(
    "INTEGER|sql.NullInt64|0|exceeds 100|%d"
    "TEXT|sql.NullString|\"\"|is empty|%s"
    "REAL|sql.NullFloat64|0.0|exceeds 95.0|%.2f"
)

# ── Random selection ─────────────────────────────────────────────────

RANDOM_SEED="$$$(date +%N)"
RANDOM=$RANDOM_SEED

FIELD_IDX=$((RANDOM % ${#FIELD_NAMES[@]}))
TYPE_IDX=$((RANDOM % ${#TYPE_PAIRS[@]}))

FIELD_NAME="${FIELD_NAMES[$FIELD_IDX]}"
IFS='|' read -r SQL_TYPE GO_TYPE DEFAULT_VAL THRESHOLD_DESC FORMAT_HINT <<< "${TYPE_PAIRS[$TYPE_IDX]}"

# Convert snake_case to PascalCase for Go field name (macOS-compatible)
GO_FIELD=$(echo "$FIELD_NAME" | awk -F'_' '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2)}1' OFS='')

# ── File targets per tier ────────────────────────────────────────────

# Tier 4: DB layer (schema, models, queries, mutations)
FILES_4=(
    "internal/db/schema/001_initial.sql"
    "internal/db/models.go"
    "internal/db/queries.go"
    "internal/db/mutations.go"
)

# Tier 6: +spawner, +specgen
FILES_6=(
    "${FILES_4[@]}"
    "internal/agent/spawner.go"
    "internal/agent/specgen.go"
)

# Tier 8: +go.go, +monitor
FILES_8=(
    "${FILES_6[@]}"
    "internal/orchestrator/go.go"
    "internal/monitor/monitor.go"
)

# Tier 10: +decompose, +TUI
FILES_10=(
    "${FILES_8[@]}"
    "internal/orchestrator/decompose.go"
    "internal/tui/panels/detail_modal.go"
)

# Tier 12: +validator, +cascade
FILES_12=(
    "${FILES_10[@]}"
    "internal/agent/validator.go"
    "internal/orchestrator/cascade.go"
)

# Tier 14: +clarify, +iterative
FILES_14=(
    "${FILES_12[@]}"
    "internal/orchestrator/clarify.go"
    "internal/orchestrator/iterative.go"
)

# Tier 16: +abtest, +reconcile
FILES_16=(
    "${FILES_14[@]}"
    "internal/orchestrator/abtest.go"
    "internal/orchestrator/reconcile.go"
)

# Tier 18: +merge, +review
FILES_18=(
    "${FILES_16[@]}"
    "internal/orchestrator/merge.go"
    "internal/orchestrator/review.go"
)

# Tier 20: +auto, +recover
FILES_20=(
    "${FILES_18[@]}"
    "internal/orchestrator/auto.go"
    "internal/orchestrator/recover.go"
)

# Tier 25: +classifier, +budget, +checkpoint, +repomap, +redecompose
FILES_25=(
    "${FILES_20[@]}"
    "internal/agent/classifier.go"
    "internal/agent/budget.go"
    "internal/agent/checkpoint.go"
    "internal/agent/repomap.go"
    "internal/orchestrator/redecompose.go"
)

# Tier 30: +conductor, +goalexpand, +metrics, +watcher, +tasks_panel
FILES_30=(
    "${FILES_25[@]}"
    "internal/orchestrator/conductor.go"
    "internal/orchestrator/goalexpand.go"
    "internal/db/metrics.go"
    "internal/monitor/watcher.go"
    "internal/tui/panels/tasks.go"
)

# Tier 35: +completion, +config, +process, +connection, +helpers
FILES_35=(
    "${FILES_30[@]}"
    "internal/agent/completion.go"
    "internal/agent/config.go"
    "internal/agent/process.go"
    "internal/db/connection.go"
    "internal/db/helpers.go"
)

# Tier 40: +go_cmd, +spawn_cmd, +status, +decompose_cmd, +merge_cmd
FILES_40=(
    "${FILES_35[@]}"
    "internal/cmd/go_cmd.go"
    "internal/cmd/spawn_cmd.go"
    "internal/cmd/status.go"
    "internal/cmd/decompose.go"
    "internal/cmd/merge.go"
)

# Tier 45: +audit, +runner, +toposort, +goalpreprocess, +helpers(orch)
FILES_45=(
    "${FILES_40[@]}"
    "internal/orchestrator/audit.go"
    "internal/orchestrator/runner.go"
    "internal/orchestrator/toposort.go"
    "internal/orchestrator/goalpreprocess.go"
    "internal/orchestrator/helpers.go"
)

# Tier 50: +tui/model, +keybindings, +agents_panel, +log_panel, +logstream/parser
FILES_50=(
    "${FILES_45[@]}"
    "internal/tui/model.go"
    "internal/tui/keybindings.go"
    "internal/tui/panels/agents.go"
    "internal/tui/panels/log.go"
    "internal/tui/logstream/parser.go"
)

# Select file list for this tier
declare -a TARGET_FILES
case "$TIER" in
    4)  TARGET_FILES=("${FILES_4[@]}") ;;
    6)  TARGET_FILES=("${FILES_6[@]}") ;;
    8)  TARGET_FILES=("${FILES_8[@]}") ;;
    10) TARGET_FILES=("${FILES_10[@]}") ;;
    12) TARGET_FILES=("${FILES_12[@]}") ;;
    14) TARGET_FILES=("${FILES_14[@]}") ;;
    16) TARGET_FILES=("${FILES_16[@]}") ;;
    18) TARGET_FILES=("${FILES_18[@]}") ;;
    20) TARGET_FILES=("${FILES_20[@]}") ;;
    25) TARGET_FILES=("${FILES_25[@]}") ;;
    30) TARGET_FILES=("${FILES_30[@]}") ;;
    35) TARGET_FILES=("${FILES_35[@]}") ;;
    40) TARGET_FILES=("${FILES_40[@]}") ;;
    45) TARGET_FILES=("${FILES_45[@]}") ;;
    50) TARGET_FILES=("${FILES_50[@]}") ;;
esac

FILE_COUNT=${#TARGET_FILES[@]}

# ── Generate per-file instructions ───────────────────────────────────

# Each file gets a CONCRETE instruction — not "verify" or "ensure it flows"
generate_file_instruction() {
    local file="$1"
    case "$file" in
        *schema/001_initial.sql)
            echo "Add a '$FIELD_NAME $SQL_TYPE' column to the tasks table (after the existing priority_label column)."
            ;;
        *models.go)
            echo "Add a '$GO_FIELD $GO_TYPE' field to the Task struct. Add the corresponding Scan target in any row-scanning helper."
            ;;
        *queries.go)
            echo "Include '$FIELD_NAME' in all SELECT column lists for tasks. Update Scan calls to include the new '$GO_FIELD' field."
            ;;
        *mutations.go)
            echo "Include '$FIELD_NAME' in the INSERT INTO tasks column list and VALUES placeholder. Add an Update${GO_FIELD} mutation: UPDATE tasks SET ${FIELD_NAME} = ? WHERE id = ?."
            ;;
        *spawner.go)
            echo "When spawning an agent, log the task's $GO_FIELD value at info level: log.Printf(\"[spawn] task %s $FIELD_NAME=%v\", task.ID, task.${GO_FIELD})."
            ;;
        *specgen.go)
            echo "In GenerateSpec, include the task's $FIELD_NAME value in the generated spec's overview table as a new row."
            ;;
        *go.go)
            echo "After creating tasks from decomposition results, set each task's $FIELD_NAME to a sensible default value using the new Update${GO_FIELD} mutation."
            ;;
        *monitor.go)
            echo "In the monitor loop, when checking completed tasks, log a warning if a task's $GO_FIELD $THRESHOLD_DESC."
            ;;
        *decompose.go)
            echo "In the complexity estimation or decomposition logic, read the $FIELD_NAME from the goal context if present and factor it into the task count decision."
            ;;
        *tui/panels/detail_modal.go)
            echo "In the task detail modal's View() method, add a new addField() call to display the task's $GO_FIELD. Use addField(\"$FIELD_NAME\", fmt.Sprintf(\"$FORMAT_HINT\", task.${GO_FIELD})) in the field rendering section alongside the existing fields."
            ;;
        *validator.go)
            echo "In ValidateTaskOutput for implementer role, check that the task's $GO_FIELD is set (not zero/empty) and log a validation note if it is missing."
            ;;
        *cascade.go)
            echo "In the complexity estimation, if the task's $GO_FIELD $THRESHOLD_DESC, bias the tier routing one level higher (e.g., Tier1 becomes Tier2)."
            ;;
        *clarify.go)
            echo "In the clarification logic, include $FIELD_NAME as a suggested clarification question when the goal mentions task sizing (e.g., append a question like 'What $FIELD_NAME should be used?' to the questions list)."
            ;;
        *iterative.go)
            echo "In the iterative round planning, log the task's $GO_FIELD value at the start of each round: log.Printf(\"[iterative] round %d task %s ${FIELD_NAME}=%v\", round, task.ID, task.${GO_FIELD})."
            ;;
        *abtest.go)
            echo "In A/B test result formatting, include $GO_FIELD in the per-arm summary table by adding a row like fmt.Fprintf(w, \"| $FIELD_NAME | %v |\\n\", arm.${GO_FIELD})."
            ;;
        *reconcile.go)
            echo "In post-session reconciliation, check if $FIELD_NAME was modified by any task and include it in the alignment score calculation (e.g., if task.${GO_FIELD} was set, count it as a covered field)."
            ;;
        *orchestrator/merge.go)
            echo "In the merge sequence, after merging a branch, log the task's $GO_FIELD value: log.Printf(\"[merge] task %s $FIELD_NAME=%v\", task.ID, task.${GO_FIELD})."
            ;;
        *orchestrator/review.go)
            echo "In the review findings summary, include $GO_FIELD as a metadata field in the structured review output (e.g., add findings = append(findings, ReviewFinding{Field: \"$FIELD_NAME\", Value: task.${GO_FIELD}}))."
            ;;
        *orchestrator/auto.go)
            echo "In the autonomous cycle loop, log $GO_FIELD for each task at the start of each cycle: log.Printf(\"[auto] cycle %d task %s $FIELD_NAME=%v\", cycle, task.ID, task.${GO_FIELD})."
            ;;
        *orchestrator/recover.go)
            echo "In session recovery, when adopting orphaned tasks, check if $GO_FIELD is set and log it as part of the recovery summary: log.Printf(\"[recover] adopted task %s $FIELD_NAME=%v\", task.ID, task.${GO_FIELD})."
            ;;
        *agent/classifier.go)
            echo "In the failure classification logic, include $GO_FIELD in the classification context: when classifying a failure, log the task's $FIELD_NAME value alongside the failure reason."
            ;;
        *agent/budget.go)
            echo "In the token budget estimation, factor in the task's $GO_FIELD: if $GO_FIELD $THRESHOLD_DESC, increase the budget by 10%. Log the adjusted budget with the $FIELD_NAME value."
            ;;
        *agent/checkpoint.go)
            echo "In the checkpoint save/restore logic, include $GO_FIELD in the checkpoint data: serialize task.${GO_FIELD} when saving and restore it when loading a checkpoint."
            ;;
        *agent/repomap.go)
            echo "In the repo map generation, add $FIELD_NAME to the task metadata section of the generated map: include a line like '$FIELD_NAME: <value>' in the task overview."
            ;;
        *orchestrator/redecompose.go)
            echo "In the re-decomposition logic (when a task exceeds context), preserve the original task's $GO_FIELD value and propagate it to the new sub-tasks created during re-decomposition."
            ;;
        *orchestrator/conductor.go)
            echo "In the conductor state management, track $GO_FIELD across the session: aggregate $FIELD_NAME values from all tasks and log a summary when the conductor completes."
            ;;
        *orchestrator/goalexpand.go)
            echo "In goal expansion (when processing @file references), check if the expanded goal text mentions $FIELD_NAME and set a flag in the goal context indicating $FIELD_NAME is goal-relevant."
            ;;
        *db/metrics.go)
            echo "In the metrics collection, add a query to compute the average $FIELD_NAME across all tasks in the session: SELECT AVG($FIELD_NAME) FROM tasks WHERE session_id = ?. Log the result."
            ;;
        *monitor/watcher.go)
            echo "In the file watcher event handler, when a file modification is detected, log the associated task's $GO_FIELD value: log.Printf(\"[watcher] file modified by task %s $FIELD_NAME=%v\", task.ID, task.${GO_FIELD})."
            ;;
        *tui/panels/tasks.go)
            echo "In the tasks panel rendering, add $FIELD_NAME as a new column in the task list table: include task.${GO_FIELD} formatted as a string in each row alongside status, role, and title."
            ;;
        *agent/completion.go)
            echo "In the agent completion handler, after determining task outcome, log the task's $GO_FIELD value: log.Printf(\"[completion] task %s $FIELD_NAME=%v outcome=%s\", taskID, task.${GO_FIELD}, outcome)."
            ;;
        *agent/config.go)
            echo "In the agent config/defaults, add a Default${GO_FIELD} constant set to $DEFAULT_VAL. Use this constant when initializing new SpawnOpts if the task's $GO_FIELD is not set."
            ;;
        *agent/process.go)
            echo "In the process management (kill/signal handling), log the task's $GO_FIELD value when killing a process: log.Printf(\"[process] killing task %s $FIELD_NAME=%v pid=%d\", taskID, task.${GO_FIELD}, pid)."
            ;;
        *db/connection.go)
            echo "In the DB connection setup, add a pragma comment documenting that the $FIELD_NAME column exists in the tasks table. Add a ValidateSchema helper that checks for the $FIELD_NAME column existence."
            ;;
        *db/helpers.go)
            echo "Add a helper function Get${GO_FIELD}Summary(ctx, sessionTaskIDs) that returns the min, max, and average $FIELD_NAME across all tasks in the session. Use: SELECT MIN($FIELD_NAME), MAX($FIELD_NAME), AVG($FIELD_NAME) FROM tasks WHERE id IN (...)."
            ;;
        *cmd/go_cmd.go)
            echo "In the go command handler, add a --${FIELD_NAME} flag (type matching $GO_TYPE) that sets the default $FIELD_NAME for all tasks created during this run. Pass it through ConductorOpts."
            ;;
        *cmd/spawn_cmd.go)
            echo "In the spawn command handler, log the task's $GO_FIELD value when spawning: log.Printf(\"[spawn-cmd] task %s $FIELD_NAME=%v\", taskID, task.${GO_FIELD})."
            ;;
        *cmd/status.go)
            echo "In the status command output, add $FIELD_NAME to the per-task status line. Format as: \"  $FIELD_NAME: %v\" using task.${GO_FIELD}."
            ;;
        *cmd/decompose.go)
            echo "In the decompose command, after creating tasks from decomposition, log a summary of $FIELD_NAME distribution: log.Printf(\"[decompose] %d tasks created, $FIELD_NAME values: %v\", len(tasks), fieldValues)."
            ;;
        *cmd/merge.go)
            echo "In the merge command handler, log the task's $GO_FIELD value before and after merge: log.Printf(\"[merge-cmd] task %s $FIELD_NAME=%v\", taskID, task.${GO_FIELD})."
            ;;
        *orchestrator/audit.go)
            echo "In the audit output, include $FIELD_NAME in the per-task audit row. Add a check that flags tasks where $GO_FIELD $THRESHOLD_DESC as a potential concern."
            ;;
        *orchestrator/runner.go)
            echo "In the runner (ExecRunner/ClaudeRunner), when executing a command for a task, log the task's $GO_FIELD value: log.Printf(\"[runner] executing for task %s $FIELD_NAME=%v\", taskID, task.${GO_FIELD})."
            ;;
        *orchestrator/toposort.go)
            echo "In the topological sort, when building the task dependency graph, annotate each node with its $GO_FIELD value. Log the sorted order with $FIELD_NAME values: log.Printf(\"[toposort] order: %s ($FIELD_NAME=%v)\", task.ID, task.${GO_FIELD})."
            ;;
        *orchestrator/goalpreprocess.go)
            echo "In goal preprocessing (before decomposition), check if the goal text mentions $FIELD_NAME and set a context flag: goalCtx[\"has_${FIELD_NAME}\"] = true. Log the detection."
            ;;
        *orchestrator/helpers.go)
            echo "Add a helper function Format${GO_FIELD}(value $GO_TYPE) string that formats the $FIELD_NAME for display using fmt.Sprintf(\"$FORMAT_HINT\", value). Use this in status/audit/log output."
            ;;
        *tui/model.go)
            echo "In the TUI root model, add $GO_FIELD to the status bar summary line. Show the average $FIELD_NAME across all tasks: fmt.Sprintf(\"avg $FIELD_NAME: $FORMAT_HINT\", avg${GO_FIELD})."
            ;;
        *tui/keybindings.go)
            echo "In the keybindings configuration, add a keybind comment documenting that 'f' key could filter tasks by $FIELD_NAME value. Add the $FIELD_NAME to the help overlay's field list."
            ;;
        *tui/panels/agents.go)
            echo "In the agents panel rendering, add $FIELD_NAME as metadata displayed next to each agent's assigned task: fmt.Sprintf(\"$FIELD_NAME=%v\", task.${GO_FIELD})."
            ;;
        *tui/panels/log.go)
            echo "In the log panel, when rendering log entries related to tasks, highlight entries that mention $FIELD_NAME with a distinct style. Add $GO_FIELD to the log entry metadata."
            ;;
        *logstream/parser.go)
            echo "In the log stream parser, add a pattern to detect $FIELD_NAME mentions in agent output. When detected, emit a parsed event with type \"field_update\" and field \"$FIELD_NAME\"."
            ;;
    esac
}

# ── Build goal text ──────────────────────────────────────────────────

FILE_LIST=""
INSTRUCTIONS=""
for f in "${TARGET_FILES[@]}"; do
    FILE_LIST+="  - $f"$'\n'
    instruction=$(generate_file_instruction "$f")
    INSTRUCTIONS+="- **$f**: $instruction"$'\n'
done

GOAL_TEXT="Add a ${FIELD_NAME} field (${SQL_TYPE} in SQL, ${GO_TYPE} in Go) to the Task model and integrate it across ${FILE_COUNT} files. The Go struct field name is ${GO_FIELD}.

Target files:
${FILE_LIST}
Per-file requirements:
${INSTRUCTIONS}
Every file listed above MUST be modified with the specific logic described. Do not skip any file."

echo "$GOAL_TEXT" > "$OUTPUT_DIR/goal.txt"

# ── Build verification script ────────────────────────────────────────

cat > "$OUTPUT_DIR/verify.sh" << 'VERIFY_HEADER'
#!/bin/bash
# Auto-generated verification script for pressure test
set -uo pipefail

REPO="${1:-.}"
PASS=0
FAIL=0
VERIFY_HEADER

# Add TOTAL
echo "TOTAL=${FILE_COUNT}" >> "$OUTPUT_DIR/verify.sh"

cat >> "$OUTPUT_DIR/verify.sh" << 'VERIFY_FUNCS'

declare -a FILE_NAMES=()
declare -a FILE_RESULTS=()

check() {
    local desc="$1"
    local short_name="$2"
    shift 2
    if "$@" >/dev/null 2>&1; then
        echo "  PASS: $desc"
        PASS=$((PASS + 1))
        FILE_RESULTS+=("PASS")
    else
        echo "  FAIL: $desc"
        FAIL=$((FAIL + 1))
        FILE_RESULTS+=("FAIL")
    fi
    FILE_NAMES+=("$short_name")
}

VERIFY_FUNCS

echo "" >> "$OUTPUT_DIR/verify.sh"
echo "echo \"=== ${FILE_COUNT}-File Pressure Test Verification (${FIELD_NAME}) ===\"" >> "$OUTPUT_DIR/verify.sh"
echo "" >> "$OUTPUT_DIR/verify.sh"

# Generate a check for each target file
for f in "${TARGET_FILES[@]}"; do
    # Short name for CSV output (strip .go and .sql extensions, normalize schema file)
    short_name=$(basename "$f" | sed -e 's/\.go$//' -e 's/\.sql$//' -e 's/001_initial/schema/' -e 's/detail_modal/model/')

    # Pattern: case-insensitive match for either snake_case or PascalCase field name
    echo "check \"$f: ${FIELD_NAME} or ${GO_FIELD}\" \"$short_name\" \\" >> "$OUTPUT_DIR/verify.sh"
    echo "    grep -qi '${FIELD_NAME}\\|${GO_FIELD}' \"\$REPO/$f\"" >> "$OUTPUT_DIR/verify.sh"
    echo "" >> "$OUTPUT_DIR/verify.sh"
done

# Build check and verdict
cat >> "$OUTPUT_DIR/verify.sh" << 'VERIFY_BUILD'

# Emit machine-readable per-file detail
detail=""
for i in "${!FILE_NAMES[@]}"; do
    if [ -n "$detail" ]; then detail="${detail},"; fi
    detail="${detail}${FILE_NAMES[$i]}=${FILE_RESULTS[$i]}"
done
echo "FILES_DETAIL: $detail"

echo ""
echo "=== Build Verification ==="
cd "$REPO" || exit 1

if go build ./... 2>/dev/null; then
    echo "  PASS: go build ./..."
    BUILD_PASS=true
else
    echo "  FAIL: go build ./..."
    BUILD_PASS=false
fi

echo ""
echo "=== Results: $PASS/$TOTAL file checks passed ==="
echo "Build: $BUILD_PASS"

# Pass threshold: >=75% of files + build passes
THRESHOLD=$(( (TOTAL * 75 + 99) / 100 ))  # ceiling division
if [ "$PASS" -ge "$THRESHOLD" ] && [ "$BUILD_PASS" = true ]; then
    echo "VERDICT: PASS (>=${THRESHOLD}/${TOTAL} threshold met + build passes)"
    exit 0
else
    echo "VERDICT: FAIL (need >=${THRESHOLD}/${TOTAL} + build, got ${PASS}/${TOTAL} + build=${BUILD_PASS})"
    exit 1
fi
VERIFY_BUILD

chmod +x "$OUTPUT_DIR/verify.sh"

# ── Write metadata ───────────────────────────────────────────────────

cat > "$OUTPUT_DIR/meta.env" << EOF
FIELD_NAME=${FIELD_NAME}
GO_FIELD=${GO_FIELD}
SQL_TYPE=${SQL_TYPE}
GO_TYPE=${GO_TYPE}
TIER=${TIER}
FILE_COUNT=${FILE_COUNT}
DEFAULT_VAL=${DEFAULT_VAL}
THRESHOLD_DESC=${THRESHOLD_DESC}
FORMAT_HINT=${FORMAT_HINT}
EOF

# ── Summary ──────────────────────────────────────────────────────────

echo "Generated pressure test goal:"
echo "  Field:  ${FIELD_NAME} (${GO_FIELD})"
echo "  Type:   ${SQL_TYPE} / ${GO_TYPE}"
echo "  Tier:   ${TIER} (${FILE_COUNT} files)"
echo "  Output: ${OUTPUT_DIR}/"
echo "    goal.txt   — goal for orchestra go"
echo "    verify.sh  — verification script"
echo "    meta.env   — metadata"
