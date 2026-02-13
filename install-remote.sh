#!/bin/bash
# Install xdg-open shim on a remote host
# Usage: ssh host 'bash -s' < install-remote.sh [NTFY_URL]
# Example: ssh devbox 'bash -s' < install-remote.sh https://ntfy.example.com/my-topic

set -euo pipefail

ntfy_url="${1:-}"

mkdir -p ~/.local/bin

cat > ~/.local/bin/xdg-open << 'SHIM'
#!/bin/bash
set -euo pipefail

url="${1:-}"

# Resolve ntfy endpoint: env var (random topics) > config file (fixed topics)
ntfy="${URL_FORWARD_NTFY:-}"
if [[ -z "$ntfy" ]]; then
  ntfy="$(sed -n 's/^url *= *"\(.*\)"/\1/p' "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml" 2>/dev/null)"
fi

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

# Create symlinks for broader coverage
ln -sf ~/.local/bin/xdg-open ~/.local/bin/sensible-browser 2>/dev/null || true

echo "Installed xdg-open shim to ~/.local/bin/"
echo "Ensure ~/.local/bin is in your PATH (before /usr/bin)"

if [[ -n "$ntfy_url" ]]; then
  mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy"
  cat > "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml" << EOF
url = "$ntfy_url"
EOF
  echo ""
  echo "Configured fixed ntfy endpoint: $ntfy_url"
  echo "No SendEnv/AcceptEnv required."
else
  echo ""
  echo "No fixed URL configured. For random topics, add to sshd_config (requires root):"
  echo "  AcceptEnv URL_FORWARD_NTFY"
  echo ""
  echo "Or re-run with a fixed URL to skip sshd_config entirely:"
  echo "  ssh host 'bash -s' < install-remote.sh https://ntfy.example.com/my-topic"
fi
