package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/abizer/nssh/internal/clipboard"
	"github.com/abizer/nssh/internal/ntfy"
	"github.com/abizer/nssh/internal/wire"
)

const inlineThreshold = 3 * 1024

func handleClipWrite(env wire.Envelope, att *ntfy.Attachment) {
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
		data, err = ntfy.FetchAttachment(att.URL)
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
		if err := clipboard.WriteImagePNG(data); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write image: %v\n", err)
		}
	} else {
		if err := clipboard.WriteText(data); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write text: %v\n", err)
		}
	}
}

func handleClipReadRequest(env wire.Envelope, topicURL string) {
	mime := env.Mime
	if mime == "" {
		mime = "text/plain"
	}

	var data []byte
	var err error
	if strings.HasPrefix(mime, "image/png") {
		data, err = clipboard.ReadImagePNG()
	} else {
		data, err = clipboard.ReadText()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: clip-read: %v\n", err)
		resp := wire.Envelope{Kind: "clip-read-response", ID: env.ID}
		resp.Body = base64.StdEncoding.EncodeToString([]byte("ERROR: " + err.Error()))
		body, _ := json.Marshal(resp)
		ntfy.PublishMessage(topicURL, string(body))
		return
	}

	resp := wire.Envelope{Kind: "clip-read-response", ID: env.ID, Mime: mime}

	if len(data) <= inlineThreshold && !strings.HasPrefix(mime, "image/") {
		resp.Body = base64.StdEncoding.EncodeToString(data)
		body, _ := json.Marshal(resp)
		if err := ntfy.PublishMessage(topicURL, string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-read response: %v\n", err)
		}
	} else {
		respJSON, _ := json.Marshal(resp)
		filename := "clip.dat"
		if strings.HasPrefix(mime, "image/png") {
			filename = "clip.png"
		}
		if err := ntfy.PublishAttachment(topicURL, string(respJSON), data, filename); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-read response: %v\n", err)
		}
	}
}
