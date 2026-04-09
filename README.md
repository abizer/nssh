# ssh-reverse-ntfy

Forward `xdg-open` calls from remote SSH sessions to your local browser, using [ntfy](https://ntfy.sh) as a message bus. OAuth flows (like `gh auth login`, `gcloud auth login`) complete automatically — including the localhost callback. Transparently uses `mosh` when both sides have it, for roaming across sleep/network changes; falls back to plain `ssh` otherwise.

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
nssh devbox                      # auto-select: mosh if available on both ends, else ssh
nssh --ssh devbox                # force plain ssh (e.g. when mosh's UDP path is blocked)
nssh --mosh devbox               # force mosh, skip the remote probe
xdg-open https://example.com     # (inside the session) opens in your local browser
gh auth login --web              # OAuth flow completes locally, including callback
```

## How It Works

1. `nssh` resolves the SSH target hostname and derives the ntfy topic: `reverse-open-<hostname>`
2. Subscribes to the ntfy topic's JSON stream in a background goroutine
3. Picks a session transport:
   - `--ssh` → plain ssh
   - `--mosh` → mosh, skipping the probe
   - default → probes the remote (`ssh host 'command -v mosh-server'`); uses mosh if both sides have it, otherwise ssh
4. Runs the interactive session in the foreground, forwarding signals
5. On URL received from ntfy:
   - If it contains `localhost:<port>` anywhere (including inside `redirect_uri` query params), starts a one-shot local listener on that port
   - Opens the URL in your local browser
   - When the OAuth provider redirects the browser to `localhost:<port>`, proxies that single request to the remote via a fresh `ssh -W` — one-shot, no shared state
   - Closes listener and tunnel immediately after the response
6. On exit: ntfy subscription cancelled, terminal modes reset — no orphan processes

If mosh fails at runtime (e.g. UDP blocked by NAT on the remote's network, or remote shell breaks bootstrap), the error surfaces directly and you rerun with `--ssh` to fall back. nssh forces `LC_ALL=C.UTF-8` in mosh's environment to side-step the common "en_US.UTF-8 isn't available" failure on vanilla Linux remotes.

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

- **Local:** macOS with `ssh`, Go (to build). `mosh` is optional — nssh auto-detects it.
- **Remote:** Linux with `curl` and `~/.local/bin` in PATH. Optional: `mosh-server` for roaming sessions.

## License

MIT
