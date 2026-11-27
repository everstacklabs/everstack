package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/api/common"
	apikey "github.com/everstacklabs/everstack/internal/lib/apikey"
)

// MaxBodyBytes bounds only the Messages surface, before JSON decoding.
const MaxBodyBytes int64 = 32 << 20

// New fronts the OpenAI wire-compatible chat handler. The supplied handler owns
// all authentication, tenant resolution, policy, routing, and usage accounting.
func New(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, 405, "method not allowed")
			return
		}
		if r.URL.Path == "/v1/messages/count_tokens" {
			writeError(w, 404, "token counting is not supported")
			return
		}
		reader := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		raw, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "request body exceeds 32 MiB")
			} else {
				writeError(w, 400, "failed to read request body")
			}
			return
		}
		body, stream, err := translateRequest(raw)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		req := r.Clone(r.Context())
		req.URL.Path = "/v1/chat/completions"
		req.URL.RawPath = ""
		req.RequestURI = req.URL.RequestURI()
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = nil
		req.TransferEncoding = nil
		req.Header.Del("Content-Length")
		req.Header.Set("Content-Type", "application/json")
		// Explicit canonical or legacy gateway credentials win, followed by x-api-key,
		// followed by an issued Bearer key. The inner chain still validates the key.
		key := common.GetHTTPHeader(req.Header, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey)
		if key == "" {
			key = strings.TrimSpace(req.Header.Get("x-api-key"))
		}
		auth := strings.TrimSpace(req.Header.Get("Authorization"))
		if key == "" && strings.HasPrefix(auth, "Bearer ") {
			candidate := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if apikey.HasIssuedKeyPrefix(candidate) {
				key = candidate
			}
		}
		if key != "" {
			req.Header.Set(common.EverstackApiKey, key)
			req.Header.Del("Authorization")
		}
		req.Header.Del("x-api-key")
		// Wire negotiation is owned by this adapter, never by client SSE headers.
		req.Header.Del("Accept")
		req.Header.Del("X-SSE-Format")
		tw := &responseWriter{w: w, header: make(http.Header), streamWanted: stream}
		inner.ServeHTTP(tw, req)
		tw.finish()
	})
}

func errorType(status int) string {
	switch status {
	case 400, 405:
		return "invalid_request_error"
	case 401:
		return "authentication_error"
	case 402:
		return "billing_error"
	case 403:
		return "permission_error"
	case 404:
		return "not_found_error"
	case 413:
		return "request_too_large"
	case 429:
		return "rate_limit_error"
	case 503, 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}
func errorObject(status int, message string) object {
	return object{"type": "error", "error": object{"type": errorType(status), "message": message}}
}
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorObject(status, message))
}
func safeMessage(raw []byte, status int) string {
	var e struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &nested) == nil && nested.Message != "" {
			return nested.Message
		}
		if e.Message != "" {
			return e.Message
		}
	}
	// Never reflect arbitrary upstream HTML or unknown bodies.
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "gateway request failed"
}
func copyHeaders(dst, src http.Header) {
	for k, values := range src {
		switch strings.ToLower(k) {
		case "content-length", "content-type", "content-encoding", "transfer-encoding", "connection":
			continue
		}
		dst[k] = append([]string(nil), values...)
	}
}

type usage struct {
	Input   int `json:"prompt_tokens"`
	Output  int `json:"completion_tokens"`
	Details *struct {
		Cached int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u usage) anthropic() object {
	input := u.Input
	if u.Details != nil {
		input -= u.Details.Cached
	}
	if input < 0 {
		input = 0
	}
	out := object{"input_tokens": input, "output_tokens": u.Output}
	if u.Details != nil {
		out["cache_read_input_tokens"] = u.Details.Cached
	}
	return out
}

type toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type chatMessage struct {
	Content *string    `json:"content"`
	Calls   []toolCall `json:"tool_calls"`
}
type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Usage   *usage `json:"usage"`
	Choices []struct {
		Index   int         `json:"index"`
		Message chatMessage `json:"message"`
		Delta   chatMessage `json:"delta"`
		Finish  string      `json:"finish_reason"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

func messageID(id string) string {
	if strings.HasPrefix(id, "msg_") {
		return id
	}
	return "msg_" + id
}
func stopReason(finish string) any {
	switch finish {
	case "stop", "stop_sequence":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	default:
		return nil
	}
}
func message(resp chatResponse) (object, error) {
	content := []object{}
	var finish string
	if len(resp.Choices) > 0 {
		c := resp.Choices[0]
		finish = c.Finish
		if c.Message.Content != nil && *c.Message.Content != "" {
			content = append(content, object{"type": "text", "text": *c.Message.Content})
		}
		for _, tc := range c.Message.Calls {
			input, _, err := toolArguments(tc.Function.Arguments)
			if err != nil {
				return nil, err
			}
			content = append(content, object{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
		}
	}
	u := usage{}
	if resp.Usage != nil {
		u = *resp.Usage
	}
	return object{"id": messageID(resp.ID), "type": "message", "role": "assistant", "model": resp.Model, "content": content, "stop_reason": stopReason(finish), "stop_sequence": nil, "usage": u.anthropic()}, nil
}

var errMalformedToolArguments = errors.New("the model returned malformed tool arguments")

func toolArguments(args string) (any, string, error) {
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	var input any
	if json.Unmarshal([]byte(args), &input) != nil {
		return nil, "", errMalformedToolArguments
	}
	return input, args, nil
}
