# Packaging

**Nothing is published.** There are no git tags, no GitHub releases, and no Homebrew tap.
GoReleaser has never been run. This document describes what exists and what would be required
to change that — it is not an install guide.

To install today, build from source:

```bash
git clone https://github.com/MochaCosine1206/orchestra.git
cd orchestra
make build          # binary at ./bin/orchestra
```

## What exists

| Artifact | State |
|----------|-------|
| `.goreleaser.yaml` | Written. Configured to cross-compile macOS arm64/amd64 and Linux amd64/arm64, and to publish a Homebrew formula |
| `scaffold-homebrew-tap.sh` | Written. Creates the tap repository structure |
| `orchestra release` | Implemented. Runs release gate checks and tags a version (`--dry-run` supported) |
| Tap repository | **Does not exist** |
| Git tags | **None** |
| GitHub releases | **None** |

## What publishing would require

1. Create the tap repository the `.goreleaser.yaml` `brews.repository` block points at.
2. Provide GoReleaser a token with write access to it.
3. Cut a first tag (`orchestra release --dry-run` first — the gate checks are the point).
4. Run GoReleaser, which builds the binaries, creates the GitHub release, and pushes the formula.

`.goreleaser.yaml`, `scaffold-homebrew-tap.sh` and `scripts/test-homebrew-tap.sh` all point at
this repository (`MochaCosine1206/orchestra`) and at a `MochaCosine1206/homebrew-tap` that has
not been created.

## Binary size

~33.5 MB: the Go binary plus embedded SQLite WASM, embedded agent definitions and templates,
and the dashboard's static assets. An earlier target of 12–15 MB in `GO-SPEC.md` was never met.
