# ssh-ntfy: Forward xdg-open URLs from remote SSH sessions to local browser
# Remote side publishes to https://ntfy.abizer.dev/reverse-open-<hostname>
# This wrapper subscribes to the matching topic based on the ssh target.
function ssh-ntfy () {
  local ntfy_base="https://ntfy.abizer.dev"

  # Resolve the effective remote hostname via ssh config
  local remote_host
  remote_host="$(ssh -G "$@" 2>/dev/null | awk '/^hostname /{print $2}')"

  if [[ -z "$remote_host" ]]; then
    echo "ssh-ntfy: could not determine remote host" >&2
    ssh "$@"
    return $?
  fi

  # Short hostname (before first dot) to match remote's $(hostname -s)
  local short_host="${remote_host%%.*}"
  local ntfy_url="${ntfy_base}/reverse-open-${short_host}"

  local fifo="$(mktemp -u "${TMPDIR:-/tmp}/ssh-ntfy.XXXXXX")"
  local curl_pid reader_pid

  echo "ssh-ntfy: subscribing to ${ntfy_url}"

  mkfifo "$fifo" || { echo "ssh-ntfy: failed to create fifo" >&2; return 1; }

  curl -s --no-buffer "${ntfy_url}/raw" > "$fifo" 2>/dev/null &
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

  trap cleanup INT HUP TERM

  sleep 0.2

  if ! kill -0 "$curl_pid" 2>/dev/null; then
    echo "ssh-ntfy: subscriber failed to start. URLs will not be forwarded." >&2
  fi

  ssh "$@"
  local ssh_exit=$?

  cleanup
  trap - INT HUP TERM

  return $ssh_exit
}
