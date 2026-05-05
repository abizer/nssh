package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/abizer/nssh/v2/internal/clipboard"
	"github.com/abizer/nssh/v2/internal/ntfy"
	"github.com/abizer/nssh/v2/internal/wire"
)

// handleClipWrite writes incoming clipboard data to the local clipboard.
// body is the pre-decoded inline payload (nil when none); when nil and an
// attachment is present, the attachment is fetched.
func handleClipWrite(env wire.Envelope, att *ntfy.Attachment, body []byte) {
	data := body
	if data == nil {
		if att == nil || att.URL == "" {
			fmt.Fprintln(os.Stderr, "nssh: clip-write: no data (empty body and no attachment)")
			return
		}
		var err error
		data, err = ntfy.FetchAttachment(att.URL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: clip-write: %v\n", err)
			return
		}
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
