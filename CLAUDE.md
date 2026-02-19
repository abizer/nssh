# CLAUDE.md

Shell scripts for forwarding `xdg-open` URLs from remote SSH sessions to local browser via ntfy.sh pub/sub.

## Architecture

- `shell.zsh` — sources two functions:
  - `nssh` — SSH wrapper that subscribes to the ntfy topic for the target host, opens URLs locally, then cleans up on exit
  - `nssh-setup` — bootstraps a remote host: installs the `xdg-open` shim to `~/.local/bin/` and writes `config.toml` with the derived ntfy URL
- Topic convention: `https://ntfy.abizer.dev/reverse-open-<short-hostname>`
- Remote `xdg-open` shim reads ntfy URL from `~/.config/ssh-ntfy/config.toml`, POSTs URLs via curl, falls through to real xdg-open otherwise

## Key constraints

- Only forward `http://` and `https://` URLs
- Never eval or execute received content on local side
- Graceful fallback when ntfy unreachable
