# Claude Orchestra

A local-first system that coordinates parallel Claude Code sessions on the same codebase. Give it a goal, and it decomposes, spawns agents in isolated worktrees, validates, and merges — all from a single command.

> **About this repository.** Orchestra is a tool I built for my own work — not a product, and
> not something running in production anywhere. This is a source snapshot published so the code
> can be read: the private working repository additionally carries research notes and planning
> documents that are not mine to publish. Everything here builds and the test suite passes.

## Quick Start

```bash
# Install
go install github.com/MochaCosine1206/orchestra/cmd/orchestra@latest

# ...or from a clone
go install ./cmd/orchestra

# Initialize in your project
cd your-project
orchestra init

# One command → decompose, spawn agents, merge results
orchestra go --goal "Add user authentication with OAuth2 and role-based access"

# Watch it work (also launches if you just run `orchestra`)
orchestra dashboard
```

## Commands

| Command | Description | Example |
|---------|-------------|---------|
| `go` | Decompose goal → spawn agents → merge | `orchestra go --goal "Add caching layer"` |
| `auto` | Autonomous mode: discover work, fix, repeat | `orchestra auto --goal "Improve test coverage" --max-cycles 3` |
| `decompose` | Plan tasks without executing | `orchestra decompose --goal "Refactor auth module"` |
| `merge` | Merge completed agent branches | `orchestra merge --test-cmd "go test ./..."` |
| `audit` | Scan for issues and tech debt | `orchestra audit --scope code` |
| `ab-test` | Compare multi-agent vs single-agent | `orchestra ab-test --goal "Add flag" --runs 3` |
| `status` | Show tasks, agents, progress | `orchestra status` |
| `tokens` | Token usage summary | `orchestra tokens` |
| `spawn` | Manual agent lifecycle | `orchestra spawn run --task-id T1` |
| `monitor` | Background heartbeat + cleanup | `orchestra monitor start` |
| `recover` | Adopt orphaned sessions | `orchestra recover` |
| `reset` | Clean slate (DB, agents, worktrees) | `orchestra reset` |
| `reconcile` | Post-session gap analysis | `orchestra reconcile` |
| `init` | Initialize Orchestra in a project | `orchestra init` |
| `dashboard` | Live TUI with task/agent panels | `orchestra dashboard` |
| `version` | Show build info | `orchestra version` |
| `doctor` | Check system health and dependencies | `orchestra doctor` |
| `release` | Gated version tagging and publishing | `orchestra release v1.0.0` |

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
| `Tab` / `Shift+Tab` | Cycle panels |
| `↑` / `↓` / `j` / `k` | Scroll within panel |
| `Enter` | Select task → view agent log |
| `g` | Enter goal (dispatches `orchestra go`) |
| `q` / `Ctrl+C` | Quit |

## How It Works

1. **Decompose** — An architect agent breaks the goal into 1-3 focused, independent tasks with a dependency DAG
2. **Spawn** — Each task gets an agent in an isolated git worktree with role-specific permissions
3. **Monitor** — A background goroutine polls heartbeats, detects failures, triggers retries (circuit breaker at 3)
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

**MCP servers available to agents:**
| Server | Purpose |
|--------|---------|
| `sqlite` | Read/write coordination state |
| `git-worktree` | Worktree lifecycle management |
| `memory` | Cross-session knowledge graph |
| `pm` | Process management for agents |
| `filesystem` | Scoped file operations |

**Agent roles (all Opus via Claude Max):**
| Role | Purpose | Tool Access |
|------|---------|-------------|
| Architect | Decomposition, system design, interface contracts | Read, Glob, Grep, Edit, Write, Bash, Task |
| Implementer | Focused code implementation following specs | Read, Edit, Write, Bash, Glob, Grep |
| Reviewer | Code review, security audit, quality gates | Read, Glob, Grep, Bash |
| Scout | Fast codebase exploration, file discovery | Read, Glob, Grep |
| Researcher | Deep research with comprehensive sources | WebSearch, WebFetch, Read, Glob, Grep, Edit, Write, Bash, Task |

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
# Kill a specific agent
orchestra spawn kill --agent-id <id>
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
- **Rate limits on Claude Max** — Rare but possible. The RetryRunner handles exponential backoff automatically.
- **Nested Claude detection** — Claude Code v2.1.41+ detects nested sessions. Orchestra strips the `CLAUDECODE` env var to prevent this.
- **Long autonomous sessions** — After ~1hr, consider shorter `--max-cycles` to prevent context degradation.

## Repository Structure

```
claude-orchestra/
├── cmd/orchestra/main.go    — Go binary entry point
├── internal/                — Go packages (13 packages, 138 files, 935 tests)
│   ├── agent/               — Spawner, classifier, validator, spec generator, budget, checkpoint
│   ├── assets/              — Embedded agent definitions, profiles, templates (go:embed)
│   ├── cmd/                 — Cobra CLI: 16 subcommands
│   ├── db/                  — SQLite layer: schema (go:embed), queries, mutations, models
│   ├── delegate/            — External command delegation
│   ├── monitor/             — Background heartbeat, dead agent cleanup, cascade failure, lenient deps
│   ├── onboard/             — Interactive onboarding questionnaire (Huh forms)
│   ├── orchestrator/        — Conductor: decompose, merge, review, auto, ab-test, toposort, reconcile
│   ├── scaffold/            — Project scaffolding for `orchestra init`
│   ├── tui/                 — Bubble Tea dashboard (panels, log streaming, keybinds)
│   └── version/             — Build metadata
├── go.mod / go.sum          — Go module (ncruces/go-sqlite3, Bubble Tea, Cobra)
├── Makefile                 — Build: make build, make test, make install-dev
├── .claude/
│   ├── agents/              — Subagent definitions (all Opus via Claude Max)
│   ├── commands/            — Slash command skills (/orchestra)
│   ├── profiles/            — Permission profiles for headless agents
│   └── rules/               — Convention rules (Go, SQL)
├── templates/task-spec.md   — Standard task specification for agents
├── scripts/                 — Experiment runners, adapters, hooks, benchmarks
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
WAL mode, `PRAGMA busy_timeout = 5000`, `PRAGMA synchronous = FULL`. Tables: agents, tasks, file_locks, events, blackboard, ideas, loops, loop_steps.

## What This Is NOT

- Not a replacement for Claude Code — it orchestrates Claude Code sessions
- Not a cloud service — runs entirely on your machine
- Not a framework you import — it's a system you run alongside your project

## License

MIT
