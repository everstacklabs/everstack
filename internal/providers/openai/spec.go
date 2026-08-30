package openai

// Spec defines default provider metadata for OpenAI. This keeps provider
// specifics colocated with the connector, avoiding central clutter.
type Spec struct {
	BaseURL    string
	APIVersion string
	Headers    HeadersSpec
	Streaming  StreamingSpec
}

type HeadersSpec struct {
	// Auth header name and printf-style value format
	AuthHeaderName   string
	AuthHeaderFormat string // e.g., "Bearer %s"

	// Additional headers the gateway can accept from clients and forward
	Acceptable []string
}

type StreamingSpec struct {
	SSEEnabled bool
	MediaType  string // typical: text/event-stream
}

// DefaultSpec returns OpenAI defaults; gateway.yaml may override these.
func DefaultSpec() Spec {
	return Spec{
		BaseURL:    "https://api.openai.com/v1",
		APIVersion: "",
		Headers: HeadersSpec{
			AuthHeaderName:   "Authorization",
			AuthHeaderFormat: "Bearer %s",
			Acceptable: []string{
				"OpenAI-Organization",
				"OpenAI-Project",
				// Everstack-specific override hints
				"MF-Model",
				"MF-Temperature",
			},
		},
		Streaming: StreamingSpec{
			SSEEnabled: true,
			MediaType:  "text/event-stream",
		},
	}
}
