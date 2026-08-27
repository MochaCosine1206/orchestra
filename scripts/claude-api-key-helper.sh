#!/bin/bash
# claude-api-key-helper.sh — Extract Claude Max OAuth token from macOS keychain
#
# Usage: Used as apiKeyHelper for claude --bare mode when Claude Max OAuth
# is the auth method (no ANTHROPIC_API_KEY available).
#
# Settings JSON:
#   {"apiKeyHelper": "/path/to/claude-api-key-helper.sh"}
#
# Then: claude --bare -p "prompt" --settings /path/to/settings.json
#
# Why this exists: --bare disables keychain reads for speed (~30% faster CPU).
# But Claude Max authenticates via OAuth stored in keychain. This helper
# bridges the gap — extracts the cached OAuth token so --bare can use it.
#
# Discovered: 2026-03-25 during B-310 investigation. The --bare flag's
# "Anthropic auth is strictly ANTHROPIC_API_KEY or apiKeyHelper" blocked
# Claude Max users. This workaround was verified working.
#
# macOS only. Requires: security (Keychain CLI), python3.

set -euo pipefail

security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null | \
  python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d['claudeAiOauth']['accessToken'])"
