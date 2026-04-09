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
	"regexp"
	"strings"
	"syscall"
	"time"
)

var localhostRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1):(\d+)`)

func ntfyBase() string {
	if v := os.Getenv("NSSH_NTFY_BASE"); v != "" {
		return v
	}
	return "https://ntfy.abizer.dev"
}

// resolveShortHost runs ssh -G to get the canonical hostname, strips the domain.
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

// extractLocalhostPort finds a localhost:<port> anywhere in the URL —
// including inside query parameters like redirect_uri.
func extractLocalhostPort(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil {
		// Top-level URL is localhost
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
			return u.Port()
		}
		// Embedded in query params (e.g. redirect_uri=http%3A%2F%2Flocalhost%3A8080%2F...)
		for _, vals := range u.Query() {
			for _, v := range vals {
				if m := localhostRe.FindStringSubmatch(v); len(m) > 1 {
					return m[1]
				}
			}
		}
	}
	// Raw regex fallback over the full URL string
	if m := localhostRe.FindStringSubmatch(rawURL); len(m) > 1 {
		return m[1]
	}
	return ""
}

// proxyOAuthCallback listens on port, accepts exactly one connection,
// proxies it to the same port on the remote via a fresh ssh -W, then closes
// everything. Each forward is an independent ssh connection — no shared
// control master — so OAuth callbacks survive network roams (sleep/wake,
// WiFi change) and work identically regardless of whether the interactive
// session is mosh or ssh.
func proxyOAuthCallback(port, sshTarget string) {
	ln, err := net.Listen("tcp", "localhost:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: listen :%s: %v\n", port, err)
		return
	}
	fmt.Fprintf(os.Stderr, "nssh: ready for OAuth callback on :%s\n", port)

	conn, err := ln.Accept()
	ln.Close() // one-shot: stop accepting immediately
	if err != nil {
		return
	}
	defer conn.Close()

	fwd := exec.Command("ssh", "-W",
		fmt.Sprintf("localhost:%s", port), sshTarget)
	fwd.Stdin = conn
	fwd.Stdout = conn
	fwd.Stderr = os.Stderr
	if err := fwd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: forward :%s: %v\n", port, err)
		return
	}
	fmt.Fprintf(os.Stderr, "nssh: OAuth callback on :%s done\n", port)
}

// resetTerminal disables mouse tracking modes that a remote tmux/vim may have
// left enabled when the SSH connection drops before it can send the "off" sequences.
func resetTerminal() {
	os.Stdout.WriteString(
		"\x1b[?1000l" + // normal tracking off
			"\x1b[?1002l" + // button-event tracking off
			"\x1b[?1003l" + // any-event tracking off
			"\x1b[?1006l" + // SGR extended mode off
			"\x1b[?25h", // cursor visible
	)
}

func handleMessage(rawURL, sshTarget string) {
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

type ntfyMsg struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

// subscribeNtfy streams the ntfy JSON endpoint and dispatches incoming messages.
// Reconnects automatically on failure.
func subscribeNtfy(ctx context.Context, topic, sshTarget string) {
	endpoint := ntfyBase() + "/" + topic + "/json"
	client := &http.Client{}

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
			var msg ntfyMsg
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Event == "message" && msg.Message != "" {
				go handleMessage(msg.Message, sshTarget)
			}
		}
		resp.Body.Close()

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// remoteHasMosh checks whether mosh-server exists on the remote host.
// Uses BatchMode so it's non-interactive: hosts that need a password
// prompt simply won't auto-upgrade to mosh, which is fine — the default
// session selection falls back to ssh. If the user wants mosh anyway,
// they can pass --mosh to skip this preflight.
func remoteHasMosh(sshTarget string) bool {
	cmd := exec.Command("ssh",
		"-o", "BatchMode=yes",
		sshTarget,
		"command -v mosh-server >/dev/null 2>&1",
	)
	return cmd.Run() == nil
}

// runSession runs an already-configured interactive session command
// (mosh or ssh) in the foreground, forwarding SIGINT/SIGTERM/SIGHUP to
// the child and waiting for it to exit.
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
	fmt.Fprintln(os.Stderr, "usage: nssh [--ssh|--mosh] <host> [ssh args...]")
	fmt.Fprintln(os.Stderr, "  --ssh   force plain ssh (skip mosh auto-detect)")
	fmt.Fprintln(os.Stderr, "  --mosh  force mosh (skip remote preflight)")
	os.Exit(1)
}

func main() {
	// Parse our own flags before the ssh target. Only --ssh and --mosh
	// are recognized; anything else falls through to ssh's argument list.
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

	ntfyTopic := "reverse-open-" + shortHost

	fmt.Fprintf(os.Stderr, "nssh: subscribing to %s/%s\n", ntfyBase(), ntfyTopic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscribeNtfy(ctx, ntfyTopic, sshTarget)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Pick the session transport.
	//   --ssh      → always ssh
	//   --mosh     → always mosh (trust the user; skip preflight)
	//   (default)  → mosh if local + remote both have it; otherwise ssh
	// If mosh fails at runtime, the user re-runs with --ssh.
	useMosh := false
	switch {
	case forceSSH:
		// stay false
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
		// Force a universally-available UTF-8 locale — side-steps the
		// "en_US.UTF-8 isn't available" mosh-server error on vanilla
		// Linux remotes (including Nix-installed mosh without glibcLocales).
		session.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	} else {
		session = exec.Command("ssh", sshArgs...)
	}

	err := runSession(session, sigs)
	resetTerminal()
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
}

