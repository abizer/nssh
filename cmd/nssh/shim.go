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
	"strconv"
	"strings"
	"time"

	"github.com/abizer/nssh/internal/ntfy"
	"github.com/abizer/nssh/internal/wire"
)

func shimTopicURL() string {
	cfg := loadConfig()
	if cfg.Topic == "" {
		fmt.Fprintln(os.Stderr, "nssh: no topic configured (not inside an nssh session?)")
		os.Exit(1)
	}
	return cfg.topicURL()
}

func shimClipWrite(topicURL, mime string) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: read stdin: %v\n", err)
		os.Exit(1)
	}
	if len(data) == 0 {
		logEvent("clip-write-empty", nil)
		return
	}
	logEvent("clip-write", map[string]any{"mime": mime, "size": len(data)})

	if len(data) <= inlineThreshold && !strings.HasPrefix(mime, "image/") {
		env := wire.Envelope{
			Kind: "clip-write",
			Mime: mime,
			Body: base64.StdEncoding.EncodeToString(data),
		}
		body, _ := json.Marshal(env)
		if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
			os.Exit(1)
		}
	}
}

func shimClipRead(topicURL, mime string) {
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	since := strconv.FormatInt(time.Now().Unix(), 10)
	logEvent("clip-read-request", map[string]any{"mime": mime, "id": id})

	req := wire.Envelope{Kind: "clip-read-request", ID: id, Mime: mime}
	body, _ := json.Marshal(req)
	if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: publish read request: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint := topicURL + "/json?since=" + since
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: subscribe: %v\n", err)
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
		if env.Body != "" {
			data, err := base64.StdEncoding.DecodeString(env.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh: decode response: %v\n", err)
				os.Exit(1)
			}
			if strings.HasPrefix(string(data), "ERROR: ") {
				fmt.Fprintln(os.Stderr, string(data))
				logEvent("clip-read-error", map[string]any{"id": id, "err": string(data)})
				os.Exit(1)
			}
			logEvent("clip-read-resolved", map[string]any{"id": id, "size": len(data), "inline": true})
			os.Stdout.Write(data)
			return
		}
		if msg.Attachment != nil && msg.Attachment.URL != "" {
			data, err := ntfy.FetchAttachment(msg.Attachment.URL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh: fetch attachment: %v\n", err)
				os.Exit(1)
			}
			logEvent("clip-read-resolved", map[string]any{"id": id, "size": len(data), "inline": false})
			os.Stdout.Write(data)
			return
		}
		logEvent("clip-read-empty", map[string]any{"id": id})
		return
	}

	fmt.Fprintln(os.Stderr, "nssh: clipboard read timed out")
	logEvent("clip-read-timeout", map[string]any{"id": id})
	os.Exit(1)
}

// -- shim personas --

func doXdgOpen(args []string) {
	if len(args) == 0 || (!strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://")) {
		cmd := execFallback("xdg-open", args)
		cmd.Run()
		os.Exit(cmd.ProcessState.ExitCode())
	}
	topicURL := shimTopicURL()
	env := wire.Envelope{Kind: "open", URL: args[0]}
	body, _ := json.Marshal(env)
	if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
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
			i++
		}
	}

	if selection != "" && selection != "clipboard" {
		cmd := execFallback("xclip", args)
		cmd.Run()
		os.Exit(cmd.ProcessState.ExitCode())
	}

	if direction == "out" && mime == "TARGETS" {
		fmt.Println("TARGETS")
		fmt.Println("image/png")
		fmt.Println("text/plain")
		fmt.Println("UTF8_STRING")
		fmt.Println("STRING")
		return
	}

	topicURL := shimTopicURL()
	switch direction {
	case "in":
		shimClipWrite(topicURL, mime)
	case "out":
		shimClipRead(topicURL, mime)
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
	topicURL := shimTopicURL()
	shimClipWrite(topicURL, mime)
}

func doWlPaste(args []string) {
	mime := "text/plain"
	for i := 0; i < len(args); i++ {
		if (args[i] == "-t" || args[i] == "--type") && i+1 < len(args) {
			mime = args[i+1]
			i++
		}
	}
	topicURL := shimTopicURL()
	shimClipRead(topicURL, mime)
}

func execFallback(name string, args []string) *exec.Cmd {
	cmd := exec.Command("/usr/bin/"+name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func shimMain(persona string, args []string) {
	// Open the log up front so every shim event is captured. We need the
	// topic from config; if it's missing, the sub-functions will bail with
	// a clear error — but we still want logging for the successful paths.
	cfg := loadConfig()
	if cfg.Topic != "" {
		openLog(cfg.Topic, persona)
		logEvent("shim-start", map[string]any{"persona": persona, "args": args})
	}
	switch persona {
	case "xdg-open", "sensible-browser":
		doXdgOpen(args)
	case "xclip":
		doXclip(args)
	case "wl-copy":
		doWlCopy(args)
	case "wl-paste":
		doWlPaste(args)
	}
}
