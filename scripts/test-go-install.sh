#!/usr/bin/env bash
# test-go-install.sh — Verify that `go install` works for this module.
# Checks module path, builds the binary, and optionally installs it.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EXPECTED_MODULE="github.com/MochaCosine1206/orchestra"
EXPECTED_INSTALL_PATH="${EXPECTED_MODULE}/cmd/orchestra"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== go install verification ==="
echo ""

# 1. Check go.mod module path
echo "--- Module path ---"
MODULE_PATH=$(head -1 "$REPO_ROOT/go.mod" | awk '{print $2}')
if [[ "$MODULE_PATH" == "$EXPECTED_MODULE" ]]; then
  pass "go.mod module path matches expected ($EXPECTED_MODULE)"
else
  fail "go.mod module path mismatch: got '$MODULE_PATH', expected '$EXPECTED_MODULE'"
  echo "  NOTE: External 'go install ${EXPECTED_INSTALL_PATH}@latest' will fail"
  echo "        until the module path is '$EXPECTED_MODULE'."
fi

# 2. Check cmd/orchestra/main.go exists and is package main
echo ""
echo "--- Entry point ---"
MAIN_GO="$REPO_ROOT/cmd/orchestra/main.go"
if [[ -f "$MAIN_GO" ]]; then
  pass "cmd/orchestra/main.go exists"
else
  fail "cmd/orchestra/main.go not found"
fi

if [[ -f "$MAIN_GO" ]] && head -5 "$MAIN_GO" | grep -q '^package main'; then
  pass "cmd/orchestra/main.go declares package main"
else
  fail "cmd/orchestra/main.go does not declare package main"
fi

# 3. Build the binary
echo ""
echo "--- Build ---"
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT

if go build -o "$BUILD_DIR/orchestra" "$REPO_ROOT/cmd/orchestra/"; then
  pass "go build ./cmd/orchestra/ succeeded"
else
  fail "go build ./cmd/orchestra/ failed"
fi

# 4. Run built binary with version flag
if [[ -x "$BUILD_DIR/orchestra" ]]; then
  VERSION_OUT=$("$BUILD_DIR/orchestra" version 2>&1 || true)
  if [[ -n "$VERSION_OUT" ]]; then
    pass "built binary runs: $VERSION_OUT"
  else
    fail "built binary produced no version output"
  fi
fi

# 5. If GOBIN is set, test go install
echo ""
echo "--- go install ---"
if [[ -n "${GOBIN:-}" ]]; then
  echo "  GOBIN=$GOBIN"
  if (cd "$REPO_ROOT" && go install ./cmd/orchestra/); then
    pass "go install ./cmd/orchestra/ succeeded"
    if [[ -x "$GOBIN/orchestra" ]]; then
      pass "binary exists at $GOBIN/orchestra"
      INSTALLED_VERSION=$("$GOBIN/orchestra" version 2>&1 || true)
      if [[ -n "$INSTALLED_VERSION" ]]; then
        pass "installed binary runs: $INSTALLED_VERSION"
      else
        fail "installed binary produced no version output"
      fi
    else
      fail "binary not found at $GOBIN/orchestra after install"
    fi
  else
    fail "go install ./cmd/orchestra/ failed"
  fi
else
  echo "  GOBIN not set — skipping go install test"
  echo "  (Set GOBIN to test installation, e.g.: GOBIN=/tmp/gobin scripts/test-go-install.sh)"
fi

# Summary
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [[ $FAIL -gt 0 ]]; then
  exit 1
fi
