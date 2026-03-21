# ssh-reverse-ntfy

Forward `xdg-open` calls from remote SSH sessions to your local browser, using [ntfy](https://ntfy.sh) as a message bus. OAuth flows (like `gh auth login`, `gcloud auth login`) complete automatically — including the localhost callback.

```
Remote Host                        ntfy                          Local Mac
┌──────────────┐                ┌───────────┐                ┌──────────────┐
│ xdg-open URL ├───POST /topic──▶           ├───subscribe────▶  open $URL   │
└──────────────┘                └───────────┘                └──────────────┘

OAuth callback:
Browser → localhost:8085 (Mac) → ssh -W localhost:8085 devbox → remote server
                                                                      ↓ response
Browser ←─────────────────────────────────────────────────────────────┘
                             [listener and tunnel close]
```

## Why?

CLI tools on remote dev servers (`gh`, `gcloud`, `npm login`) try to open a browser. This fails silently over SSH. This tool forwards those URLs to your local machine — and for OAuth flows with a `localhost` callback, it automatically proxies the callback back to the remote server so the auth completes without manual port forwarding.

## Install

**Local:**
```bash
just build   # builds ./nssh
just run     # builds and installs to ~/.local/bin/nssh
```

Add to your shell config (for `nssh-setup`):
```bash
source /path/to/ssh-reverse-ntfy/shell.zsh
```

**Remote (one-time per host):**
```bash
just setup devbox
```

This SSHs into the host, installs the `xdg-open` shim to `~/.local/bin/`, and writes the ntfy config. Ensure `~/.local/bin` is in `PATH` on the remote (before `/usr/bin`).

For NixOS/home-manager hosts, the shim and config are managed automatically — no manual setup needed.

## Usage

```bash
nssh devbox                    # connect with URL forwarding
xdg-open https://example.com  # opens in your local browser
gh auth login --web            # OAuth flow completes locally, including callback
```

## How It Works

1. `nssh` resolves the SSH target hostname and derives the ntfy topic: `reverse-open-<hostname>`
2. Starts an SSH ControlMaster for connection reuse (used later for OAuth proxying)
3. Subscribes to the ntfy topic's JSON stream in the background
4. Runs your interactive SSH session via the control socket
5. On URL received:
   - If it contains `localhost:<port>` anywhere (including inside `redirect_uri` query params), starts a one-shot local listener on that port
   - Opens the URL in your local browser
   - When the OAuth provider redirects the browser to `localhost:<port>`, proxies that single request to the remote VM via `ssh -W` — reusing the control master, no reauth
   - Closes listener and tunnel immediately after the response
6. On exit: ntfy subscription cancelled, control master torn down, socket removed — no orphan processes

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `NSSH_NTFY_BASE` | `https://ntfy.abizer.dev` | ntfy server base URL |

## Security

- **URL-only** — only `http://` and `https://` URLs are forwarded
- **No eval** — received content is never executed
- **One-shot proxy** — port listeners close after a single request
- **Graceful fallback** — plain `ssh` if ntfy is unreachable or hostname can't be resolved

Self-host ntfy for additional isolation: [docs.ntfy.sh/install](https://docs.ntfy.sh/install/).

## Requirements

- **Local:** macOS with `ssh`, Go (to build)
- **Remote:** Linux with `curl` and `~/.local/bin` in PATH

## License

MIT
