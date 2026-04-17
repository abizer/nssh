# nssh

_Written by [Claude Opus 4.7](https://www.anthropic.com/news/claude-opus-4-7) via Claude Code_

Paste images into [Claude Code](https://claude.ai/claude-code) over SSH. Also bridges text clipboard, `xdg-open` URLs, and OAuth callbacks between remote sessions and your local machine — over SSH or mosh.

## The problem

You're running Claude Code on a remote dev server. You want to Ctrl+V a screenshot from your laptop so Claude can see your UI bug. But there's no clipboard bridge — the remote machine has no idea what's on your Mac's pasteboard.

The usual workarounds are painful: `scp` the screenshot over, `base64`-encode it, set up X11 forwarding, or just describe the bug in words. If you're using mosh (because your WiFi is flaky or you close your laptop between sessions), it's even worse — mosh doesn't support port forwarding, SSH ControlMaster multiplexing, or OSC 52 for anything larger than 256 bytes.

nssh fixes this. It's an SSH/mosh wrapper that bridges your clipboard (text and images, any size) through a self-hosted [ntfy](https://ntfy.sh) pub/sub channel. On the remote, a single static binary symlinked as `xclip` intercepts clipboard calls — so Claude Code's Ctrl+V image paste just works, transparently, without any changes to Claude Code itself.

The same channel carries `xdg-open` URLs in the other direction. When `gh auth login` or `gcloud auth login` tries to open a browser on your remote server, nssh forwards the URL to your local machine and proxies the OAuth callback back — even over mosh, where there's no SSH tunnel to piggyback on.

## How it works

```
              ┌─────────────┐
              │  ntfy server │  (self-hosted, per-host topic)
              └──────┬──────┘
        publish      │      subscribe
    ┌────────────────┤├────────────────┐
    ▼                                  ▼
┌────────┐                        ┌────────┐
│ Remote │  nssh symlinked as     │ Local  │  nssh session wrapper
│ Server │  xclip / xdg-open     │ Mac    │  + ntfy subscriber
└────────┘                        └────────┘
```

**Clipboard (laptop → remote):** Take a screenshot on your Mac, Ctrl+V in Claude Code on the remote. Claude Code calls `xclip -t image/png -o` under the hood. Our `xclip` shim publishes a read-request to ntfy. The local nssh process reads your Mac pasteboard via `pngpaste`, publishes the PNG bytes as an ntfy attachment, and the shim delivers them to Claude Code's stdin. ~200ms round trip.

**Clipboard (remote → laptop):** `echo "some text" | xclip -sel clip -i` on the remote publishes to ntfy. The local nssh subscriber writes it to your Mac clipboard via `pbcopy`. Works for text of any size and images.

**URLs + OAuth (remote → laptop):** `xdg-open https://...` on the remote publishes the URL to ntfy. The local nssh opens it in your browser. If the URL contains a `localhost` callback (OAuth flows), nssh spins up a one-shot local listener, proxies the browser's callback to the remote via a fresh `ssh -W`, and tears everything down after one request. Each callback is an independent SSH connection — no ControlMaster, no socket files — so it works identically whether your session is SSH or mosh.

**Why ntfy instead of SSH tunnels?** Mosh is UDP-based and deliberately doesn't tunnel anything — no port forwarding, no Unix sockets, no side channels. The only in-band escape hatch is OSC 52, which mosh caps at 256 bytes and doesn't support for images. ntfy gives us a durable, roaming-safe message bus that survives everything mosh survives: sleep/wake, network changes, NAT traversal.

## Install

**Local (macOS):**
```bash
brew install abizer/tap/nssh
brew install pngpaste          # for clipboard image support
```

Or build from source:
```bash
just install                   # builds ./nssh and drops it in ~/.local/bin/
```

**Remote (one-time per host):**
```bash
nssh --infect devbox
```

`--infect` detects the remote's OS/arch via `uname`, downloads the matching binary from the latest GitHub release (caches it locally), scps it to `~/.local/bin/nssh`, and creates the shim symlinks (`xdg-open`, `xclip`, `wl-copy`, `wl-paste`, `sensible-browser`). Ensure `~/.local/bin` is in PATH on the remote — nssh warns if not.

For nix/home-manager managed hosts, add the flake input and symlinks are set up declaratively.

## Usage

```bash
# Connect (auto-selects mosh if both sides have it)
nssh devbox
nssh --ssh devbox        # force plain SSH
nssh --mosh devbox       # force mosh

# Inside the remote session:

# Paste an image into Claude Code
# Just Ctrl+V — it works. Claude Code calls xclip, our shim handles it.

# Copy text to your Mac clipboard
echo "hello" | xclip -sel clip -i

# Read your Mac clipboard on the remote
xclip -sel clip -o

# Pull a screenshot from your Mac to a file
xclip -sel clip -t image/png -o > screenshot.png

# Open a URL in your local browser
xdg-open https://example.com

# OAuth flows complete automatically
gh auth login --web
gcloud auth login
```

## Architecture

One Go binary, everywhere. nssh dispatches on `argv[0]`:

| Invoked as | Behavior |
|------------|----------|
| `nssh` | SSH/mosh session wrapper + ntfy subscriber |
| `xclip` | Clipboard bridge (read/write text and images via ntfy) |
| `wl-copy` / `wl-paste` | Wayland clipboard bridge |
| `xdg-open` | URL forwarding + OAuth callback proxy |

The same binary cross-compiles for macOS and Linux. On your Mac it runs as the session wrapper; on remotes it's symlinked as the shim personas.

```
cmd/nssh/              Single binary (session + shim, dispatched on argv[0])
internal/wire/         JSON envelope type shared between session and shim modes
internal/ntfy/         ntfy HTTP helpers (publish, attach, fetch)
internal/clipboard/    macOS pasteboard (pbcopy, pbpaste, pngpaste, osascript)
```

### Wire format

JSON envelopes on a per-connection ntfy topic:

| Kind | Direction | Purpose |
|------|-----------|---------|
| `open` | remote → local | Open URL in local browser |
| `clip-write` | remote → local | Write to Mac clipboard |
| `clip-read-request` | remote → local | Request Mac clipboard contents |
| `clip-read-response` | local → remote | Clipboard data response |

Small text (≤3KB) is base64-inlined. Larger payloads and images use ntfy attachments.

## Configuration

**Zero config required.** By default, nssh uses the public [ntfy.sh](https://ntfy.sh) server and generates a random, unguessable topic (`nssh_<random>`) for each connection. The topic is written to `~/.config/nssh/session` on the remote at connect time.

To pin settings, create `~/.config/nssh/config.toml` (on either side):

```toml
server = "https://ntfy.example.com"  # default: https://ntfy.sh
topic = "my-fixed-topic"             # default: random per-connection
```

The `NSSH_NTFY_BASE` environment variable overrides the server.

## Requirements

- **Local:** macOS, Go 1.25+, [`pngpaste`](https://github.com/jcsalterego/pngpaste) (`brew install pngpaste`)
- **Remote:** Linux with `~/.local/bin` in PATH. Zero runtime deps.
- **Optional:** Self-hosted [ntfy](https://docs.ntfy.sh/install/) for privacy (public ntfy.sh works out of the box).
- **Optional:** `mosh` on both ends for session roaming.

## License

MIT
