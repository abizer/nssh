package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runRemoteScript pipes a shell script to `bash -l -s` on the remote and
// returns its stdout. Stderr passes through to the user's terminal so SSH
// errors (auth, connection refused, host key) are visible. BatchMode=yes
// — every caller wants a deterministic non-interactive run; key auth that
// works for the initial connection works for every subsequent call.
func runRemoteScript(sshTarget, script string) ([]byte, error) {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "bash", "-l", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// prepareRemote probes the remote's nssh version and writes the session file +
// seeds the JSONL log in a single SSH login-shell invocation. Returns the
// remote nssh version, or "" if not installed / unreadable. Non-fatal on
// errors — shim may still work with a pinned config.toml or no log at all.
func prepareRemote(sshTarget string, cfg nsshConfig) string {
	event := LogEvent{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Event:   "session-open",
		Side:    "session-init",
		Server:  cfg.Server,
		Topic:   cfg.Topic,
		Target:  sshTarget,
		Version: buildVersion,
	}
	eventJSON, _ := json.Marshal(event)

	// bash -l so PATH includes ~/.local/bin even for non-interactive sessions.
	// Heredocs with quoted delimiters ('EOF') prevent shell expansion inside,
	// so TOML and JSON pass through verbatim regardless of contents.
	script := fmt.Sprintf(`set -e
if command -v nssh >/dev/null 2>&1; then
  echo "NSSH_VERSION: $(nssh --version 2>/dev/null | head -1 | awk '{print $2}')"
else
  echo "NSSH_VERSION: none"
fi
dir="${XDG_STATE_HOME:-$HOME/.local/state}/nssh"
mkdir -p "$dir"
cat > "$dir/session" <<'NSSH_SESSION_EOF'
server = "%s"
topic = "%s"
NSSH_SESSION_EOF
cat >> "$dir/nssh.%s.jsonl" <<'NSSH_LOG_EOF'
%s
NSSH_LOG_EOF
`, cfg.Server, cfg.Topic, cfg.Topic, string(eventJSON))

	out, err := runRemoteScript(sshTarget, script)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: remote prepare: %v\n", err)
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		v, ok := strings.CutPrefix(line, "NSSH_VERSION: ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || v == "none" {
			return ""
		}
		return v
	}
	return ""
}

// resolveShortHost queries `ssh -G` to resolve the user's host alias to its
// real hostname, then strips the domain suffix. Returns "" if ssh -G fails
// or the alias has no hostname mapping.
func resolveShortHost(sshArgs []string) string {
	out, err := exec.Command("ssh", append([]string{"-G"}, sshArgs...)...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "hostname ") {
			host := strings.TrimSpace(strings.TrimPrefix(line, "hostname "))
			if idx := strings.Index(host, "."); idx >= 0 {
				host = host[:idx]
			}
			return host
		}
	}
	return ""
}
