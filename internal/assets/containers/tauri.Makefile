# tauri.Makefile — Dark Factory quality gate for Tauri 2 projects
#
# Copy this file to the project root as `Makefile` and adjust paths as needed.
#
# Targets:
#   build   — compile Rust backend (debug profile)
#   test    — Rust unit tests + Vitest frontend tests
#   lint    — Clippy (errors only) + Biome check
#   gate    — lint → test → build (the full CI gate, in order)
#   clean   — remove Cargo build artifacts
#
# The Tauri GUI binary cannot be launched inside the container (no display
# server). These targets cover everything that CAN run headlessly.

.PHONY: build test lint gate clean

# ── build ─────────────────────────────────────────────────────────────────────
# Compiles the Tauri/Rust backend. Uses debug profile for speed in CI.
# Change to `cargo build --release` if you need the production binary.
build:
	cd src-tauri && cargo build

# ── test ──────────────────────────────────────────────────────────────────────
# 1. Rust unit + integration tests (cargo test runs in src-tauri/)
# 2. Frontend unit tests via Vitest (run from project root where package.json lives)
test:
	cd src-tauri && cargo test
	npx vitest run

# ── lint ──────────────────────────────────────────────────────────────────────
# 1. Clippy with -D warnings — any warning is a build error.
# 2. Biome check — lint + format check for frontend (JS/TS/JSON).
#    Add `--write` to auto-fix; omit here to keep lint non-destructive.
lint:
	cd src-tauri && cargo clippy -- -D warnings
	npx biome check .

# ── gate ──────────────────────────────────────────────────────────────────────
# Full quality gate run in order. Fail fast: if lint fails, test and build
# are skipped. This is the default CMD in tauri.Dockerfile.
gate: lint test build

# ── clean ─────────────────────────────────────────────────────────────────────
# Remove Cargo's target/ directory. Does not touch node_modules or dist/.
clean:
	cd src-tauri && cargo clean
