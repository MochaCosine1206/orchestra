#!/usr/bin/env bash
# test-homebrew-tap.sh — Verify Homebrew tap and formula installation locally.
# Builds a snapshot, generates a test formula, installs it, verifies, and cleans up.
# Idempotent and safe to run repeatedly.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FORMULA_PATH="$REPO_ROOT/test-formula.rb"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

cleanup() {
  echo ""
  echo "--- Cleanup ---"
  brew uninstall orchestra 2>/dev/null && echo "  Uninstalled orchestra" || true
  rm -f "$FORMULA_PATH" && echo "  Removed test-formula.rb" || true
}

echo "=== Homebrew tap verification ==="
echo ""

# 1. Check brew is installed
echo "--- Prerequisites ---"
if ! command -v brew &>/dev/null; then
  echo "  SKIP: brew not installed — cannot run Homebrew tests"
  exit 0
fi
pass "brew is installed ($(brew --version | head -1))"

# 2. Check goreleaser
if ! command -v goreleaser &>/dev/null; then
  echo "  SKIP: goreleaser not installed — needed for snapshot build"
  echo "  Install: brew install goreleaser"
  exit 0
fi
pass "goreleaser is installed"

# 3. Check tap repo exists
echo ""
echo "--- Tap repository ---"
if command -v gh &>/dev/null; then
  if gh repo view Plyne-Technologies/homebrew-tap &>/dev/null; then
    pass "Plyne-Technologies/homebrew-tap exists on GitHub"
  else
    fail "Plyne-Technologies/homebrew-tap not found (or gh not authenticated)"
  fi
else
  echo "  SKIP: gh CLI not installed — cannot verify tap repo"
fi

# 4. Build snapshot
echo ""
echo "--- Snapshot build ---"
(cd "$REPO_ROOT" && goreleaser build --snapshot --clean)

# Find the binary for the current platform
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)
BINARY=$(find "$REPO_ROOT/dist" -path "*${GOOS}_${GOARCH}*/orchestra" -type f 2>/dev/null | head -1)

if [[ -z "$BINARY" ]]; then
  echo "  FAIL: No snapshot binary found for ${GOOS}/${GOARCH}"
  echo "  Available:"
  ls -la "$REPO_ROOT/dist/"
  exit 1
fi
BINARY=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")
pass "snapshot binary built: $BINARY"

# 5. Generate test formula
echo ""
echo "--- Test formula ---"
cat > "$FORMULA_PATH" <<RUBY
class Orchestra < Formula
  desc "Multi-agent code orchestration — coordinate parallel Claude Code agents"
  homepage "https://github.com/Plyne-Technologies/Claude-Orchestra"
  license "MIT"
  version "0.0.0-test"

  depends_on :macos

  def install
    bin.install "$BINARY" => "orchestra"
  end

  test do
    system "#{bin}/orchestra", "version"
  end
end
RUBY
pass "generated test-formula.rb"

# 6. Ensure clean state before install
brew uninstall orchestra 2>/dev/null || true

# 7. Install from local formula
echo ""
echo "--- Install & verify ---"
trap cleanup EXIT

if brew install --formula "$FORMULA_PATH"; then
  pass "brew install --formula succeeded"
else
  fail "brew install --formula failed"
  exit 1
fi

# 8. Verify orchestra version
VERSION_OUT=$(orchestra version 2>&1 || true)
if [[ -n "$VERSION_OUT" ]]; then
  pass "orchestra version: $VERSION_OUT"
else
  fail "orchestra version produced no output"
fi

# 9. Verify orchestra doctor
DOCTOR_EXIT=0
orchestra doctor 2>&1 || DOCTOR_EXIT=$?
if [[ $DOCTOR_EXIT -eq 0 ]]; then
  pass "orchestra doctor ran without crash"
elif [[ $DOCTOR_EXIT -le 1 ]]; then
  pass "orchestra doctor ran (non-zero exit is OK for missing optional deps)"
else
  fail "orchestra doctor crashed (exit code $DOCTOR_EXIT)"
fi

# Cleanup happens via trap

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [[ $FAIL -gt 0 ]]; then
  exit 1
fi
