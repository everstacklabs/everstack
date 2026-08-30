package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WrapWithSSENegotiation wraps an http.Handler so that when the client sends
// Accept: text/event-stream, we:
//   - set stream=true in the JSON body (if present)
//   - set SSE headers on the response
//   - reframe each JSON line written by the downstream handler into an SSE event
//     as: "data: <json>\n\n"
//
// For non-SSE requests, the handler is passed through untouched.
func WrapWithSSENegotiation(next http.Handler) http.Handler {
	return WrapWithSSENegotiationWith(next, func(_ *http.Request) string { return "proto" })
}

// WrapWithSSENegotiationWith allows selecting an SSE chunk format per request.
// selectFormat should return a format key like "openai" or "proto".
func WrapWithSSENegotiationWith(next http.Handler, selectFormat func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Engage SSE if client explicitly requested streaming in body, even if Accept is missing
		streamWanted := false
		if r.Body != nil {
			bodyBytes, _ := io.ReadAll(r.Body)
			if len(bodyBytes) > 0 {
				var obj map[string]interface{}
				_ = json.Unmarshal(bodyBytes, &obj)
				if v, ok := obj["stream"].(bool); ok && v {
					streamWanted = true
				}
			}
			// restore body for downstream
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Route selector may indicate SSE is desired (e.g., openai format)
		format := strings.ToLower(selectFormat(r))
		forceSSE := format != "proto"

		if !acceptsSSE(r.Header.Get("Accept")) && !streamWanted && !forceSSE {
			next.ServeHTTP(w, r)
			return
		}
		// ensure downstream sees SSE expectation
		r.Header.Set("Accept", "text/event-stream")

		// Prepare SSE response headers
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")

		// Wrap ResponseWriter to transform newline-delimited JSON to SSE events
		sseW := &sseWriter{ResponseWriter: w, buf: make([]byte, 0, 4096), format: format}
		next.ServeHTTP(sseW, r)
		sseW.flushPending()
	})
}

func acceptsSSE(accept string) bool {
	return strings.Contains(strings.ToLower(accept), "text/event-stream")
}

// sseWriter wraps ResponseWriter and converts each JSON line into an SSE data event.
// It also reshapes grpc-gateway streaming JSON (with top-level {"result": {...}})
// into OpenAI-style chunk objects and emits a final [DONE] sentinel.
type sseWriter struct {
	http.ResponseWriter
	buf         []byte
	format      string
	wroteHeader bool

	// Per-stream state for the "openai" chunk format: the first frame's
	// id/model/created are reused on every chunk (OpenAI keeps them stable
	// across a stream), and roleSent tracks which choice indexes have had
	// their priming `delta: {"role":"assistant"}` emitted.
	streamID      string
	streamModel   string
	streamCreated int64
	roleSent      map[int]bool
}

// Implement http.Flusher to avoid "Flush not supported" errors.
func (s *sseWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Swallow duplicate WriteHeader calls to prevent superfluous WriteHeader logs.
func (s *sseWriter) WriteHeader(status int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	// Enforce SSE headers at the moment of sending headers to the client,
	// so downstream handlers cannot override Content-Type.
	h := s.ResponseWriter.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	s.ResponseWriter.WriteHeader(status)
}

func (s *sseWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	reader := bufio.NewReader(bytes.NewReader(s.buf))
	var consumed int
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// No full line available yet; keep buffered
			break
		}
		consumed += len(line)
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}
		// Normalize grpc-gateway event-stream framing
		if strings.HasPrefix(trimmed, "event: ") {
			// skip event labels
			continue
		}
		if strings.HasPrefix(trimmed, "data: ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "data: "))
		}
		// Drop any non-JSON stray lines to avoid leaking logs into SSE
		if !isLikelyJSON([]byte(trimmed)) {
			continue
		}

		// Ensure headers are written once before body
		if !s.wroteHeader {
			s.WriteHeader(http.StatusOK)
		}

		payload := s.reframeChunk(trimmed)
		if _, e := s.ResponseWriter.Write([]byte("data: ")); e != nil {
			return 0, e
		}
		if _, e := s.ResponseWriter.Write(payload); e != nil {
			return 0, e
		}
		if _, e := s.ResponseWriter.Write([]byte("\n\n")); e != nil {
			return 0, e
		}
		if f, ok := s.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
	// keep only the unconsumed tail in buffer
	s.buf = s.buf[consumed:]
	return len(p), nil
}

func (s *sseWriter) reframeChunk(line string) []byte {
	if s.format == "openai" {
		return s.transformToOpenAIChunk(line)
	}
	// Default: pass-through JSON, but wrapped as SSE by Write()
	return []byte(line)
}

func (s *sseWriter) transformToOpenAIChunk(line string) []byte {
	// Try to parse JSON; if it fails, pass through
	var top map[string]interface{}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return []byte(line)
	}
	// grpc-gateway uses {"result": {...}}, extract if present
	obj, ok := top["result"].(map[string]interface{})
	if !ok {
		obj = top
	}

	// Error frames (gateway error middleware or grpc-gateway status JSON)
	// must not be swallowed into an empty delta — surface them as an OpenAI
	// error payload so clients fail loudly instead of "completing" empty.
	if errBody := openAIErrorFromFrame(obj); errBody != nil {
		return errBody
	}

	// Track stream-stable fields from whichever frame carries them.
	if id, ok := obj["id"].(string); ok && id != "" {
		s.streamID = id
	}
	if m, ok := obj["model"].(string); ok && m != "" {
		s.streamModel = m
	}
	if s.streamCreated == 0 {
		switch c := obj["created"].(type) {
		case float64:
			s.streamCreated = int64(c)
		case string:
			var n int64
			if _, err := fmt.Sscanf(c, "%d", &n); err == nil {
				s.streamCreated = n
			}
		}
	}
	if s.roleSent == nil {
		s.roleSent = map[int]bool{}
	}

	outChoices := make([]map[string]interface{}, 0, 1)
	if choices, ok := obj["choices"].([]interface{}); ok {
		for i, c := range choices {
			ch, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			idx := i
			if v, ok := ch["index"].(float64); ok {
				idx = int(v)
			}
			text := ""
			var providerContent []interface{}
			if msg, ok := ch["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].([]interface{}); ok {
					for _, p := range content {
						if part, ok := p.(map[string]interface{}); ok {
							if t, ok := part["text"].(string); ok {
								text += t
							}
							if native, ok := providerContentValue(part); ok {
								providerContent = append(providerContent, native)
							}
						}
					}
				}
			}
			// Alternatively, if already delta-like
			if delta, ok := ch["delta"].(map[string]interface{}); ok {
				if c, ok := delta["content"].(string); ok {
					text = c
				}
				if native, ok := delta["provider_content"].([]interface{}); ok {
					providerContent = append(providerContent, native...)
				}
			}

			delta := map[string]interface{}{}
			if !s.roleSent[idx] {
				delta["role"] = "assistant"
				s.roleSent[idx] = true
			}
			if text != "" {
				delta["content"] = text
			}
			if len(providerContent) > 0 {
				// Everstack extension: accumulate these opaque native chunks and
				// send them back on the next assistant message as
				// `provider_content`.
				delta["provider_content"] = providerContent
			}

			var finish interface{}
			if fr, ok := ch["finish_reason"].(string); ok && fr != "" {
				finish = strings.ToLower(strings.TrimPrefix(fr, "FINISH_REASON_"))
			}

			outChoices = append(outChoices, map[string]interface{}{
				"index":         idx,
				"delta":         delta,
				"finish_reason": finish,
			})
		}
	}

	chunk := map[string]interface{}{
		"id":      s.streamID,
		"object":  "chat.completion.chunk",
		"created": s.streamCreated,
		"model":   s.streamModel,
		"choices": outChoices,
	}
	// Usage arrives on the final frame; forward it the way OpenAI does with
	// stream_options.include_usage (an extra field is harmless otherwise).
	if u, ok := obj["usage"].(map[string]interface{}); ok && len(u) > 0 {
		chunk["usage"] = u
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return []byte(line)
	}
	return b
}

func providerContentValue(part map[string]interface{}) (interface{}, bool) {
	raw, _ := part["provider_json"].(string)
	if raw == "" {
		raw, _ = part["providerJson"].(string)
	}
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, false
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, false
	}
	return value, true
}

// openAIErrorFromFrame returns an OpenAI-shaped error body when the frame is
// an error payload ({"error":{...}} or a bare grpc-gateway {"code","message"}
// status), or nil when the frame is a normal delta.
func openAIErrorFromFrame(obj map[string]interface{}) []byte {
	var message string
	if e, ok := obj["error"].(map[string]interface{}); ok {
		if m, ok := e["message"].(string); ok && m != "" {
			message = m
		}
	} else if m, ok := obj["message"].(string); ok && m != "" {
		if _, hasCode := obj["code"]; hasCode {
			if _, hasChoices := obj["choices"]; !hasChoices {
				message = m
			}
		}
	}
	if message == "" {
		return nil
	}
	b, err := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "api_error",
			"param":   nil,
			"code":    nil,
		},
	})
	if err != nil {
		return nil
	}
	return b
}

func (s *sseWriter) flushPending() {
	// Emit [DONE] to signal end of stream
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	_, _ = s.ResponseWriter.Write([]byte("data: [DONE]\n\n"))
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	s.buf = s.buf[:0]
}
