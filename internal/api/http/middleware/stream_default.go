package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// WithStreamingDefault ensures stream=true is set in JSON bodies so downstream takes the streaming path.
// It only modifies requests with a JSON object/array body; others pass through untouched.
func WithStreamingDefault(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) == 0 {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next.ServeHTTP(w, r)
			return
		}
		// Only attempt to patch JSON bodies
		trimmed := bytes.TrimSpace(bodyBytes)
		if !(trimmed[0] == '{' || trimmed[0] == '[') {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next.ServeHTTP(w, r)
			return
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &obj); err == nil {
			obj["stream"] = true
			if patched, err := json.Marshal(obj); err == nil {
				bodyBytes = patched
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		next.ServeHTTP(w, r)
	})
}
