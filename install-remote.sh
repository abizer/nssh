#!/bin/bash
# Install xdg-open shim on a remote host
# Run: ssh-ntfy host 'bash -s' < install-remote.sh

set -euo pipefail

mkdir -p ~/.local/bin

cat > ~/.local/bin/xdg-open << 'SHIM'
#!/bin/bash
set -euo pipefail

url="${1:-}"

if [[ -z "${URL_FORWARD_TOPIC:-}" ]] || [[ ! "$url" =~ ^https?:// ]]; then
  exec /usr/bin/xdg-open "$@"
fi

if curl -sf -m 5 -d "$url" "https://ntfy.sh/$URL_FORWARD_TOPIC" >/dev/null 2>&1; then
  exit 0
else
  exec /usr/bin/xdg-open "$@"
fi
SHIM

chmod +x ~/.local/bin/xdg-open

# Create symlinks for broader coverage
ln -sf ~/.local/bin/xdg-open ~/.local/bin/sensible-browser 2>/dev/null || true

echo "Installed xdg-open shim to ~/.local/bin/"
echo "Ensure ~/.local/bin is in your PATH (before /usr/bin)"
echo ""
echo "Add to sshd_config (requires root):"
echo "  AcceptEnv URL_FORWARD_TOPIC"
