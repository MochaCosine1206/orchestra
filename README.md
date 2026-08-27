# Claude Orchestra

A local-first system that coordinates parallel Claude Code sessions on the same codebase. Give it a goal, and it decomposes, spawns agents in isolated worktrees, validates, and merges — all from a single command.

> **About this repository.** Orchestra is a tool I built for my own work — not a product, and
> not something running in production anywhere. This is a source snapshot published so the code
> can be read: the private working repository additionally carries research notes and planning
> documents that are not mine to publish. Everything here builds and the test suite passes.

## Requirements

Go 1.25+, Git, and Claude Code on your `PATH`. `orchestra doctor` checks these and reports the
optional extras (Docker for sandboxed agents, `jq`, `sqlite3`, GitHub CLI, `flock`).

## Install

Build from source:

```bash
git clone https://github.com/MochaCosine1206/orchestra.git
cd orchestra
make build          # binary at ./bin/orchestra
# or install onto your PATH:
go install ./cmd/orchestra
```

There are no tagged releases and no published Homebrew tap. `.goreleaser.yaml` and
`scaffold-homebrew-tap.sh` describe how packaging *would* work; neither has been run.

## Quick Start

```bash
# Initialize in your project
cd your-project
orchestra init

# One command → decompose, spawn agents, merge results
orchestra go --goal "Add user authentication with OAuth2 and role-based access"

# Watch it work (also launches if you just run `orchestra`)
orchestra dashboard
```

## Commands

### Running work

| Command | Description | Example |
|---------|-------------|---------|
| `go` | Decompose goal → spawn agents → merge | `orchestra go --goal "Add caching layer"` |
| `exec` | Execute a multi-phase YAML spec | `orchestra exec --spec plan.yaml --max-parallel 3` |
| `auto` | Autonomous mode: discover work, fix, repeat | `orchestra auto --goal "Improve test coverage" --max-cycles 3` |
| `new` | Create a whole project from an idea | `orchestra new --idea "CLI todo app in Go" --execute` |
| `decompose` | Plan tasks without executing | `orchestra decompose --goal "Refactor auth module"` |
| `generate-spec` | Generate a phased YAML spec from an idea | `orchestra generate-spec --idea "..." --output plan.yaml` |
| `merge` | Merge completed agent branches | `orchestra merge --test-cmd "go test ./..."` |

### Inspecting and steering

| Command | Description | Example |
|---------|-------------|---------|
| `dashboard` | Live TUI with task/agent panels | `orchestra dashboard` |
| `status` | Show tasks, agents, progress | `orchestra status --json` |
| `tokens` | Token usage summary | `orchestra tokens` |
| `audit` | Scan for issues and tech debt | `orchestra audit --scope code` |
| `reconcile` | Post-session gap analysis | `orchestra reconcile` |
| `projects` | Manage registered projects | `orchestra projects list` |
| `queue` | Manage the daemon priority queue | `orchestra queue list` |

### Agents and lifecycle

| Command | Description | Example |
|---------|-------------|---------|
| `spawn` | Manual agent lifecycle (`run`, `batch`, `kill`, `respawn`) | `orchestra spawn run --task T1` |
| `monitor` | Background heartbeat + cleanup (`start`, `status`, `run-once`) | `orchestra monitor start` |
| `daemon` | Background daemon (`start`, `stop`, `status`, `install`, `logs`) | `orchestra daemon status` |
| `heal` | Healing system diagnostics (`status`, `history`) | `orchestra heal status` |
| `recover` | Adopt an orphaned conductor session | `orchestra recover --spawn` |
| `reset` | Clean slate (DB, agents, worktrees) | `orchestra reset --keep-db` |
| `docker` | Docker runtime for sandboxed agents (`build`, `token`) | `orchestra docker build` |

### Evaluation and release

| Command | Description | Example |
|---------|-------------|---------|
| `eval` | Evaluation framework (`run`, `compare`, `report`, `scenarios`) | `orchestra eval run` |
| `ab-test` | Compare multi-agent vs single-agent | `orchestra ab-test --goal "Add flag" --runs 3` |
| `release` | Gated version tagging and publishing | `orchestra release --dry-run` |
| `cache` | Manage the plan decomposition cache | `orchestra cache clear` |
| `doctor` | Check environment dependencies | `orchestra doctor` |
| `init` | Initialize Orchestra in a project | `orchestra init` |
| `version` | Print version information | `orchestra version` |

Every command takes `--help`. Most of the work commands accept `--dry-run`.

## Dashboard

The Bubble Tea TUI provides live-updating panels for monitoring orchestration.

**Panels:**
- **Tasks** — Status, assignment, dependencies for all tasks
- **Agents** — Role, PID, heartbeat, current task
- **Logs** — Structured stream-json parsing with per-tool-category colors
- **Status bar** — Session goal, cycle count, circuit breaker state

**Keyboard shortcuts:**

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Next / previous panel |
| `↑` `↓` / `k` `j` | Move up / down |
| `g` / `G` | Jump to top / bottom |
| `Enter` | Select |
| `d` | Task detail |
| `Space` | Pause / resume log |
| `r` | Retry failed task |
| `x` | Kill running task |
| `/` | Filter tasks |
| `:` | Goal input |
| `b` | Blackboard |
| `Esc` | Close / cancel |
| `?` | Help |
| `q` / `Ctrl+C` | Quit |

`orchestra dashboard --web` serves the same view over HTTP instead of the terminal.

## How It Works

1. **Decompose** — An architect agent breaks the goal into focused, independent tasks with a dependency DAG
2. **Spawn** — Each task gets an agent in an isolated git worktree with role-specific permissions
3. **Monitor** — A background goroutine polls heartbeats, detects failures, triggers retries (circuit breaker trips after 3 consecutive failures)
4. **Validate** — Completed work passes a test gate (`--test-cmd`) before proceeding
5. **Merge** — Branches merge in topological order (Kahn's algorithm) with conflict detection
6. **Reconcile** — Post-session scan for orphan worktrees, uncommitted work, goal alignment gaps

**Autonomous mode** (`orchestra auto`) repeats this cycle, discovering new work from audit findings, TODO markers, and failed retries. Circuit breakers prevent runaway loops (2 empty cycles, 3 all-fail, 6hr timeout).

**Iterative mode** runs sequential `claude -p` rounds for tasks touching 4+ files, where each round sees prior commits via git.

## Claude Code Integration

Orchestra spawns agents via `claude -p` (headless mode) with these flags:

```bash
claude -p "<task prompt>" \
  --output-format stream-json \
  --verbose \
  --model opus \
  --permission-mode bypassPermissions
```

**MCP servers scaffolded for agents** (`orchestra init --mcp`):

| Server | Purpose |
|--------|---------|
| `sqlite` | Read/write coordination state |
| `git-worktree` | Worktree lifecycle management |
| `memory` | Cross-session knowledge graph |
| `pm` | Process management for agents |
| `filesystem` | Scoped file operations |
| `playwright` | Browser automation for verification |

## Troubleshooting

### WAL lock recovery
```bash
# If SQLite reports "database is locked"
orchestra reset  # Nuclear option: clears DB, kills agents, removes worktrees
# Or manually:
sqlite3 .orchestra/orchestrator.db "PRAGMA wal_checkpoint(TRUNCATE);"
```

### Stuck agents
```bash
# Check agent status
orchestra status
# Kill the agent working a given task
orchestra spawn kill --task <task-id>
# Kill all agents and clean up
orchestra reset
```

### Worktree cleanup
```bash
# List worktrees
git worktree list
# Remove orphaned worktrees (orchestra does this on reset)
orchestra reset
```

### Known issues from dogfooding
- **`orchestra init --non-interactive` fails.** It still enters the Telegram setup wizard and
  aborts on EOF, leaving the project uninitialized. Use plain `orchestra init`, which skips the
  wizard when no terminal is attached.
- **Rate limits on Claude Max** — Rare but possible. The RetryRunner handles exponential backoff automatically.
- **Nested Claude detection** — Claude Code v2.1.41+ detects nested sessions. Orchestra strips the `CLAUDECODE` env var to prevent this.
- **Long autonomous sessions** — After ~1hr, consider shorter `--max-cycles` to prevent context degradation.

## Repository Structure

```
├── cmd/
│   ├── orchestra/           — CLI entry point
│   ├── mock-agent/          — Test double for agent spawning
│   └── telegram-bridge/     — Approval/notification bridge
├── internal/
│   ├── agent/               — Spawning, roles, checkpoints, failure classification, Docker args
│   ├── assets/              — Embedded agent definitions and templates
│   ├── bridge/              — Telegram approval + AskUserQuestion routing
│   ├── cage/                — Hard-limit enforcement (the "cage pattern")
│   ├── cmd/                 — Cobra command definitions
│   ├── config/              — Global configuration read/write
│   ├── daemon/              — Background daemon, cron, discovery scanning, decision queue
│   ├── dashboard/           — Web dashboard, SSE, HITL handlers, artifacts
│   ├── db/                  — SQLite schema, queries, mutations, metrics
│   ├── delegate/            — External command delegation
│   ├── eval/                — LLM-as-judge scoring, golden transcripts, A/B harness
│   ├── github/              — GitHub App auth, PR title/description generation
│   ├── governor/            — Budgets, rate limits, circuit breakers, runaway detection
│   ├── healing/             — Build-error diagnosis and automated fixes
│   ├── healthcheck/         — Three-layer heartbeat and escalation
│   ├── isolation/           — Project locks and shared rate limiting
│   ├── monitor/             — Heartbeats, stall scoring, rabbit-hole detection
│   ├── onboard/             — Interactive questionnaire for `orchestra init`
│   ├── orchestrator/        — Conductor: decompose, merge, review, auto, ab-test, toposort
│   ├── priority/            — Priority engine, trust scoring, autonomy, alignment
│   ├── quality/             — Quality gates and ship decisions
│   ├── recursion/           — Depth guards, immutable paths, agent caps
│   ├── sandbox/             — Container runtime, network and mount policy
│   ├── scaffold/            — Project scaffolding and MCP config for `orchestra init`
│   ├── tui/                 — Bubble Tea dashboard (panels, log streaming, keybinds)
│   └── version/             — Build metadata and Claude version checks
├── src/ · src-tauri/        — Tauri desktop shell and React frontend
├── .claude/
│   ├── agents/              — Subagent definitions
│   ├── profiles/            — Permission profiles for headless agents
│   ├── commands/            — Slash command skills (/orchestra)
│   └── rules/               — Convention rules (Go, SQL)
├── templates/task-spec.md   — Standard task specification for agents
├── scripts/                 — Experiment runners, adapters, hooks, benchmarks
├── go.mod / go.sum          — Go module (ncruces/go-sqlite3, Bubble Tea, Cobra)
├── Makefile                 — make build, make test, make install-dev
├── CHANGELOG.md             — Project changelog (Keep a Changelog format)
├── logs/                    — Agent stream-json output (gitignored)
├── pids/                    — PID files for agents + monitor (gitignored)
└── .worktree/               — Git worktrees for agent isolation (gitignored)
```

## Configuration

### Agent definitions (`.claude/agents/`)
Markdown files with YAML frontmatter defining role, model, tools, and system prompt instructions.

### Permission profiles (`.claude/profiles/`)
JSON files with `allow`/`deny` tool permission lists per role, used with `--permission-mode dontAsk`.

### Convention rules (`.claude/rules/`)
Markdown files automatically injected into agent system prompts for project-specific conventions.

### Slash command (`.claude/commands/orchestra.md`)
The `/orchestra` skill that integrates with Claude Code's interactive mode.

### SQLite database
Opened WAL-mode with `busy_timeout=5000`, `synchronous=FULL`, `fullfsync=ON` and `foreign_keys=ON`.
`orchestra init` creates 20 tables, which group roughly as:

- **Coordination** — `tasks`, `agents`, `conductors`, `events`, `file_locks`, `blackboard`, `ideas`
- **Loops and planning** — `loops`, `loop_steps`, `plan_cache`
- **Merge and quality** — `merge_queue_entries`, `quality_ratchet`, `ship_decisions`
- **Evaluation** — `eval_runs`, `eval_results`, `eval_scenarios`, `eval_versions`
- **Health** — `stall_scores`, `drift_scores`, `healing_log`

Additional tables (`priority_queue`, `work_items`, `run_history`, `token_usage` and others) are
created by the daemon and priority migrations when those subsystems are first used.

## What This Is NOT

- Not a replacement for Claude Code — it orchestrates Claude Code sessions
- Not a cloud service — runs entirely on your machine
- Not a framework you import — it's a system you run alongside your project
- Not packaged — no releases, no Homebrew tap; build it from source

## License

MIT
