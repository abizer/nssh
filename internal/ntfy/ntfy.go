package ntfy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// Attachment is the attachment metadata from ntfy's JSON stream.
type Attachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

// Msg is a single message from the ntfy JSON stream.
type Msg struct {
	Event      string      `json:"event"`
	Message    string      `json:"message"`
	Attachment *Attachment `json:"attachment,omitempty"`
}

// PublishMessage POSTs a text message to the given ntfy topic URL.
func PublishMessage(topicURL, body string) error {
	resp, err := http.Post(topicURL, "application/json", bytes.NewBufferString(body))
	if err != nil {
		return fmt.Errorf("ntfy publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy publish: %s: %s", resp.Status, b)
	}
	return nil
}

// PublishAttachment PUTs binary data as an attachment to the topic URL.
// The message string is sent in the X-Message header so the subscriber
// receives it alongside the attachment metadata.
func PublishAttachment(topicURL, message string, data []byte, filename string) error {
	req, err := http.NewRequest("PUT", topicURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("ntfy attach: %w", err)
	}
	req.Header.Set("Filename", filename)
	req.Header.Set("X-Message", message)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy attach: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy attach: %s: %s", resp.Status, b)
	}
	return nil
}

// FetchAttachment downloads binary data from an ntfy attachment URL.
func FetchAttachment(attachURL string) ([]byte, error) {
	resp, err := http.Get(attachURL)
	if err != nil {
		return nil, fmt.Errorf("ntfy fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ntfy fetch: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
