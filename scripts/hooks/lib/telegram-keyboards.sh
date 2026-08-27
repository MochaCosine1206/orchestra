#!/bin/bash
# Inline keyboard construction and callback handling for Telegram
# Source this file after telegram-api.sh and state-helpers.sh
#
# Callback data format (64-byte limit): "action:request_id"
# Request IDs: 8-char hex for compact callback data

# kb_approval(request_id) — returns JSON for [[Approve, Deny], [Skip]] keyboard
kb_approval() {
  local request_id="$1"
  cat << EOF
{"inline_keyboard":[[{"text":"✅ Approve","callback_data":"approve:${request_id}"},{"text":"❌ Deny","callback_data":"deny:${request_id}"}],[{"text":"⏭ Skip","callback_data":"skip:${request_id}"}]]}
EOF
}

# kb_options(request_id, labels...) — returns JSON for option selection keyboard
# Each label becomes a button with callback_data "opt:request_id:index"
kb_options() {
  local request_id="$1"
  shift
  local buttons=""
  local idx=0
  for label in "$@"; do
    if [ -n "$buttons" ]; then
      buttons="$buttons,"
    fi
    buttons="$buttons[{\"text\":\"${label}\",\"callback_data\":\"opt:${request_id}:${idx}\"}]"
    idx=$((idx + 1))
  done
  echo "{\"inline_keyboard\":[${buttons}]}"
}

# kb_yesno(request_id) — returns JSON for [[Yes, No]] keyboard
kb_yesno() {
  local request_id="$1"
  echo "{\"inline_keyboard\":[[{\"text\":\"Yes\",\"callback_data\":\"yes:${request_id}\"},{\"text\":\"No\",\"callback_data\":\"no:${request_id}\"}]]}"
}

# cb_parse(callback_data) — extracts action and request_id
# Output: two lines — action, then request_id (plus optional extra field)
cb_parse() {
  local data="$1"
  local action="${data%%:*}"
  local rest="${data#*:}"
  echo "$action"
  echo "$rest"
}

# cb_generate_id() — generate 8-char hex request ID
cb_generate_id() {
  head -c 4 /dev/urandom | xxd -p
}

# cb_write_decision(request_id, decision, slug) — write decision to state + /tmp
cb_write_decision() {
  local request_id="$1"
  local decision="$2"
  local slug="${3:-}"

  # Always write to /tmp for quick polling
  echo "$decision" > "/tmp/telegram-decision-${request_id}"

  # Also write to slug state if available
  if [ -n "$slug" ]; then
    local state_dir
    state_dir=$(get_state_dir "$slug")
    mkdir -p "$state_dir/telegram/decisions"
    echo "$decision" > "$state_dir/telegram/decisions/${request_id}"
  fi
}

# cb_read_decision(request_id, timeout_seconds) — poll for decision, return decision or "timeout"
cb_read_decision() {
  local request_id="$1"
  local timeout="${2:-300}"
  local decision_file="/tmp/telegram-decision-${request_id}"
  local elapsed=0
  local interval=1

  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -f "$decision_file" ]; then
      local decision
      decision=$(cat "$decision_file" 2>/dev/null || true)
      rm -f "$decision_file"
      echo "$decision"
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done

  echo "timeout"
  return 1
}
