# CLAUDE.md

Go binary (`nssh`) that forwards `xdg-open` URLs from remote SSH sessions to
the local browser via ntfy pub/sub, with automatic one-shot OAuth port
proxying. Wraps `mosh` when both ends have it (for session roaming across
sleep/network changes), or falls back to plain `ssh`.

## Architecture

- `main.go` — the `nssh` binary. Replaces the old shell `nssh` function.
  - Parses optional `--ssh` / `--mosh` flags to force a transport
  - Resolves short hostname via `ssh -G` (for the ntfy topic)
  - Subscribes to the ntfy JSON stream for the host's topic in a goroutine
  - Picks session transport: `--ssh` forces ssh; `--mosh` forces mosh; default
    probes `remoteHasMosh` and picks mosh if both sides have it
  - Runs the interactive session (mosh or ssh) in the foreground via
    `runSession`, forwarding SIGINT/SIGTERM/SIGHUP to the child
  - On URL received from ntfy: if a `localhost:<port>` is found anywhere in
    the URL (including inside query params like `redirect_uri`), starts
    `proxyOAuthCallback` in a goroutine
  - On exit: ntfy context cancelled, terminal mouse-tracking modes reset
- `setup.sh` — bootstraps a remote host: installs the `xdg-open` shim to
  `~/.local/bin/` and writes `config.toml`. Called via `just setup <host>`.
- `shell.zsh` — stub, sourced by dotfiles for compatibility. No logic remains.
- `justfile` — `build`, `run`, `setup`
- `flake.nix` — nix package for use by dotfiles/home-manager consumers

## Session transport selection

| Invocation          | Behavior                                                                                               |
|---------------------|--------------------------------------------------------------------------------------------------------|
| `nssh host`         | Probe `ssh -o BatchMode=yes host 'command -v mosh-server'`; use mosh if both ends have it, else ssh   |
| `nssh --ssh host`   | Force plain ssh (skip the probe)                                                                       |
| `nssh --mosh host`  | Force mosh (skip the probe — trust the user)                                                           |

Mosh invocations set `LC_ALL=C.UTF-8` / `LANG=C.UTF-8` in the child env to
side-step the common "en_US.UTF-8 isn't available" error on vanilla Linux
remotes where that locale hasn't been generated (seen on exe.dev VMs,
Nix-installed mosh without glibcLocales, etc.).

If mosh fails at runtime (UDP blocked by NAT, remote shell breaks bootstrap,
etc.) nssh prints mosh's error and exits. Rerun with `--ssh` to fall back.

## OAuth callback proxy

When a URL contains `localhost:<port>` (top-level or in a query param), `nssh`:

1. Listens on that port locally
2. Accepts exactly one connection (the browser's OAuth callback GET)
3. Proxies it to the remote via a fresh `ssh -W localhost:<port> <host>`
4. Returns the response to the browser
5. Closes listener and tunnel immediately — one-shot, nothing lingers

Each OAuth forward is an independent SSH connection — no ControlMaster, no
socket files. This makes the forward behave identically whether the
interactive session is mosh or ssh, and makes it roam-safe (a mid-session
sleep/wake or network change doesn't break future OAuth forwards).

## Topic convention

`<NSSH_NTFY_BASE>/reverse-open-<short-hostname>` — base defaults to
`https://ntfy.abizer.dev`, overridable via `NSSH_NTFY_BASE` env var.

## Key constraints

- Only forward `http://` and `https://` URLs
- Never eval or execute received content on local side
- Graceful fallback to plain `ssh` when hostname can't be resolved
- stdlib only — no external Go dependencies
