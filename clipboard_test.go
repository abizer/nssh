package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleClipWriteInlineText(t *testing.T) {
	skipUnlessClipboard(t)
	want := "hello from remote VM"
	env := envelope{
		Kind: "clip-write",
		Mime: "text/plain",
		Body: base64.StdEncoding.EncodeToString([]byte(want)),
	}
	handleClipWrite(env, nil)

	got, err := readText()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}

func TestHandleClipWriteAttachment(t *testing.T) {
	skipUnlessClipboard(t)
	want := "large text payload here"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(want))
	}))
	defer srv.Close()

	env := envelope{Kind: "clip-write", Mime: "text/plain"}
	att := &ntfyAttachment{URL: srv.URL + "/clip.txt"}
	handleClipWrite(env, att)

	got, err := readText()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}

func TestHandleClipReadRequestInline(t *testing.T) {
	skipUnlessClipboard(t)
	// Seed the clipboard with known text.
	writeText([]byte("read me back"))

	var published string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		published = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	env := envelope{Kind: "clip-read-request", ID: "test-123", Mime: "text/plain"}
	handleClipReadRequest(env, srv.URL)

	// Parse the published response.
	var resp envelope
	if err := json.Unmarshal([]byte(published), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %q)", err, published)
	}
	if resp.Kind != "clip-read-response" {
		t.Errorf("kind = %q, want clip-read-response", resp.Kind)
	}
	if resp.ID != "test-123" {
		t.Errorf("id = %q, want test-123", resp.ID)
	}
	decoded, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "read me back" {
		t.Errorf("body = %q, want %q", decoded, "read me back")
	}
}

func TestHandleClipReadRequestLargeText(t *testing.T) {
	skipUnlessClipboard(t)
	// Seed clipboard with text larger than inlineThreshold.
	big := strings.Repeat("x", inlineThreshold+100)
	writeText([]byte(big))

	var gotMethod string
	var gotMessage string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotMessage = r.Header.Get("X-Message")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	env := envelope{Kind: "clip-read-request", ID: "big-1", Mime: "text/plain"}
	handleClipReadRequest(env, srv.URL)

	if gotMethod != "PUT" {
		t.Errorf("method = %q, want PUT (attachment path)", gotMethod)
	}
	if !strings.Contains(gotMessage, "clip-read-response") {
		t.Errorf("X-Message should contain clip-read-response, got %q", gotMessage)
	}
	if string(gotBody) != big {
		t.Errorf("attachment body len = %d, want %d", len(gotBody), len(big))
	}
}
