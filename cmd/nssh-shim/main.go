package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abizer/ssh-reverse-ntfy/internal/ntfy"
	"github.com/abizer/ssh-reverse-ntfy/internal/wire"
)

func readConfig() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "ssh-ntfy", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh-shim: cannot read %s: %v\n", path, err)
		os.Exit(1)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "url") {
			// url = "https://..."
			if idx := strings.Index(line, `"`); idx >= 0 {
				end := strings.LastIndex(line, `"`)
				if end > idx {
					return line[idx+1 : end]
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "nssh-shim: no url found in %s\n", path)
	os.Exit(1)
	return ""
}

const inlineThreshold = 3 * 1024

// clipWrite reads data from stdin and publishes a clip-write message.
func clipWrite(topicURL, mime string) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh-shim: read stdin: %v\n", err)
		os.Exit(1)
	}
	if len(data) == 0 {
		return
	}

	if len(data) <= inlineThreshold && !strings.HasPrefix(mime, "image/") {
		env := wire.Envelope{
			Kind: "clip-write",
			Mime: mime,
			Body: base64.StdEncoding.EncodeToString(data),
		}
		body, _ := json.Marshal(env)
		if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "nssh-shim: %v\n", err)
			os.Exit(1)
		}
	} else {
		env := wire.Envelope{Kind: "clip-write", Mime: mime}
		msg, _ := json.Marshal(env)
		filename := "clip.dat"
		if strings.HasPrefix(mime, "image/png") {
			filename = "clip.png"
		}
		if err := ntfy.PublishAttachment(topicURL, string(msg), data, filename); err != nil {
			fmt.Fprintf(os.Stderr, "nssh-shim: %v\n", err)
			os.Exit(1)
		}
	}
}

// clipRead publishes a clip-read-request and waits for the response.
func clipRead(topicURL, mime string) {
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	since := strconv.FormatInt(time.Now().Unix(), 10)

	// Publish the read request.
	req := wire.Envelope{Kind: "clip-read-request", ID: id, Mime: mime}
	body, _ := json.Marshal(req)
	if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
		fmt.Fprintf(os.Stderr, "nssh-shim: publish read request: %v\n", err)
		os.Exit(1)
	}

	// Subscribe to the topic with a 5-second timeout, looking for our response.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint := topicURL + "/json?since=" + since
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh-shim: subscribe: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var msg ntfy.Msg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event != "message" {
			continue
		}
		env, ok := wire.Parse(msg.Message)
		if !ok || env.Kind != "clip-read-response" || env.ID != id {
			continue
		}

		// Got our response.
		if env.Body != "" {
			data, err := base64.StdEncoding.DecodeString(env.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh-shim: decode response: %v\n", err)
				os.Exit(1)
			}
			if strings.HasPrefix(string(data), "ERROR: ") {
				fmt.Fprintln(os.Stderr, string(data))
				os.Exit(1)
			}
			os.Stdout.Write(data)
			return
		}
		if msg.Attachment != nil && msg.Attachment.URL != "" {
			data, err := ntfy.FetchAttachment(msg.Attachment.URL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh-shim: fetch attachment: %v\n", err)
				os.Exit(1)
			}
			os.Stdout.Write(data)
			return
		}
		// Empty response = empty clipboard.
		return
	}

	fmt.Fprintln(os.Stderr, "nssh-shim: clipboard read timed out")
	os.Exit(1)
}

// -- personas --

func doXdgOpen(args []string) {
	if len(args) == 0 || (!strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://")) {
		// Fall through to real xdg-open.
		cmd := execFallback("xdg-open", args)
		cmd.Run()
		os.Exit(cmd.ProcessState.ExitCode())
	}
	topicURL := readConfig()
	env := wire.Envelope{Kind: "open", URL: args[0]}
	body, _ := json.Marshal(env)
	if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
		// Fall through on failure.
		cmd := execFallback("xdg-open", args)
		cmd.Run()
		os.Exit(cmd.ProcessState.ExitCode())
	}
}

func doXclip(args []string) {
	direction := "in"
	selection := ""
	mime := "text/plain"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-i", "-in":
			direction = "in"
		case "-o", "-out":
			direction = "out"
		case "-selection":
			if i+1 < len(args) {
				selection = args[i+1]
				i++
			}
		case "-t", "-target":
			if i+1 < len(args) {
				mime = args[i+1]
				i++
			}
		case "-f", "-filter":
			direction = "in"
		case "-l", "-loops":
			i++ // skip value
		}
	}

	// Only bridge CLIPBOARD selection. PRIMARY/others fall through.
	if selection != "" && selection != "clipboard" {
		cmd := execFallback("xclip", args)
		cmd.Run()
		os.Exit(cmd.ProcessState.ExitCode())
	}

	// TARGETS query: return the types our bridge supports. Apps like Claude
	// Code probe this before attempting an image read.
	if direction == "out" && mime == "TARGETS" {
		fmt.Println("TARGETS")
		fmt.Println("image/png")
		fmt.Println("text/plain")
		fmt.Println("UTF8_STRING")
		fmt.Println("STRING")
		return
	}

	topicURL := readConfig()
	switch direction {
	case "in":
		clipWrite(topicURL, mime)
	case "out":
		clipRead(topicURL, mime)
	}
}

func doWlCopy(args []string) {
	mime := "text/plain"
	for i := 0; i < len(args); i++ {
		if (args[i] == "-t" || args[i] == "--type") && i+1 < len(args) {
			mime = args[i+1]
			i++
		}
	}
	topicURL := readConfig()
	clipWrite(topicURL, mime)
}

func doWlPaste(args []string) {
	mime := "text/plain"
	for i := 0; i < len(args); i++ {
		if (args[i] == "-t" || args[i] == "--type") && i+1 < len(args) {
			mime = args[i+1]
			i++
		}
	}
	topicURL := readConfig()
	clipRead(topicURL, mime)
}

// execFallback finds the real binary in PATH (skipping our own) and execs it.
func execFallback(name string, args []string) *exec.Cmd {
	// Look for the real binary, skipping ~/.local/bin where we live.
	cmd := exec.Command("/usr/bin/"+name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func main() {
	persona := filepath.Base(os.Args[0])
	args := os.Args[1:]

	switch persona {
	case "xdg-open":
		doXdgOpen(args)
	case "xclip":
		doXclip(args)
	case "wl-copy":
		doWlCopy(args)
	case "wl-paste":
		doWlPaste(args)
	case "sensible-browser":
		doXdgOpen(args)
	default:
		fmt.Fprintf(os.Stderr, "nssh-shim: unknown persona %q (invoke via symlink)\n", persona)
		os.Exit(2)
	}
}
