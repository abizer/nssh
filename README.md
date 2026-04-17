# ssh-reverse-ntfy

Forward clipboard, `xdg-open` calls, and OAuth callbacks between remote SSH/mosh sessions and your local Mac, using [ntfy](https://ntfy.sh) as a message bus. Clipboard bridging works for text of any size and images (PNG), enabling tools like Claude Code to paste screenshots over SSH. Transparently uses `mosh` when both sides have it, for roaming across sleep/network changes; falls back to plain `ssh` otherwise.

```
Remote Host                        ntfy                          Local Mac
┌──────────────┐                ┌───────────┐                ┌──────────────┐
│ xdg-open URL ├───POST /topic──▶           ├───subscribe────▶  open $URL   │
│ xclip -i     ├───POST /topic──▶           ├───subscribe────▶  pbcopy      │
│ xclip -o     ├───POST /topic──▶           ├───subscribe────▶  pbpaste ────┐
│              ◀──────────────────────────────────────────────── response  ◀─┘
└──────────────┘                └───────────┘                └──────────────┘
```

## Why?

**URLs:** CLI tools on remote dev servers (`gh`, `gcloud`, `npm login`) try to open a browser. This fails silently over SSH. nssh forwards those URLs to your local machine — and for OAuth flows with a `localhost` callback, it automatically proxies the callback back.

**Clipboard:** Mosh limits OSC 52 clipboard to ~256 bytes and doesn't support images at all. SSH port forwarding doesn't survive network changes. nssh bridges the full clipboard (text and images, any size) through ntfy, which survives mosh, NAT, and network roaming. Remote tools that use `xclip` or `wl-copy`/`wl-paste` — including Claude Code's Ctrl+V image paste — work transparently.

## Install

**Local (macOS):**
```bash
just build     # builds ./nssh
just install   # copies to ~/.local/bin/nssh and ad-hoc signs it

# Optional: for clipboard image support
brew install pngpaste
```

**Remote (one-time per host):**
```bash
just setup devbox
```

This cross-compiles `nssh` for linux/amd64, copies it to the remote host, and symlinks it as `xdg-open`, `xclip`, `wl-copy`, `wl-paste`. Ensure `~/.local/bin` is in PATH on the remote (before `/usr/bin`).

## Usage

```bash
nssh devbox                      # auto-select: mosh if available, else ssh
nssh --ssh devbox                # force plain ssh
nssh --mosh devbox               # force mosh, skip remote probe

# Inside the remote session:
xdg-open https://example.com     # opens in your local browser
gh auth login --web              # OAuth flow completes locally, including callback
echo hello | xclip -sel clip -i  # copies "hello" to your Mac clipboard
xclip -sel clip -o               # prints your Mac clipboard contents
xclip -sel clip -t image/png -o > shot.png  # pulls a Mac screenshot to a file
```

Claude Code image paste (Ctrl+V) works automatically — CC calls `xclip` under the hood, which our shim intercepts.

## Architecture

One binary, everywhere. `nssh` dispatches on `argv[0]`:

| argv[0] | Mode | Description |
|---------|------|-------------|
| `nssh` (or anything else) | session | SSH/mosh wrapper + ntfy subscriber |
| `xclip` | shim | Clipboard bridge via ntfy |
| `wl-copy` / `wl-paste` | shim | Wayland clipboard bridge |
| `xdg-open` / `sensible-browser` | shim | URL forwarding |

```
cmd/nssh/              The single binary
internal/wire/         Shared JSON envelope type
internal/ntfy/         Shared ntfy HTTP helpers
internal/clipboard/    macOS pasteboard helpers (pbcopy, pngpaste, osascript)
```

### Wire format

JSON envelopes on a per-host ntfy topic (`reverse-open-<hostname>`):

| Kind | Direction | Description |
|------|-----------|-------------|
| `open` | remote → local | Open a URL in the local browser |
| `clip-write` | remote → local | Write data to the Mac clipboard |
| `clip-read-request` | remote → local | Request Mac clipboard contents |
| `clip-read-response` | local → remote | Response with clipboard data |

Small text (≤3KB) is base64-inlined. Larger payloads and images go as ntfy attachments.

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `NSSH_NTFY_BASE` | `https://ntfy.abizer.dev` | ntfy server base URL |

## Security

- **URL-only** — only `http://` and `https://` URLs are forwarded
- **No eval** — received content is never executed
- **CLIPBOARD only** — PRIMARY selection is not bridged
- **One-shot OAuth proxy** — port listeners close after a single request
- **Graceful fallback** — plain `ssh` if ntfy is unreachable

Self-host ntfy for isolation: [docs.ntfy.sh/install](https://docs.ntfy.sh/install/).

## Requirements

- **Local:** macOS, Go (to build), `pngpaste` (for clipboard images — `brew install pngpaste`)
- **Remote:** Linux with `~/.local/bin` in PATH. No runtime dependencies — nssh cross-compiles as a static Go binary.
- **Optional:** `mosh` on both ends for session roaming

## License

MIT
