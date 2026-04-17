# CLAUDE.md

Go multi-binary project: `nssh` (local Mac side) and `nssh-shim` (remote Linux
side). Together they bridge clipboard, `xdg-open` URLs, and OAuth callbacks
between local macOS and remote VMs over ntfy pub/sub — surviving mosh, NAT,
and network roaming.

## Repo layout

```
cmd/nssh/              Local-side binary (SSH/mosh wrapper, ntfy subscriber)
cmd/nssh-shim/         Remote-side binary (symlinked as xclip, wl-copy, xdg-open, etc.)
internal/wire/         Shared envelope type and parser
internal/ntfy/         Shared ntfy HTTP helpers (publish, attach, fetch)
internal/clipboard/    macOS pasteboard helpers (pbcopy, pbpaste, pngpaste, osascript)
docs/                  Design docs
setup.sh               Installs nssh-shim + symlinks + config on a remote host
justfile               Build recipes
flake.nix              Nix packages for nssh and nssh-shim
```

## Building

```bash
just build          # builds cmd/nssh for local platform
just build-shim     # cross-compiles cmd/nssh-shim for linux/amd64
just setup <host>   # builds shim + scp + symlinks on remote
just test           # runs all tests
```

## Architecture

### nssh (local, macOS)

- Wraps `ssh` or `mosh` with automatic transport selection
- Subscribes to `ntfy.abizer.dev/reverse-open-<host>` in a background goroutine
- Dispatches incoming messages by `kind`:
  - `open`: opens URL locally, proxies OAuth callbacks via fresh `ssh -W`
  - `clip-write`: writes to macOS clipboard (text via pbcopy, images via osascript)
  - `clip-read-request`: reads macOS clipboard, publishes response back to ntfy

### nssh-shim (remote, Linux)

- Single static binary, symlinked as `xdg-open`, `xclip`, `wl-copy`, `wl-paste`
- Dispatches on `os.Args[0]` (persona), parses the relevant CLI flags
- Publishes clipboard data / URL open requests to ntfy as JSON envelopes
- For clipboard reads: publishes a request with correlation ID, subscribes to
  the topic stream with a 5s timeout waiting for the matching response

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
