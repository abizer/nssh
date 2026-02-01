# ssh-ntfy: Forward xdg-open URLs from remote SSH sessions to local browser via ntfy.sh
function ssh-ntfy () {
  local topic="$(openssl rand -hex 16)"
  echo "ntfy topic: $topic";
  local subscriber_pid

  cleanup() {
    if [[ -n "$subscriber_pid" ]] && kill -0 "$subscriber_pid" 2>/dev/null; then
      kill "$subscriber_pid" 2>/dev/null
      wait "$subscriber_pid" 2>/dev/null
    fi
  }
  trap cleanup EXIT INT HUP TERM

  # Start subscriber in background, opening URLs as they arrive
  (
    curl -s --no-buffer "https://ntfy.sh/$topic/raw" 2>/dev/null | while read -r url; do
      [[ "$url" =~ ^https?:// ]] && open "$url"
    done
  ) &
  subscriber_pid=$!

  # Give subscriber a moment to connect
  sleep 0.2

  # Verify subscriber started
  if ! kill -0 "$subscriber_pid" 2>/dev/null; then
    echo "Warning: ntfy subscriber failed to start. URLs will not be forwarded." >&2
  fi

  # Run ssh with the topic in environment
  URL_FORWARD_TOPIC="$topic" ssh "$@"
}
