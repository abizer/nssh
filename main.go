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
// proxies it to the same port on the remote via ssh -W, then closes everything.
func proxyOAuthCallback(port, sshTarget, controlSocket string) {
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

	// Tunnel to remote port via the existing control socket — no re-auth.
	fwd := exec.Command("ssh", "-S", controlSocket, "-W",
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

func handleMessage(rawURL, sshTarget, controlSocket string) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return
	}
	if port := extractLocalhostPort(rawURL); port != "" {
		go proxyOAuthCallback(port, sshTarget, controlSocket)
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
func subscribeNtfy(ctx context.Context, topic, sshTarget, controlSocket string) {
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
				go handleMessage(msg.Message, sshTarget, controlSocket)
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

// controlMaster manages a persistent SSH mux master process.
type controlMaster struct {
	cmd    *exec.Cmd
	socket string
	target string
}

func startControlMaster(target, socket string) (*controlMaster, error) {
	os.Remove(socket)
	cm := &controlMaster{socket: socket, target: target}
	cm.cmd = exec.Command("ssh",
		"-M", "-S", socket,
		"-N",
		"-o", "ControlPersist=no",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		target,
	)
	cm.cmd.Stderr = os.Stderr
	if err := cm.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start control master: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("ssh", "-S", socket, "-O", "check", target).Run() == nil {
			return cm, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	cm.cmd.Process.Kill()
	return nil, fmt.Errorf("control master: timed out waiting for %s", socket)
}

func (cm *controlMaster) close() {
	if cm.cmd.Process != nil {
		cm.cmd.Process.Signal(syscall.SIGTERM)
		cm.cmd.Wait()
	}
	os.Remove(cm.socket)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nssh <host> [ssh args...]")
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	sshArgs := os.Args[1:]
	sshTarget := os.Args[1]

	shortHost := resolveShortHost(sshArgs)
	if shortHost == "" {
		fmt.Fprintln(os.Stderr, "nssh: could not resolve hostname, falling back to plain ssh")
		cmd := exec.Command("ssh", sshArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Run()
		return
	}

	ntfyTopic := "reverse-open-" + shortHost
	controlSocket := fmt.Sprintf("/tmp/.nssh-%s.sock", shortHost)

	fmt.Fprintf(os.Stderr, "nssh: subscribing to %s/%s\n", ntfyBase(), ntfyTopic)

	cm, err := startControlMaster(sshTarget, controlSocket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
		os.Exit(1)
	}
	defer cm.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscribeNtfy(ctx, ntfyTopic, sshTarget, controlSocket)

	// Interactive session reuses the control master socket.
	ssh := exec.Command("ssh", append([]string{"-S", controlSocket}, sshArgs...)...)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	if err := ssh.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: ssh: %v\n", err)
		os.Exit(1)
	}

	sshDone := make(chan error, 1)
	go func() { sshDone <- ssh.Wait() }()

	select {
	case err := <-sshDone:
		resetTerminal()
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
	case sig := <-sigs:
		ssh.Process.Signal(sig)
		<-sshDone
		resetTerminal()
	}
}

