#!/usr/bin/env bash
# Install nssh + clipboard/xdg-open symlinks on a remote host.
# Usage: ./setup.sh <host> [extra ssh args...]
# Expects nssh-linux to be already built (the justfile handles this).
# No config is written — nssh writes a per-connection session file at
# connect time with the ntfy server and topic.
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: setup.sh <host> [ssh args...]" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="${SCRIPT_DIR}/nssh-linux"

if [[ ! -f "$BINARY" ]]; then
  echo "nssh: nssh-linux binary not found — run 'just build-linux' first" >&2
  exit 1
fi

echo "nssh: installing on $1"

# Copy the cross-compiled binary to the remote.
scp -q "$BINARY" "$1:~/.local/bin/nssh"

# Set up symlinks on the remote.
ssh "$@" bash -s << 'REMOTE'
set -euo pipefail

mkdir -p ~/.local/bin
chmod +x ~/.local/bin/nssh

# Symlink shim personas.
for name in xdg-open sensible-browser xclip wl-copy wl-paste; do
  ln -sf ~/.local/bin/nssh ~/.local/bin/"$name"
done

echo "Installed nssh to ~/.local/bin/"
echo "  Symlinks: xdg-open, sensible-browser, xclip, wl-copy, wl-paste"
echo "  Ensure ~/.local/bin is in your PATH (before /usr/bin)"
REMOTE

echo "nssh: setup complete for $1"
