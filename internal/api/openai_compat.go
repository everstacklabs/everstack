package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// This file holds the response-side converters for the /openai/v1 surface.
// The request side (Bearer -> x-evs-api-key, path rewrite, content-as-string ->
// content-as-array) lives in api.go; everything here turns the gateway's
// proto-JSON output back into OpenAI wire shapes.

// grpcCodeToHTTPStatus maps the gRPC status codes grpc-gateway leaks into JSON
// error bodies onto the HTTP statuses an OpenAI client expects. grpc-gateway
// usually sets a matching HTTP status itself, but the gateway's error
// middleware sometimes flattens everything to 200/500 — the body code is the
// reliable signal.
func grpcCodeToHTTPStatus(code int) int {
	switch code {
	case 3: // INVALID_ARGUMENT
		return http.StatusBadRequest
	case 5: // NOT_FOUND
		return http.StatusNotFound
	case 7: // PERMISSION_DENIED
		return http.StatusForbidden
	case 8: // RESOURCE_EXHAUSTED
		return http.StatusTooManyRequests
	case 16: // UNAUTHENTICATED
		return http.StatusUnauthorized
	case 4: // DEADLINE_EXCEEDED
		return http.StatusGatewayTimeout
	case 12: // UNIMPLEMENTED
		return http.StatusNotImplemented
	case 14: // UNAVAILABLE
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// openAIErrorType names the error class the way the OpenAI API does, so SDK
// retry/backoff heuristics keyed on `type` keep working.
func openAIErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusNotFound:
		return "invalid_request_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}

// protoErrorToOpenAI recognises the two error shapes the /v1 chain emits —
// the gateway error middleware's {"error":{"code","message","type"}} and
// grpc-gateway's bare {"code","message"} — and rewrites them into the OpenAI
// error envelope with a matching HTTP status. Returns ok=false when the body
// is not an error, so callers keep the original bytes.
func protoErrorToOpenAI(body []byte, fallbackStatus int) ([]byte, int, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, 0, false
	}
	var probe struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(trimmed, &probe) != nil {
		return nil, 0, false
	}

	var code int
	var message string
	switch {
	case probe.Error != nil && probe.Error.Message != "":
		code = probe.Error.Code
		message = probe.Error.Message
	case probe.Message != "" && probe.Code != 0:
		code = probe.Code
		message = probe.Message
	default:
		return nil, 0, false
	}

	status := fallbackStatus
	if mapped := grpcCodeToHTTPStatus(code); code != 0 {
		status = mapped
	}
	if status < 400 {
		status = http.StatusInternalServerError
	}

	out, err := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    openAIErrorType(status),
			"param":   nil,
			"code":    nil,
		},
	})
	if err != nil {
		return nil, 0, false
	}
	return out, status, true
}

// gatewayModelsToOpenAI converts the gateway's ListModels response
// ({"providers":[{"provider":"openai","models":[...]}]}) into the OpenAI
// GET /v1/models list shape.
func gatewayModelsToOpenAI(body []byte) ([]byte, bool) {
	var in struct {
		Providers []struct {
			Provider string   `json:"provider"`
			Models   []string `json:"models"`
		} `json:"providers"`
	}
	if json.Unmarshal(body, &in) != nil {
		return nil, false
	}
	data := make([]map[string]interface{}, 0, 8)
	seen := map[string]struct{}{}
	for _, p := range in.Providers {
		for _, m := range p.Models {
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			data = append(data, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"created":  0,
				"owned_by": p.Provider,
			})
		}
	}
	out, err := json.Marshal(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
	if err != nil {
		return nil, false
	}
	return out, true
}

// protoEmbeddingsToOpenAI unwraps the grpc-gateway stream envelope around the
// Embeddings RPC. The inner EmbeddingsResponse already follows the OpenAI
// shape (object/data/model/usage), but the server-streaming transcoding wraps
// each frame as {"result":{...}} on its own line. Merge the frames' data
// arrays and keep the last non-empty scalar fields.
func protoEmbeddingsToOpenAI(body []byte) ([]byte, bool) {
	merged := map[string]interface{}{"object": "list"}
	var data []interface{}
	any := false

	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(line, &top) != nil {
			continue
		}
		raw := json.RawMessage(line)
		if res, ok := top["result"]; ok {
			raw = res
		}
		var frame map[string]interface{}
		if json.Unmarshal(raw, &frame) != nil {
			continue
		}
		if len(frame) == 0 {
			continue
		}
		any = true
		if d, ok := frame["data"].([]interface{}); ok {
			data = append(data, d...)
		}
		for _, k := range []string{"model", "id", "usage", "object"} {
			if v, ok := frame[k]; ok && v != nil && v != "" {
				merged[k] = v
			}
		}
	}
	if !any {
		return nil, false
	}
	merged["data"] = data
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, false
	}
	return out, true
}

// sseSniffWriter passes an SSE response through untouched but captures a
// non-SSE response for post-processing. The /openai/v1 streaming path needs
// this because whether the /v1 chain streams depends on the instance's SSE
// feature flag: when SSE is on we must NOT buffer (chunks flow live), and
// when it is off the chain answers with newline-delimited proto-JSON that has
// to be converted to a single OpenAI JSON body instead.
type sseSniffWriter struct {
	w         http.ResponseWriter
	streaming bool
	decided   bool
	header    http.Header
	buf       bytes.Buffer
	status    int
}

func (s *sseSniffWriter) Header() http.Header {
	if s.decided && s.streaming {
		return s.w.Header()
	}
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}

func (s *sseSniffWriter) decide(status int) {
	s.decided = true
	s.status = status
	ct := s.header.Get("Content-Type")
	s.streaming = strings.HasPrefix(strings.ToLower(ct), "text/event-stream")
	if s.streaming {
		dst := s.w.Header()
		for k, vs := range s.header {
			for _, v := range vs {
				dst.Add(k, v)
			}
		}
		s.w.WriteHeader(status)
	}
}

func (s *sseSniffWriter) WriteHeader(status int) {
	if s.decided {
		return
	}
	s.decide(status)
}

func (s *sseSniffWriter) Write(p []byte) (int, error) {
	if !s.decided {
		s.decide(http.StatusOK)
	}
	if s.streaming {
		return s.w.Write(p)
	}
	return s.buf.Write(p)
}

// Flush forwards flushes only in streaming mode; buffered mode flushes at the
// end after conversion.
func (s *sseSniffWriter) Flush() {
	if s.streaming {
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// finishBuffered converts and sends the captured non-SSE response. Used when
// the client asked for a stream but the chain answered with plain JSON
// (SSE disabled): degrade to a single chat.completion body.
func (s *sseSniffWriter) finishBuffered() {
	if s.streaming {
		return
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	body := s.buf.Bytes()
	if status == http.StatusOK {
		if conv, ok := protoChatCompletionToOpenAI(body); ok {
			body = conv
		}
	} else if conv, mapped, ok := protoErrorToOpenAI(body, status); ok {
		body, status = conv, mapped
	}
	dst := s.w.Header()
	for k, vs := range s.header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	dst.Set("Content-Type", "application/json")
	s.w.WriteHeader(status)
	_, _ = s.w.Write(body)
}
