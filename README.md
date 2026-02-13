# ssh-reverse-ntfy

*Written by Claude 4.5 Opus via [Claude Code](https://claude.ai/code)*

Forward `xdg-open` calls from remote SSH sessions to your local browser, using [ntfy.sh](https://ntfy.sh) as a message bus.

```
Remote Host                        ntfy.sh                       Local Mac
┌──────────────┐                ┌───────────┐                ┌──────────────┐
│ xdg-open URL ├───POST /topic──▶           ├───subscribe────▶  open $URL   │
└──────────────┘                └───────────┘                └──────────────┘
```

## Why?

When working on a remote dev server over SSH, CLI tools like `gh auth login`, `gcloud auth login`, or `npm login` try to open a browser. These fail silently because there's no browser on the remote host. This tool forwards those URLs back to your local machine.

## Quick Start

### Fixed topic (recommended for private ntfy instances)

No `sshd_config` changes, no `SendEnv`/`AcceptEnv`, works through tmux.

**Local:**
```bash
# Add to ~/.zshrc or ~/.bashrc
source /path/to/ssh-reverse-ntfy/shell.zsh

# Create ~/.config/ssh-ntfy/config.toml
mkdir -p ~/.config/ssh-ntfy
echo 'url = "https://ntfy.example.com/my-ssh-topic"' > ~/.config/ssh-ntfy/config.toml
```

**Remote:**
```bash
ssh devbox 'bash -s' < install-remote.sh https://ntfy.example.com/my-ssh-topic
```

The install script writes the same `config.toml` on the remote.

### Random topic (default, uses ntfy.sh)

Requires `SendEnv`/`AcceptEnv` configuration.

**Local:**
```bash
# Add to ~/.zshrc or ~/.bashrc
source /path/to/ssh-reverse-ntfy/shell.zsh

# Add to ~/.ssh/config
Host devbox
  SendEnv URL_FORWARD_NTFY
```

**Remote:**
```bash
ssh devbox 'bash -s' < install-remote.sh
# Then add to /etc/ssh/sshd_config (requires root):
#   AcceptEnv URL_FORWARD_NTFY
```

### Both modes

Ensure `~/.local/bin` is in your PATH on the remote (before `/usr/bin`).

## Usage

```bash
ssh-ntfy devbox               # connect with URL forwarding
xdg-open https://example.com  # opens in your local browser
gh auth login --web            # OAuth flow completes locally
```

## How It Works

1. `ssh-ntfy` reads the ntfy URL from `~/.config/ssh-ntfy/config.toml`, or generates a random topic on ntfy.sh
2. Locally, a background subscriber listens for messages on that URL
3. **Fixed topic:** the remote `xdg-open` shim reads the same URL from its own `config.toml`
4. **Random topic:** the URL is passed via `SendEnv`/`AcceptEnv` as `URL_FORWARD_NTFY`
5. The shim POSTs URLs to the ntfy endpoint, the local subscriber calls `open`
6. When SSH exits, the subscriber is cleaned up

## Files

| File | Where | Purpose |
|------|-------|---------|
| `shell.zsh` | Local | `ssh-ntfy` function |
| `xdg-open` | Remote `~/.local/bin/` | Intercepts URL opens |
| `install-remote.sh` | — | Installs the shim |
| `ssh_config` | Local `~/.ssh/config` | Example SendEnv config |

## Security

- **Random topic per session** (default) — 128 bits of entropy, unguessable
- **URL-only** — Only `http://` and `https://` URLs are forwarded
- **No eval** — The local subscriber only calls `open`, never executes received content
- **Graceful fallback** — If ntfy is unreachable, falls through to normal `xdg-open`

For additional security, [self-host ntfy](https://docs.ntfy.sh/install/) and configure `config.toml` on both sides.

## Requirements

- **Local:** macOS with `curl` and `openssl`
- **Remote:** Linux with `curl` and `~/.local/bin` in PATH

## License

MIT
