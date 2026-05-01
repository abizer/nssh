package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/mod/semver"

	"github.com/abizer/nssh/v2/internal/ntfy"
	"github.com/abizer/nssh/v2/internal/wire"
)

// nsshMain runs the default `nssh [--ssh|--mosh] <host>` flow:
// resolves the host, opens the per-session log, prepares the remote
// (writes session file + seeds remote log + probes installed version),
// starts the ntfy subscriber goroutine, then execs ssh or mosh
// interactively and waits for it to exit.
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

	cfg := loadSessionConfig()
	if cfg.Topic == "" {
		if existing := findActiveSessionForHost(shortHost); existing != nil {
			cfg.Topic = existing.Topic
			cfg.Server = existing.Server
			fmt.Fprintf(os.Stderr, "nssh: joining active session for %s (PID %d)\n", shortHost, existing.PID)
		} else {
			cfg.Topic = generateTopic()
		}
	}
	fmt.Fprintf(os.Stderr, "nssh: subscribing to %s\n", cfg.topicURL())

	openLog(cfg.Topic, "session")
	logEvent(LogEvent{Event: "session-start", Target: sshTarget, Host: shortHost, Server: cfg.Server})

	sessionFile, err := registerSession(cfg, sshTarget, shortHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: register session: %v\n", err)
	}
	defer unregisterSession(sessionFile)

	// One SSH login-shell to probe version, write the session file, and seed
	// the remote JSONL log before the interactive session starts.
	remoteVer := prepareRemote(sshTarget, cfg)
	if localVer := version(); isReleaseVersion(localVer) {
		switch {
		case remoteVer == "":
			fmt.Fprintln(os.Stderr, "nssh: not installed on remote — clipboard bridge will not work")
			if promptYes("  install it now?") {
				infectRemote(sshTarget, false)
			}
		case semver.Compare(remoteVer, localVer) != 0:
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

	session, useMosh := selectTransport(forceSSH, forceMosh, sshArgs, sshTarget)
	sessErr := runSession(session, sigs)
	resetTerminal()
	exitCode := 0
	if exitErr, ok := sessErr.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	logEvent(LogEvent{Event: "session-end", Exit: &exitCode, Mosh: &useMosh})
	unregisterSession(sessionFile) // defers don't fire under os.Exit
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// runSession execs the interactive ssh/mosh subprocess, wires its stdio to
// our terminal, and forwards INT/TERM/HUP signals until it exits.
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

// resetTerminal disables xterm mouse-tracking modes and re-shows the cursor.
// vim, htop, etc. enable these and don't always restore them on exit; this
// runs after the interactive session ends so the local prompt is sane again.
func resetTerminal() {
	os.Stdout.WriteString(
		"\x1b[?1000l" + "\x1b[?1002l" + "\x1b[?1003l" + "\x1b[?1006l" + "\x1b[?25h",
	)
}

// remoteHasMosh checks if mosh-server is on the remote's PATH. Used to
// auto-select transport when neither --ssh nor --mosh is given.
func remoteHasMosh(sshTarget string) bool {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "command -v mosh-server >/dev/null 2>&1")
	return cmd.Run() == nil
}

// selectTransport picks ssh or mosh based on the user's flags and (if
// neither is forced) whether mosh is installed locally and on the remote.
// Returns the configured exec.Cmd plus a useMosh flag for downstream
// logging. When mosh is selected we force a UTF-8 locale because mosh's
// terminal emulation breaks under POSIX/C locales on minimal images.
func selectTransport(forceSSH, forceMosh bool, sshArgs []string, sshTarget string) (*exec.Cmd, bool) {
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
	if useMosh {
		fmt.Fprintln(os.Stderr, "nssh: using mosh for interactive session")
		cmd := exec.Command("mosh", sshTarget)
		cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
		return cmd, true
	}
	return exec.Command("ssh", sshArgs...), false
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

// subscribeNtfy maintains a long-lived GET on the ntfy /json endpoint,
// reconnecting on disconnect/timeout. Each line of the response is a ntfy
// event; messages with non-empty bodies are dispatched to handleMessage in
// their own goroutines. Stops when ctx is canceled.
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

// handleMessage parses an incoming ntfy message envelope and dispatches it
// to the appropriate handler based on Kind. Unknown kinds are logged and
// dropped. clip-read-response is intentionally ignored — it's the remote
// shim's response to an outgoing read request, not for us.
func handleMessage(msg ntfy.Msg, topicURL, sshTarget string) {
	env, ok := wire.Parse(msg.Message)
	if !ok {
		fmt.Fprintf(os.Stderr, "nssh: ignoring unrecognized message (%d bytes)\n", len(msg.Message))
		logEvent(LogEvent{Event: "msg-unknown", Size: len(msg.Message)})
		return
	}

	// Decode the inline body once: handlers that need it use this slice and
	// logMessage uses its length for size accounting.
	var body []byte
	if env.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(env.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: %s: base64 decode: %v\n", env.Kind, err)
			return
		}
		body = decoded
	}

	size := len(body)
	if msg.Attachment != nil {
		size = int(msg.Attachment.Size)
	}
	logMessage("in", env, size)

	switch env.Kind {
	case "open":
		handleOpen(env.URL, sshTarget)
	case "clip-write":
		handleClipWrite(env, msg.Attachment, body)
	case "clip-read-request":
		handleClipReadRequest(env, topicURL)
	case "clip-read-response":
		// Responses are for the remote shim, not us. Ignore.
	default:
		fmt.Fprintf(os.Stderr, "nssh: unknown envelope kind %q\n", env.Kind)
	}
}
