package wire

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantOk   bool
		wantKind string
		wantURL  string
	}{
		{"envelope open", `{"kind":"open","url":"https://example.com/"}`, true, "open", "https://example.com/"},
		{"with localhost redirect", `{"kind":"open","url":"https://login.example.com/oauth?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcb"}`, true, "open", "https://login.example.com/oauth?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcb"},
		{"missing kind", `{"url":"https://example.com/"}`, false, "", ""},
		{"malformed JSON", `{"kind":`, false, "", ""},
		{"empty body", "", false, "", ""},
		{"bare URL rejected", "https://example.com/", false, "", ""},
		{"unknown kind parses", `{"kind":"clip-write","body":"xyz"}`, true, "clip-write", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, ok := Parse(tc.body)
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
