package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// oauthAcceptTimeout caps how long proxyOAuthCallback waits for the browser
// to make its callback. OAuth flows are normally <1 minute end-to-end; if
// the user closes the tab, the listener would otherwise leak the port and a
// goroutine until the nssh session ends.
const oauthAcceptTimeout = 5 * time.Minute

var localhostRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1):(\d+)`)

// extractLocalhostPort scans rawURL for a localhost:<port> reference and
// returns the port if found. Recognizes the host directly, query-string
// values (the typical OAuth redirect_uri encoding), and a final regex
// fallback for any other shape. Returns "" if no localhost reference.
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

// proxyOAuthCallback opens a one-shot listener on localhost:port and pipes
// the first incoming connection through `ssh -W localhost:<port> <target>`,
// effectively forwarding the browser's OAuth callback to the same port on
// the remote machine. A fresh ssh -W per callback works regardless of the
// outer transport (mosh, no ControlMaster, etc).
func proxyOAuthCallback(port, sshTarget string) {
	ln, err := net.Listen("tcp", "localhost:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: listen :%s: %v\n", port, err)
		return
	}
	fmt.Fprintf(os.Stderr, "nssh: ready for OAuth callback on :%s\n", port)
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn.SetDeadline(time.Now().Add(oauthAcceptTimeout))
	}
	conn, err := ln.Accept()
	ln.Close()
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			fmt.Fprintf(os.Stderr, "nssh: no OAuth callback on :%s within %s — gave up\n", port, oauthAcceptTimeout)
		}
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

// handleOpen opens an http(s) URL in the local browser. If the URL contains
// a localhost:<port> reference (OAuth redirect_uri), starts a one-shot
// proxy goroutine in parallel so the callback can flow back to the remote.
// Non-http URLs are silently ignored — this is only for browser-bound
// content from the remote shim.
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
