#!/usr/bin/env bash
# queue-tech-debt.sh — Queue tech debt goals for overnight daemon execution
# Usage: bash scripts/queue-tech-debt.sh
# Prerequisites: daemon DB initialized, daemon running or about to start
set -euo pipefail

ORCHESTRA="${ORCHESTRA_BIN:-bin/orchestra}"
PROJECT_PATH="$(pwd)"

echo "Queueing tech debt goals for overnight execution..."
echo "Project: $PROJECT_PATH"
echo ""

# Goal 1: slog migration (highest value — enables structured logging)
$ORCHESTRA queue add "$PROJECT_PATH" \
  "Migrate all log.Printf calls to log/slog structured logging. Replace every import \"log\" with \"log/slog\". Convert log.Printf(\"msg %v\", x) to slog.Info(\"msg\", \"key\", x) and log.Printf(\"error: %v\", err) to slog.Error(\"error\", \"err\", err). Files to migrate: internal/agent/spawner.go, internal/agent/validator.go, internal/dashboard/server.go, internal/dashboard/sse.go, internal/dashboard/handlers.go, internal/monitor/monitor.go, internal/orchestrator/reconcile.go, and all files in internal/bridge/ and cmd/telegram-bridge/. Verify: go build ./... passes and grep -rn '\"log\"' --include='*.go' internal/ returns zero matches (excluding log/slog and _test.go)." \
  -p 90
echo "  [1/5] slog migration queued (priority 90)"

# Goal 2: Agent safety bounds (B-313)
$ORCHESTRA queue add "$PROJECT_PATH" \
  "Add maxTurns and disallowedTools frontmatter to all 6 agent definitions in .claude/agents/. Reviewer: maxTurns 60, disallowedTools [Write, Edit]. Scout: maxTurns 30, disallowedTools [Write, Edit, Bash]. Implementer: maxTurns 80. Architect: maxTurns 60. Researcher: maxTurns 120. Editor: maxTurns 80, disallowedTools [Bash]. Read each agent .md file, add the fields after the existing frontmatter tools/allowedTools lines. Verify: each file has valid YAML frontmatter with both fields present." \
  -p 80
echo "  [2/5] Agent safety bounds queued (priority 80)"

# Goal 3: Claude version preflight (B-315)
$ORCHESTRA queue add "$PROJECT_PATH" \
  "Add a Claude Code minimum version check to orchestra go, orchestra exec, orchestra auto, and orchestra new commands. In internal/cmd/go_cmd.go (and exec_cmd.go, auto.go, new_cmd.go), at the start of RunE, call a new helper CheckClaudeVersion(minVersion string) that runs 'claude --version', parses the semver output, and returns an error if below v2.1.83. Add the helper to internal/version/claude_check.go. Log a warning but don't block (use --require-version flag to hard-fail). Verify: go build ./... and go test ./internal/version/ ./internal/cmd/." \
  -p 70
echo "  [3/5] Claude version check queued (priority 70)"

# Goal 4: Post-merge file audit (B-296)
$ORCHESTRA queue add "$PROJECT_PATH" \
  "Implement post-merge file audit in internal/orchestrator/merge.go. After each successful branch merge, compare git diff --name-only against the task's files array from the DB. If any files were modified that aren't in the task's ownership list, insert a file_violation event into the events table with the task_id, extra file paths, and violation type. Add a --violations flag to orchestra status (internal/cmd/status.go) that queries and displays these events. Verify: go test ./internal/orchestrator/ ./internal/cmd/ pass." \
  -p 60
echo "  [4/5] Post-merge file audit queued (priority 60)"

# Goal 5: bridge/ structured errors
$ORCHESTRA queue add "$PROJECT_PATH" \
  "Standardize error handling in internal/bridge/ package. For every HTTP handler in handlers.go that returns an error or writes a plain text error response, convert to JSON error responses: {\"error\": \"message\", \"code\": \"error_type\"}. Also migrate any remaining log.Printf calls in the bridge package to slog (if not already done by the slog migration goal). Verify: go build ./internal/bridge/ and go test ./internal/bridge/." \
  -p 50
echo "  [5/5] bridge/ errors queued (priority 50)"

echo ""
echo "5 goals queued. View with: $ORCHESTRA queue list"
echo ""
echo "To run overnight with sandbox:"
echo "  1. Configure daemon: echo '{\"interval\":\"30s\",\"max_concurrent\":1,\"sandbox\":true}' > ~/.config/orchestra/daemon.json"
echo "  2. Start daemon: $ORCHESTRA daemon run"
echo ""
echo "Or run sequentially without daemon:"
echo "  $ORCHESTRA go --sandbox -g '<goal from queue>'"
