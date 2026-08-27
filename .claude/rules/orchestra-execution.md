---
paths:
  - specs/**
  - .orchestra/**
  - internal/daemon/**
  - internal/orchestrator/**
  - notes/dark-factory-plan.md
---

# Orchestra Execution Rules

## Project Pollution Prevention — CRITICAL

Failed runs leave stale branches, untracked files, and wrong branch checkouts that CORRUPT future runs. Rules:

- **Never reuse a project directory after multiple failed attempts.** Create a new version (v2, v3, v4) for each clean test.
- **Before ANY spec execution**, verify: `git status` shows clean tree, `git branch` shows correct branch, zero untracked files that conflict with agent output.
- **After `orchestra reset`**, also run `git clean -fd` to remove untracked files left by previous agents.
- **If merge errors mention "untracked working tree files"**, the project is polluted. Start fresh.
- **Tests must start from clean baselines** — never test a fix on top of a previous failed attempt. Create a new project dir to prove the fix works scientifically.

## Binary Management — CRITICAL

There are TWO orchestra binaries. They MUST stay in sync:
- `bin/orchestra` — project-local, used by conductor-run when CWD is the Orchestra repo
- `~/go/bin/orchestra` — global, used by daemon and external projects

**After ANY code change:**
1. `cd ~/projects/orchestra`
2. `go build -o bin/orchestra ./cmd/orchestra/`
3. `go build -o ~/go/bin/orchestra ./cmd/orchestra/`
4. `bin/orchestra version` — verify it runs and shows current commit
5. ONLY THEN launch conductor-run or daemon

**NEVER** use `cp` to copy one binary to the other — different builds may have different sizes.
**NEVER** assume the binary is current — always rebuild after code changes.
**NEVER** launch a conductor without verifying `orchestra version` first.

## Spec Strategy — Always Use Phased Specs

For ANY project with dependencies between tasks (which is nearly all projects):
- Generate a PHASED SPEC YAML, not a single-goal decomposition
- Phase 1: scaffold/foundation (must complete before anything else)
- Phase 2: core backend/data layer (parallel within phase, depends on Phase 1)
- Phase 3: integration/UI (depends on Phase 2)
- Phase 4: tests/validation (depends on everything)

Single-goal mode (`orchestra go -g "..."`) tries to parallelize everything and cascade-fails when dependencies aren't met. Phased specs (`orchestra exec --spec spec.yaml`) enforce ordering.

The factory's pipeline should: requirements doc → generate phased spec YAML → `orchestra exec --spec`

## Running Orchestra

1. **Decomposition takes 1-3 minutes** — the LLM call for task decomposition is a long operation. Never kill the process during decomposition. Monitor with `tail -f /tmp/orchestra-*.log` or check `ps aux | grep claude.*-p`.

2. **Always run in background with log capture:**
   ```bash
   nohup orchestra exec --spec <spec.yaml> --start-phase <phase> > /tmp/orchestra-<name>.log 2>&1 &
   ```
   Or for foreground monitoring: run without nohup and watch the output.

3. **Check process state before assuming failure:**
   - `ps -p <PID>` — is the conductor alive?
   - `ps aux | grep "claude.*-p"` — are agent processes running?
   - Check conductor log: `.orchestra/logs/conductor-*.log`
   - Check DB: `sqlite3 .orchestra/orchestrator.db "SELECT status FROM conductors ORDER BY started_at DESC LIMIT 1;"`

4. **Never run `orchestra reset` without reading agent logs first** — reset destroys worktrees and branches. Copy logs before reset.

## Monitoring Running Builds

1. Check task status: `sqlite3 .orchestra/orchestrator.db "SELECT substr(title,1,50), status FROM tasks WHERE conductor_id='<session>' ORDER BY priority DESC;"`
2. Check agent processes: `ps aux | grep "claude.*-p" | grep -v grep`
3. Read conductor log: `tail -20 .orchestra/logs/conductor-<session>.log`
4. Read agent logs (JSONL): `.orchestra/logs/t-<task-id>.jsonl`

## When Tasks Fail

1. **Trace before assuming** — read the agent log JSONL to find the actual error
2. **Check if it's a pre-existing issue** — run `go test ./...` on dev to see if tests already fail
3. **Validation failures are often transient** — concurrent worktree operations can cause test interference. Verify the agent's actual code by building/testing in the worktree directly
4. **Check retry count** — Orchestra retries up to 3 times. Check if it exhausted retries or is still working

## Timing Expectations

| Operation | Expected Duration |
|-----------|------------------|
| Decomposition (LLM call) | 1-3 minutes |
| Simple agent task | 5-15 minutes |
| Complex agent task | 15-45 minutes |
| Full spec phase (5 tasks) | 30-90 minutes |
| FIFO merge queue | 2-5 minutes per branch |

## Common Mistakes to Avoid

- Don't pipe Orchestra output through `head` or other truncating commands — causes SIGPIPE
- Don't assume rate limiting without checking rate_limit_event in logs
- Don't kill during decomposition — wait for the LLM call to complete
- Don't run `orchestra go` when another conductor is active — check `conductor:active` blackboard key
- Don't forget `--start-phase` when using `orchestra exec` with specs — without it, runs all phases
