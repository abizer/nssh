package wire

import "encoding/json"

// Envelope is the JSON wire format for messages on the ntfy topic.
type Envelope struct {
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
	Mime string `json:"mime,omitempty"`
	Body string `json:"body,omitempty"`
	ID   string `json:"id,omitempty"`
}

// Parse unmarshals a raw ntfy message body. Returns ok=false if the body
// is not a valid JSON envelope with a non-empty Kind field.
func Parse(body string) (Envelope, bool) {
	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Kind == "" {
		return Envelope{}, false
	}
	return env, true
}
