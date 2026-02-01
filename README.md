# ssh-reverse-open

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

### Local (macOS)

1. Source the shell function:
   ```bash
   # Add to ~/.zshrc or ~/.bashrc
   source /path/to/ssh-reverse-open/shell.zsh
   ```

2. Configure SSH to send the environment variable:
   ```bash
   # Add to ~/.ssh/config
   Host devbox
     SendEnv URL_FORWARD_TOPIC
   ```

### Remote (Linux)

1. Install the shim:
   ```bash
   ssh-ntfy devbox 'bash -s' < install-remote.sh
   ```

2. Ensure `~/.local/bin` is in your PATH (before `/usr/bin`)

3. Ask your sysadmin to add to `/etc/ssh/sshd_config`:
   ```
   AcceptEnv URL_FORWARD_TOPIC
   ```

## Usage

```bash
ssh-ntfy devbox           # connect with URL forwarding enabled
xdg-open https://example.com  # opens in your local browser
gh auth login --web           # OAuth flow completes locally
```

## How It Works

1. `ssh-ntfy` generates a random topic ID and starts a background subscriber
2. The topic ID is passed to the remote via `SendEnv`
3. On the remote, the `xdg-open` shim POSTs URLs to ntfy.sh
4. The local subscriber receives the URL and calls `open`
5. When SSH exits, the subscriber is cleaned up

## Files

| File | Where | Purpose |
|------|-------|---------|
| `shell.zsh` | Local | `ssh-ntfy` function |
| `xdg-open` | Remote `~/.local/bin/` | Intercepts URL opens |
| `install-remote.sh` | — | Installs the shim |
| `ssh_config` | Local `~/.ssh/config` | Example SendEnv config |

## Security

- **Random topic per session** — 128 bits of entropy, unguessable
- **URL-only** — Only `http://` and `https://` URLs are forwarded
- **No eval** — The local subscriber only calls `open`, never executes received content
- **Graceful fallback** — If ntfy is unreachable, falls through to normal `xdg-open`

For additional security, you can [self-host ntfy](https://docs.ntfy.sh/install/) and change the base URL in both scripts.

## Requirements

- **Local:** macOS with `curl` and `openssl`
- **Remote:** Linux with `curl` and `~/.local/bin` in PATH

## License

MIT
