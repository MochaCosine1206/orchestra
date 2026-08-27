---
description: "Run Claude Orchestra: decompose goals, spawn agents, check status, merge branches, audit code. Use when user mentions orchestra, agents, decompose, or multi-agent tasks."
argument-hint: <subcommand> [args] — subcommands: help, go, auto, status, merge, audit, tokens, ab-test
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

| Subcommand | Usage | Description |
|---|---|---|
| `help` | `/orchestra help` | Show this reference table |
| `go` | `/orchestra go [--goal "..."] [--review] [--test-cmd CMD] [--max-parallel N] [--dry-run]` | Run full workflow. With `--goal`, auto-decomposes first. `--review` adds reviewer gate |
| `auto` | `/orchestra auto [--goal "..."] [--sources audit,gaps,markers] [--max-cycles N] [--max-parallel N] [--dry-run]` | Autonomous loop: finds work, executes, merges, repeats |
| `auto --pause` | `/orchestra auto --pause` | Pause a running autonomous session |
| `auto --resume` | `/orchestra auto --resume` | Resume a paused autonomous session |
| `auto --stop` | `/orchestra auto --stop` | Stop autonomous mode gracefully |
| `decompose` | `/orchestra decompose --goal "describe your task"` | Break a goal into parallel agent tasks |
| `status` | `/orchestra status [--json]` | Dashboard: tasks, agents, tokens, conductor state |
| `merge` | `/orchestra merge [--test-cmd CMD] [--dry-run]` | Merge completed task branches in dependency order |
| `tokens` | `/orchestra tokens` | Aggregate token usage report by role |
| `audit` | `/orchestra audit [--scope all\|tests\|code\|gaps] [--dry-run]` | Bug bot: scan codebase for issues |
| `ab-test` | `/orchestra ab-test --goal "..." --test-cmd CMD [--runs N] [--routing per-role] [--dry-run]` | A/B test: multi-agent vs single-agent |

## Routing Rules

1. **Empty argument or `help`**: Display the subcommand reference table above. Do NOT run any command.
2. **Known subcommand** (`go`, `auto`, `decompose`, `status`, `merge`, `tokens`, `audit`, `ab-test`): Execute via Bash:
   ```bash
   orchestra <subcommand> <args>
   ```
3. **`--version`**: Run `orchestra version`
4. **Natural language** (e.g., "run 3 agents on auth feature"): Map to the closest subcommand. If the user describes a goal, use `go --goal "..."`.
5. **Unknown input**: Show the reference table and suggest the closest match.

## Working Directory

Always run from the project root. The `$ARGUMENTS` variable contains everything after `/orchestra`.

```bash
orchestra $ARGUMENTS
```

## Error Handling

- If `orchestra` exits with database errors, run `orchestra init` then retry.
- If no tasks exist and the user runs `go`, suggest `decompose --goal "..."` first, or use `go --goal "..."` to combine both steps.

## Examples

- `/orchestra go --goal "Add user authentication"` — decompose + execute in one step
- `/orchestra go --goal "Add user authentication" --review` — decompose + review gate + execute
- `/orchestra auto --goal "Refactor API" --sources audit --max-cycles 3` — autonomous: goal + audit sweep
- `/orchestra auto --sources audit,gaps,markers` — autonomous: find work from audit, gaps, and code markers
- `/orchestra auto --pause` — pause a running autonomous session
- `/orchestra auto --resume` — resume a paused session
- `/orchestra auto --stop` — stop autonomous mode gracefully
- `/orchestra status` — show current task dashboard
- `/orchestra status --json` — machine-readable status
- `/orchestra merge --test-cmd "npm test"` — merge with test gate
- `/orchestra audit --scope tests --dry-run` — preview test issues
- `/orchestra tokens` — see token usage breakdown
