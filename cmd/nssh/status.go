package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sessionInfo is one entry in the local sessions registry, or the remote's
// ~/.local/state/nssh/session projected into the same shape.
type sessionInfo struct {
	PID     int       `json:"pid"`
	Target  string    `json:"target"`
	Topic   string    `json:"topic"`
	Server  string    `json:"server"`
	Started time.Time `json:"started"`
	Log     string    `json:"log"`
	remote  bool      // not persisted; true when synthesised from the remote session file
}

func sessionsDir() string {
	return filepath.Join(stateDir(), "sessions")
}

// registerSession writes a pidfile describing the current session. Called once
// by nsshMain just before the subscriber starts. Returns the file path so the
// caller can unregister explicitly (defers don't fire under os.Exit).
func registerSession(cfg nsshConfig, target string) (string, error) {
	dir := sessionsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid()))
	info := sessionInfo{
		PID:     os.Getpid(),
		Target:  target,
		Topic:   cfg.Topic,
		Server:  cfg.Server,
		Started: time.Now().UTC(),
		Log:     filepath.Join(stateDir(), "nssh."+cfg.Topic+".jsonl"),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

func unregisterSession(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// activeSessions scans the sessions dir, GCs entries whose PID is no longer
// alive, and returns the survivors sorted by start time.
func activeSessions() []sessionInfo {
	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []sessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var info sessionInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		// Signal 0 is a permission/existence probe on POSIX.
		if err := syscall.Kill(info.PID, 0); err != nil {
			_ = os.Remove(path)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// readRemoteSession returns the ~/.local/state/nssh/session TOML projected
// into a sessionInfo, or nil if the file isn't there. Used by `nssh status`
// on remote hosts where there's no sessions/ dir, just the single pinned file.
func readRemoteSession() *sessionInfo {
	path := filepath.Join(stateDir(), "session")
	m := readTOML(path)
	if m["topic"] == "" {
		return nil
	}
	stat, err := os.Stat(path)
	started := time.Time{}
	if err == nil {
		started = stat.ModTime().UTC()
	}
	return &sessionInfo{
		Target:  "(remote)",
		Topic:   m["topic"],
		Server:  m["server"],
		Started: started,
		Log:     filepath.Join(stateDir(), "nssh."+m["topic"]+".jsonl"),
		remote:  true,
	}
}

func statusCmd(args []string) {
	tail := false
	for _, a := range args {
		switch a {
		case "--tail", "-t":
			tail = true
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, "usage: nssh status [--tail]")
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "nssh: unexpected arg %q\n", a)
			os.Exit(1)
		}
	}

	locals := activeSessions()
	remote := readRemoteSession()

	if len(locals) == 0 && remote == nil {
		fmt.Println("no active nssh sessions")
		return
	}

	printSessions(locals, remote)

	if !tail {
		return
	}

	var paths, labels []string
	for _, s := range locals {
		paths = append(paths, s.Log)
		labels = append(labels, s.Target)
	}
	if remote != nil {
		paths = append(paths, remote.Log)
		labels = append(labels, remote.Target)
	}
	tailFiles(paths, labels)
}

func printSessions(locals []sessionInfo, remote *sessionInfo) {
	now := time.Now()
	if len(locals) > 0 {
		fmt.Println("active local sessions:")
		fmt.Printf("  %-8s %-20s %-28s %s\n", "PID", "TARGET", "TOPIC", "UPTIME")
		for _, s := range locals {
			fmt.Printf("  %-8d %-20s %-28s %s\n",
				s.PID, truncate(s.Target, 20), truncate(s.Topic, 28),
				shortDuration(now.Sub(s.Started)))
		}
	}
	if remote != nil {
		if len(locals) > 0 {
			fmt.Println()
		}
		fmt.Println("remote session:")
		fmt.Printf("  topic:   %s\n", remote.Topic)
		fmt.Printf("  server:  %s\n", remote.Server)
		if !remote.Started.IsZero() {
			fmt.Printf("  started: %s (%s ago)\n",
				remote.Started.Local().Format(time.RFC3339),
				shortDuration(now.Sub(remote.Started)))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

// tailFiles polls each of the given jsonl files and pretty-prints new events.
// One reader goroutine per file; they push formatted lines into a shared chan
// that the main goroutine drains. Blocks until SIGINT/SIGTERM.
func tailFiles(paths, labels []string) {
	fmt.Fprintf(os.Stderr, "following %d session(s) — Ctrl+C to stop\n", len(paths))

	out := make(chan string, 64)
	stop := make(chan struct{})

	for i, p := range paths {
		go tailOne(p, labels[i], out, stop)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case line := <-out:
			fmt.Println(line)
		case <-sigs:
			close(stop)
			return
		}
	}
}

func tailOne(path, label string, out chan<- string, stop <-chan struct{}) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: open %s: %v\n", path, err)
		return
	}
	defer f.Close()
	// Start at end — only want new events.
	_, _ = f.Seek(0, io.SeekEnd)
	reader := bufio.NewReader(f)
	var partial string

	for {
		select {
		case <-stop:
			return
		default:
		}
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			partial += line
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: read %s: %v\n", path, err)
			return
		}
		out <- formatEvent(strings.TrimRight(partial+line, "\n"), label)
		partial = ""
	}
}

func formatEvent(raw, label string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return fmt.Sprintf("[%s] %s", label, raw)
	}
	ts := ""
	if v, ok := m["ts"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			ts = t.Local().Format("15:04:05")
		}
	}
	event, _ := m["event"].(string)

	switch event {
	case "msg-send":
		return formatWireMessage(label, ts, "→", m)
	case "msg-recv":
		return formatWireMessage(label, ts, "←", m)
	}

	var keys []string
	for k := range m {
		switch k {
		case "ts", "event", "pid", "side":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] %s %s", label, ts, event)
	if side, _ := m["side"].(string); side != "" && side != "session" {
		fmt.Fprintf(&sb, " (%s)", side)
	}
	for _, k := range keys {
		fmt.Fprintf(&sb, " %s=%v", k, m[k])
	}
	return sb.String()
}

func formatWireMessage(label, ts, arrow string, m map[string]any) string {
	kind, _ := m["kind"].(string)
	mime, _ := m["mime"].(string)
	urlStr, _ := m["url"].(string)
	var size int64
	if v, ok := m["size"].(float64); ok {
		size = int64(v)
	}

	var detail strings.Builder
	switch {
	case urlStr != "":
		fmt.Fprintf(&detail, " %s", urlStr)
	case mime != "":
		fmt.Fprintf(&detail, " %s", mime)
	}
	if size > 0 {
		fmt.Fprintf(&detail, " (%s)", humanSize(size))
	}
	return fmt.Sprintf("[%s] %s %s %s%s", label, ts, arrow, kind, detail.String())
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
