package middleware

import (
	"encoding/json"
	"strconv"
	"strings"
)

// SSEFormatOpenAIResponses selects the OpenAI Responses API event stream
// (event: response.created, response.output_text.delta, response.completed,
// ...) for the CreateResponse server stream. The chat "openai" format would
// reshape those frames into chat.completion.chunk objects, which an OpenAI
// Responses client cannot read.
const SSEFormatOpenAIResponses = "openai-responses"

// responsesStreamState turns the CreateResponse server stream, which carries
// successive snapshots of the response object, into the OpenAI Responses
// event sequence. Each snapshot is diffed against what has already been sent,
// so a provider that grows the text across snapshots yields one delta per
// growth and a loop that only reports the finished response yields the whole
// text as a single delta.
type responsesStreamState struct {
	seq      int
	created  bool
	terminal bool
	items    []*responsesItemState
}

type responsesItemState struct {
	added bool
	done  bool
	// sent is the text (message content parts) or arguments (function
	// calls) already delivered as deltas, per content index.
	sent     map[int]string
	partSeen map[int]bool
}

// responsesEvent is one SSE event: its name and JSON payload.
type responsesEvent struct {
	name string
	data []byte
}

func (st *responsesStreamState) emit(out []responsesEvent, name string, fields map[string]interface{}) []responsesEvent {
	fields["type"] = name
	fields["sequence_number"] = st.seq
	st.seq++
	b, err := json.Marshal(fields)
	if err != nil {
		return out
	}
	return append(out, responsesEvent{name: name, data: b})
}

// events converts one grpc-gateway stream line into the OpenAI events it
// implies. Error frames become an `error` event.
func (st *responsesStreamState) events(line string) []responsesEvent {
	var top map[string]interface{}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return nil
	}
	obj, ok := top["result"].(map[string]interface{})
	if !ok {
		obj = top
	}
	var out []responsesEvent
	if msg := frameErrorMessage(obj); msg != "" {
		return st.emit(out, "error", map[string]interface{}{
			"code":    nil,
			"message": msg,
			"param":   nil,
		})
	}
	if st.terminal {
		return nil
	}
	if d, ok := obj["delta"].(map[string]interface{}); ok {
		return st.deltaEvents(out, d)
	}

	resp := ProtoResponseToOpenAI(obj)
	status, _ := resp["status"].(string)
	terminal := status != "" && status != "in_progress" && status != "queued"

	if !st.created {
		st.created = true
		opening := cloneMap(resp)
		opening["status"] = "in_progress"
		opening["output"] = []interface{}{}
		delete(opening, "usage")
		out = st.emit(out, "response.created", map[string]interface{}{"response": opening})
		out = st.emit(out, "response.in_progress", map[string]interface{}{"response": cloneMap(opening)})
	}

	output, _ := resp["output"].([]interface{})
	for i, raw := range output {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for len(st.items) <= i {
			st.items = append(st.items, &responsesItemState{sent: map[int]string{}, partSeen: map[int]bool{}})
		}
		out = st.itemEvents(out, i, item, terminal)
	}

	if terminal {
		st.terminal = true
		name := "response.failed"
		switch status {
		case "completed":
			name = "response.completed"
		case "incomplete":
			name = "response.incomplete"
		}
		out = st.emit(out, name, map[string]interface{}{"response": resp})
	}
	return out
}

func (st *responsesStreamState) itemEvents(out []responsesEvent, idx int, item map[string]interface{}, terminal bool) []responsesEvent {
	is := st.items[idx]
	if is.done {
		return out
	}
	itemID, _ := item["id"].(string)
	itemType, _ := item["type"].(string)
	itemStatus, _ := item["status"].(string)

	if !is.added {
		is.added = true
		opening := cloneMap(item)
		opening["status"] = "in_progress"
		switch itemType {
		case "message":
			opening["content"] = []interface{}{}
		case "function_call":
			opening["arguments"] = ""
		}
		out = st.emit(out, "response.output_item.added", map[string]interface{}{
			"output_index": idx,
			"item":         opening,
		})
	}

	switch itemType {
	case "message":
		parts, _ := item["content"].([]interface{})
		for ci, raw := range parts {
			part, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if !is.partSeen[ci] {
				is.partSeen[ci] = true
				out = st.emit(out, "response.content_part.added", map[string]interface{}{
					"item_id":       itemID,
					"output_index":  idx,
					"content_index": ci,
					"part":          emptyPart(part),
				})
			}
			if t, _ := part["type"].(string); t != "output_text" {
				continue
			}
			text, _ := part["text"].(string)
			if delta := textDelta(is.sent[ci], text); delta != "" {
				out = st.emit(out, "response.output_text.delta", map[string]interface{}{
					"item_id":       itemID,
					"output_index":  idx,
					"content_index": ci,
					"delta":         delta,
					"logprobs":      []interface{}{},
				})
			}
			is.sent[ci] = text
		}
	case "function_call":
		args, _ := item["arguments"].(string)
		if delta := textDelta(is.sent[0], args); delta != "" {
			out = st.emit(out, "response.function_call_arguments.delta", map[string]interface{}{
				"item_id":      itemID,
				"output_index": idx,
				"delta":        delta,
			})
		}
		is.sent[0] = args
	}

	if !terminal && (itemStatus == "" || itemStatus == "in_progress") {
		return out
	}
	is.done = true
	switch itemType {
	case "message":
		parts, _ := item["content"].([]interface{})
		for ci, raw := range parts {
			part, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := part["type"].(string); t == "output_text" {
				text, _ := part["text"].(string)
				out = st.emit(out, "response.output_text.done", map[string]interface{}{
					"item_id":       itemID,
					"output_index":  idx,
					"content_index": ci,
					"text":          text,
					"logprobs":      []interface{}{},
				})
			}
			out = st.emit(out, "response.content_part.done", map[string]interface{}{
				"item_id":       itemID,
				"output_index":  idx,
				"content_index": ci,
				"part":          part,
			})
		}
	case "function_call":
		args, _ := item["arguments"].(string)
		name, _ := item["name"].(string)
		out = st.emit(out, "response.function_call_arguments.done", map[string]interface{}{
			"item_id":      itemID,
			"output_index": idx,
			"name":         name,
			"arguments":    args,
		})
	}
	return st.emit(out, "response.output_item.done", map[string]interface{}{
		"output_index": idx,
		"item":         item,
	})
}

// deltaEvents converts a delta frame (what one item grew by since the
// previous frame) into its OpenAI delta event, and adds the fragment to the
// running text so the item's done events, and any later snapshot of it, are
// measured against everything already delivered. The handler announces an
// item and its part in a snapshot before the first delta naming them; a
// delta for anything else cannot be placed and is dropped.
func (st *responsesStreamState) deltaEvents(out []responsesEvent, d map[string]interface{}) []responsesEvent {
	idx := int(toInt64(d["output_index"]))
	if !st.created || idx < 0 || idx >= len(st.items) {
		return out
	}
	is := st.items[idx]
	if !is.added || is.done {
		return out
	}
	itemID, _ := d["item_id"].(string)
	if text, _ := d["content_delta"].(string); text != "" {
		ci := int(toInt64(d["content_index"]))
		if !is.partSeen[ci] {
			return out
		}
		is.sent[ci] += text
		out = st.emit(out, "response.output_text.delta", map[string]interface{}{
			"item_id":       itemID,
			"output_index":  idx,
			"content_index": ci,
			"delta":         text,
			"logprobs":      []interface{}{},
		})
	}
	if args, _ := d["arguments_delta"].(string); args != "" {
		is.sent[0] += args
		out = st.emit(out, "response.function_call_arguments.delta", map[string]interface{}{
			"item_id":      itemID,
			"output_index": idx,
			"delta":        args,
		})
	}
	return out
}

// textDelta returns what text adds to sent. A snapshot that is not an
// extension of what was sent cannot be expressed as a delta; it is sent
// whole rather than dropped.
func textDelta(sent, text string) string {
	if strings.HasPrefix(text, sent) {
		return text[len(sent):]
	}
	return text
}

func emptyPart(part map[string]interface{}) map[string]interface{} {
	p := cloneMap(part)
	if t, _ := p["type"].(string); t == "output_text" {
		p["text"] = ""
	}
	return p
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// frameErrorMessage returns the message of an error frame (the grpc-gateway
// mid-stream {"error":{...}} or a bare {"code","message"} status), or "".
func frameErrorMessage(obj map[string]interface{}) string {
	if e, ok := obj["error"].(map[string]interface{}); ok {
		// A response snapshot also has an "error" field (status failed);
		// that one is part of the response, not a transport error.
		if _, isResponse := obj["object"]; !isResponse {
			if m, ok := e["message"].(string); ok && m != "" {
				return m
			}
		}
		return ""
	}
	if m, ok := obj["message"].(string); ok && m != "" {
		if _, hasCode := obj["code"]; hasCode {
			if _, isResponse := obj["object"]; !isResponse {
				return m
			}
		}
	}
	return ""
}

// ProtoResponseToOpenAI converts one CreateResponseResponse, as the
// grpc-gateway renders it (proto field names, int64 as strings, enums as
// numbers, empty fields omitted), into the OpenAI Responses API response
// object.
func ProtoResponseToOpenAI(obj map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"object":               "response",
		"output":               []interface{}{},
		"error":                nil,
		"incomplete_details":   nil,
		"metadata":             map[string]interface{}{},
		"previous_response_id": nil,
	}
	for _, k := range []string{"id", "status", "model", "temperature", "top_p", "max_output_tokens", "previous_response_id", "metadata", "usage", "error", "incomplete_details", "reasoning"} {
		if v, ok := obj[k]; ok && v != nil {
			out[k] = v
		}
	}
	if v, ok := obj["created_at"]; ok {
		out["created_at"] = toInt64(v)
	}
	if t, ok := obj["truncation"].(map[string]interface{}); ok {
		if s, ok := t["type"].(string); ok && s != "" {
			out["truncation"] = s
		}
	}
	if items, ok := obj["output"].([]interface{}); ok {
		conv := make([]interface{}, 0, len(items))
		for _, raw := range items {
			if item, ok := raw.(map[string]interface{}); ok {
				conv = append(conv, protoOutputItemToOpenAI(item))
			}
		}
		out["output"] = conv
	}
	return out
}

func protoOutputItemToOpenAI(item map[string]interface{}) map[string]interface{} {
	out := cloneMap(item)
	if r, ok := item["role"]; ok {
		out["role"] = roleName(r)
	}
	if t, _ := item["type"].(string); t == "message" {
		if _, ok := out["role"]; !ok {
			out["role"] = "assistant"
		}
		parts, _ := item["content"].([]interface{})
		conv := make([]interface{}, 0, len(parts))
		for _, raw := range parts {
			part, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			pt, _ := part["type"].(string)
			if text, ok := part["text"].(string); ok && (pt == "" || pt == "text" || pt == "output_text") {
				conv = append(conv, map[string]interface{}{
					"type":        "output_text",
					"text":        text,
					"annotations": []interface{}{},
				})
				continue
			}
			conv = append(conv, part)
		}
		out["content"] = conv
	}
	return out
}

func roleName(v interface{}) string {
	switch r := v.(type) {
	case float64:
		switch int(r) {
		case 1:
			return "system"
		case 2:
			return "user"
		case 3:
			return "assistant"
		case 4:
			return "function"
		case 5:
			return "tool"
		}
	case string:
		return strings.ToLower(strings.TrimPrefix(r, "ROLE_"))
	}
	return "assistant"
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
