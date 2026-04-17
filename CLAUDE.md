# CLAUDE.md

Single Go binary (`nssh`) that runs everywhere. On the local side it wraps
SSH/mosh sessions and subscribes to ntfy for incoming clipboard and URL
messages. On remote hosts the same binary is symlinked as `xclip`, `wl-copy`,
`wl-paste`, and `xdg-open` — dispatching on `argv[0]` to act as a clipboard
bridge and URL forwarder over ntfy pub/sub.

## Repo layout

```
cmd/nssh/              The single binary (session wrapper + shim, dispatched on argv[0])
internal/wire/         Shared envelope type and parser
internal/ntfy/         Shared ntfy HTTP helpers (publish, attach, fetch)
internal/clipboard/    macOS pasteboard helpers (pbcopy, pbpaste, pngpaste, osascript)
docs/                  Design docs
setup.sh               Cross-compiles + installs nssh + symlinks on a remote host
justfile               Build recipes
flake.nix              Nix package
```

## Building

```bash
just build          # builds nssh for local platform
just build-linux    # cross-compiles nssh for linux/amd64
just install        # copies nssh to ~/.local/bin/ and ad-hoc signs it
just setup <host>   # cross-compiles + scp + symlinks on remote
just test           # runs all tests
```

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
- Subscribes to `ntfy.abizer.dev/reverse-open-<host>` in a background goroutine
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

`<NSSH_NTFY_BASE>/reverse-open-<short-hostname>` — defaults to
`https://ntfy.abizer.dev`, overridable via `NSSH_NTFY_BASE`.

## Key constraints

- stdlib only — no external Go dependencies
- nssh-shim must cross-compile as a static binary with zero runtime deps
- Never eval or execute received content on local side
- Only bridge CLIPBOARD selection, not PRIMARY
