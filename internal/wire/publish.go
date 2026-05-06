package wire

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/abizer/nssh/v2/internal/ntfy"
)

// InlineThreshold is the size cutoff for inline-vs-attachment publish.
// Payloads ≤ InlineThreshold bytes with a non-image mime ride as base64
// inside the envelope's Body field; larger payloads and all images go
// out as ntfy attachments.
const InlineThreshold = 3 * 1024

// Publish marshals env and ships it to topicURL, choosing inline (base64
// in Body) vs. attachment automatically based on len(data) and env.Mime.
// data may be nil for envelopes that carry no payload.
//
// env is taken by value so the inline-mode mutation of env.Body does not
// leak back to the caller — useful for log lines built from the same
// envelope after publish.
func Publish(topicURL string, env Envelope, data []byte) error {
	if len(data) <= InlineThreshold && !strings.HasPrefix(env.Mime, "image/") {
		if data != nil {
			env.Body = base64.StdEncoding.EncodeToString(data)
		}
		body, _ := json.Marshal(env)
		return ntfy.PublishMessage(topicURL, string(body))
	}
	body, _ := json.Marshal(env)
	filename := "clip.dat"
	if strings.HasPrefix(env.Mime, "image/png") {
		filename = "clip.png"
	}
	return ntfy.PublishAttachment(topicURL, string(body), data, filename)
}
