package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// responseWriter intercepts status and headers until either a real SSE chunk or
// a complete unary/error response is known. Only one SSE frame is buffered.
const (
	MaxInnerBodyBytes = 64 << 20
	MaxSSEEventBytes  = 8 << 20
)

type responseWriter struct {
	eventDataBytes    int
	inputKnownAtStart bool
	inputAtStart      int
	w                 http.ResponseWriter
	header            http.Header
	status            int
	streamWanted      bool
	buffer            bytes.Buffer
	data              []string
	started, done     bool
	hasContent        bool
	textOpen          bool
	index             int
	finishReason      string
	usage             usage
	sawFinish         bool
	tools             map[int]bool
	writeErr          error
}

func (s *responseWriter) Header() http.Header { return s.header }
func (s *responseWriter) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
}
func (s *responseWriter) Flush() {
	if s.started {
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
	}
}
func (s *responseWriter) Write(p []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if s.done {
		return len(p), nil
	}
	if s.status == 0 {
		s.status = 200
	}
	if s.status < 200 || s.status >= 300 || !s.streamWanted || !strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") {
		if len(p) > MaxInnerBodyBytes-s.buffer.Len() {
			s.fail(502, "gateway response exceeds 64 MiB limit")
			s.writeErr = io.ErrClosedPipe
			return 0, s.writeErr
		}
		return s.buffer.Write(p)
	}
	n := len(p)
	// SSE data is parsed incrementally, including frames split across writes.
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := len(p)
		if i >= 0 {
			part = i
		}
		// Count partial data lines too, so withholding a newline cannot
		// grow an event beyond its aggregate data budget.
		var prefix [6]byte
		used := copy(prefix[:], s.buffer.Bytes())
		used += copy(prefix[used:], p[:part])
		lineBytes := s.buffer.Len() + part
		dataTooLarge := false
		if used >= 5 && string(prefix[:5]) == "data:" {
			header := 5
			if used >= 6 && prefix[5] == ' ' {
				header = 6
			}
			payload := lineBytes - header
			if lineBytes > 0 {
				last := byte(0)
				if part > 0 {
					last = p[part-1]
				} else {
					last = s.buffer.Bytes()[s.buffer.Len()-1]
				}
				if last == '\r' {
					payload--
				}
			}
			budget := MaxSSEEventBytes - s.eventDataBytes
			if len(s.data) > 0 {
				budget--
			}
			dataTooLarge = payload > budget
		}
		if dataTooLarge || part > MaxSSEEventBytes+7-s.buffer.Len() {
			s.fail(502, "gateway SSE event exceeds 8 MiB limit")
			s.writeErr = io.ErrClosedPipe
			return 0, s.writeErr
		}
		if i < 0 {
			s.buffer.Write(p)
			break
		}
		s.buffer.Write(p[:i])
		line := strings.TrimSuffix(s.buffer.String(), "\r")
		s.buffer.Reset()
		p = p[i+1:]
		if line == "" {
			if len(s.data) > 0 {
				s.frame(strings.Join(s.data, "\n"))
				s.data = nil
				s.eventDataBytes = 0
			}
		} else if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			size := len(value)
			if len(s.data) > 0 {
				size++
			}
			if size > MaxSSEEventBytes-s.eventDataBytes {
				s.fail(502, "gateway SSE event exceeds 8 MiB limit")
				s.writeErr = io.ErrClosedPipe
				return 0, s.writeErr
			}
			s.eventDataBytes += size
			s.data = append(s.data, value)
		}
		if s.done {
			break
		}
	}

	if s.writeErr != nil {
		return 0, s.writeErr
	}
	return n, nil
}
func (s *responseWriter) event(kind string, fields object) {
	if s.writeErr != nil {
		return
	}
	fields["type"] = kind
	raw, err := json.Marshal(fields)
	if err != nil {
		s.writeErr = err
		return
	}
	_, s.writeErr = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", kind, raw)
	s.Flush()
}
func (s *responseWriter) start(resp chatResponse) {
	if s.started {
		return
	}
	copyHeaders(s.w.Header(), s.header)
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.WriteHeader(s.status)
	s.started = true
	u := usage{}
	s.inputKnownAtStart = resp.Usage != nil
	if resp.Usage != nil {
		u = *resp.Usage
	}
	u.Output = 0
	s.inputAtStart = u.anthropic()["input_tokens"].(int)
	msg := object{"id": messageID(resp.ID), "type": "message", "role": "assistant", "model": resp.Model, "content": []object{}, "stop_reason": nil, "stop_sequence": nil, "usage": u.anthropic()}
	s.event("message_start", object{"message": msg})
	s.event("ping", object{})
}
func (s *responseWriter) closeText() {
	if s.textOpen {
		s.event("content_block_stop", object{"index": s.index})
		s.index++
		s.textOpen = false
	}
}
func (s *responseWriter) frame(data string) {
	if s.done {
		return
	}
	if data == "[DONE]" {
		if !s.hasContent {
			s.fail(502, "gateway stream ended without content")
			return
		}
		if !s.sawFinish {
			s.finishReason = "stop"
			if len(s.tools) > 0 {
				s.finishReason = "tool_calls"
			}
		}
		s.complete()
		return
	}
	var resp chatResponse
	if json.Unmarshal([]byte(data), &resp) != nil {
		s.fail(502, "invalid gateway stream")
		return
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		s.fail(streamErrorStatus(resp.Error), safeMessage([]byte(data), 500))
		return
	}
	s.start(resp)
	for i := range resp.Choices {
		if resp.Choices[i].Index != 0 {
			continue
		}
		for n := range resp.Choices[i].Delta.Calls {
			call := &resp.Choices[i].Delta.Calls[n]
			_, args, err := toolArguments(call.Function.Arguments)
			if err != nil {
				s.fail(502, err.Error())
				return
			}
			call.Function.Arguments = args
		}
	}
	if resp.Usage != nil {
		s.usage = *resp.Usage
	}
	for _, c := range resp.Choices {
		if c.Index != 0 {
			continue
		}
		if c.Delta.Content != nil && *c.Delta.Content != "" {
			s.hasContent = true
			if !s.textOpen {
				s.event("content_block_start", object{"index": s.index, "content_block": object{"type": "text", "text": ""}})
				s.textOpen = true
			}
			s.event("content_block_delta", object{"index": s.index, "delta": object{"type": "text_delta", "text": *c.Delta.Content}})
		}
		for _, tc := range c.Delta.Calls {
			if s.tools == nil {
				s.tools = map[int]bool{}
			}
			if s.tools[tc.Index] {
				s.fail(502, "gateway sent an incomplete tool call")
				return
			}
			s.tools[tc.Index] = true
			s.hasContent = true
			s.closeText()
			s.event("content_block_start", object{"index": s.index, "content_block": object{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": object{}}})
			s.event("content_block_delta", object{"index": s.index, "delta": object{"type": "input_json_delta", "partial_json": tc.Function.Arguments}})
			s.event("content_block_stop", object{"index": s.index})
			s.index++
		}
		if c.Finish != "" {
			s.finishReason = c.Finish
			s.sawFinish = true
		}
	}
}
func streamErrorStatus(raw []byte) int {
	var e struct {
		Type string `json:"type"`
		Code any    `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	switch e.Type {
	case "authentication_error":
		return 401
	case "permission_error":
		return 403
	case "not_found_error":
		return 404
	case "invalid_request_error":
		return 400
	case "rate_limit_error":
		return 429
	case "insufficient_quota", "billing_error":
		return 402
	case "overloaded_error":
		return 503
	}
	return 500
}
func (s *responseWriter) fail(status int, msg string) {
	if s.done {
		return
	}
	s.done = true
	if s.started {
		s.event("error", errorObject(status, msg))
	} else {
		copyHeaders(s.w.Header(), s.header)
		writeError(s.w, status, msg)
	}
}
func (s *responseWriter) complete() {
	if s.done {
		return
	}
	if !s.started {
		s.fail(502, "empty gateway stream")
		return
	}
	s.closeText()
	finalUsage := object{"output_tokens": s.usage.Output}
	if !s.inputKnownAtStart || s.usage.anthropic()["input_tokens"].(int) != s.inputAtStart {
		finalUsage["input_tokens"] = s.usage.anthropic()["input_tokens"]
	}
	if s.usage.Details != nil {
		finalUsage["cache_read_input_tokens"] = s.usage.Details.Cached
	}
	s.event("message_delta", object{"delta": object{"stop_reason": stopReason(s.finishReason), "stop_sequence": nil}, "usage": finalUsage})
	s.event("message_stop", object{})
	s.done = true
}
func (s *responseWriter) finish() {
	if s.done {
		return
	}
	if s.status == 0 {
		s.status = 200
	}
	if s.status < 200 || s.status >= 300 {
		copyHeaders(s.w.Header(), s.header)
		writeError(s.w, s.status, safeMessage(s.buffer.Bytes(), s.status))
		return
	}
	if s.streamWanted && strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") {
		// A missing DONE marker is a truncated stream, even after a finish chunk.
		s.fail(502, "gateway stream ended before completion")
		return
	}
	var resp chatResponse
	if json.Unmarshal(s.buffer.Bytes(), &resp) != nil {
		s.fail(502, "invalid gateway response")
		return
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		s.fail(streamErrorStatus(resp.Error), safeMessage(s.buffer.Bytes(), 500))
		return
	}
	if len(resp.Choices) == 0 {
		s.fail(502, "invalid gateway response")
		return
	}
	if s.streamWanted {
		// The existing chat path degrades to unary when SSE is disabled. Preserve
		// Messages streaming semantics by emitting that completed result as events.
		s.start(resp)
		if resp.Usage != nil {
			s.usage = *resp.Usage
		}
		resp.Choices[0].Delta = resp.Choices[0].Message
		for i := range resp.Choices[0].Delta.Calls {
			resp.Choices[0].Delta.Calls[i].Index = i
		}
		raw, _ := json.Marshal(resp)
		s.frame(string(raw))
		s.complete()
		return
	}
	msg, err := message(resp)
	if err != nil {
		s.fail(502, err.Error())
		return
	}
	copyHeaders(s.w.Header(), s.header)
	s.w.Header().Set("Content-Type", "application/json")
	s.w.WriteHeader(s.status)
	_ = json.NewEncoder(s.w).Encode(msg)
}

var _ http.Flusher = (*responseWriter)(nil)
var _ io.Writer = (*responseWriter)(nil)
