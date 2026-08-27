---
paths:
  - cmd/**
  - internal/**
  - "*.go"
  - go.mod
  - go.sum
  - Makefile
---

# Go Conventions

## Project Structure
- All packages under `internal/` — no `pkg/` directory
- Entry point: `cmd/orchestra/main.go` (thin, delegates to `internal/cmd`)
- Package names match directory names, lowercase, no underscores

## Cobra CLI Patterns
- Factory functions returning `*cobra.Command` via `New*Cmd()`
- Always `RunE` (never `Run`) — propagate errors to caller
- Use `cmd.Println` / `cmd.PrintErrln` (never `fmt.Println`) — respects `SetOut`/`SetErr`
- Required flags via `cmd.MarkFlagRequired()`, not manual validation
- Persistent flags on root for shared options (`--db`, `--verbose`)

## Error Handling
- Sentinel errors: `var Err* = errors.New("...")` at package level
- Wrap with context: `fmt.Errorf("opening database: %w", err)`
- Never swallow errors — return them or log explicitly
- Use `errors.Is()` / `errors.As()` for comparison

## SQLite (ncruces/go-sqlite3)
- WAL mode + `busy_timeout=5000` + `foreign_keys=ON` on every connection
- Read-only connections: `?mode=ro` — unlimited pool
- Write connections: `SetMaxOpenConns(1)` — single writer
- `context.Context` on all query functions
- Parameterized queries only — never interpolate values into SQL strings

## Testing
- Table-driven tests with `t.Run()` subtests
- In-memory SQLite (`:memory:`) for unit tests
- `testdata/` directories for golden files
- `t.Helper()` on test helper functions
- `t.Parallel()` where tests are independent

## Logging
- `log/slog` for structured logging
- Pass `*slog.Logger` explicitly (no globals)

## TUI (Bubble Tea)
- Plugin broadcast pattern: all panels receive all `tea.Msg`
- Height constraint: panels must not exceed allocated height
- Focus management: only focused panel handles `KeyMsg`
- `tea.Tick` for periodic updates (2s SQLite poll)
- Non-terminal detection: `term.IsTerminal()` check before alt-screen

## Releasing
- Bump `LatestVersion` in `internal/version/version.go` to match the new tag
- Tag: `git tag vX.Y.Z && git push --tags`
- Users upgrade: `go install github.com/MochaCosine1206/orchestra/cmd/orchestra@latest`
