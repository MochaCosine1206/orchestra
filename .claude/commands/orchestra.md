---
description: "Run Claude Orchestra: decompose goals, spawn agents, check status, merge branches, audit code, reset state. Use when user mentions orchestra, agents, decompose, or multi-agent tasks."
argument-hint: <subcommand> [args] — subcommands: help, go, exec, new, generate-spec, auto, status, merge, reset, recover, audit, tokens, ab-test, init, spawn, monitor, dashboard
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
---

# Orchestra: Multi-Agent Conductor

You are the operator interface for Claude Orchestra. Route the user's request to the `orchestra` binary.

## Current Orchestra State

```
!`orchestra status 2>/dev/null | head -30`
```

## Subcommand Reference

### Core Workflow

| Subcommand | Usage | Description |
|---|---|---|
| `go` | `orchestra go --goal "..." [flags]` | Full workflow: decompose + spawn + monitor + merge (single-phase) |
| `exec` | `orchestra exec --spec spec.yaml [flags]` | Execute a multi-phase YAML spec (phases, gates, depends_on) |
| `new` | `orchestra new --idea "..." [flags]` | Create project dir + git init + orchestra init + generate spec |
| `generate-spec` | `orchestra generate-spec --idea "..." [-o spec.yaml]` | Generate a phased YAML spec from a project idea |
| `auto` | `orchestra auto [--goal "..."] [--sources audit,gaps,markers] [--max-cycles N]` | Autonomous loop: finds work, executes, merges, repeats |
| `decompose` | `orchestra decompose --goal "..."` | Break a goal into parallel agent tasks |
| `merge` | `orchestra merge [--base-branch BRANCH] [--test-cmd CMD] [--dry-run]` | Merge completed branches in dependency order |
| `status` | `orchestra status [--json]` | Dashboard: tasks, agents, tokens, conductor state |
| `reconcile` | `orchestra reconcile` | Post-session reconciliation and gap analysis |

### Agent Management

| Subcommand | Usage | Description |
|---|---|---|
| `spawn run` | `orchestra spawn run --task TASK_ID` | Spawn an agent for a pending task |
| `spawn batch` | `orchestra spawn batch [--max-parallel N]` | Batch-spawn unblocked pending tasks |
| `spawn kill` | `orchestra spawn kill --agent AGENT_ID` | Kill a running agent |
| `spawn respawn` | `orchestra spawn respawn --task TASK_ID` | Respawn a failed task with circuit breaker |

### Session Management

| Subcommand | Usage | Description |
|---|---|---|
| `reset` | `orchestra reset [--keep-db] [--dry-run]` | Full cleanup: remove worktrees, branches, reset DB, clean logs |
| `recover` | `orchestra recover [--spawn] [--merge] [--reset]` | Recover orphan session after crash |
| `init` | `orchestra init [--clean] [--all] [--claude-code] [--mcp] [--loops]` | Initialize orchestra in a project |

### Runtime & Docker

| Subcommand | Usage | Description |
|---|---|---|
| `docker` | `orchestra docker [subcommand]` | Docker runtime management (build, token, test) |
| `version` | `orchestra version` | Print version information |

### Monitoring & Analysis

| Subcommand | Usage | Description |
|---|---|---|
| `dashboard` | `orchestra dashboard` | Launch the TUI dashboard |
| `monitor start` | `orchestra monitor start` | Start monitor as foreground process |
| `monitor run-once` | `orchestra monitor run-once` | Run a single monitor cycle (debugging) |
| `monitor status` | `orchestra monitor status` | Check monitor status from blackboard |
| `audit` | `orchestra audit [--scope all\|tests\|code\|gaps] [--dry-run]` | Scan codebase for issues |
| `tokens` | `orchestra tokens` | Aggregate token usage report by role |
| `ab-test` | `orchestra ab-test --goal "..." --test-cmd CMD [--runs N]` | A/B test: multi-agent vs single-agent |

## Key Flags for `go`

| Flag | Default | Description |
|---|---|---|
| `--goal, -g` | (required) | Goal description |
| `--foreground` | false | Run conductor in foreground (blocks until completion) |
| `--base-branch` | auto-detect | Target branch to merge into (detects dev/main/master) |
| `--max-parallel` | 8 | Maximum concurrent agents |
| `--max-tasks` | 8 | Maximum tasks for decomposition (iterative: max rounds, capped at 5) |
| `--max-files-per-task` | 15 | Maximum files per task (0 = unlimited) |
| `--iterative` | false | Session-cycling mode for large goals |
| `--cascade` | false | Cascade routing: auto-select single/iterative/multi-agent |
| `--review` | false | Enable pre-decompose and pre-merge review gates |
| `--test-cmd` | none | Post-implementation test command |
| `--test-cmd-timeout` | 300 | Test command timeout in seconds |
| `--test-failure-mode` | (none) | Test failure behavior: revert_and_refine, warn_only, revert_no_refine |
| `--repo-map` | false | Include compact repo map in task specs |
| `--clarify` | false | Enable goal clarification before decomposition |
| `--clarify-mode` | auto | Clarification mode: auto, cli |
| `--model-strategy` | all-opus | Model strategy: all-opus, per-role, all-sonnet |
| `--runtime` | local | Execution runtime: local, docker |
| `--file-enforcement` | (none) | File ownership enforcement: defense, pessimistic |
| `--lenient-deps` | false | Proceed with partial predecessor output on mixed blocker outcomes |
| `--reconcile` | true | Run post-session reconciliation |
| `--read-only-files` | (none) | Files to exclude from modification targets |
| `--disable-action-expansion` | false | Skip vague file action expansion |
| `--interval` | 15 | Monitor poll interval in seconds |
| `--dry-run` | false | Show plan without executing |

## Key Flags for `exec`

| Flag | Default | Description |
|---|---|---|
| `--spec` | (required) | Path to YAML spec file |
| `--start-phase` | (none) | Resume from this phase ID (skip prior phases) |
| `--max-parallel` | 8 | Maximum concurrent agents per phase |
| `--base-branch` | auto-detect | Base branch for merges |
| `--review` | false | Enable review gates |
| `--repo-map` | true | Include repo map in task specs |
| `--runtime` | local | Execution runtime: local, docker |
| `--merge-mode` | local | Merge mode: local, pr |
| `--reconcile` | true | Run reconciliation after final phase |
| `--dry-run` | false | Show execution plan without running |

## Key Flags for `new`

| Flag | Default | Description |
|---|---|---|
| `--idea` | (required) | High-level project idea |
| `--name` | (derived from idea) | Project directory name |
| `--parent-dir` | `.` | Parent directory for the new project |
| `--execute` | false | Execute the generated spec after creation |
| `--constraints` | (none) | Comma-separated constraints |
| `--tech-stack` | (none) | Tech stack as key=value pairs (e.g. "language=go,framework=gin") |
| `--include` | (none) | Directories/files to copy into project before spec generation (repeatable) |

## Key Flags for `generate-spec`

| Flag | Default | Description |
|---|---|---|
| `--idea` | (required) | High-level project idea |
| `-o, --output` | spec.yaml | Output path for the generated spec |
| `--constraints` | (none) | Comma-separated constraints |
| `--tech-stack` | (none) | Tech stack as key=value pairs |
| `--repo-context` | false | Include codebase context in the prompt |

## Autonomous Mode Controls

| Command | Description |
|---|---|
| `orchestra auto --goal "..." --max-cycles 5` | Run up to 5 cycles on a goal |
| `orchestra auto --sources audit,gaps,markers` | Find work from multiple sources |
| `orchestra auto --pause` | Pause a running autonomous session |
| `orchestra auto --resume` | Resume a paused session |
| `orchestra auto --stop` | Stop autonomous mode gracefully |

## Routing Rules

1. **Empty argument or `help`**: Display the subcommand reference table above. Do NOT run any command.
2. **Known subcommand**: Execute via Bash: `orchestra <subcommand> <args>`
3. **`--version`**: Run `orchestra version`
4. **Natural language** (e.g., "run 3 agents on auth feature"): Map to the closest subcommand. If the user describes a goal, use `go --goal "..."`.
5. **Cleanup requests** (e.g., "clean up", "start fresh", "remove worktrees"): Use `orchestra reset`.
6. **Recovery requests** (e.g., "session crashed", "orphan tasks"): Use `orchestra recover`.
7. **Unknown input**: Show the reference table and suggest the closest match.

## Working Directory

Always run from the project root. The `$ARGUMENTS` variable contains everything after `/orchestra`.

```bash
orchestra $ARGUMENTS
```

## Error Handling

- If `orchestra` is not found, run `go install ./cmd/orchestra` or `go build -o orchestra ./cmd/orchestra` first.
- If database errors, run `orchestra init` then retry.
- If orphan session blocks `go`, run `orchestra recover --reset` or `orchestra reset`.
- If merge targets wrong branch, use `--base-branch` flag explicitly.
- If worktrees are leftover from a failed run, use `orchestra reset`.

## CRITICAL: Choosing `go` vs `exec`

| Scenario | Command | Why |
|----------|---------|-----|
| **Book/content project with spec.yaml** | `orchestra exec --spec spec.yaml` | `exec` understands phases, gates, and depends_on. Runs phases sequentially. |
| **Code task (single goal)** | `orchestra go -g "goal"` | `go` decomposes a single goal into parallel tasks. No phase awareness. |
| **Code task with @file goal** | `orchestra go -g "@SPEC.md"` | Expands file into goal text. Still single-phase decomposition. |

**NEVER use `orchestra go -g "@spec.yaml"` for multi-phase book/content specs.** The `go` command flattens the entire spec into a single decomposition pass, extracting only the first phase's tasks and ignoring all subsequent phases. This is how v8 only produced specs but zero chapters.

### Common Pitfalls

| Pitfall | What Happens | Fix |
|---------|-------------|-----|
| `go -g "@spec.yaml"` on a phased spec | Only first phase runs | Use `exec --spec spec.yaml` |
| `go --cascade` on research/content specs | `IsResearch` routes to Tier 1 (single agent) | Don't use `--cascade` for content projects (G141) |
| `go --max-tasks 8` (default) on 25-task spec | Decomposer collapses to 8 tasks | Set `--max-tasks` >= total tasks in spec |
| Untracked files matching agent output | Merge errors: "untracked files would be overwritten" | Commit all pre-existing files before launching |

## Project Setup Workflow

### New project from scratch
```bash
orchestra new --idea "REST API for todo items in Go" --parent-dir ~/projects --execute
```

### New version of existing project (books/content)
Only research carries forward. Everything else is written from scratch.
See `notes/version-process-textbook.md` for the full 5-step process.
See `research/architecture/202-spec-generation-guide.md` (doc 202) for spec quality checklist.

**One-liner with `--include`:**
```bash
orchestra new --idea "v10 textbook on AI agents..." --include /prev/research --name project-v10 --execute
```

**Manual (when you need more control):**
```bash
VERSION=9
mkdir -p ~/projects/project-v${VERSION}
cd ~/projects/project-v${VERSION}
git init && git checkout -b dev
mkdir -p research specs chapters
cp /path/to/prev-version/research/branch-*.md research/   # ONLY research
# Write CLAUDE.md and spec.yaml from scratch (use doc 202 as guide)
# Write .gitignore
git add . && git commit -m "v${VERSION} initial: research + spec"
orchestra init
orchestra exec --spec spec.yaml --dry-run   # verify plan
orchestra exec --spec spec.yaml --max-parallel 3
```

## Examples

```bash
# Project setup
orchestra new --idea "billing microservice" --name billing-api    # create + generate spec
orchestra new --idea "React dashboard" --execute                   # create + generate + run
orchestra generate-spec --idea "REST API" --tech-stack "language=go" -o spec.yaml  # spec only

# Book/content projects — ALWAYS use exec
orchestra exec --spec spec.yaml                        # run all phases sequentially
orchestra exec --spec spec.yaml --dry-run              # preview execution plan
orchestra exec --spec spec.yaml --start-phase content  # resume from a specific phase
orchestra exec --spec spec.yaml --max-parallel 3       # limit concurrent agents

# Code tasks — use go
orchestra go --foreground --goal "Add user authentication" --base-branch main
orchestra go --foreground --goal "Refactor API layer" --review --test-cmd "go test ./..."
orchestra go --foreground --goal "Add tests for all handlers" --max-parallel 8 --repo-map
orchestra go --goal "Large refactor" --iterative  # session-cycling for big tasks
orchestra go --goal "Implement feature" --dry-run  # preview decomposition

# Autonomous mode
orchestra auto --goal "Refactor API" --sources audit --max-cycles 3
orchestra auto --sources audit,gaps,markers  # find work automatically
orchestra auto --pause   # pause running session
orchestra auto --resume  # resume paused session

# Merge with options
orchestra merge --base-branch master --test-cmd "npm test"
orchestra merge --dry-run  # preview merge plan

# Cleanup & recovery
orchestra reset                    # nuke everything, start fresh
orchestra reset --keep-db          # remove worktrees but keep history
orchestra reset --dry-run          # preview what would be removed
orchestra recover                  # check for orphan session
orchestra recover --spawn          # adopt session and spawn pending tasks
orchestra recover --merge          # adopt session and merge completed

# Status & monitoring
orchestra status
orchestra status --json
orchestra dashboard
orchestra tokens
orchestra audit --scope tests

# A/B testing
orchestra ab-test --goal "Optimize query" --test-cmd "go test ./..." --runs 3
```
