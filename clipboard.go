package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// inlineThreshold is the max decoded size (bytes) for base64-inlined payloads.
// Anything larger goes via ntfy attachment.
const inlineThreshold = 3 * 1024

// handleClipWrite receives clipboard data from a remote VM and writes it to
// the macOS pasteboard. Data arrives either inline (base64 in env.Body) or as
// an ntfy attachment (binary at attachment.URL).
func handleClipWrite(env envelope, att *ntfyAttachment) {
	var data []byte
	var err error

	switch {
	case env.Body != "":
		data, err = base64.StdEncoding.DecodeString(env.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write: base64 decode: %v\n", err)
			return
		}
	case att != nil && att.URL != "":
		data, err = fetchAttachment(att.URL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write: %v\n", err)
			return
		}
	default:
		fmt.Fprintln(os.Stderr, "nssh: clip-write: no data (empty body and no attachment)")
		return
	}

	mime := env.Mime
	if mime == "" {
		mime = "text/plain"
	}

	if strings.HasPrefix(mime, "image/png") {
		if err := writeImagePNG(data); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write image: %v\n", err)
		}
	} else {
		if err := writeText(data); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write text: %v\n", err)
		}
	}
}

// handleClipReadRequest reads the macOS clipboard and publishes a
// clip-read-response back to the ntfy topic. The response correlates with
// the request via env.ID.
func handleClipReadRequest(env envelope, topicURL string) {
	mime := env.Mime
	if mime == "" {
		mime = "text/plain"
	}

	var data []byte
	var err error
	if strings.HasPrefix(mime, "image/png") {
		data, err = readImagePNG()
	} else {
		data, err = readText()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: clip-read: %v\n", err)
		publishErrorResponse(topicURL, env.ID, err.Error())
		return
	}

	resp := envelope{
		Kind: "clip-read-response",
		ID:   env.ID,
		Mime: mime,
	}

	if len(data) <= inlineThreshold && !strings.HasPrefix(mime, "image/") {
		resp.Body = base64.StdEncoding.EncodeToString(data)
		body, _ := json.Marshal(resp)
		if err := publishMessage(topicURL, string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-read response: %v\n", err)
		}
	} else {
		respJSON, _ := json.Marshal(resp)
		filename := "clip.dat"
		if strings.HasPrefix(mime, "image/png") {
			filename = "clip.png"
		}
		if err := publishAttachment(topicURL, string(respJSON), data, filename); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-read response: %v\n", err)
		}
	}
}

func publishErrorResponse(topicURL, id, errMsg string) {
	resp := envelope{
		Kind: "clip-read-response",
		ID:   id,
	}
	// Stuff the error into Body with a prefix the shim can detect.
	resp.Body = base64.StdEncoding.EncodeToString([]byte("ERROR: " + errMsg))
	body, _ := json.Marshal(resp)
	publishMessage(topicURL, string(body))
}
