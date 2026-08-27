#!/bin/bash
# SubagentStop quality gate — checks per-role criteria before allowing stop.
# Stop hooks in agent frontmatter are auto-remapped to SubagentStop.
# Input (stdin JSON): agent_type, stop_hook_active, last_assistant_message
# Output: {"decision":"block","reason":"..."} to retry, or exit 0 to allow.
set -uo pipefail

INPUT=$(cat)
STOP_ACTIVE=$(echo "$INPUT" | jq -r '.stop_hook_active // false' 2>/dev/null || echo "false")
AGENT_TYPE=$(echo "$INPUT" | jq -r '.agent_type // "unknown"' 2>/dev/null || echo "unknown")
LAST_MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // ""' 2>/dev/null || echo "")

# Guard: if already retried once, always allow stop
[ "$STOP_ACTIVE" = "true" ] && exit 0

# Guard: if no message, can't check quality
[ -z "$LAST_MSG" ] && exit 0

case "$AGENT_TYPE" in
  Implementer|implementer)
    # Check: tests mentioned as passing
    if ! echo "$LAST_MSG" | grep -qiE "tests? pass|all pass|PASS|passed|✓.*test|test.*✓"; then
      echo '{"decision":"block","reason":"Run tests and confirm they pass before finishing. Include test results in your response."}'
      exit 0
    fi
    ;;
  Researcher|researcher)
    # Check: sources cited
    if ! echo "$LAST_MSG" | grep -qiE "https?://|source:|sources:|cited|reference"; then
      echo '{"decision":"block","reason":"Cite your sources with URLs before finishing. Include a Sources section."}'
      exit 0
    fi
    ;;
  Reviewer|reviewer)
    # Check: verdict given
    if ! echo "$LAST_MSG" | grep -qiE "APPROVE|REQUEST CHANGES|REJECT|verdict:|finding"; then
      echo '{"decision":"block","reason":"Provide a clear verdict (APPROVE/REQUEST CHANGES/REJECT) before finishing."}'
      exit 0
    fi
    ;;
  *)
    # architect, scout, unknown — pass through
    ;;
esac

exit 0
