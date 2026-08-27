# GO-SPEC.md — Go Orchestrator Specification

**Status:** current as of 2026-08-27. Where this document and the code disagree, the code wins.
`README.md` is the user-facing reference; this covers implementation decisions.

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

27 internal packages. `internal/orchestrator` (76 files) and `internal/cmd` (44 files) are the
two largest; `internal/agent`, `internal/priority`, `internal/tui` and `internal/dashboard`
follow.

```
cmd/
├── orchestra/          — CLI entry point
├── mock-agent/         — Test double for agent spawning
└── telegram-bridge/    — Approval/notification bridge

internal/
├── agent/         (25)  Spawning, roles, checkpoints, failure classification, Docker args
├── assets/         (2)  Embedded agent definitions and templates (go:embed)
├── bridge/        (16)  Telegram approval + AskUserQuestion routing
├── cage/           (2)  Hard-limit enforcement (the "cage pattern")
├── cmd/           (44)  Cobra command definitions
├── config/         (8)  Global configuration read/write
├── core/          (16)  Shared layer for CLI/TUI/dashboard/daemon: daemon DB and schema,
│                        fairness engine, scheduling policy, multi-project status, service
│                        install and locking, PR creation, log-stream and markdown renderers
├── daemon/        (17)  Background daemon, cron, discovery scanning, decision queue
├── dashboard/     (21)  Web dashboard, SSE, HITL handlers, artifacts
├── db/            (14)  SQLite schema, queries, mutations, metrics
├── delegate/       (2)  External command delegation
├── eval/           (9)  LLM-as-judge scoring, golden transcripts, A/B harness
├── github/         (4)  GitHub App auth, PR title/description generation
├── governor/       (9)  Budgets, rate limits, circuit breakers, runaway detection
├── healing/       (10)  Build-error diagnosis and automated fixes
├── healthcheck/    (4)  Three-layer heartbeat and escalation
├── isolation/      (2)  Project locks and shared rate limiting
├── monitor/       (10)  Heartbeats, stall scoring, rabbit-hole detection
├── onboard/        (6)  Interactive questionnaire for `orchestra init`
├── orchestrator/  (76)  Conductor: decompose, merge, review, auto, ab-test, toposort
├── priority/      (27)  Priority engine, trust scoring, autonomy, alignment
├── quality/       (12)  Quality gates and ship decisions
├── recursion/     (10)  Depth guards, immutable paths, agent caps
├── sandbox/        (9)  Container runtime, network and mount policy
├── scaffold/       (4)  Project scaffolding and MCP config
├── tui/           (23)  Bubble Tea dashboard (panels, log streaming, keybinds)
└── version/        (4)  Build metadata and Claude version checks
```

## Cobra Command Structure

28 commands (excluding Cobra's generated `help` and `completion`). Those with subcommands are
marked.

```
orchestra
├── go                  Decompose goal → spawn agents → merge
├── exec                Execute a multi-phase YAML spec
├── auto                Autonomous multi-cycle mode
├── new                 Create a new project from an idea
├── decompose           Decompose a goal into tasks
├── generate-spec       Generate a phased YAML spec from an idea
├── merge               Merge completed branches
├── dashboard           Launch the TUI dashboard (--web for HTTP)
├── status              Show orchestra status
├── tokens              Show token usage
├── audit               Scan for issues
├── reconcile           Post-session reconciliation and gap analysis
├── projects  ›         add · list · remove · scan · show
├── queue     ›         add · cancel · demote · history · list · promote
├── spawn     ›         batch · kill · respawn · run
├── monitor   ›         run-once · start · status
├── daemon    ›         install · logs · run · start · status · stop · uninstall
├── heal      ›         history · status
├── docker    ›         build · token
├── eval      ›         compare · report · run · scenarios
├── cache     ›         clear
├── ab-test             Compare multi-agent vs single-agent
├── recover             Recover an orphan conductor session
├── reset               Remove all worktrees and reset the database
├── release             Run release gate checks and tag a new version
├── init                Initialize orchestra in a project
├── doctor              Check environment dependencies
└── version             Print version information
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

**Nothing is published today.** There are no git tags, no GitHub releases, and no Homebrew tap
(`MochaCosine1206/homebrew-tap` does not exist). GoReleaser has never been run. The only
supported install is building from source:

```bash
git clone https://github.com/MochaCosine1206/orchestra.git
cd orchestra && make build
```

The sections below describe the intended packaging, not the current state.

### Homebrew (planned)
`.goreleaser.yaml` is configured to publish a formula to a `homebrew-tap` repository. Creating
that repository is a prerequisite; `scaffold-homebrew-tap.sh` exists to do it.

### Go install (works once the repository is public)
```bash
go install github.com/MochaCosine1206/orchestra/cmd/orchestra@latest
```
With no tags, this resolves to a pseudo-version of the default branch.

### GitHub Releases (planned)
Pre-built binaries for macOS arm64/amd64 and Linux amd64/arm64 via GoReleaser.

### Binary size
**~33.5 MB** as built today (Go binary + embedded SQLite WASM + embedded templates + dashboard
assets). The original 12–15 MB target in this document was never met and no longer reflects what
the binary contains.

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

- Go `testing` package: **2,126 test functions across 163 test files**, against 224 non-test Go files
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

1. ~~**Module path**~~ — Resolved: `github.com/MochaCosine1206/orchestra` in this published
   snapshot. The private working repository this was extracted from uses a different module path.
2. ~~**Minimum Go version**~~ — Resolved: `go 1.25.0` in `go.mod`.
3. ~~**Bubble Tea v1 vs v2**~~ — Resolved: v1 (stable). See TUI section above.
4. **When to split repos** — Still open. `cmd/orchestra/` lives in this repository; a split has
   not been needed.
5. **Packaging** — Still open, and the reason Distribution above describes intent rather than
   reality. Requires creating the tap repository and cutting a first tag.
