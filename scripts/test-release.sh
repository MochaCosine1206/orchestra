#!/usr/bin/env bash
# test-release.sh — Build a local snapshot binary via GoReleaser and verify outputs.
# Useful for verifying the release pipeline without pushing a tag.
set -euo pipefail

if ! command -v goreleaser &>/dev/null; then
  echo "ERROR: goreleaser not found — install from https://goreleaser.com/install/"
  exit 1
fi

echo "Building snapshot release..."
goreleaser build --snapshot --clean

echo ""
echo "Verifying snapshot binaries..."

errors=0

# Check darwin_arm64 binary
if [ -f dist/orchestra_darwin_arm64/orchestra ]; then
  echo "  ✓ darwin_arm64 binary exists"
else
  echo "  ✗ darwin_arm64 binary missing (expected dist/orchestra_darwin_arm64/orchestra)"
  errors=$((errors + 1))
fi

# Check linux_amd64 binary
if [ -f dist/orchestra_linux_amd64_v1/orchestra ]; then
  echo "  ✓ linux_amd64 binary exists"
elif [ -f dist/orchestra_linux_amd64/orchestra ]; then
  echo "  ✓ linux_amd64 binary exists"
else
  echo "  ✗ linux_amd64 binary missing (expected dist/orchestra_linux_amd64*/orchestra)"
  errors=$((errors + 1))
fi

# Run the native binary with 'version' to verify it works
native_arch=$(uname -m)
native_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$native_arch" in
  x86_64) native_arch="amd64" ;;
  aarch64) native_arch="arm64" ;;
esac

native_bin=""
for candidate in "dist/orchestra_${native_os}_${native_arch}/orchestra" "dist/orchestra_${native_os}_${native_arch}_v1/orchestra"; do
  if [ -f "$candidate" ]; then
    native_bin="$candidate"
    break
  fi
done

if [ -n "$native_bin" ]; then
  version_output=$("$native_bin" version 2>&1) || true
  if [ -n "$version_output" ]; then
    echo "  ✓ binary runs: $version_output"
  else
    echo "  ✗ binary 'version' returned empty output"
    errors=$((errors + 1))
  fi
else
  echo "  ⚠ no native binary for ${native_os}/${native_arch} — skipping runtime check"
fi

echo ""
if [ "$errors" -gt 0 ]; then
  echo "FAIL: $errors verification error(s)"
  exit 1
fi

echo "All checks passed."
echo ""
echo "Snapshot binaries:"
ls -lh dist/orchestra_*/orchestra 2>/dev/null || ls -lh dist/
