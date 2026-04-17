#!/usr/bin/env bash
# nssh-shim: multi-persona shim dispatched on $0.
# Symlinked as xdg-open, xclip, wl-copy, wl-paste on remote hosts.
# Communicates with the local nssh process via ntfy pub/sub.
set -euo pipefail

CONF="${XDG_CONFIG_HOME:-$HOME/.config}/ssh-ntfy/config.toml"
NTFY="$(sed -n 's/^url *= *"\(.*\)"/\1/p' "$CONF" 2>/dev/null || true)"

if [[ -z "$NTFY" ]]; then
  echo "nssh-shim: no ntfy URL configured in $CONF" >&2
  exit 1
fi

# -- helpers --

json_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

publish_json() {
  curl -sf -m 5 -H 'Content-Type: application/json' -d "$1" "$NTFY" >/dev/null 2>&1
}

publish_attachment() {
  local message="$1" filename="$2" file="$3"
  curl -sf -m 15 -X PUT \
    -H "Filename: $filename" \
    -H "X-Message: $message" \
    --data-binary "@$file" \
    "$NTFY" >/dev/null 2>&1
}

# clip_write: read stdin, send as clip-write to ntfy
clip_write() {
  local mime="${1:-text/plain}"
  local tmp
  tmp=$(mktemp /tmp/nssh-clip.XXXXXX)
  trap 'rm -f "$tmp"' EXIT
  cat > "$tmp"
  local size
  size=$(wc -c < "$tmp" | tr -d ' ')

  if [[ "$size" -le 3072 ]] && [[ "$mime" != image/* ]]; then
    local b64
    b64=$(base64 < "$tmp" | tr -d '\n')
    publish_json "{\"kind\":\"clip-write\",\"mime\":\"$(json_escape "$mime")\",\"body\":\"$b64\"}"
  else
    local filename="clip.dat"
    [[ "$mime" == image/png ]] && filename="clip.png"
    publish_attachment \
      "{\"kind\":\"clip-write\",\"mime\":\"$(json_escape "$mime")\"}" \
      "$filename" "$tmp"
  fi
}

# clip_read: request Mac clipboard, wait for response, write to stdout
clip_read() {
  local mime="${1:-text/plain}"
  local id="${RANDOM}${RANDOM}${RANDOM}"
  local since
  since=$(date +%s)

  # Publish the read request.
  publish_json "{\"kind\":\"clip-read-request\",\"id\":\"$id\",\"mime\":\"$(json_escape "$mime")\"}"

  # Stream the topic for up to 3 seconds, looking for our response.
  local matched=""
  while IFS= read -r line; do
    case "$line" in
      *"clip-read-response"*"$id"*)
        matched="$line"
        break
        ;;
    esac
  done < <(curl -sf -m 3 "${NTFY}/json?since=${since}" 2>/dev/null || true)

  if [[ -z "$matched" ]]; then
    echo "nssh-shim: clipboard read timed out" >&2
    exit 1
  fi

  # Extract the response using python3 (universally available).
  python3 -c "
import json, sys, base64
raw = sys.argv[1]
stream = json.loads(raw)
msg = json.loads(stream.get('message', '{}'))
body = msg.get('body', '')
if body:
    data = base64.b64decode(body)
    if data.startswith(b'ERROR: '):
        sys.stderr.buffer.write(data + b'\n')
        sys.exit(1)
    sys.stdout.buffer.write(data)
elif 'attachment' in stream and stream['attachment'].get('url'):
    import urllib.request
    with urllib.request.urlopen(stream['attachment']['url']) as r:
        sys.stdout.buffer.write(r.read())
else:
    sys.stderr.write('nssh-shim: empty clipboard response\n')
    sys.exit(1)
" "$matched"
}

# -- personas --

do_xdg_open() {
  local url="${1:-}"
  if [[ ! "$url" =~ ^https?:// ]]; then
    exec /usr/bin/xdg-open "$@"
  fi
  local esc_url
  esc_url=$(json_escape "$url")
  if ! publish_json "{\"kind\":\"open\",\"url\":\"$esc_url\"}"; then
    exec /usr/bin/xdg-open "$@"
  fi
}

do_xclip() {
  local direction="in" selection="" mime="text/plain"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -i|-in)       direction="in"; shift ;;
      -o|-out)      direction="out"; shift ;;
      -selection)   selection="${2:-}"; shift 2 ;;
      -t|-target)   mime="${2:-text/plain}"; shift 2 ;;
      -f|-filter)   direction="in"; shift ;; # filter = copy + passthrough; we only copy
      -l|-loops)    shift 2 ;; # ignore loop count
      *)            shift ;;
    esac
  done

  # Only bridge the CLIPBOARD selection. PRIMARY falls through.
  if [[ -n "$selection" ]] && [[ "$selection" != "clipboard" ]]; then
    if command -v /usr/bin/xclip >/dev/null 2>&1; then
      exec /usr/bin/xclip "$@"
    fi
    exit 0
  fi

  case "$direction" in
    in)  clip_write "$mime" ;;
    out) clip_read "$mime" ;;
  esac
}

do_wl_copy() {
  local mime="text/plain"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -t|--type)  mime="${2:-text/plain}"; shift 2 ;;
      *)          shift ;;
    esac
  done
  clip_write "$mime"
}

do_wl_paste() {
  local mime="text/plain"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -t|--type)  mime="${2:-text/plain}"; shift 2 ;;
      --no-newline) shift ;;
      *)          shift ;;
    esac
  done
  clip_read "$mime"
}

# -- dispatch --

case "$(basename "$0")" in
  xdg-open)        do_xdg_open "$@" ;;
  xclip)           do_xclip "$@" ;;
  wl-copy)         do_wl_copy "$@" ;;
  wl-paste)        do_wl_paste "$@" ;;
  nssh-shim|shim*) # Direct invocation for testing
    echo "nssh-shim: use via symlink (xdg-open, xclip, wl-copy, wl-paste)" >&2
    exit 1
    ;;
  *) echo "nssh-shim: unknown persona $(basename "$0")" >&2; exit 2 ;;
esac
