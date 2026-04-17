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
	"strings"
	"syscall"
	"time"

	"github.com/abizer/ssh-reverse-ntfy/internal/ntfy"
	"github.com/abizer/ssh-reverse-ntfy/internal/wire"
)

var localhostRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1):(\d+)`)

func ntfyBase() string {
	if v := os.Getenv("NSSH_NTFY_BASE"); v != "" {
		return v
	}
	return "https://ntfy.abizer.dev"
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
		return
	}
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

func subscribeNtfy(ctx context.Context, topic, sshTarget string) {
	endpoint := ntfyBase() + "/" + topic + "/json"
	topicURL := ntfyBase() + "/" + topic
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
			var msg ntfy.Msg
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Event == "message" && msg.Message != "" {
				go handleMessage(msg, topicURL, sshTarget)
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
	fmt.Fprintln(os.Stderr, "usage: nssh [--ssh|--mosh] <host> [ssh args...]")
	fmt.Fprintln(os.Stderr, "  --ssh   force plain ssh (skip mosh auto-detect)")
	fmt.Fprintln(os.Stderr, "  --mosh  force mosh (skip remote preflight)")
	os.Exit(1)
}

func main() {
	persona := filepath.Base(os.Args[0])
	switch persona {
	case "xdg-open", "sensible-browser", "xclip", "wl-copy", "wl-paste":
		shimMain(persona, os.Args[1:])
		return
	}
	// Default: nssh session mode (works regardless of binary name).
	nsshMain()
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

	ntfyTopic := "reverse-open-" + shortHost
	fmt.Fprintf(os.Stderr, "nssh: subscribing to %s/%s\n", ntfyBase(), ntfyTopic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscribeNtfy(ctx, ntfyTopic, sshTarget)

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

	err := runSession(session, sigs)
	resetTerminal()
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
}
