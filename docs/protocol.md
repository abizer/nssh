# Protocol & schema reference

Wire format, log schema, and config precedence — everything you'd need
to interoperate with nssh from another tool. For the "why," see
[internals.md](./internals.md).

## Wire envelope

Every message published to the ntfy topic is a JSON object. The Go
type is `wire.Envelope` in `internal/wire/envelope.go`:

```go
type Envelope struct {
    Kind string `json:"kind"`           // required, always set
    URL  string `json:"url,omitempty"`  // open
    Mime string `json:"mime,omitempty"` // clip-write, clip-read-*
    Body string `json:"body,omitempty"` // base64 of inline payload
    ID   string `json:"id,omitempty"`   // clip-read correlation
}
```

`Parse` rejects messages with empty `Kind` — anything that round-trips
through `wire.Parse` has at minimum a kind discriminator.

### Inline vs. attachment

Payloads ride either inside the envelope's `Body` (base64-encoded) or
outside as ntfy attachments. The choice is made by `wire.Publish`:

- `len(data) <= InlineThreshold` (3072 bytes) **and** mime doesn't
  start with `image/` → **inline**, base64 in `Body`, sent as a
  text/plain HTTP POST to the topic.
- Otherwise → **attachment**, raw bytes PUT to the topic, with the
  envelope JSON in the `X-Message` header and a filename
  (`clip.png` for image PNG, `clip.dat` otherwise) in `Filename`.

The threshold is conservative: ntfy.sh's free tier caps inline
messages at 4 KB; staying under 3 KB after base64 expansion (which
adds ~33%) keeps us safe. Attachment uploads have a higher size cap
on ntfy.sh and unlimited on self-hosted.

### Kinds

| Kind | Direction | Required fields | Notes |
|------|-----------|-----------------|-------|
| `open` | remote → local | `url` | URL to open in the laptop's browser. `handleOpen` filters to `http(s)://` only. |
| `clip-write` | remote → local | `mime`, payload | Write data to the macOS clipboard. Empty payload (`Body == ""` and no attachment) is dropped with a stderr message. |
| `clip-read-request` | remote → local | `id`, `mime` | Ask the local side to read its clipboard. The shim subscribes to the topic with `?since=<now>` for 5s, waiting for a matching response. |
| `clip-read-response` | local → remote | `id`, `mime`, payload | Response to `clip-read-request`. The shim filters by `id` so concurrent reads don't cross paths. Body prefixed with `ERROR: ` indicates failure (e.g. clipboard tools missing). |

### MIME conventions

- `text/plain` — default. Inline, no special handling.
- `image/png` — image. Always sent as attachment regardless of size
  (the inline-threshold rule excludes any `image/*` mime). On the local
  side `pngpaste` reads, `osascript` writes (PNGf class).
- Other mimes: passed through. Wire transport is the same as `text/plain`
  (size threshold drives inline-vs-attachment).

The shim's `xclip -t TARGETS -o` returns a static list (`image/png`,
`text/plain`, `UTF8_STRING`, `STRING`) — Claude Code probes this before
trying Ctrl-V on an image, so we list image/png even though we don't
actually inspect the clipboard until asked.

## Log schema

Every nssh process appends JSONL to
`$XDG_STATE_HOME/nssh/nssh.<topic>.jsonl` (default
`~/.local/state/nssh/nssh.<topic>.jsonl`). The Go type is `LogEvent`
in `cmd/nssh/log.go`:

```go
type LogEvent struct {
    TS    string `json:"ts"`              // RFC3339Nano UTC
    Event string `json:"event"`           // see vocabulary below
    Side  string `json:"side,omitempty"`  // "session" (local) or persona name (remote shim)
    PID   int    `json:"pid,omitempty"`

    // Wire-message details (msg-send / msg-recv).
    Kind string `json:"kind,omitempty"`
    Mime string `json:"mime,omitempty"`
    ID   string `json:"id,omitempty"`
    URL  string `json:"url,omitempty"`
    Size int    `json:"size,omitempty"`

    // Session lifecycle.
    Target  string `json:"target,omitempty"`
    Server  string `json:"server,omitempty"`
    Topic   string `json:"topic,omitempty"`
    Version string `json:"version,omitempty"`
    Exit    *int   `json:"exit,omitempty"`
    Mosh    *bool  `json:"mosh,omitempty"`

    // Shim invocation.
    Persona string   `json:"persona,omitempty"`
    Args    []string `json:"args,omitempty"`

    // Error context.
    Err string `json:"err,omitempty"`
}
```

### Event vocabulary

| Event | Emitted by | Fields | Meaning |
|-------|------------|--------|---------|
| `session-open` | local (during `prepareRemote`, written to remote log via SSH heredoc) | `server`, `topic`, `target`, `version` | Local nssh announces itself to the remote at session start. Side is `session-init`. |
| `session-start` | local | `target`, `server` | Local subscriber is starting. |
| `session-end` | local | `exit`, `mosh` | Local interactive session ended. `exit` is `0` on success; `mosh` records which transport was used. |
| `msg-send` | either | `kind`, `mime`, `id`, `url`, `size` | Envelope published to the topic. |
| `msg-recv` | either | `kind`, `mime`, `id`, `url`, `size` | Envelope received from the topic. |
| `msg-unknown` | either | `size` | Topic message that didn't parse as a valid envelope. |
| `shim-start` | remote shim | `persona`, `args` | Shim invocation. |
| `clip-write-empty` | remote shim | `mime` | `xclip -i` / `wl-copy` got empty stdin; nothing to publish. |
| `clip-read-empty` | remote shim | `id` | Local side returned empty clipboard. |
| `clip-read-error` | remote shim | `id`, `err` | Local side returned an `ERROR:` body. |
| `clip-read-timeout` | remote shim | `id` | 5s elapsed without a matching response on the topic. |
| `publish-failed` | remote shim | `kind`, `err` | ntfy publish returned an error (network, 4xx, etc.). |

`size` is the decoded payload length (bytes), not the wire size. For
attachments, it's the attachment's reported size; for inline payloads,
it's the decoded base64 length.

`Exit` and `Mosh` are pointer-typed because they need to record an
explicit zero/false meaning — `exit=0` (success) and `mosh=false`
(used ssh) are real values that should appear in the log, not be
silently dropped by `omitempty`. All other zero-valued fields *are*
dropped.

## Config precedence

Two sources of truth, plus environment overrides:

1. **`$NSSH_NTFY_BASE`** — environment variable. Sets `server` only;
   wins over everything.
2. **`$XDG_CONFIG_HOME/nssh/config.toml`** (default
   `~/.config/nssh/config.toml`) — persistent user config:
   ```toml
   server = "https://ntfy.example.com"
   topic = "my-pinned-topic"
   ```
   Wins over the session file.
3. **`$XDG_STATE_HOME/nssh/session`** (default
   `~/.local/state/nssh/session`) — written by `nssh <host>` on the
   local side and pushed to the remote at session start (via the
   `prepareRemote` SSH heredoc). Same TOML shape, two keys:
   `server`, `topic`. The remote shim reads this to know where to
   publish.
4. **Defaults** — `server = https://ntfy.sh`, `topic = nssh_<random>`
   (12 random bytes of base32, lowercase, prefixed `nssh_`).

The minimal-TOML reader (`readTOML` in `config.go`) handles only
`key = "value"` lines, blank lines, and `# comments`. No sections,
no arrays, no escaping. That's sufficient for both files.

## State directory layout

Local (`~/.local/state/nssh/`):

```
nssh.<topic>.jsonl             # per-topic log; one file per session by default
sessions/<pid>.json            # active-session registry, GC'd on `nssh status`
```

Remote (`~/.local/state/nssh/`):

```
session                        # TOML: server, topic. Read by shims.
nssh.<topic>.jsonl             # log file shared with local — both sides append.
```

The session file on the remote is **per-host**, not per-session. If you
run two `nssh` sessions to the same remote, the second session's
`prepareRemote` overwrites the first's `session` file with its own
topic. Don't do that, or pin a topic in `config.toml`.

## Endpoints touched on ntfy

| Method | Path | Used for |
|--------|------|----------|
| `POST` | `/<topic>` | Publish inline message (text/plain body). |
| `PUT` | `/<topic>` | Publish attachment (binary body, `Filename` + `X-Message` headers). |
| `GET` | `/<topic>/json` | Subscribe (long-poll, newline-JSON stream). |
| `GET` | `/<topic>/json?since=<unix>` | Bounded subscribe (used by the shim's clip-read response wait). |
| `GET` | `<attachment.url>` | Fetch a published attachment. URL is provided in the message's `attachment.url` field. |

The local subscriber holds the long-poll `GET /<topic>/json` open
indefinitely, with a 90-second read deadline (`deadlineConn` in
`session.go`) to detect zombie connections. ntfy sends `event=keepalive`
~every 55s; absence of any data for >90s triggers a reconnect.
