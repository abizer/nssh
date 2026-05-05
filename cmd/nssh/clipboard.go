package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/abizer/nssh/v2/internal/clipboard"
	"github.com/abizer/nssh/v2/internal/ntfy"
	"github.com/abizer/nssh/v2/internal/wire"
)

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
		if perr := wire.Publish(topicURL, resp, []byte("ERROR: "+err.Error())); perr != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-read error response: %v\n", perr)
		}
		logMessage("out", resp, 0)
		return
	}

	resp := wire.Envelope{Kind: "clip-read-response", ID: env.ID, Mime: mime}
	if err := wire.Publish(topicURL, resp, data); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: clip-read response: %v\n", err)
	}
	logMessage("out", resp, len(data))
}
