# ssh-reverse-ntfy

Forward `xdg-open` calls from remote SSH sessions to your local browser, using [ntfy.sh](https://ntfy.sh) as a message bus.

```
Remote Host                        ntfy.sh                       Local Mac
┌──────────────┐                ┌───────────┐                ┌──────────────┐
│ xdg-open URL ├───POST /topic──▶           ├───subscribe────▶  open $URL   │
└──────────────┘                └───────────┘                └──────────────┘
```

## Why?

When working on a remote dev server over SSH, CLI tools like `gh auth login`, `gcloud auth login`, or `npm login` try to open a browser. These fail silently because there's no browser on the remote host. This tool forwards those URLs back to your local machine.

## Setup

**Local:**
```bash
# Add to ~/.zshrc or ~/.bashrc
source /path/to/ssh-reverse-ntfy/shell.zsh
```

**Remote (one-time per host):**
```bash
nssh-setup devbox
```

This SSHs into the host, installs the `xdg-open` shim to `~/.local/bin/`, and writes the ntfy config. Ensure `~/.local/bin` is in PATH on the remote (before `/usr/bin`).

## Usage

```bash
nssh devbox                    # connect with URL forwarding
xdg-open https://example.com  # opens in your local browser
gh auth login --web            # OAuth flow completes locally
```

## How It Works

1. `nssh` resolves the SSH target hostname and derives the ntfy topic: `reverse-open-<hostname>`
2. A background curl subscriber listens for messages on that topic
3. On the remote, the `xdg-open` shim reads the same ntfy URL from `~/.config/ssh-ntfy/config.toml`
4. The shim POSTs URLs to the ntfy endpoint; the local subscriber calls `open`
5. When SSH exits, the subscriber is cleaned up

No `sshd_config` changes, no `SendEnv`/`AcceptEnv`, no VM reboots. Works through tmux.

## Files

| File | Purpose |
|------|---------|
| `shell.zsh` | `nssh` (connect) and `nssh-setup` (bootstrap remote) |

## Security

- **URL-only** — Only `http://` and `https://` URLs are forwarded
- **No eval** — The local subscriber only calls `open`, never executes received content
- **Graceful fallback** — If ntfy is unreachable, falls through to normal `xdg-open`

For additional security, [self-host ntfy](https://docs.ntfy.sh/install/).

## Requirements

- **Local:** macOS with `curl`
- **Remote:** Linux with `curl` and `~/.local/bin` in PATH

## License

MIT
