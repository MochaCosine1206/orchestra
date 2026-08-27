# Installing Orchestra via Homebrew

## Prerequisites

- macOS or Linux with [Homebrew](https://brew.sh) installed
- Run `brew --version` to verify

## Install

```bash
# Add the Plyne Technologies tap
brew tap plyne-technologies/tap

# Install orchestra
brew install orchestra
```

## Verify Installation

```bash
# Check the installed version
orchestra version

# Run diagnostics to confirm everything is working
orchestra doctor
```

Both commands should complete without errors. `orchestra doctor` checks for
required dependencies (git, Claude CLI, SQLite) and reports any issues.

## Upgrade

```bash
# Update Homebrew and all taps
brew update

# Upgrade orchestra to the latest version
brew upgrade orchestra
```

To check what version you're currently running vs. what's available:

```bash
brew outdated orchestra
```

## Uninstall

```bash
brew uninstall orchestra

# Optionally remove the tap
brew untap plyne-technologies/tap
```

## How Formula Updates Work

The orchestra formula is automatically updated via [GoReleaser](https://goreleaser.com/).
When a new version tag (e.g., `v0.5.0`) is pushed to the Claude-Orchestra repository:

1. GoReleaser builds binaries for macOS (amd64, arm64) and Linux (amd64, arm64)
2. GoReleaser generates an updated Homebrew formula with the correct SHA256 checksums
3. The formula is pushed to the `Plyne-Technologies/homebrew-tap` repository
4. `brew update` picks up the new formula on your next run

No manual intervention is required — tag a release and Homebrew users get it
automatically.

## Troubleshooting

### `brew tap` fails with permission error

Make sure your GitHub credentials are working:

```bash
gh auth status
```

The tap is a public repository, so no special access is required. If you're
behind a corporate proxy, configure Homebrew's proxy settings:

```bash
export ALL_PROXY=http://proxy.example.com:8080
brew tap plyne-technologies/tap
```

### `brew install orchestra` shows "formula not found"

The formula may not have been published yet. Check:

```bash
brew tap-info plyne-technologies/tap
ls "$(brew --repo plyne-technologies/tap)/Formula/"
```

If `Formula/` is empty, no release has been tagged yet in the orchestra repo.

### `orchestra version` shows unexpected version

Force a fresh install:

```bash
brew uninstall orchestra
brew install orchestra
orchestra version
```

### `orchestra doctor` reports missing dependencies

`orchestra doctor` checks for:

- **git** — install via `brew install git` or Xcode Command Line Tools
- **claude** — Claude CLI, see [Claude Code docs](https://docs.anthropic.com/en/docs/claude-code)
- **sqlite3** — usually pre-installed on macOS; `brew install sqlite` on Linux

### Build from source (alternative)

If the Homebrew formula doesn't work for your platform:

```bash
git clone https://github.com/Plyne-Technologies/Claude-Orchestra.git
cd Claude-Orchestra
make build
make install-dev
```

This builds the `orchestra` binary and installs it to your `$GOPATH/bin`.
