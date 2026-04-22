package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/abizer/nssh/v2/internal/ntfy"
	"github.com/abizer/nssh/v2/internal/wire"
)

var localhostRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1):(\d+)`)

// prepareRemote probes the remote's nssh version and writes the session file +
// seeds the JSONL log in a single SSH login-shell invocation. Returns the
// remote nssh version, or "" if not installed / unreadable. Non-fatal on
// errors — shim may still work with a pinned config.toml or no log at all.
func prepareRemote(sshTarget string, cfg nsshConfig) string {
	event := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"event":   "session-open",
		"side":    "session-init",
		"server":  cfg.Server,
		"topic":   cfg.Topic,
		"target":  sshTarget,
		"version": buildVersion,
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

	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "bash", "-l", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
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

func extractLocalhostPort(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil {
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
			return u.Port()
		}
		for _, vals := range u.Query() {
			for _, v := range vals {
				if m := localhostRe.FindStringSubmatch(v); len(m) > 1 {
					return m[1]
				}
			}
		}
	}
	if m := localhostRe.FindStringSubmatch(rawURL); len(m) > 1 {
		return m[1]
	}
	return ""
}

func proxyOAuthCallback(port, sshTarget string) {
	ln, err := net.Listen("tcp", "localhost:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: listen :%s: %v\n", port, err)
		return
	}
	fmt.Fprintf(os.Stderr, "nssh: ready for OAuth callback on :%s\n", port)
	conn, err := ln.Accept()
	ln.Close()
	if err != nil {
		return
	}
	defer conn.Close()
	fwd := exec.Command("ssh", "-W", fmt.Sprintf("localhost:%s", port), sshTarget)
	fwd.Stdin = conn
	fwd.Stdout = conn
	fwd.Stderr = os.Stderr
	if err := fwd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: forward :%s: %v\n", port, err)
		return
	}
	fmt.Fprintf(os.Stderr, "nssh: OAuth callback on :%s done\n", port)
}

func resetTerminal() {
	os.Stdout.WriteString(
		"\x1b[?1000l" + "\x1b[?1002l" + "\x1b[?1003l" + "\x1b[?1006l" + "\x1b[?25h",
	)
}

func handleMessage(msg ntfy.Msg, topicURL, sshTarget string) {
	env, ok := wire.Parse(msg.Message)
	if !ok {
		fmt.Fprintf(os.Stderr, "nssh: ignoring unrecognized message (%d bytes)\n", len(msg.Message))
		logEvent("message-ignored", map[string]any{"size": len(msg.Message)})
		return
	}
	fields := map[string]any{"kind": env.Kind}
	if env.Mime != "" {
		fields["mime"] = env.Mime
	}
	if env.ID != "" {
		fields["id"] = env.ID
	}
	if env.URL != "" {
		fields["url"] = env.URL
	}
	if msg.Attachment != nil {
		fields["attachment_size"] = msg.Attachment.Size
	}
	logEvent("message-in", fields)

	switch env.Kind {
	case "open":
		handleOpen(env.URL, sshTarget)
	case "clip-write":
		handleClipWrite(env, msg.Attachment)
	case "clip-read-request":
		handleClipReadRequest(env, topicURL)
	case "clip-read-response":
		// Responses are for the remote shim, not us. Ignore.
	default:
		fmt.Fprintf(os.Stderr, "nssh: unknown envelope kind %q\n", env.Kind)
	}
}

func handleOpen(rawURL, sshTarget string) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return
	}
	if port := extractLocalhostPort(rawURL); port != "" {
		go proxyOAuthCallback(port, sshTarget)
	}
	if err := exec.Command("open", rawURL).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: open: %v\n", err)
	}
}

// deadlineConn wraps net.Conn to push the read deadline forward on every Read.
// The ntfy server sends keepalive events every ~55s, so if no bytes arrive
// for well past that window the connection is silently dead (laptop sleep, NAT
// rebind, proxy drop) — the next Read returns i/o timeout and the subscriber
// reconnects. Without this, Read can block forever on a zombie TCP socket.
type deadlineConn struct {
	net.Conn
	period time.Duration
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.period))
	return c.Conn.Read(p)
}

func subscribeNtfy(ctx context.Context, cfg nsshConfig, sshTarget string) {
	topicURL := cfg.topicURL()
	endpoint := topicURL + "/json"

	dialer := &net.Dialer{KeepAlive: 15 * time.Second}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &deadlineConn{Conn: conn, period: 90 * time.Second}, nil
			},
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}

	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "nssh: ntfy: %v — retrying\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var msg ntfy.Msg
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Event == "message" && msg.Message != "" {
				go handleMessage(msg, topicURL, sshTarget)
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "nssh: ntfy stream ended (%v) — reconnecting\n", err)
		}
		resp.Body.Close()

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func remoteHasMosh(sshTarget string) bool {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "command -v mosh-server >/dev/null 2>&1")
	return cmd.Run() == nil
}

func runSession(cmd *exec.Cmd, sigs <-chan os.Signal) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			return err
		case sig := <-sigs:
			cmd.Process.Signal(sig)
		}
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  nssh [--ssh|--mosh] <host> [ssh args...]   open a session")
	fmt.Fprintln(os.Stderr, "  nssh infect [--force] <host>               install on a remote host")
	fmt.Fprintln(os.Stderr, "  nssh infect [--force] self                 symlink personas on this machine")
	fmt.Fprintln(os.Stderr, "  nssh status [--tail]                       show active sessions")
	fmt.Fprintln(os.Stderr, "  nssh --version                             print version info")
	os.Exit(1)
}

// buildVersion is set by ldflags at release build time (e.g. in the homebrew
// formula). For go install / go build with a tagged module it's empty and we
// fall back to debug.ReadBuildInfo.
var buildVersion string

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("nssh (build info unavailable)")
		return
	}
	v := buildVersion
	if v == "" {
		v = info.Main.Version
	}
	if v == "" {
		v = "(devel)"
	}
	fmt.Printf("nssh %s\n", v)
	fmt.Printf("  go      %s\n", info.GoVersion)
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			fmt.Printf("  commit  %s\n", s.Value)
		case "vcs.time":
			fmt.Printf("  built   %s\n", s.Value)
		case "vcs.modified":
			if s.Value == "true" {
				fmt.Println("  dirty   true")
			}
		case "GOOS":
			fmt.Printf("  os      %s\n", s.Value)
		case "GOARCH":
			fmt.Printf("  arch    %s\n", s.Value)
		}
	}
}

func main() {
	persona := filepath.Base(os.Args[0])
	switch persona {
	case "xdg-open", "sensible-browser", "xclip", "wl-copy", "wl-paste":
		shimMain(persona, os.Args[1:])
		return
	}
	// Invoked as nssh (or equivalent). Route on first arg.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "infect":
			infectCmd(os.Args[2:])
			return
		case "status":
			statusCmd(os.Args[2:])
			return
		case "-v", "--version":
			printVersion()
			return
		case "-h", "--help":
			usage()
		}
	}
	nsshMain()
}

// infectCmd handles `nssh infect [--force] <target|self>`.
func infectCmd(args []string) {
	force := false
	var target string
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, "usage: nssh infect [--force] <host|self>")
			os.Exit(1)
		default:
			if target != "" {
				fmt.Fprintf(os.Stderr, "nssh: unexpected arg %q\n", a)
				os.Exit(1)
			}
			target = a
		}
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "usage: nssh infect [--force] <host|self>")
		os.Exit(1)
	}
	if target == "self" {
		infectSelf(force)
		return
	}
	infectRemote(target, force)
}

func nsshMain() {
	args := os.Args[1:]
	forceSSH := false
	forceMosh := false
	for len(args) > 0 {
		switch args[0] {
		case "--ssh":
			forceSSH = true
			args = args[1:]
			continue
		case "--mosh":
			forceMosh = true
			args = args[1:]
			continue
		case "-h", "--help":
			usage()
		}
		break
	}
	if forceSSH && forceMosh {
		fmt.Fprintln(os.Stderr, "nssh: --ssh and --mosh are mutually exclusive")
		os.Exit(1)
	}
	if len(args) < 1 {
		usage()
	}

	sshArgs := args
	sshTarget := args[0]

	shortHost := resolveShortHost(sshArgs)
	if shortHost == "" {
		fmt.Fprintln(os.Stderr, "nssh: could not resolve hostname, falling back to plain ssh")
		cmd := exec.Command("ssh", sshArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Run()
		return
	}

	cfg := loadConfig()
	if cfg.Topic == "" {
		cfg.Topic = generateTopic()
	}
	fmt.Fprintf(os.Stderr, "nssh: subscribing to %s\n", cfg.topicURL())

	openLog(cfg.Topic, "session")
	logEvent("session-start", map[string]any{
		"target": sshTarget,
		"server": cfg.Server,
	})

	sessionFile, err := registerSession(cfg, sshTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: register session: %v\n", err)
	}
	defer unregisterSession(sessionFile)

	// One SSH login-shell to probe version, write the session file, and seed
	// the remote JSONL log before the interactive session starts.
	remoteVer := prepareRemote(sshTarget, cfg)
	if localVer := version(); looksLikeSemver(localVer) {
		switch {
		case remoteVer == "":
			fmt.Fprintln(os.Stderr, "nssh: not installed on remote — clipboard bridge will not work")
			if promptYes("  install it now?") {
				infectRemote(sshTarget, false)
			}
		case remoteVer != localVer:
			fmt.Fprintf(os.Stderr, "nssh: remote version %s, local %s\n", remoteVer, localVer)
			if promptYes("  update remote to " + localVer + "?") {
				infectRemote(sshTarget, false)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscribeNtfy(ctx, cfg, sshTarget)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	useMosh := false
	switch {
	case forceSSH:
	case forceMosh:
		useMosh = true
	default:
		if _, err := exec.LookPath("mosh"); err == nil && remoteHasMosh(sshTarget) {
			useMosh = true
		}
	}

	var session *exec.Cmd
	if useMosh {
		fmt.Fprintln(os.Stderr, "nssh: using mosh for interactive session")
		session = exec.Command("mosh", sshTarget)
		session.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	} else {
		session = exec.Command("ssh", sshArgs...)
	}

	sessErr := runSession(session, sigs)
	resetTerminal()
	exitCode := 0
	if exitErr, ok := sessErr.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	logEvent("session-end", map[string]any{"exit": exitCode, "mosh": useMosh})
	unregisterSession(sessionFile) // defers don't fire under os.Exit
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
