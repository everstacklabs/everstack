package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// WithSSEAcceptForStreaming sets Accept: text/event-stream automatically
// when the JSON request body contains {"stream": true}. This allows the
// SSE negotiator to engage without requiring the client to set the header.
func WithSSEAcceptForStreaming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			bodyBytes, _ := io.ReadAll(r.Body)
			if len(bodyBytes) > 0 && isLikelyJSON(bodyBytes) {
				var obj map[string]interface{}
				if json.Unmarshal(bodyBytes, &obj) == nil {
					if v, ok := obj["stream"].(bool); ok && v {
						r.Header.Set("Accept", "text/event-stream")
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		next.ServeHTTP(w, r)
	})
}

// WithSSEAcceptForStreamingDefault also enables SSE Accept when the request omits
// the stream flag but the server default desires streaming. It also injects
// "stream": true into the body if defaulting to streaming.
func WithSSEAcceptForStreamingDefault(next http.Handler, defaultStream bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
		}
		var present, value bool
		var obj map[string]interface{}
		if len(bodyBytes) > 0 && isLikelyJSON(bodyBytes) {
			_ = json.Unmarshal(bodyBytes, &obj)
			if v, ok := obj["stream"].(bool); ok {
				present, value = true, v
			}
		}
		// Auto-enable SSE header when streaming is on (explicit or default)
		if (present && value) || (!present && defaultStream) {
			r.Header.Set("Accept", "text/event-stream")
		}
		// If stream is absent and default says to stream, inject stream:true into the body
		if !present && defaultStream {
			if obj == nil {
				obj = make(map[string]interface{}, 1)
			}
			obj["stream"] = true
			if patched, err := json.Marshal(obj); err == nil {
				bodyBytes = patched
			}
		}
		if bodyBytes != nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		next.ServeHTTP(w, r)
	})
}

// StreamingConfig provides dynamic streaming configuration
type StreamingConfig struct {
	// IsEnabled returns whether streaming is enabled at all (hard gate)
	IsEnabled func() bool
	// IsDefault returns whether streaming should be the default when not specified
	IsDefault func() bool
}

// WithSSEAcceptForStreamingDynamic controls streaming based on runtime configuration.
// When streaming is disabled (IsEnabled returns false), it forces stream:false regardless
// of what the client requests. When enabled, it uses IsDefault to determine the default
// behavior for requests that don't specify stream.
func WithSSEAcceptForStreamingDynamic(next http.Handler, config StreamingConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
		}
		var present, value bool
		var obj map[string]interface{}
		if len(bodyBytes) > 0 && isLikelyJSON(bodyBytes) {
			_ = json.Unmarshal(bodyBytes, &obj)
			if v, ok := obj["stream"].(bool); ok {
				present, value = true, v
			}
		}

		// Check if streaming is enabled at all (hard gate)
		streamingEnabled := config.IsEnabled()
		defaultStream := config.IsDefault()

		// If streaming is disabled, force stream:false and remove SSE headers
		if !streamingEnabled {
			r.Header.Del("Accept")
			if present && value {
				// Client requested streaming but it's disabled - force it off
				if obj == nil {
					obj = make(map[string]interface{})
				}
				obj["stream"] = false
				if patched, err := json.Marshal(obj); err == nil {
					bodyBytes = patched
				}
			}
		} else {
			// Streaming is enabled - apply normal logic
			// Auto-enable SSE header when streaming is on (explicit or default)
			if (present && value) || (!present && defaultStream) {
				r.Header.Set("Accept", "text/event-stream")
			}
			// If stream is absent and default says to stream, inject stream:true into the body
			if !present && defaultStream {
				if obj == nil {
					obj = make(map[string]interface{}, 1)
				}
				obj["stream"] = true
				if patched, err := json.Marshal(obj); err == nil {
					bodyBytes = patched
				}
			}
		}

		if bodyBytes != nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		next.ServeHTTP(w, r)
	})
}

func isLikelyJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}
