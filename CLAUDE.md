# CLAUDE.md

Shell scripts for forwarding `xdg-open` URLs from remote SSH sessions to local browser via ntfy.sh pub/sub.

## Architecture

- `shell.zsh` — local `ssh-ntfy` wrapper: reads ntfy URL from `~/.config/ssh-ntfy/config.toml` (or generates random topic on ntfy.sh), subscribes via curl
- `xdg-open` — remote shim: resolves ntfy endpoint from `config.toml` (fixed) or `$URL_FORWARD_NTFY` env var (random), POSTs URLs, falls through to real xdg-open otherwise
- `~/.config/ssh-ntfy/config.toml` — shared config format on both local and remote: `url = "https://..."`
- Two modes:
  - **Fixed topic** (`config.toml` exists): both sides read their own config file, no `SendEnv`/`AcceptEnv` needed
  - **Random topic** (default): env var passed via `SendEnv`/`AcceptEnv`, requires sshd_config

## Key constraints

- Random topics must be unguessable (128-bit random)
- Only forward `http://` and `https://` URLs
- Never eval or execute received content on local side
- Graceful fallback when ntfy unreachable
