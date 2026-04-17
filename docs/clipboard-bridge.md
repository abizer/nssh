# Clipboard bridge over the ntfy tunnel

Status: Planned
Owner: abizer
Last updated: 2026-04-14

## Goal

Bridge the macOS system clipboard to/from remote VMs we connect to via `nssh`,
for payloads of any size (large text, binary, images), transparently to any
remote program that already speaks `xclip` / `wl-copy` / `wl-paste`. Extend the
existing ntfy-based reverse tunnel that already carries `xdg-open` calls.

## Non-goals

- A general-purpose clipboard manager.
- A persistent background daemon on the Mac. The clipboard bridge lives inside
  the per-VM `nssh` process and dies with the connection. This is deliberate:
  `nssh` already owns connection lifecycle, so the subscriber, the mac-side
  clipboard reader, and the ntfy auth all follow it for free.
- Inventing a new terminal ↔ app protocol for image paste. Several terminal
  apps (Claude Code, others) already handle Ctrl+V image paste locally via
  whatever mechanism their host terminal (Ghostty, iTerm2, etc.) exposes.
  Our job is to make that same mechanism resolve remotely — the exact shape
  depends on the local protocol, see [Image paste over the bridge](#image-paste-over-the-bridge).
- Defending against a compromised VM. Security is out of scope for v1; the
  existing tunnel's threat model (self-hosted ntfy, deny-all ACL with a
  wildcard rw grant to `reverse-open-*`, host-scoped topics, 72h retention,
  TLS) is accepted as-is.

## Background

`nssh` is a thin Go wrapper around `ssh` / `mosh` that, among other things,
spawns a goroutine (`subscribeNtfy`, main.go:134) which long-polls
`https://ntfy.abizer.dev/reverse-open-<host>` and acts on incoming messages.
Today the only action is opening URLs locally in macOS when a remote program
runs `xdg-open <url>` — the remote side has a shell shim at `~/.local/bin/xdg-open`
(installed by `setup.sh`) which `curl`s the URL into the topic.

The ntfy deployment at `~/src/noor/apps/ntfy/deployment.yaml` has
`NTFY_AUTH_DEFAULT_ACCESS=deny-all` globally, but grants
`*:reverse-open-*:rw` — anonymous read/write on any topic under the
`reverse-open-` prefix (deployment.yaml:87-88). No tokens are needed, by design.
Attachment size and retention use ntfy v2.20.1 defaults (~15MB per attachment,
72h retention). Those defaults are acceptable for v1; if we ever ship something
bigger, bump the Helm config.

The clipboard bridge extends the same tunnel to also carry clipboard data.

## Paste semantics

A key realisation that shapes the design: on a remote terminal session,
"paste" actually has two completely different meanings depending on the
destination, and only one of them involves the bridge.

### Text pasted via `Cmd+V` / `Ctrl+V` keystroke in a terminal

Already works today, bridge uninvolved. Ghostty reads the Mac pasteboard via
Cocoa, types the characters into its PTY (optionally bracketed-paste wrapped),
mosh-client ships them over UDP to mosh-server, and the remote app reads them
from stdin like any other typed input. No clipboard object exists on the VM.
Large text works fine; it's just more UDP frames. Mosh's 256-byte ceiling
applies only to OSC 52 escape sequences, not to raw PTY input.

The bridge does not need to do anything for this case and must not break it.

### Explicit clipboard access from a remote program

When a remote program calls `xclip -selection clipboard -i/-o`, `wl-copy`,
`wl-paste`, GTK/Qt clipboard APIs, nvim's `+` register, tmux's `copy-pipe`,
`pass -c` (password manager), `gh pr view | xclip`, etc., the program is
explicitly asking the X11/Wayland server for clipboard data — it is *not*
reading from stdin. On a headless VM this normally fails (no X server), or
reaches an X server that has nothing to do with your Mac pasteboard.

This is where the bridge slots in. We install a shadow `xclip` / `wl-copy` /
`wl-paste` on the remote that speaks the same CLI as the real tools but
routes reads and writes through ntfy to the local nssh process, which
touches the real macOS pasteboard.

### Image paste over the bridge

The image-paste case initially seemed like it might require touching the
terminal ↔ app protocol, but research into how Claude Code actually handles
image paste shows the bridge's existing integration point — the shadow
`xclip` / `wl-paste` shim — is exactly the right place.

**Claude Code does not receive image bytes through the PTY.** Regardless of
terminal, CC reads the host OS clipboard directly from its own process via
native APIs:

- **macOS**: `NSPasteboard` via the Objective-C runtime, linking AppKit
  directly (the CC binary has `NSBitmapImageRep`, `NSPasteboard` symbols).
- **Linux / X11**: shells out to `xclip -selection clipboard -t image/png -o`.
- **Linux / Wayland**: shells out to `wl-paste --type image/png`.
- **Windows**: Win32 clipboard API.

Ctrl+V in CC is intercepted by CC as an internal keybind; it never becomes a
terminal paste event. The terminal emulator (Ghostty, iTerm2, WezTerm, Kitty)
is a bystander. This is also why Cmd+V on macOS-local sometimes silently
fails on images: Cmd+V is captured by the terminal emulator, which tries to
paste from *its* view of the clipboard and emits nothing usable for raw image
data, while Ctrl+V is CC's own keybind and invokes NSPasteboard directly.

For the remote case we care about (CC running on a Linux VM over mosh), this
means CC on the VM will invoke `xclip -selection clipboard -t image/png -o`
or `wl-paste --type image/png` when the user presses Ctrl+V. **Our xclip /
wl-paste shim is therefore the exact, correct integration point** — no PTY
interception, no escape-sequence work, no CC changes. CC thinks it's talking
to a local clipboard; the shim routes the request through ntfy to the Mac.

### Prior art: cc-clip

[cc-clip](https://github.com/ShunmeiCho/cc-clip) already built exactly this
shadow-xclip architecture for Claude Code, and independently confirmed CC's
behaviour by tracing its syscalls. cc-clip's transport layer is different:
a local HTTP daemon on `127.0.0.1:18339` fronted by `ssh RemoteForward
127.0.0.1:18339 127.0.0.1:18339` — which **does not work over mosh**,
because mosh does no port forwarding. That limitation is the reason the
ntfy-backed design exists in the first place.

Our design is a strict superset of cc-clip's: same remote-side shim CLI
surface, same integration point, but an ntfy-based transport that survives
mosh and network roaming. We should borrow cc-clip's shim argv handling
idioms (and possibly the header name conventions) for consistency and
discoverability.

### macOS-side clipboard reader: not `pbpaste`

`pbpaste` is **text-only** and cannot read image data from the pasteboard.
To read PNG bytes out of `NSPasteboard.general`, v1 has three choices:

1. **`pngpaste`** ([github.com/jcsalterego/pngpaste](https://github.com/jcsalterego/pngpaste)) — a 100-line ObjC utility that does exactly `NSPasteboard.general.data(forType: .png)` and writes to stdout. Install via brew. Cleanest option. nssh's README can note it as a required dependency.
2. **`osascript -e 'the clipboard as «class PNGf»'`** — returns a hex-encoded `{data PNGf...}` blob that has to be parsed and unwrapped. Works with zero extra install but is gross.
3. **20 lines of Swift / Cgo inside `nssh`** — talk to `NSPasteboard` from Go via Cgo and the ObjC runtime. Most invasive, but fully self-contained.

Recommendation: **option 1 (`pngpaste`)** for v1, with the `osascript` path
as a fallback if `pngpaste` isn't on PATH. The setup.sh equivalent can
`command -v pngpaste || echo "install pngpaste via brew"` at nssh install
time on macOS.

### Known limitation: apps that read X11 selections natively

Tools that bypass the xclip CLI entirely and talk to X11 `SelectionRequest`
events directly (cc-clip's README specifically calls out Codex CLI) will
not be caught by our shim, because there's no `xclip` invocation to
intercept. cc-clip solves this by standing up an Xvfb server with a custom
X11 bridge — that's a meaningful amount of code and it's out of scope for
v1. File a follow-up if/when we hit this with an app we care about.

### Paths that already work, fully covered by the xclip shim alone

- Claude Code image paste over mosh (via `xclip -t image/png -o`).
- Remote scripts that call `xclip -t image/png -o` or `wl-paste --type image/png`.
- Remote GUI apps whose GTK/Qt clipboard backends shell out to `xclip` / `wl-paste` under the hood (not all of them do, some link X11 directly — see limitation above).
- Ad-hoc "save my Mac screenshot to a file on the VM": `xclip -selection clipboard -t image/png -o > /tmp/shot.png`.

### Not building: OSC 5522 and DPM 5252

Kitty has drafted [OSC 5522](https://sw.kovidgoyal.net/kitty/clipboard/)
as a MIME-typed extension to OSC 52, and Tim Culverhouse has proposed
[DEC Private Mode 5252](https://rockorager.dev/misc/bracketed-paste-mime/)
for unsolicited paste MIME-type announcements. Neither is shipping across
terminals. Even if Ghostty and Kitty land both tomorrow, Claude Code would
also have to switch from "read OS clipboard directly" to "consume
terminal-mediated paste", which is a bigger architectural shift than we
should assume. **Don't design around these.** Revisit if CC ever starts
emitting a specific escape sequence on paste.

## Design

### One shim, many personas

Do not symlink or re-exec the `nssh` Go binary for this. Instead, keep the
existing `setup.sh`-installed shell shim pattern and generalise it: one script
(`nssh-shim` or similar) dropped into `~/.local/bin/` on the remote, and then
symlinked to `xdg-open`, `xclip`, `wl-copy`, `wl-paste`. The shim dispatches
on `$0` (the name it was invoked as) and implements the relevant CLI for each.

This keeps `nssh` focused on connection management and keeps the "speak
ntfy from the remote side" surface contained to a single script that's easy
to read, easy to rev, and easy to diff in setup.sh.

Dispatch sketch:

```sh
#!/usr/bin/env bash
# ~/.local/bin/nssh-shim (installed by setup.sh, symlinked as xdg-open/xclip/wl-copy/wl-paste)

set -euo pipefail
source "${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml"  # trivially-sourceable

case "$(basename "$0")" in
  xdg-open)  exec "$(dirname "$0")/_shim-xdg-open"  "$@" ;;
  xclip)     exec "$(dirname "$0")/_shim-xclip"     "$@" ;;
  wl-copy)   exec "$(dirname "$0")/_shim-wl-copy"   "$@" ;;
  wl-paste)  exec "$(dirname "$0")/_shim-wl-paste"  "$@" ;;
  *) echo "nssh-shim: unknown persona $0" >&2; exit 2 ;;
esac
```

(Or keep everything in one file and use `case` branches for each persona. The
file split is stylistic.)

### Topic reuse

We **reuse the existing `reverse-open-<host>` topic**. The existing ACL already
covers it, config.toml already points at it, and the subscriber goroutine is
already running. What changes is the wire format: the current message body is
a raw URL string that `handleMessage` sniffs via `strings.HasPrefix(body, "http")`.
We replace that with a structured JSON envelope:

```json
{"kind":"open","url":"https://..."}
{"kind":"clip-write","mime":"text/plain;charset=utf-8","body":"<=3KB inline"}
{"kind":"clip-write","mime":"image/png","attachment":"https://ntfy.../.../attachment/abc.png"}
{"kind":"clip-read-request","id":"uuid-v4","mime":"text/plain"}
{"kind":"clip-read-response","id":"uuid-v4","mime":"text/plain","body":"..."}
{"kind":"clip-read-response","id":"uuid-v4","mime":"image/png","attachment":"https://..."}
```

`handleMessage` becomes a switch on `kind`. For the transition window,
accept a bare URL body as `{"kind":"open","url":<body>}` so the old shim
keeps working until rolled out.

**Size threshold for inline vs attachment**: 3KB of body text inline, anything
larger or binary goes via ntfy attachment. The threshold is a constant in the
shim, tunable later.

**Correlation-id matching**: reads are request/response. The shim generates
a UUID, publishes a `clip-read-request`, then long-polls the same topic with
`since=<now>` filtering on `clip-read-response` envelopes whose `id` matches,
with a default timeout of 3s. On the local side, nssh's subscriber sees the
request, reads the Mac pasteboard, publishes the response, keeps going.

### Local-side clipboard I/O

Inside `main.go`, `handleMessage` grows a dispatcher. New cases:

- **`clip-write` text inline**: decode body, run `pbcopy`.
- **`clip-write` attachment**: `http.Get` the attachment URL, pipe bytes to the appropriate sink:
  - text → `pbcopy`
  - `image/*` → write to a temp file, run `osascript -e 'set the clipboard to (read (POSIX file "…") as «class PNGf»)'` (or equivalent for jpeg/tiff), cleanup.
- **`clip-read-request`**:
  - `text/*`: run `pbpaste`, publish `clip-read-response` with body inline or as attachment depending on size.
  - `image/png`: run `pngpaste -` (reads PNG bytes to stdout), `http.PUT` the bytes as an ntfy attachment, publish response referencing it. Fall back to `osascript -e 'the clipboard as «class PNGf»'` + hex-unwrap if `pngpaste` is not on PATH.
  - If the Mac pasteboard doesn't contain the requested MIME, publish a response with empty body and an `error` field (or just empty body; the shim treats empty as "nothing to paste" and exits 0 with no output, matching xclip semantics for an empty selection).

A tiny helper `mac_clipboard.go` encapsulates the four operations:
`readText`, `writeText`, `readImagePNG(destPath)`, `writeImagePNG(srcPath)`.
- `readText` / `writeText`: thin wrappers over `pbpaste` / `pbcopy`.
- `writeImagePNG`: wraps `osascript -e 'set the clipboard to (read (POSIX file "…") as «class PNGf»)'`.
- `readImagePNG`: prefers `pngpaste -` (binary-safe PNG reader), falls back to the osascript hex-unwrap route.

No Cgo, no SwiftPM, just `exec.Command`. macOS-only is fine — nssh's local
side is macOS-only by definition. `pngpaste` becomes a soft dependency
(nssh should warn at startup if `pngpaste` is missing and image paste will
fall back to the slower osascript path).

### Remote shim behaviour per persona

**`xclip` shim** (covers the common flags; everything else passes through
to real `/usr/bin/xclip` if installed, else errors):

```
xclip -selection clipboard -i                    → read stdin, POST clip-write (text)
xclip -selection clipboard -i -t image/png       → read stdin bytes, PUT attachment, POST clip-write (image/png)
xclip -selection clipboard -o                    → publish clip-read-request text, await response, write to stdout
xclip -selection clipboard -o -t image/png       → publish clip-read-request image/png, await response, GET attachment, write bytes to stdout
xclip -selection primary ...                     → fall through: ignore (we only bridge CLIPBOARD, not PRIMARY)
```

Only `-selection clipboard` is bridged. PRIMARY is an X-ism that doesn't map
cleanly to macOS and isn't worth the complexity. Anything that expects it
falls through to the real xclip if present, or no-ops.

**`wl-copy` / `wl-paste` shims** are the Wayland equivalents with analogous
flag handling. These exist specifically for Wayland VMs that don't ship xclip.

**`xdg-open` shim** keeps its current behaviour: publish
`{"kind":"open","url":...}` when the arg looks like an http(s) URL, fall
through to real xdg-open otherwise. Just repackaged from plain-URL body to
JSON envelope.

### config.toml

No new keys required. `config.toml` already stores the ntfy URL, which is
the same topic we reuse. The shim sources it, the remote side curl-ifies.

### tmux integration

After the shim is rolled out, rebind tmux copy-mode yank to pipe through
the shim instead of relying on `set-clipboard on`:

```tmux
bind -T copy-mode-vi y send -X copy-pipe-and-cancel 'xclip -selection clipboard -i'
```

This makes tmux yanks of any size route through the bridge uniformly,
sidestepping mosh's OSC 52 256-byte cap. The `set-clipboard on` + terminfo
`clipboard` feature from the terminal-features fix stays in place as a
fallback for non-tmux SSH sessions and tiny selections where OSC 52 is fine.

This tmux.conf change lands in the dotfiles repo, not in ssh-reverse-ntfy.

### Dotfiles install step

In `~/.config/dotfiles/modules/linux/home.nix` (where the `xdg-open` shim is
currently installed per the earlier investigation), extend the install to
also place `xclip`, `wl-copy`, and `wl-paste` as symlinks to the same
`nssh-shim` script. Nix makes this trivial.

## Implementation plan

Tasks in roughly dependency order. Each is small enough to be a single commit.

1. **Wire format switch — backwards-compatible** (ssh-reverse-ntfy)
   Extend `handleMessage` in main.go to accept either a bare URL body (legacy)
   or a JSON envelope with a `kind` field. Add an `envelope` struct and a
   dispatch switch currently containing only one case: `open`. Land and test
   that the existing xdg-open flow still works against both formats.

2. **Mac clipboard helper** (ssh-reverse-ntfy)
   New file `mac_clipboard.go` with `readText`, `writeText`, `readImagePNG`,
   `writeImagePNG`. Unit tests gated on `runtime.GOOS == "darwin"`.

3. **Attachment upload/download helpers** (ssh-reverse-ntfy)
   Tiny wrappers over `http.Put` / `http.Get` against `$NTFY_BASE/<topic>/…`,
   returning a URL for upload and a `[]byte` for download. Handles the ntfy
   attachment URL convention.

4. **Clipboard handlers** (ssh-reverse-ntfy)
   Add `clip-write`, `clip-read-request`, `clip-read-response` cases to the
   dispatcher. `clip-read-request` cases spawn a response publish. Response
   matching (by `id`) lives in a small in-process map keyed by pending IDs.

5. **Generalised shim script** (ssh-reverse-ntfy/setup.sh)
   Replace the current inline `xdg-open` heredoc with a multi-persona script
   (`nssh-shim`). Update setup.sh to install the script and symlink it as
   `xdg-open` on the remote for backwards compatibility, with install paths
   parameterised so dotfiles can hook the same install.

6. **xclip persona in the shim** (ssh-reverse-ntfy)
   Implement `-selection clipboard [-t mime] -i|-o`. Unknown flags fall
   through to `/usr/bin/xclip` if present, else exit 1.

7. **wl-copy / wl-paste personas** (ssh-reverse-ntfy)
   Same as xclip but with Wayland flag surface.

8. **Dotfiles install** (dotfiles repo)
   Symlink `xclip`, `wl-copy`, `wl-paste` in `~/.local/bin` on Linux hosts,
   pointing at `nssh-shim`. Keep the existing `xdg-open` symlink.

9. **tmux copy-mode rebind** (dotfiles repo)
   Change copy-pipe to `xclip -selection clipboard -i`. Leaves the existing
   `set-clipboard on` + `terminal-features …:clipboard` in place.

10. **Drop legacy URL body support** (ssh-reverse-ntfy, follow-up)
    After a grace window, remove the bare-URL branch from `handleMessage`.
    All callers now speak the JSON envelope.

## Testing

For each of the scenarios below, verify the end-to-end path with a fresh
`nssh` session (kill any existing tmux/nssh to avoid stale shims):

- `echo hello | xclip -selection clipboard -i` on the VM → `pbpaste` on Mac returns `hello`.
- `dd if=/dev/urandom bs=1 count=1000000 | base64 | xclip -selection clipboard -i` → Mac pbpaste returns the full 1MB base64 string.
- `xclip -selection clipboard -o` on the VM with a Mac clipboard of "world" → prints `world`.
- `screencapture -ci` on Mac (copies a region to clipboard as PNG), then on VM: `xclip -selection clipboard -t image/png -o > /tmp/shot.png && file /tmp/shot.png` → confirms PNG.
- A tmux copy-mode yank of 10KB of text over mosh → Mac pbpaste returns the full text.
- `xdg-open https://example.com` on the VM still opens Safari on Mac (regression check).
- `echo foo | wl-copy` on a Wayland VM → `pbpaste` on Mac returns `foo`.

## Open risks / deferred work

- **Concurrent read requests across multiple VMs**: if two VMs ask for a clipboard read in the same ~100ms window, responses may collide. Correlation-id filtering handles this on the shim side, but the subscriber needs to be sure it's responding from the correct VM's context. Since each VM has its own `reverse-open-<host>` topic, this is moot — cross-talk can't happen.
- **Long-running nssh sessions and clipboard watchers**: we deliberately don't subscribe to pasteboard changes. A `clip-write` from a VM is pushed; a `clip-read-request` is pulled. No polling of the Mac pasteboard. Keeps the design boring.
- **Image formats beyond PNG**: `image/jpeg`, `image/tiff` can be added symmetrically. Not needed for v1.
- **Attachment size cap**: ntfy defaults apply. A screenshot PNG is typically 1-5MB, well within limits. Bump `NTFY_ATTACHMENT_FILE_SIZE_LIMIT` in the Helm values if we start hitting the ceiling.
- **No encryption**: payloads traverse self-hosted ntfy in plaintext (TLS over the wire, plaintext at rest for the 72h retention window). Acceptable per non-goals. If it stops being acceptable, add a shared-secret-derived AEAD envelope in the shim and the local handler; the topic body and attachment bytes become ciphertext.
