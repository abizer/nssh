#!/usr/bin/env bash
# Install the xdg-open shim + ntfy config on a remote host.
# Usage: ./setup.sh <host> [extra ssh args...]
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: setup.sh <host> [ssh args...]" >&2
  exit 1
fi

ntfy_base="${NSSH_NTFY_BASE:-https://ntfy.abizer.dev}"
short_host="$(ssh -G "$@" 2>/dev/null | awk '/^hostname /{print $2}' | cut -d. -f1)"

if [[ -z "$short_host" ]]; then
  echo "nssh: could not determine remote hostname" >&2
  exit 1
fi

ntfy_url="${ntfy_base}/reverse-open-${short_host}"
echo "nssh: installing shim on $1 (topic: ${ntfy_url})"

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

echo "nssh: setup complete for $1"
