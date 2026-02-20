# ssh-ntfy: Forward xdg-open URLs from remote SSH sessions to local browser
# Remote side publishes to https://ntfy.abizer.dev/reverse-open-<hostname>
# This wrapper subscribes to the matching topic based on the ssh target.

_nssh_ntfy_base="https://ntfy.abizer.dev"

# Resolve short hostname from ssh args (used by both nssh and nssh-setup)
_nssh_resolve_host() {
  local remote_host
  remote_host="$(ssh -G "$@" 2>/dev/null | awk '/^hostname /{print $2}')"
  [[ -n "$remote_host" ]] && echo "${remote_host%%.*}"
}

# Install the xdg-open shim + config on a remote host
# Usage: nssh-setup <host> [extra ssh args...]
function nssh-setup() {
  if [[ $# -eq 0 ]]; then
    echo "Usage: nssh-setup <host> [ssh args...]" >&2
    return 1
  fi

  local short_host
  short_host="$(_nssh_resolve_host "$@")"

  if [[ -z "$short_host" ]]; then
    echo "ssh-ntfy: could not determine remote host" >&2
    return 1
  fi

  local ntfy_url="${_nssh_ntfy_base}/reverse-open-${short_host}"

  echo "ssh-ntfy: installing shim on $1 (topic: ${ntfy_url})"

  ssh "$@" bash -s -- "$ntfy_url" << 'REMOTE'
set -euo pipefail
ntfy_url="$1"

mkdir -p ~/.local/bin

cat > ~/.local/bin/xdg-open << 'SHIM'
#!/bin/bash
set -euo pipefail

url="${1:-}"
ntfy="$(sed -n 's/^url *= *"\(.*\)"/\1/p' "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml" 2>/dev/null)"

if [[ -z "$ntfy" ]] || [[ ! "$url" =~ ^https?:// ]]; then
  exec /usr/bin/xdg-open "$@"
fi

if curl -sf -m 5 -d "$url" "$ntfy" >/dev/null 2>&1; then
  exit 0
else
  exec /usr/bin/xdg-open "$@"
fi
SHIM

chmod +x ~/.local/bin/xdg-open
ln -sf ~/.local/bin/xdg-open ~/.local/bin/sensible-browser 2>/dev/null || true

mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy"
cat > "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml" << EOF
url = "$ntfy_url"
EOF

echo "Installed xdg-open shim to ~/.local/bin/"
echo "Configured ntfy endpoint: $ntfy_url"
echo "Ensure ~/.local/bin is in your PATH (before /usr/bin)"
REMOTE

  echo "ssh-ntfy: setup complete for $1"
}

# Connect with URL forwarding
# Usage: nssh <host> [extra ssh args...]
function nssh() {
  local short_host
  short_host="$(_nssh_resolve_host "$@")"

  if [[ -z "$short_host" ]]; then
    echo "ssh-ntfy: could not determine remote host" >&2
    ssh "$@"
    return $?
  fi

  local ntfy_url="${_nssh_ntfy_base}/reverse-open-${short_host}"

  echo "ssh-ntfy: subscribing to ${ntfy_url}"

  curl -s --no-buffer "${ntfy_url}/raw" | while read -r url; do
    [[ "$url" =~ ^https?:// ]] && open "$url"
  done &
  local sub_pid=$!

  trap "kill $sub_pid 2>/dev/null; wait $sub_pid 2>/dev/null" INT HUP TERM

  ssh "$@"
  local ssh_exit=$?

  kill $sub_pid 2>/dev/null
  wait $sub_pid 2>/dev/null
  trap - INT HUP TERM

  return $ssh_exit
}
