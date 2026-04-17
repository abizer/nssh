# CLAUDE.md

Single Go binary (`nssh`) that runs everywhere. On the local side it wraps
SSH/mosh sessions and subscribes to ntfy for incoming clipboard and URL
messages. On remote hosts the same binary is symlinked as `xclip`, `wl-copy`,
`wl-paste`, and `xdg-open` — dispatching on `argv[0]` to act as a clipboard
bridge and URL forwarder over ntfy pub/sub.

## Repo layout

```
cmd/nssh/              The single binary (session wrapper + shim + --infect, dispatched on argv[0])
internal/wire/         Shared envelope type and parser
internal/ntfy/         Shared ntfy HTTP helpers (publish, attach, fetch)
internal/clipboard/    macOS pasteboard helpers (pbcopy, pbpaste, pngpaste, osascript)
docs/                  Design docs
.github/workflows/     CI (cachix.yaml for nix, release.yml for tagged releases)
justfile               Build recipes
flake.nix              Nix package
```

## Building

```bash
just build          # builds nssh for local platform
just install        # copies nssh to ~/.local/bin/ and ad-hoc signs it
just test           # runs all tests
```

## Remote setup

```bash
nssh --infect <host>
```

Downloads the matching binary from the latest GitHub release, scps it to
the remote, and sets up the shim symlinks. No build tooling or config
required on the remote.

## Architecture

### Dispatch on argv[0]

| argv[0] | Mode | Description |
|---------|------|-------------|
| `nssh` (or anything else) | session | SSH/mosh wrapper + ntfy subscriber |
| `xclip` | shim | Clipboard bridge via ntfy |
| `wl-copy` / `wl-paste` | shim | Wayland clipboard bridge via ntfy |
| `xdg-open` / `sensible-browser` | shim | URL forwarding via ntfy |

### Session mode (local)

- Wraps `ssh` or `mosh` with automatic transport selection
- Generates a random topic (or reads a pinned one from config), writes it to
  the remote's `~/.config/nssh/session`, subscribes to ntfy in a background goroutine
- Dispatches incoming messages by `kind`:
  - `open`: opens URL locally, proxies OAuth callbacks via fresh `ssh -W`
  - `clip-write`: writes to macOS clipboard (text via pbcopy, images via osascript)
  - `clip-read-request`: reads macOS clipboard, publishes response back to ntfy

### Shim mode (remote)

- Dispatches on `os.Args[0]` (persona), parses the relevant CLI flags
- Publishes clipboard data / URL open requests to ntfy as JSON envelopes
- For clipboard reads: publishes a request with correlation ID, subscribes to
  the topic stream with a 5s timeout waiting for the matching response
- Handles `xclip -t TARGETS -o` by returning a static list of supported types
  (image/png, text/plain, etc.) — Claude Code probes this before attempting
  image paste via Ctrl+V

### Wire format

JSON envelopes on the ntfy topic. Every message has a `kind` field:

| Kind | Direction | Description |
|------|-----------|-------------|
| `open` | remote → local | Open a URL in the local browser |
| `clip-write` | remote → local | Write data to the Mac clipboard |
| `clip-read-request` | remote → local | Request the Mac clipboard contents |
| `clip-read-response` | local → remote | Response with clipboard data |

Small text (≤3KB) is base64-encoded inline in the `body` field. Larger payloads
and images are sent as ntfy attachments (PUT with `Filename` + `X-Message` headers).

### Topic convention

Each connection gets a random topic (`nssh_<random>`) by default — unguessable,
no config required. nssh writes the server + topic to `~/.config/nssh/session`
on the remote before launching the shell. The shim reads this file.

Optional `~/.config/nssh/config.toml` on either side to pin values:
```toml
server = "https://ntfy.example.com"  # default: https://ntfy.sh
topic = "my-fixed-topic"             # default: random per-connection
```

Priority: `NSSH_NTFY_BASE` env > config.toml > session file > defaults.

## Key constraints

- stdlib only — no external Go dependencies
- Single binary cross-compiles for macOS and Linux with zero runtime deps
- Never eval or execute received content on local side
- Only bridge CLIPBOARD selection, not PRIMARY
