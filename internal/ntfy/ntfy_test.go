package ntfy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishMessage(t *testing.T) {
	var gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()

	body := `{"kind":"open","url":"https://example.com"}`
	if err := PublishMessage(srv.URL, body); err != nil {
		t.Fatal(err)
	}
	if gotBody != body {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
	if gotCT != "text/plain" {
		t.Errorf("content-type = %q, want text/plain", gotCT)
	}
}

func TestPublishMessageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()
	if err := PublishMessage(srv.URL, "test"); err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestPublishAttachment(t *testing.T) {
	var gotMethod, gotFilename, gotMessage string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotFilename = r.Header.Get("Filename")
		gotMessage = r.Header.Get("X-Message")
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	data := []byte("fake png bytes")
	msg := `{"kind":"clip-write","mime":"image/png"}`
	if err := PublishAttachment(srv.URL, msg, data, "clip.png"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotFilename != "clip.png" {
		t.Errorf("Filename = %q", gotFilename)
	}
	if gotMessage != msg {
		t.Errorf("X-Message = %q", gotMessage)
	}
	if !bytes.Equal(gotBody, data) {
		t.Errorf("body mismatch")
	}
}

func TestFetchAttachment(t *testing.T) {
	want := []byte("binary data here")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(want)
	}))
	defer srv.Close()
	got, err := FetchAttachment(srv.URL + "/file/abc.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFetchAttachmentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if _, err := FetchAttachment(srv.URL + "/gone"); err == nil {
		t.Fatal("expected error for 404")
	}
}
