# ssh-ntfy: Forward xdg-open URLs from remote SSH sessions to local browser via ntfy.sh
function ssh-ntfy () {
  local topic="$(openssl rand -hex 16)"
  local fifo="$(mktemp -u "${TMPDIR:-/tmp}/ssh-ntfy.XXXXXX")"
  local curl_pid reader_pid

  echo "ntfy topic: $topic"

  mkfifo "$fifo" || { echo "ssh-ntfy: failed to create fifo" >&2; return 1; }

  # FIFO splits the pipeline into two direct children with explicit PIDs.
  # The old (curl | while read)& hid curl inside a subshell — killing the
  # subshell left curl orphaned (blocked on network read, never got SIGPIPE).

  curl -s --no-buffer "https://ntfy.sh/$topic/raw" > "$fifo" 2>/dev/null &
  curl_pid=$!

  while read -r url; do
    [[ "$url" =~ ^https?:// ]] && open "$url"
  done < "$fifo" &
  reader_pid=$!

  cleanup() {
    kill "$curl_pid" "$reader_pid" 2>/dev/null
    wait "$curl_pid" "$reader_pid" 2>/dev/null
    rm -f "$fifo"
  }

  # Safety net for signals; primary cleanup is inline after ssh
  trap cleanup INT HUP TERM

  sleep 0.2

  if ! kill -0 "$curl_pid" 2>/dev/null; then
    echo "Warning: ntfy subscriber failed to start. URLs will not be forwarded." >&2
  fi

  URL_FORWARD_TOPIC="$topic" ssh "$@"
  local ssh_exit=$?

  cleanup
  trap - INT HUP TERM

  return $ssh_exit
}
