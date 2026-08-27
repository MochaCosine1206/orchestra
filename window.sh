#!/usr/bin/env bash
# window.sh — Open the Orchestra Dashboard in the default browser
# Usage: ./window.sh [port]

PORT="${1:-3000}"
URL="http://localhost:${PORT}"

echo "Opening Orchestra Dashboard at ${URL}"

if command -v open &>/dev/null; then
    open "$URL"
elif command -v xdg-open &>/dev/null; then
    xdg-open "$URL"
else
    echo "Visit ${URL} in your browser"
fi
