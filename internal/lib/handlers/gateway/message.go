package gateway

import "strings"

// Helpers to build normalized messages.

func Text(text string) ContentPart {
	return ContentPart{Type: "text", Text: &text}
}

func ImageURL(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &url}
}

func NewMessage(role MessageRole, parts ...ContentPart) Message {
	return Message{Role: role, Content: parts}
}

// ConstructConfigFromHeaders builds a minimal per-request config override from headers.
// You can extend this to parse routing hints like provider, model, temperature, etc.
func ConstructConfigFromHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range headers {
		// Example: allow MF-Model and MF-Provider overrides
		switch strings.ToLower(k) {
		case "mf-model":
			out["model"] = v
		case "mf-provider":
			out["provider"] = v
		case "mf-temperature":
			out["temperature"] = v
		}
	}
	return out
}
