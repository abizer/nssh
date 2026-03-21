# CLAUDE.md

Go binary (`nssh`) that forwards `xdg-open` URLs from remote SSH sessions to the local browser via ntfy pub/sub, with automatic one-shot OAuth port proxying.

## Architecture

- `main.go` — the `nssh` binary. Replaces the old shell `nssh` function.
  - Resolves short hostname via `ssh -G`
  - Starts an SSH ControlMaster (`ssh -M -S /tmp/.nssh-<host>.sock`) for connection reuse
  - Subscribes to the ntfy JSON stream for the host's topic
  - On URL received: if a `localhost:<port>` is found anywhere in the URL (including inside query params like `redirect_uri`), starts `proxyOAuthCallback` in a goroutine
  - Interactive SSH session runs via the control socket
  - On exit: cancels the ntfy subscription, tears down the control master, removes socket — no orphan processes
- `setup.sh` — bootstraps a remote host: installs the `xdg-open` shim to `~/.local/bin/` and writes `config.toml`. Called via `just setup <host>`.
- `shell.zsh` — stub, sourced by dotfiles for compatibility. No logic remains here.
- `justfile` — `build`, `run`, `setup`

## OAuth callback proxy

When a URL contains `localhost:<port>` (top-level or in a query param), `nssh`:
1. Listens on that port locally
2. Accepts exactly one connection (the browser's OAuth callback GET)
3. Forwards it to the remote VM via `ssh -S <socket> -W localhost:<port> <host>` (reuses control master, no reauth)
4. Returns the response to the browser
5. Closes listener and tunnel immediately — one-shot, nothing lingers

## Topic convention

`<NSSH_NTFY_BASE>/reverse-open-<short-hostname>` — base defaults to `https://ntfy.abizer.dev`, overridable via `NSSH_NTFY_BASE` env var.

## Key constraints

- Only forward `http://` and `https://` URLs
- Never eval or execute received content on local side
- Graceful fallback to plain `ssh` when hostname can't be resolved
- stdlib only — no external Go dependencies
