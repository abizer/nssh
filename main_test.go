package main

import "testing"

func TestParseEnvelope(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantOk   bool
		wantKind string
		wantURL  string
	}{
		{
			name:     "envelope open",
			body:     `{"kind":"open","url":"https://example.com/"}`,
			wantOk:   true,
			wantKind: "open",
			wantURL:  "https://example.com/",
		},
		{
			name:     "envelope open with localhost redirect",
			body:     `{"kind":"open","url":"https://login.example.com/oauth?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcb"}`,
			wantOk:   true,
			wantKind: "open",
			wantURL:  "https://login.example.com/oauth?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcb",
		},
		{
			name:   "envelope missing kind",
			body:   `{"url":"https://example.com/"}`,
			wantOk: false,
		},
		{
			name:   "malformed JSON",
			body:   `{"kind":`,
			wantOk: false,
		},
		{
			name:   "empty body",
			body:   "",
			wantOk: false,
		},
		{
			name:   "bare URL is no longer accepted",
			body:   "https://example.com/",
			wantOk: false,
		},
		{
			name:     "envelope with unknown kind still parses",
			body:     `{"kind":"clip-write","body":"xyz"}`,
			wantOk:   true,
			wantKind: "clip-write",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, ok := parseEnvelope(tc.body)
			if ok != tc.wantOk {
				t.Fatalf("ok=%v want %v", ok, tc.wantOk)
			}
			if !ok {
				return
			}
			if env.Kind != tc.wantKind {
				t.Errorf("Kind=%q want %q", env.Kind, tc.wantKind)
			}
			if tc.wantURL != "" && env.URL != tc.wantURL {
				t.Errorf("URL=%q want %q", env.URL, tc.wantURL)
			}
		})
	}
}
