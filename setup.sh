#!/usr/bin/env bash
# Install the nssh-shim binary + ntfy config on a remote host.
# Usage: ./setup.sh <host> [extra ssh args...]
# Expects nssh-shim (linux/amd64) to be already built in the repo root
# (the justfile's `setup` target handles this automatically).
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: setup.sh <host> [ssh args...]" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Prefer the cross-compiled binary (nssh-shim-amd64 from `just build-linux`),
# fall back to nssh-shim (from `just build`).
SHIM="${SCRIPT_DIR}/nssh-shim-amd64"
if [[ ! -f "$SHIM" ]]; then
  SHIM="${SCRIPT_DIR}/nssh-shim"
fi
if [[ ! -f "$SHIM" ]]; then
  echo "nssh: nssh-shim binary not found — run 'just build-linux' first" >&2
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

# Copy the cross-compiled binary to the remote.
scp -q "$SHIM" "$1:~/.local/bin/nssh-shim"

# Set up symlinks and config on the remote.
ssh "$@" bash -s -- "$ntfy_url" << 'REMOTE'
set -euo pipefail
ntfy_url="$1"

mkdir -p ~/.local/bin
chmod +x ~/.local/bin/nssh-shim

# Symlink all personas.
for name in xdg-open sensible-browser xclip wl-copy wl-paste; do
  ln -sf ~/.local/bin/nssh-shim ~/.local/bin/"$name"
done

# Write the ntfy config.
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy"
cat > "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml" << EOF
url = "$ntfy_url"
EOF

echo "Installed nssh-shim to ~/.local/bin/"
echo "  Symlinks: xdg-open, sensible-browser, xclip, wl-copy, wl-paste"
echo "  Configured ntfy endpoint: $ntfy_url"
echo "  Ensure ~/.local/bin is in your PATH (before /usr/bin)"
REMOTE

echo "nssh: setup complete for $1"
