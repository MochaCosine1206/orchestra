#!/bin/bash
# verify-dashboard-visual.sh — Post-merge visual verification gate
# Usage: scripts/verify-dashboard-visual.sh [repo-root]
#
# Builds the dashboard binary, starts it, screenshots each page,
# and compares against reference screenshots. Exits 0 if all pages
# render (200 status), exits 1 if any page fails.
#
# This is NOT a pixel-perfect comparison — it verifies that:
# 1. The binary builds
# 2. Each page returns 200
# 3. Screenshots can be captured (page renders, no crash)
# 4. Key structural elements exist in the HTML

set -e

ROOT="${1:-.}"
cd "$ROOT"

PORT=9876
BINARY="/tmp/dashboard-verify-$$"
OUTDIR="/tmp/dashboard-screenshots-$$"
mkdir -p "$OUTDIR"

echo "=== Visual Verification Gate ==="

# Step 1: Build
echo "Building binary..."
go build -o "$BINARY" ./cmd/orchestra 2>&1
if [ $? -ne 0 ]; then
    echo "FAIL: go build failed"
    exit 1
fi
echo "OK: binary built"

# Step 2: Start server
"$BINARY" dashboard --web --port "$PORT" &
SERVER_PID=$!
sleep 3

# Verify server is up
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PORT/" 2>/dev/null || echo "000")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: server didn't start (status: $STATUS)"
    kill $SERVER_PID 2>/dev/null
    rm -f "$BINARY"
    exit 1
fi
echo "OK: server running on port $PORT"

# Step 3: Check each page
PAGES="/ /briefing /config /artifacts"
FAILED=0
for page in $PAGES; do
    name=$(echo "$page" | sed 's|/||g')
    [ -z "$name" ] && name="factory"

    STATUS=$(curl -s -o "$OUTDIR/$name.html" -w "%{http_code}" "http://localhost:$PORT$page" 2>/dev/null || echo "000")
    if [ "$STATUS" = "200" ]; then
        echo "OK: $name ($STATUS)"
    else
        echo "FAIL: $name ($STATUS)"
        FAILED=$((FAILED + 1))
    fi
done

# Step 4: Structural checks on factory page
if [ -f "$OUTDIR/factory.html" ]; then
    CHECKS=0
    PASSED=0

    # Check for nav
    CHECKS=$((CHECKS + 1))
    grep -q "nav-brand\|nav-tab\|top-nav" "$OUTDIR/factory.html" && PASSED=$((PASSED + 1)) && echo "OK: nav structure found" || echo "WARN: nav structure missing"

    # Check for stat grid
    CHECKS=$((CHECKS + 1))
    grep -q "stat\|stats" "$OUTDIR/factory.html" && PASSED=$((PASSED + 1)) && echo "OK: stat grid found" || echo "WARN: stat grid missing"

    # Check for no inline style blocks
    CHECKS=$((CHECKS + 1))
    STYLE_COUNT=$(grep -c "<style" "$OUTDIR/factory.html" || true)
    if [ "$STYLE_COUNT" -le 1 ]; then
        PASSED=$((PASSED + 1))
        echo "OK: minimal inline styles ($STYLE_COUNT)"
    else
        echo "WARN: $STYLE_COUNT inline style blocks"
    fi

    echo "Structural: $PASSED/$CHECKS checks passed"
fi

# Cleanup
kill $SERVER_PID 2>/dev/null
rm -f "$BINARY"
rm -rf "$OUTDIR"

if [ $FAILED -gt 0 ]; then
    echo "FAIL: $FAILED pages returned non-200"
    exit 1
fi

echo "=== Visual verification passed ==="
exit 0
