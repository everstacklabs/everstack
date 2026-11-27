package client

import (
	"encoding/json"
	"strconv"
	"strings"
)

func validID(id string) bool {
	return strings.TrimSpace(id) != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n\x00")
}
func decode(raw []byte, id, protocol string, send bool) (Result, error) {
	var env struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || env.JSONRPC != "2.0" || (protocol == Legacy && id == "") || env.ID != id || (env.Result == nil) == (env.Error == nil) {
		return Result{}, failure("invalid_envelope")
	}
	if env.Error != nil {
		var rpcError struct {
			Code    *int    `json:"code"`
			Message *string `json:"message"`
		}
		parsed := json.Unmarshal(env.Error, &rpcError) == nil && rpcError.Code != nil
		if protocol == Legacy && (!parsed || rpcError.Message == nil) {
			return Result{}, failure("invalid_rpc_error")
		}
		if parsed {
			// Only the integer leaves this package, never the remote message.
			return Result{}, failure("rpc_" + strconv.Itoa(*rpcError.Code))
		}
		return Result{}, failure("remote_rpc_error")
	}
	raw = env.Result
	kind := "task"
	if protocol == V1 && send {
		var union struct {
			Task    json.RawMessage `json:"task"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(raw, &union) != nil || (union.Task == nil) == (union.Message == nil) {
			return Result{}, failure("invalid_result")
		}
		raw = union.Task
		if union.Message != nil {
			raw = union.Message
			kind = "message"
		}
	} else if protocol == Legacy {
		var k struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &k) != nil || (k.Kind != "task" && k.Kind != "message") {
			return Result{}, failure("invalid_result")
		}
		kind = k.Kind
	}
	if kind == "message" {
		if !send {
			return Result{}, failure("expected_task")
		}
		text, err := messageText(raw, protocol)
		return Result{State: "completed", Text: text}, err
	}
	var task struct {
		ID        string `json:"id"`
		ContextID string `json:"contextId"`
		Status    struct {
			State   json.RawMessage `json:"state"`
			Message json.RawMessage `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			ID    string                       `json:"artifactId"`
			Parts []map[string]json.RawMessage `json:"parts"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(raw, &task) != nil || !validID(task.ID) || ((protocol == Legacy || task.ContextID != "") && !validID(task.ContextID)) {
		return Result{}, failure("invalid_task")
	}
	state := decodeState(task.Status.State, protocol)
	if state == "" {
		return Result{}, failure("invalid_task_state")
	}
	result := Result{TaskID: task.ID, ContextID: task.ContextID, State: state}
	var statusText string
	if protocol == Legacy && len(task.Status.Message) > 0 && string(task.Status.Message) != "null" {
		var err error
		statusText, err = messageText(task.Status.Message, protocol)
		if err != nil {
			return result, err
		}
	}
	var texts []string
	for _, artifact := range task.Artifacts {
		if !validID(artifact.ID) {
			return Result{}, failure("invalid_artifact")
		}
		text, err := partsText(artifact.Parts, protocol)
		if err != nil {
			return result, err
		}
		texts = append(texts, text)
	}
	result.Text = strings.Join(texts, "\n")
	if result.Text == "" && len(task.Status.Message) > 0 && string(task.Status.Message) != "null" {
		if protocol == Legacy {
			result.Text = statusText
		} else {
			text, err := messageText(task.Status.Message, protocol)
			if err != nil {
				return result, err
			}
			result.Text = text
		}
	}
	return result, nil
}
func decodeState(raw json.RawMessage, protocol string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var state string
	if protocol == Legacy {
		if json.Unmarshal(raw, &state) != nil {
			return ""
		}
		switch state {
		case "submitted", "working", "completed", "failed", "canceled", "rejected", "input-required", "auth-required", "unknown":
			return state
		}
		return ""
	}
	// ProtoJSON permits enum names and their integer representation.
	names := []string{"unknown", "submitted", "working", "completed", "failed", "canceled", "input-required", "rejected", "auth-required"}
	var index int
	if json.Unmarshal(raw, &index) == nil && index >= 0 && index < len(names) {
		return names[index]
	}
	if json.Unmarshal(raw, &state) != nil {
		return ""
	}
	switch state {
	case "TASK_STATE_UNSPECIFIED":
		return "unknown"
	case "TASK_STATE_SUBMITTED":
		return "submitted"
	case "TASK_STATE_WORKING":
		return "working"
	case "TASK_STATE_COMPLETED":
		return "completed"
	case "TASK_STATE_FAILED":
		return "failed"
	case "TASK_STATE_CANCELED":
		return "canceled"
	case "TASK_STATE_INPUT_REQUIRED":
		return "input-required"
	case "TASK_STATE_REJECTED":
		return "rejected"
	case "TASK_STATE_AUTH_REQUIRED":
		return "auth-required"
	}
	return ""
}
func messageText(raw json.RawMessage, protocol string) (string, error) {
	var msg struct {
		Kind  string                       `json:"kind"`
		ID    string                       `json:"messageId"`
		Role  json.RawMessage              `json:"role"`
		Parts []map[string]json.RawMessage `json:"parts"`
	}
	if json.Unmarshal(raw, &msg) != nil || !validID(msg.ID) {
		return "", failure("invalid_message")
	}
	if protocol == Legacy {
		if msg.Kind != "message" || string(msg.Role) != `"agent"` {
			return "", failure("invalid_message")
		}
	} else if string(msg.Role) != `"ROLE_AGENT"` && string(msg.Role) != "2" {
		return "", failure("invalid_message")
	}
	return partsText(msg.Parts, protocol)
}
func partsText(parts []map[string]json.RawMessage, protocol string) (string, error) {
	if len(parts) == 0 {
		return "", failure("invalid_parts")
	}
	var texts []string
	for _, part := range parts {
		if protocol == Legacy && string(part["kind"]) != `"text"` {
			return "", failure("unsupported_content")
		}
		// Fail visibly instead of dropping file/data content or following URLs.
		for _, key := range []string{"raw", "url", "data", "file"} {
			if _, ok := part[key]; ok {
				return "", failure("unsupported_content")
			}
		}
		var text *string
		if json.Unmarshal(part["text"], &text) != nil || text == nil {
			return "", failure("unsupported_content")
		}
		texts = append(texts, *text)
	}
	return strings.Join(texts, "\n"), nil
}
