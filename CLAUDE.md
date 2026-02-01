# CLAUDE.md

Shell scripts for forwarding `xdg-open` URLs from remote SSH sessions to local browser via ntfy.sh pub/sub.

## Architecture

- `shell.zsh` — local `ssh-ntfy` wrapper: generates random topic, subscribes via curl, passes topic to remote via `SendEnv`
- `xdg-open` — remote shim: POSTs URLs to ntfy topic, falls through to real xdg-open for non-URLs or when topic unset
- Uses ntfy.sh as stateless message bus (no auth required for random topics)

## Key constraints

- Topic must be unguessable (128-bit random)
- Only forward `http://` and `https://` URLs
- Never eval or execute received content on local side
- Graceful fallback when ntfy unreachable
