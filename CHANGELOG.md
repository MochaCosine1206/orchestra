# Changelog

All notable changes to Claude Orchestra are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

> **No version has been tagged.** There are no git tags and no releases; `0.1.0` below was a
> milestone marker, not a published artifact. Everything since sits under Unreleased.
> Identifiers such as `B-289`, `G136` and `L-026` are internal ticket references and have no
> public tracker.

## [Unreleased]

### Added
- Dashboard test suite expanded from 81.4% to 85.4% coverage — 40+ new tests covering HITL failure triage (skip/replan/invalid), merge reject, clarify/priority/plan edge cases, extractFilesOwned helper, git handler latest resolution, project actions (stop/replan/retry/skip with task IDs), factory pause-all with conductors, nav-status with data, config theme persistence, ExecuteControl retry/skip with tasks, briefing data with cookies
- B-289 Phase 2: Self-correcting spec generation loop — `GenerateSpecWithValidation()` wraps spec generation + plan validation in a correction loop. On validation failure, gaps are fed back into the prompt and regenerated. Circuit breaker at 3 iterations, returns best result by coverage score. Fail-open on validation errors. `PriorGaps`/`PriorCoveredSummary` fields on `GenerateSpecOpts`. `generate-spec` and `new` commands now use the correction loop automatically. 4 new tests
- Enhanced `/orchestra-guide` skill — full command reference with all flags, interactive workflow advisor, plan-first project creation pattern. Claude now acts as UX layer over Orchestra CLI
- `--include` flag for `orchestra new` — copy existing files/directories into a new project before spec generation. Enables content project iteration: `orchestra new --idea "..." --include ./research`. Repeatable for multiple paths. 5 new tests
- Content-aware spec generation — `GenerateSpec()` now produces correct specs for content projects (books, textbooks, courses) in addition to code projects. Prompt rewritten with universal + code-specific + content-specific rule sets; LLM picks the right rules based on the idea text. JSON schema role enum expanded to include editor + illustrator (7 roles). Dual example specs loaded (code: billing-api, content: dev-psychology-book). `@file` expansion added to spec generation via `expandFileReferences()`. Validator error message updated to list all 7 roles. Content example spec (`content-book-spec.yaml`) embedded via `go:embed`. 8 new tests
- I-061: Structured CLI error handling — `OrchestraError` type with 8 exit codes (0=success, 2=usage, 3=database, 4=git, 5=partial, 6=total, 7=preflight), human-readable and JSON formatters, heuristic recovery suggestions, per-task failure details. `--json` flag added to `go`, `exec`, `auto`, and `merge` commands. `conductor-run` (default detached path) now uses the same rich error infrastructure. Fixes silent exit 0 on merge failures (merge.go bug). 11 new tests
- Editor subagent role — content quality editing with developmental, consistency, and polish passes. New embedded agent definition (`internal/assets/agents/editor.md`), permission profile (`internal/assets/profiles/editor.json`), `validRoles` updated to 6 roles
- G119: Global config package (`internal/config/`) — XDG-compliant `~/.config/orchestra/config.json` with `ORCHESTRA_CONFIG` env override. `Load()`/`Save()`/`Path()` API. `orchestra init` onboarding asks for default project directory, persists to global config. `orchestra new` reads `default_project_dir` when `--parent-dir` not explicitly set. 8 new tests (6 config, 2 new_cmd integration)

### Fixed
- B-289: Spec generation no longer silently drops phases from planning documents. `extractPhaseHints()` parses Branch/Phase/Step headings and injects them as a `## REQUIRED PHASES` section in the LLM prompt. New rule 25 enforces phase coverage. `--fail-closed` flag on `generate-spec` returns error if critical gaps remain after all iterations
- B-288: Content phase gates no longer auto-pass silently. `PhaseGate.Mode` field (`test`/`acceptance`/`none`, default auto-detect) added. When `test_cmd` is empty but acceptance criteria exist, `checkAcceptanceHeuristics()` evaluates them via file-count and glob-pattern checks. Unevaluable criteria auto-pass with a warning
- B-287: Staging branch now always merged to dev after every phase (not just final). `--start-phase` forks from dev directly since it has all prior work. Conductor deactivation no longer skipped for non-final phases. DB staging branch lookup kept as safety fallback
- B-286: `orchestra new --idea "@file"` now resolves @file references relative to the invoking CWD, not the new project directory. Pre-expands via `ExpandFileReferencesUnlimited()` (new exported wrapper in goalexpand.go) before passing to GenerateSpec. Previously, `@notes/plan.md` would fail because `notes/` doesn't exist in the freshly-created project
- G136 follow-up: Gate retry no longer re-decomposes the goal. `RetryFailedTasks` now cleans up stale worktrees/branches and spawns directly (monitor + batch spawn + wait + merge), preventing duplicate tasks and "branch already exists" errors that blocked long-running content pipelines. Defense-in-depth cleanup added to `spawner.Run()` as a safety net
- G139: Rate-limited agents (`is_error:true` + `subtype:"success"`) no longer misclassified as validation failures. `CheckLogResult` respects `is_error:true`, `DetectRateLimitEvent` extracts exact `resetsAt` epoch from JSONL, global cooldown prevents N agents from each hitting the limit. 8 new tests
- G138: `correctDecomposerFilePaths()` now runs BEFORE `additional_files` merge — prevents edit reports (e.g., `ch01-dev-edit.md`) from being incorrectly mapped to chapter files by ordinal matcher. Root cause of an editor gate failure during a long content run
- G131 (B-283): Classifier now matches "Prompt is too long" and `prompt_too_long` JSONL error type as `context_exhausted` — previously misclassified as `normal_failure`, bypassing re-decomposition recovery
- G132 (B-284): Removed "late" progress guard from context exhaustion re-decomposition — agents with 3+ commits were incorrectly denied re-decomposition, falling back to respawn-and-fail-again

### Changed
- L-026: Hierarchical+FIFO now auto-enabled at 3+ tasks (was opt-in via `--hierarchical`). A/B experiment (B-282, 15 runs): hierarchical is faster (186s vs 240s), zero merge conflicts, higher pass rate (75% vs 57%). Falls back to flat if clustering degenerates

### Added (Track A: Merge Safety — M6.5)
- A.2 (B-280): FIFO merge queue — `merge_queue_entries` table (migration 006), `MergeQueueEntry` model, queue processor (`mergequeue.go`), `--merge-strategy fifo` CLI flag, auto-enable at 3+ tasks, monitor FIFO resume, toposort-based position assignment, refinement requeuing with circuit breaker (max 2)
- A.3 (B-281): Hierarchical decomposition — `feature_cluster` column on tasks (migration 007), LLM-driven clustering in decompose prompt, two-level merge (task→cluster branch→staging→dev), cluster branch eager creation, spawner cluster branch awareness, `--hierarchical` CLI flag, fallback to flat when clustering degenerate
- A.4 (B-282): A/B experiment scripts — `run-hierarchical-experiment.sh` (flat vs hierarchical arms), `verify-hierarchical.sh`, `analyze-hierarchical.sh` with CSV output

### Added (Track E: Phase Pipeline Gap Fixes)
- E.1 (G110): Phase-scoped merge filtering — `phase_id` column on tasks table, `ListTaskIDsByPhase()` query, `Merge()` filters by phase when `PhaseID` set, `merge-base --is-ancestor` defense skips already-merged branches. DB migration 005
- E.2 (G111): Cross-phase worktree fork point — `StagingBranch` in `GoResult`, rolling base in `GoSpec()` loop, `KeepStaging` flag preserves staging between phases, only final phase merges to dev
- E.3 (G112): Agent verification enforcement — `runBuildAndTest()` in validator.go runs `go build` + `go test` in agent worktree post-completion, JSONL trace audit for observability

### Added
- B-277: `orchestra new` command — greenfield project creation from an idea. Creates project directory, `git init`, `orchestra init` (full scaffolding), generates phased YAML spec via architect agent, prints summary. Optional `--execute` flag runs the spec immediately. Flags: `--idea` (required), `--name` (default: derived from idea via `projectNameFromIdea`), `--tech-stack`, `--constraints`, `--parent-dir`, `--execute`. Testable `initNewProject()` with dependency injection for git operations. 12 tests (7 name derivation + 5 integration)
- B-278: `orchestra generate-spec` command — LLM-based spec generation from a high-level idea. `GenerateSpec()` on Conductor calls Claude with JSON schema to produce an `OrchestraSpec`, validates it, writes YAML. Prompt includes idea, optional tech stack, repo context, constraints, and embedded example spec. CLI flags: `--idea` (required), `--output`/`-o` (default "spec.yaml"), `--tech-stack` ("k=v,k=v" format), `--constraints` (comma-separated), `--repo-context`. Added `json` struct tags to all 5 spec types for LLM JSON→YAML round-tripping. 8 tests
- B-276 + B-279: `orchestra exec` command — Cobra CLI wrapper around `GoSpec()` engine. Flags: `--spec` (required YAML path), `--start-phase` (resume from phase ID), `--dry-run` (print execution plan), `--max-parallel`, `--interval`, `--review`, `--reconcile`, `--repo-map`, `--base-branch`, `--runtime`, `--merge-mode`. Dry-run prints phases in topological order with task counts, role breakdown, gate commands, and dependency info. B-279 phase resume wired through `--start-phase` flag to `GoSpecOpts.StartPhase`. 5 tests
- B-275: Phase execution engine — `GoSpec()` loops through phases in topological order, calls `Go()` per phase via `GoRunner` interface, gates advancement on test commands. `GoSpecOpts` for global defaults (dry run, start phase resume, max parallel, merge mode). `GoSpecResult` aggregates done/failed counts across phases. `buildPhaseGoOpts()` converts phase definitions to `GoOpts` with defense auto-enabled at 3+ tasks, reconcile only on final phase. `checkPhaseGate()` reuses `runTestCmd()`. `ConductorGoSpec()` convenience method with Telegram notifications. 16 tests
- B-274: YAML spec format, parser, and validator for multi-phase orchestration — `OrchestraSpec` schema with phases, tasks, gates, and metadata. `ParseSpec()` reads YAML, `ValidateSpec()` enforces 8 rules (schema completeness, ID uniqueness, DAG acyclic, file exclusivity, cross-phase overlap warnings, valid roles, ID format, dangling depends_on). `TopoSortPhases()` (Kahn's algorithm) and `PhaseGoOpts()` helper for B-275. 22 tests, 3-phase billing API example spec embedded via `go:embed`. New dependency: `gopkg.in/yaml.v3`
- B-273: PR-based merge mode (`--merge-mode pr`) — creates a GitHub PR from the staging branch instead of local `update-ref` + `reset --hard`. Includes `gh` CLI preflight validation, monitor auto-merge/recovery PR routing, conditional staging branch preservation, and SQLite migration 004. 15 new tests across merge, mutations, and monitor packages
- B-145: Merge state transactions for crash recovery — three new columns on conductors table (`merge_status`, `merge_started_at`, `merge_branches_done`) track merge progress through NULL→merging→staging→done state machine. Monitor's `recoverMerge()` resumes from correct phase after conductor crash; `SkipBranches` avoids re-running test gates for already-verified branches. Migration 003, 3 new DB mutations, instrumented `Merge()` and `MergeStagingToDev()`
- B-241: Session auto-restart via SessionEnd hook with `matcher: "other"` — fires only on non-user-initiated exits (limits, crashes, API errors). Replaces broken regex-on-last_assistant_message detection. Circuit breaker (3 restarts/hour), 30s cooldown, Telegram notification, `exec claude --continue`. New file: `scripts/hooks/session-end-restart.sh`
- Exported `ProjectSlugFromPath`, `LoadTelegramCreds`, `SendTelegramMessage`, `ReadTopicID` from `internal/orchestrator/notify.go` for cross-package reuse

### Fixed
- G123: Phase gate test used as per-task merge test — `buildPhaseGoOpts()` passed `config.TestCmd` (phase gate) to `GoOpts.TestCmd`, causing collective-outcome gates (e.g., "all 11 files exist") to run on each individual task merge. Fix: set `TestCmd: ""` in `buildPhaseGoOpts()`; phase gate runs post-`Go()` via `checkPhaseGate()` only
- G115: Validator `go build ./...` fails in experiment worktrees — `runBuildAndTest()` now accepts `testCmdOverride` from `conductor:test_cmd` blackboard key; when set, uses that command instead of default `go build ./...` + `go test ./...`
- G116: Experiment script missing `mkdir -p` for `bin/` and `.orchestra/` dirs in clone
- G117: Merge queue refinement re-enqueue — `enqueueCompletedTasks()` now transitions "refining" entries back to "pending" when their task completes again
- G118: `isClusterComplete()` treated failed tasks as complete — now requires all tasks to be "done" with merged queue entries; failed tasks make cluster incomplete
- G114: GoSpec gate tests run in target project dir, not process CWD — `checkPhaseGate()` resolves correct working directory
- G98: session-end-restart.sh now detects active Claude sessions via `lsof -a -d cwd -c claude` before triggering restart — prevents false restarts when session is replaced rather than crashed
- L-011: Coverage check no longer counts read-only files — `filterPathList` helper excludes them from goal file coverage, eliminating false "67% coverage" warnings
- G97: Cascade failure race with refinement — `ResetCascadeFailedDependents` reverses cascade-failed dependents when blocker enters refinement; monitor defers cascade when blocker still has refinement rounds
- G96: Telegram Push It button gated on `conductor:merge_complete` blackboard marker — buttons don't appear until merge completes; push handler blocks during merge
- L4: `detectBaseBranch()` now checks `origin/<branch>` remote refs when local refs don't exist, creating local tracking branches on the fly — fixes detached-HEAD clones (SWE-bench external repos)
- L2: `RespawnForRefinement()` fallback changed from hardcoded `"dev"` to `DetectBaseBranch(s.RepoRoot)` — refinement resets now work on any repo
- L3: `phase2AutoSpawn()` detects "already assigned" errors and clears stale assignments via new `ClearTaskAssignment()` — tasks auto-recover next cycle instead of permanent stuck loop
- Review prompt templates now inject actual base branch from blackboard instead of hardcoding "dev"
- B-271: stop-notify.sh and telegram-check.sh skip notifications during benchmark runs (`ORCHESTRA_BENCHMARK=1`); mid-session summary dedup guard prevents duplicate Telegram sends

### Changed
- MaxParallel default raised 5→8 — B-156 stress tests validated 8 concurrent writers with zero SQLITE_BUSY; aligns with MaxTasks default (8)

### Fixed
- Dashboard conductor polling: `NewWithConductor()` created a channel that disabled DB event polling, making external conductor sessions (`orchestra go`) invisible in standalone dashboard. Now always polls DB events

### Added
- 5-agent parallel codebase audit via `orchestra go` — capabilities, roadmap, tech debt, doc alignment, test coverage (1,730 lines of reports in research/audits/)
- 4-agent audit fix batch via `orchestra go` — doc staleness fixes, model ID constants, eval→bash -c in adapters, research index catch-up
- Hash-based Telegram summary dedup — prevents re-sending stale summaries on compaction restarts
- Centralized model ID constants (`ModelOpus`, `ModelSonnet`) in agent/config.go — replaced 13+ hardcoded strings

### Fixed
- TestDecomposeMaxTasksDefault expected "3" but B-236 changed default to 8
- TestDecompose_DuplicateFileWarning expected first-task-wins but code correctly uses highest-priority-wins
- Unsafe `eval "$test_cmd"` replaced with `bash -c "$test_cmd"` in all 3 adapter scripts
- Doc staleness: SPEC.md process manager, model tiers, cost section; README.md resolved known issues; counts updated across 4 docs
- File permissions stripped by salvage mechanism restored (54 scripts, G93)

### Research
- research/gaps/012-claude-p-timeout-structured-output-limits.md — `claude -p` has no process timeout, JSON schema accepts invalid keywords
- G93: Salvage strips file execute permissions
- G94: Conductor log overwrites between sessions

- B-240: Environment-aware capability features — `probeTestEnvironment()` detects pytest/go/jest availability before test gate (exit codes 2-5 = broken env); `TestFailureMode` enum (revert_and_refine/warn_only/revert_no_refine); test command timeout via `context.WithTimeout`; `--disable-action-expansion` for pre-specified goals; `--read-only-files` excludes test files from task decomposition; SWE-EVO adapter returns real test commands + `orchestra-flags` command; 11 new tests
- B-243: Telegram hook reliability — stop hook: `set -e` removed (silent death on parse errors), Markdown→HTML parse_mode (special chars no longer kill notifications), staleness check for stop_hook_active; mid-session hook: pending reply delivery sends Claude's summary back to Telegram, hint in block reason tells Claude to write summary
- B-236: MaxFilesPerTask cap (default 15) + dynamic MaxTasks (3→8) — `enforceFileCap()` mechanically splits oversized tasks by directory proximity; `computeEffectiveMaxTasks()` sets dynamic floor `ceil(goalFiles/cap)`; prompt scaling for large goals; iterative rounds capped at 5 independently; `--max-files-per-task` CLI flag on go/decompose/conductor-run; 16 new tests
- B-236 verification: tier 50 pressure test PASS — 50/50 files correct (was 82-86%), 8 tasks (max 10 files each), 2 self-corrected via refinement
- Stop-notify dedup guard — 10-second window prevents duplicate Telegram notifications
- Stop-notify now shows filenames (up to 5 basenames) instead of generic "N files modified"
- B-216: SWE-EVO external benchmark — 5/5 PASS subset (100%) across 5 repos (dask, dvc, requests, pydantic, scikit-learn), 97.5% file coverage (39/40), 0 merge conflicts; 6 infrastructure learnings (doc 115); full 48-task run pending
- SWE-EVO adapter and download script (`scripts/adapters/swe-evo-adapter.sh`, `scripts/download-swe-evo.sh`) — 48 evolution tasks, avg 21 files/task, 7 Python repos, real pytest verification
- Adapters now accept file paths (not just inline JSON) — required for SWE-EVO patches up to 179K chars
- Benchmark harness improvements: `merge-test-cmd` adapter command (skip merge-time tests for external benchmarks), `apply-test-patch` (pre-orchestra test injection), test name path stripping (avoid action expansion), `--max-files-per-task` pass-through
- Claude Code hooks for trace-before-assume discipline — Stop hook (prompt/haiku) blocks stopping with unresolved failures, PostToolUse hook (command) detects test/build failures and injects trace→fix→verify feedback
- Gaps G84-G85: file ownership enforcement DB path bug, refinement revert bug
- Pressure test extended to tier 50 — goal generator now supports tiers 35, 40, 45, 50 (20 new files: completion, config, process, connection, helpers, go_cmd, spawn_cmd, status, decompose_cmd, merge_cmd, audit, runner, toposort, goalpreprocess, helpers(orch), model, keybindings, agents_panel, log_panel, logstream/parser)
- Gap retest tracker (doc 110) for systematic fix verification
- B-215, B-216, B-217 backlog items from doc 105 next steps
- B-213: Ceiling exploration through tier 30 — 100% pass rate at tiers 14, 16, 30; 67% at tier 25 (outlier); perfect 30/30 run; no ceiling found (doc 105)
- B-212: External benchmark validation — 9/9 SWE-bench Verified tasks PASS (100%) across 6 Python repos: astropy, django, matplotlib, pylint, sphinx, sympy (doc 109)
- `scripts/run-external-benchmark.sh` — external benchmark runner with adapter system
- `scripts/adapters/swe-bench-adapter.sh` — SWE-bench task-to-Orchestra goal converter
- `scripts/run-pressure-test.sh` — non-deterministic progressive pressure testing (tiers 4-30)
- `scripts/generate-pressure-goal.sh` — randomized goal generation for pressure tests
- `scripts/download-swe-bench.sh` — SWE-bench dataset download and filtering
- B-205: Post-decomposition file coverage validation — extract goal files, resolve paths, inject as prompt hint
- B-206: Mandatory acceptance criteria per task — 2-5 concrete checkboxes, vague AC detection
- B-207: Goal specificity improvements — `<default_to_action>` prompt, concrete decomposer instructions
- Post-decomposition file exclusivity check — detect and reassign duplicate file assignments
- Defense mode auto-enable at 3+ tasks — auto-enables file watcher + conflict resolution
- Gaps G77-G82: scaling and external benchmark gaps (merge resolution, re-decomposition cascade, task count sensitivity, coverage gaps, TUI misses)
- Docs 105-109: pressure test baseline, experiment methodology, orchestration paper, SWE-bench access guide, external benchmark results
- B-192: File ownership enforcement — wire decompose-time file locks, prompt exclusivity rule, merge-time lock release, 5 tests
- B-189: Agent cascade routing (`--cascade` flag) — complexity estimation + 3-tier routing (single→iterative→multi-agent), 8 tests
- B-164: Eight-file ceiling A/B test (doc 084) — 3-arm, 9-run experiment confirming structural ceiling (*superseded by B-213: ceiling broken*)
- Spec-anchored review mode (`ReviewModeSpecDiff`) — anchors code review to task spec acceptance criteria
- Structured JSON review mode (`ReviewModeStructured`) — typed `ReviewFinding` with severity/confidence/file/category
- A/B test flags: `--review-test`, `--structured-review`, `--cascade` on `orchestra ab-test`
- `--cascade` flag on `orchestra go` for cascade routing
- `--max-tasks` flag on `orchestra go` (was only on `orchestra decompose`)
- `scripts/verify-benchmark.sh` — automated 8-file benchmark verification
- B-160: Failure cascade test with lenient deps (7 tests, 9 with subtests)
- B-165: Pre-push hook for documentation staleness detection + this changelog
- B-159: Memory profiling + goroutine leak detection (17 tests)
- B-158: Chaos test — random agent kills (9 tests)
- B-163: Complexity ceiling diagnosis (5 tests)
- B-157: Long-running session stress test (4 tests)
- B-155: 8-agent concurrent stress test (3 tests)
- B-173: GetStatusSummary query optimization (-46% P99 at 8 writers)
- B-154: 5-agent concurrent stress test (4 tests) + ClaimTask atomic CAS fix
- B-156: WAL contention under load (7 tests)
- B-149: Post-session reconciliation system (salvage, scoring, gap detection)
- Lenient dependency cascade mode for mixed-outcome blockers

### Changed
- BACKLOG.md: corrected test counts (B-156: 7 not 6, B-159: 17 not 18)
- Total test count: 935 across all packages

### Fixed
- G84: File ownership hook queried wrong DB path (`orchestrator.db` instead of `.orchestra/orchestrator.db`) — enforcement was completely non-functional; agents could modify any file regardless of ownership
- G85: RespawnForRefinement didn't revert bad commits — second agent inherited contaminated branch history; now resets to fork point before relaunching
- G77/G83: CLAUDECODE nesting guard kills resumed/relaunched agents — added `filterEnv` to `Launch()` and `Resume()` in spawner.go (Run() already had it); added `unset CLAUDECODE` to pressure test script
- SWE-bench adapter: `python` → `python3` for macOS compatibility (G82)
- `orchestra reset` now kills all agent PIDs (not just conductor) and restores git state if behind upstream
- Agents overwriting `.claude/settings.json` on merge — spawner now writes `.claude/.gitignore` instead of copying profile as settings.json
- Reverted agent benchmark contamination — Run 2 Arm A agent escaped worktree isolation and committed `priority_label` changes directly to dev (6f24e6f)
- Pre-push hook false positives — improved to only flag docs relevant to the specific code areas changed
- AssignTask race condition under concurrent access (replaced with ClaimTask WHERE status='pending')
- GetStatusSummary bottleneck: combined 4 queries into 1 UNION ALL with indexes

---

## [0.1.0] - 2026-02-12

### Added
- Go binary (`orchestra`) with Cobra CLI and Bubble Tea TUI dashboard
- 16 subcommands: go, auto, decompose, merge, audit, spawn, monitor, reconcile, recover, reset, etc.
- SQLite coordination layer with WAL mode (ncruces/go-sqlite3, pure Go/WASM)
- Git worktree isolation for parallel agent execution
- 5 agent roles: architect, implementer, reviewer, scout, researcher
- Stream-json log parser with colored TUI rendering
- Goal clarification system (auto/CLI/TUI modes)
- Iterative session-cycling mode for 4-7 file tasks
- Autonomous mode with circuit breakers (2 empty, 3 all-fail, 6hr timeout)
- Re-decomposition on context exhaustion
- Post-failure refinement loop with reviewer critique injection
- Node.js TCP server removed — Go binary handles all DB access
- Permission profiles for headless agent spawning (dontAsk mode)

### Removed
- Legacy bash scripts (13,870 lines) — Go binary is sole interface
- Cost tracking (Claude Max = unlimited usage)
- Node.js DB server (replaced by Go's ncruces/go-sqlite3)
