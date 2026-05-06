package wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureServer returns an httptest server that records the last request's
// method, body, and the Filename / X-Message attachment headers.
type capture struct {
	method, filename, message string
	body                      []byte
}

func captureServer(t *testing.T) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.filename = r.Header.Get("Filename")
		c.message = r.Header.Get("X-Message")
		c.body, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestPublishInlineSmallText(t *testing.T) {
	srv, got := captureServer(t)
	env := Envelope{Kind: "clip-write", Mime: "text/plain"}
	data := []byte("hello, world")

	if err := Publish(srv.URL, env, data); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" {
		t.Errorf("method = %q, want POST (inline path)", got.method)
	}

	var sent Envelope
	if err := json.Unmarshal(got.body, &sent); err != nil {
		t.Fatalf("unmarshal: %v (body=%q)", err, got.body)
	}
	if sent.Kind != "clip-write" || sent.Mime != "text/plain" {
		t.Errorf("envelope round trip mismatched: %+v", sent)
	}
	decoded, err := base64.StdEncoding.DecodeString(sent.Body)
	if err != nil {
		t.Fatalf("Body not base64: %v", err)
	}
	if !bytes.Equal(decoded, data) {
		t.Errorf("Body decode mismatch: %q vs %q", decoded, data)
	}
}

func TestPublishAttachmentForImage(t *testing.T) {
	srv, got := captureServer(t)
	env := Envelope{Kind: "clip-write", Mime: "image/png"}
	data := []byte{0x89, 0x50, 0x4E, 0x47} // small but image → attachment

	if err := Publish(srv.URL, env, data); err != nil {
		t.Fatal(err)
	}
	if got.method != "PUT" {
		t.Errorf("method = %q, want PUT (attachment path)", got.method)
	}
	if got.filename != "clip.png" {
		t.Errorf("filename = %q, want clip.png", got.filename)
	}
	if !bytes.Equal(got.body, data) {
		t.Errorf("attachment body mismatch")
	}
	// X-Message carries the JSON envelope.
	var sent Envelope
	if err := json.Unmarshal([]byte(got.message), &sent); err != nil {
		t.Fatalf("X-Message not envelope JSON: %v", err)
	}
	if sent.Body != "" {
		t.Errorf("attachment envelope should not carry inline Body, got %q", sent.Body)
	}
}

func TestPublishAttachmentForLargeText(t *testing.T) {
	srv, got := captureServer(t)
	env := Envelope{Kind: "clip-write", Mime: "text/plain"}
	data := []byte(strings.Repeat("x", InlineThreshold+1))

	if err := Publish(srv.URL, env, data); err != nil {
		t.Fatal(err)
	}
	if got.method != "PUT" {
		t.Errorf("method = %q, want PUT (over threshold)", got.method)
	}
	if got.filename != "clip.dat" {
		t.Errorf("filename = %q, want clip.dat (non-image)", got.filename)
	}
}

func TestPublishCallerEnvelopeUnmutated(t *testing.T) {
	srv, _ := captureServer(t)
	env := Envelope{Kind: "clip-write", Mime: "text/plain"}
	if err := Publish(srv.URL, env, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if env.Body != "" {
		t.Errorf("caller's envelope mutated: Body = %q", env.Body)
	}
}

func TestPublishNilData(t *testing.T) {
	srv, got := captureServer(t)
	env := Envelope{Kind: "open", URL: "https://example.com"}
	if err := Publish(srv.URL, env, nil); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" {
		t.Errorf("method = %q, want POST", got.method)
	}
	var sent Envelope
	if err := json.Unmarshal(got.body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Body != "" {
		t.Errorf("Body should be empty for nil data, got %q", sent.Body)
	}
	if sent.URL != "https://example.com" {
		t.Errorf("URL not preserved: %q", sent.URL)
	}
}
