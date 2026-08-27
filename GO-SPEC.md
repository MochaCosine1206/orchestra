# GO-SPEC.md — Go Orchestrator Specification

## Overview

Claude Orchestra's orchestration layer is a Go binary providing a Cobra CLI + Bubble Tea TUI. Agents run as `claude -p` subprocess sessions. Migration from bash scripts is complete — all orchestration, spawning, monitoring, and CRUD operations are in Go.

**Non-goal:** Replacing `claude -p` with the Anthropic Agent SDK. Agents must remain Claude Code CLI sessions to retain built-in tools, MCP servers, permission profiles, `.claudeignore`, CLAUDE.md injection, and session resume.

## Architecture

```
orchestra (Go binary)
├── CLI mode    — `orchestra go --goal "..." --headless`    (Cobra, stdout)
├── TUI mode    — `orchestra` or `orchestra dashboard`      (Bubble Tea)
└── Skill mode  — `/orchestra go --goal "..."`              (Claude Code dispatch)
    │
    ├── Reads/writes SQLite directly (ncruces/go-sqlite3, WAL mode)
    ├── Spawns `claude -p` subprocesses (os/exec)
    ├── Monitors PIDs, heartbeats, timeouts (goroutines)
    └── Manages git worktrees (os/exec → git CLI)
```

### Current State (Phase G4 Complete)

```
orchestra (Go binary)
├── 16 CLI subcommands (go, auto, status, merge, audit, ab-test, decompose,
│   spawn, monitor, reconcile, recover, reset, init, tokens, version, dashboard)
├── TUI dashboard with live updates + log streaming + goal input
├── Embedded SQLite (ncruces/go-sqlite3, no Node.js TCP server)
├── Process supervisor (goroutines replace monitor.sh daemon)
├── Git worktree management
├── Post-session reconciliation (orphan cleanup, goal alignment, follow-ups)
├── Goal clarification + preprocessing (@file expansion, ambiguity detection)
├── Lenient dependency cascade mode
└── Spawns claude -p with permission profiles + model routing
```

## Technology Stack

| Component | Library | Purpose |
|-----------|---------|---------|
| CLI framework | `spf13/cobra` | Subcommand routing, flag parsing, help generation |
| TUI framework | `charmbracelet/bubbletea` v1 | Elm-architecture terminal UI (stable) |
| Styling | `charmbracelet/lipgloss` v1 | Colors, borders, layout |
| Components | `charmbracelet/bubbles` | Table, viewport, spinner, progress, help, textinput |
| Rich table | `evertras/bubble-table` | Task list with sorting, filtering, selection |
| Forms | `charmbracelet/huh` | Onboarding wizard, goal input |
| SQLite | `ncruces/go-sqlite3` | Pure Go (WASM), WAL mode, no CGO |
| Distribution | `goreleaser/goreleaser` | Cross-compile + Homebrew tap + GitHub Releases |
| Testing | `charmbracelet/x/exp/teatest` | Golden file TUI testing |

## Project Structure

```
cmd/orchestra/
├── main.go                 — Entry point (~10 lines), delegates to internal/cmd

internal/
├── cmd/                    — Cobra CLI (16 subcommands)
│   ├── root.go             — Root command, persistent flags, subcommand registration
│   ├── root_test.go        — Skeleton tests (subcommand presence, flags, version)
│   ├── dashboard.go        — `orchestra dashboard` (full TUI, default)
│   ├── status.go           — `orchestra status [--json]` (inline/headless)
│   ├── go_cmd.go           — `orchestra go --goal "..."` (orchestrate)
│   ├── auto.go             — `orchestra auto --goal "..." --sources ...`
│   ├── merge.go            — `orchestra merge --test-cmd CMD`
│   ├── decompose.go        — `orchestra decompose --goal "..."`
│   ├── audit.go            — `orchestra audit --scope ...`
│   ├── abtest.go           — `orchestra ab-test --goal "..." --test-cmd CMD`
│   ├── init_cmd.go         — `orchestra init` (project setup + onboarding)
│   ├── spawn_cmd.go        — `orchestra spawn run|kill|respawn|batch`
│   ├── monitor_cmd.go      — `orchestra monitor run-once|start|status`
│   ├── recover_cmd.go      — `orchestra recover [--merge]` (adopt orphaned sessions)
│   ├── reset_cmd.go        — `orchestra reset [--force]` (clean state)
│   ├── reconcile_cmd.go    — `orchestra reconcile [--dry-run] [--skip-llm]`
│   ├── tokens.go           — `orchestra tokens`
│   └── version.go          — `orchestra version`
├── version/
│   └── version.go          — Version constants (injected via ldflags)
├── tui/
│   ├── model.go            — OrchestraModel (root Bubble Tea model)
│   ├── panels/
│   │   ├── tasks.go        — Task table (evertras/bubble-table)
│   │   ├── agents.go       — Agent status list with heartbeat/retry badges
│   │   ├── log.go          — Log viewer (bubbles/viewport, auto-scroll)
│   │   └── status_bar.go   — Bottom bar: task/agent/lock counts, session time, help hints
│   ├── logstream/
│   │   └── parser.go       — JSONL stream-json parser (typed LogEntry structs)
│   ├── messages.go         — Custom tea.Msg types
│   ├── keybindings.go      — Key mapping definitions
│   └── styles.go           — Lip Gloss theme definitions
├── db/
│   ├── connection.go       — SQLite connection (WAL, busy_timeout)
│   ├── queries.go          — Read queries for TUI + CLI + lenient dep queries
│   ├── mutations.go        — Write queries (task state, agent, blackboard, locks)
│   ├── models.go           — Task, Agent, BlackboardEntry structs
│   └── schema.go           — Table definitions, migrations (go:embed)
├── agent/
│   ├── spawner.go          — Agent lifecycle (Run, Launch, Resume, Respawn, Batch)
│   ├── completion.go       — Post-exit handling (CheckLogResult, SalvageWorktreeChanges)
│   ├── classifier.go       — Failure classification (rate_limit, session_limit, context_exhausted, normal)
│   ├── specgen.go          — Task spec generation (replaces spec-gen.sh)
│   ├── validator.go        — Role-specific output validation
│   ├── config.go           — Role defaults, model routing, ResolveModel()
│   ├── process.go          — PID operations, kill with graceful shutdown
│   ├── budget.go           — Per-agent token budget tracking
│   ├── checkpoint.go       — Agent checkpoint save/restore
│   └── repomap.go          — Repository file tree generation for specs
├── assets/                 — Embedded assets (go:embed)
│   ├── agents/             — Agent role definitions (.md)
│   ├── profiles/           — Permission profiles (.json)
│   └── templates/          — Task spec templates
├── orchestrator/
│   ├── conductor.go        — Conductor struct, New(), ClaudeRunner interface
│   ├── go.go               — Go() orchestration (decompose → spawn → wait → merge → reconcile)
│   ├── auto.go             — Autonomous multi-cycle mode
│   ├── decompose.go        — Task decomposition via claude -p
│   ├── merge.go            — Topological merge ordering (Kahn's algorithm)
│   ├── review.go           — Review gate management (default, spec-diff, structured JSON modes)
│   ├── audit.go            — Codebase audit scanning
│   ├── abtest.go           — A/B test harness (review-test, structured-review, cascade arms)
│   ├── cascade.go          — Agent cascade: complexity estimation + tier routing (single→iterative→multi-agent)
│   ├── reconcile.go        — Post-session reconciliation (orphans, alignment, follow-ups)
│   ├── recover.go          — Orphaned session recovery
│   ├── clarify.go          — Goal clarification (ambiguity detection, option generation)
│   ├── goalpreprocess.go   — Goal preprocessing (@file expansion, git validation)
│   ├── goalexpand.go       — Goal expansion helpers
│   ├── redecompose.go      — Re-decomposition on context exhaustion
│   ├── iterative.go        — Session-cycling iterative mode
│   ├── toposort.go         — Topological sort for dependency ordering
│   ├── runner.go           — ClaudeRunner interface, ExecRunner, MockRunner, RetryRunner
│   ├── tokens.go           — Token usage reporting
│   └── helpers.go          — Shared helpers (activate/deactivate conductor, cleanup)
├── monitor/
│   └── monitor.go          — Goroutine supervisor (heartbeats, cascade, lenient deps, auto-merge)
├── onboard/                — Interactive onboarding (Huh forms)
├── scaffold/               — Project scaffolding for `orchestra init`
└── delegate/               — External command delegation
```

## Cobra Command Structure

```
orchestra                           # Default: launch TUI dashboard
orchestra dashboard                 # Explicit: launch TUI dashboard
orchestra go --goal "..."           # Orchestrate a goal (headless by default)
  --goal "description"              # Required: what to build
  --test-cmd "cmd"                  # Post-implementation test
  --iterative                       # Session-cycling mode for large goals
  --review                          # Enable pre-decompose + pre-merge review gates
  --headless                        # No TUI (default when piped or in CI)
  --dry-run                         # Show plan without executing
  --max-tasks N                     # Cap decomposed task count (default 3)
  --max-parallel N                  # Max concurrent agents (default 2)
  --interval N                      # Monitor poll interval in seconds (default 15)
  --model-strategy S                # Model routing: all-opus, per-role, all-sonnet
  --base-branch B                   # Base branch for worktrees (default: current)
  --clarify                         # Enable goal clarification before decompose
  --clarify-mode M                  # Clarification mode: auto, cli, tui
  --repo-map                        # Inject repo file tree into agent specs
  --reconcile                       # Run post-session reconciliation (default true)
  --lenient-deps                    # Enable lenient dependency cascade mode
  --cascade                         # Agent cascade: route by complexity tier (single→iterative→multi-agent)
orchestra auto                      # Autonomous multi-cycle mode
  --goal "description"              # Optional starting goal
  --sources audit,markers,gaps      # Work discovery sources
  --max-cycles N                    # Circuit breaker (default 10)
orchestra status                    # Show status (inline text or TUI)
  --json                            # Machine-readable JSON output
orchestra merge                     # Merge completed branches
  --test-cmd "cmd"                  # Test gate per branch
  --review                          # Pre-merge code review
  --dry-run                         # Show merge plan
orchestra decompose                 # Decompose goal into tasks
  --goal "description"              # Required
  --max-tasks N                     # Cap task count (default 3)
orchestra audit                     # Scan for issues
  --scope all|tests|code|gaps       # What to scan
  --dry-run                         # Report only
orchestra ab-test                   # Compare multi-agent vs single-agent
  --goal "description"              # Required
  --test-cmd "cmd"                  # Required
  --runs N                          # Repetitions (default 1)
  --clarify                         # Enable goal clarification for both arms
  --review-test                     # A/B test spec-anchored review vs default review
  --structured-review               # A/B test structured JSON review findings
  --cascade                         # A/B test cascade routing vs default Go()
orchestra spawn                     # Agent process management
  orchestra spawn run               # Spawn agent for a task
  orchestra spawn kill              # Kill a running agent
  orchestra spawn respawn           # Respawn a failed agent
  orchestra spawn batch             # Spawn agents for multiple tasks
orchestra monitor                   # Background supervisor
  orchestra monitor run-once        # Run one monitor cycle
  orchestra monitor start           # Start continuous monitoring
  orchestra monitor status          # Show monitor state
orchestra reconcile                 # Post-session analysis
  --dry-run                         # Report only, no side effects
  --skip-llm                        # Skip LLM goal alignment analysis
  --session ID                      # Reconcile a specific session
orchestra recover                   # Recover orphaned sessions
  --merge                           # Auto-merge after recovery
orchestra reset                     # Clean state (DB, agents, worktrees, git)
  --force                           # Skip confirmation
  # Also kills all agent PIDs and restores git state if behind upstream
orchestra init                      # Initialize orchestra in a project
  --clean                           # Clean existing state before init
orchestra tokens                    # Show token usage summary
orchestra version                   # Print version
```

## TUI Layout

Three-zone layout: Tasks (left ~45%), Agents+Log (right ~55%, split top/bottom), Status+Help (bottom 2 rows).

```
+============================================================+
|  CLAUDE ORCHESTRA v0.1.0-dev                    2 agents   |
+============================+===============================+
|  TASKS (8)           [Tab] |  AGENTS (2)            [Tab]  |
|                            |                               |
|  > a1b2 RUNNING impl auth |  A-01 opus impl-auth 2m  45K  |
|    c3d4 RUNNING impl db   |  A-02 opus impl-db   1m  32K  |
|    e5f6 PENDING impl api  |                               |
|    g7h8 DONE    scout dep |  LOG: impl-auth         [auto] |
|                            |  Reading src/auth/handler.ts   |
|                            |  Analyzing existing patterns   |
|                            |  Writing new middleware...      |
+============================+===============================+
|  2 running | 1 pending | 1 done    2 agents    0 locks    |
|  [j/k] nav  [tab] panel  [enter] focus  [?] help  [q] quit|
+============================================================+
```

**Panel details:**
- **Task table** — Columns: ID (short), Status (colored), Role, Agent, Title, Elapsed. Sorted by status group: running → pending → done → failed. Selection highlights corresponding agent.
- **Agent panel** — Each row: ID, role, model, task, heartbeat age, token count, retry badges.
- **Log viewer** — Tails selected agent's output file. Auto-scroll with pause (Space). Diff syntax highlighting.
- **Status bar** — Task summary counts, agent count, active locks, session elapsed time.
- **Help bar** — Context-sensitive key hints for the focused panel.

### Keyboard Navigation

| Key | Action | Phase |
|-----|--------|-------|
| `Tab` / `Shift+Tab` | Cycle focus: tasks → agents → log | P0 |
| `j` / `k` or `↑` / `↓` | Navigate within focused panel | P0 |
| `Enter` | Select task → show its agent log | P0 |
| `g` / `G` | Jump to top / bottom of list | P0 |
| `q` / `Ctrl+C` | Quit | P0 |
| `?` | Show help overlay (modal) | P0 |
| `Space` | Pause/resume auto-scroll (log) or auto mode | P1 |
| `r` | Retry failed task | P1 |
| `k` (on agent) | Kill running agent | P1 |
| `d` | Task detail modal | P1 |
| `m` | Trigger merge | P1 |
| `a` | Start autonomous mode | P1 |
| `/` | Filter tasks by regex | P1 |
| `o` | Cycle sort order | P1 |
| `1-5` | Direct panel focus | P1 |
| `b` | Blackboard inspector modal | P2 |
| `l` | Toggle full-screen panel | P2 |
| `@` | Project switcher | P3 |

### TUI Feature Roadmap

| Tier | Items | Features | Backlog Refs |
|------|-------|----------|-------------|
| **P0 — MVP** | B-087 | 4 panels, keyboard nav, SQLite polling, resize, non-terminal fallback | B-087 |
| **P1 — Interactivity** | B-090, B-091, B-092 | Log streaming, goal input, task/agent actions, filter/sort, toasts | B-090–B-092 |
| **P2 — Advanced Views** | B-093+ | Blackboard inspector, DAG, file locks, A/B results, full-screen | B-102, B-103 |
| **P3 — Multi-Project** | B-101+ | Project switcher, themes, mouse, clipboard, custom keybindings | B-101, B-104 |

See `research/techniques/055-tui-dashboard-research.md` for the full 100+ feature list with descriptions.

### Multi-Project Vision

Orchestra is a window into multiple connected projects:
- `@` key opens project/idea switcher
- Each project has its own `orchestrator.db`
- Dashboard aggregates status across projects
- Global config at `~/.config/orchestra/config.toml`
- Per-project config at `<project>/.orchestra/`
- Project discovery: walk-up search (Phase 1), config-listed paths (Phase 2)

### Resolved Open Questions

| Question | Decision | Rationale |
|----------|----------|-----------|
| Bubble Tea v1 vs v2? | **v1** (stable 0.x series) | v2 is alpha with breaking changes; all reference TUIs use v1 |
| Polling interval? | **2s default**, `--poll-interval` flag | Balances responsiveness (500ms too aggressive) with DB load (5s too sluggish) |
| Log streaming? | **fsnotify + ticker hybrid** | fsnotify for instant updates, 500ms ticker as fallback |
| Minimum terminal? | **80x24** with graceful degradation | VT100 standard; below 80 cols hide agent panel; below 60 show list only |
| Multi-project discovery? | **Walk-up first**, config-listed later | Walk-up for CWD project, config for project switcher (B-101) |

## SQLite Access Pattern

The Go binary is the sole database accessor:

```
Go binary (reader+writer)
         |
    ncruces/go-sqlite3
         |
    WAL mode
         |
  orchestrator.db
```

**Critical pragmas:**
```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
```

## Distribution

### Homebrew (primary)
```bash
brew tap plyne-technologies/orchestra
brew install orchestra
```

### Go install (developers)
```bash
go install github.com/Plyne-Technologies/orchestra/cmd/orchestra@latest
```

### GitHub Releases (manual)
Pre-built binaries for macOS arm64/amd64 and Linux amd64/arm64 via GoReleaser.

### Binary size target
~12-15 MB (Go binary + embedded SQLite WASM + embedded templates)

## Embedded Assets

Using `go:embed` to bundle:
- `.claude/agents/*.md` — Agent role definitions
- `.claude/profiles/*.json` — Permission profiles
- `templates/task-spec.md` — Task specification template
- `internal/db/schema/*.sql` — Database schema (go:embed)
- Default `CLAUDE.md` template for `orchestra init`

## Migration Phases (All Complete)

All four migration phases are complete. The Go binary is the sole interface — legacy bash scripts have been removed.

- **Phase G1** (DONE): Go skeleton, Cobra CLI, read-only TUI dashboard, `orchestra status`
- **Phase G2** (DONE): TUI interactivity, log streaming, goal input
- **Phase G3** (DONE): Go process management (spawner.go, monitor.go, classifier.go)
- **Phase G4** (DONE): Full conductor logic, embedded SQLite, CRUD, GoReleaser, `orchestra init`

## Testing Strategy

- Go `testing` package with 935 test functions covering all orchestration logic
- Mock `claude -p` with test fixtures (MockRunner, RetryRunner)
- Golden file tests for TUI output (teatest)
- SQLite queries tested against known database states
- End-to-end tests: Go binary runs full orchestration cycle

## Claude Code Integration

### `/orchestra` Skill
The `/orchestra` skill dispatches to the `orchestra` binary:
```bash
orchestra go --goal "..."
```

### Agent Definitions
Agent `.md` files are embedded in the Go binary and extracted during `orchestra init` to the project's `.claude/agents/` directory. This means:
- Users don't need to copy agent files manually
- Updates to agent definitions ship with binary updates
- Projects can customize their local copies

### Headless Mode for Agents
When `claude -p` agents need to invoke orchestra commands (e.g., checking status), they use headless mode:
```bash
orchestra status --json    # Machine-readable, no TUI
orchestra merge --headless # No interactive prompts
```

## Open Questions

1. **Module path:** `github.com/Plyne-Technologies/orchestra` or `github.com/MochaCosine1206/orchestra`? — Currently using `claude-orchestra`
2. **Minimum Go version:** 1.22+ (for `ncruces/go-sqlite3` WASM support)? — Currently using 1.25
3. ~~**Bubble Tea v1 vs v2:**~~ Resolved: v1 (stable). See TUI section above.
4. **When to split repos:** Develop in `cmd/orchestra/` in this repo, split when ready for B-040 distribution?
