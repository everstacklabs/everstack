package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/google/uuid"
)

const (
	codeTaskNotFound         = -32001
	codePushNotSupported     = -32003
	codeUnsupportedOperation = -32004
	codeContentNotSupported  = -32005
	codeVersionNotSupported  = -32009
	v1RunFailureText         = "Agent execution failed."
)

// Version negotiation ignores specification patch versions (spec section 3.6).
// An absent version stays on the legacy binding, without method-name sniffing.
func bindingVersion(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) == 3 && parts[2] != "" {
		for _, c := range parts[2] {
			if c < '0' || c > '9' {
				return version
			}
		}
		return parts[0] + "." + parts[1]
	}
	return version
}

type v1Message struct {
	MessageID string       `json:"messageId"`
	Role      string       `json:"role"`
	Parts     []v1TextPart `json:"parts"`
	ContextID string       `json:"contextId"`
}
type v1TextPart struct {
	Text string `json:"text"`
}
type v1Task struct {
	ID        string       `json:"id"`
	ContextID string       `json:"contextId"`
	Status    v1TaskStatus `json:"status"`
}
type v1TaskStatus struct {
	State     string     `json:"state"`
	Message   *v1Message `json:"message"`
	Timestamp string     `json:"timestamp"`
}
type v1SendResult struct {
	Message *v1Message `json:"message,omitempty"`
	Task    *v1Task    `json:"task,omitempty"`
}
type v1SendParams struct {
	Message *struct {
		MessageID string                       `json:"messageId"`
		Role      json.RawMessage              `json:"role"`
		Parts     []map[string]json.RawMessage `json:"parts"`
		ContextID string                       `json:"contextId"`
	} `json:"message"`
	Configuration *struct {
		ReturnImmediately          bool            `json:"returnImmediately"`
		AcceptedOutputModes        []string        `json:"acceptedOutputModes"`
		TaskPushNotificationConfig json.RawMessage `json:"taskPushNotificationConfig"`
	} `json:"configuration"`
	// tenant and metadata never select the authenticated tenant or API key.
}

func (s *Server) handleV1RPC(w http.ResponseWriter, r *http.Request, ctx context.Context, tenantID, agentID string) {
	var raw json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		writeV1Error(w, nil, codeParseError, "invalid JSON-RPC request")
		return
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeV1Error(w, nil, codeParseError, "invalid JSON-RPC request")
		return
	}
	var req rpcRequest
	if json.Unmarshal(raw, &req) != nil || req.JSONRPC != jsonRPCVersion || req.Method == "" || !validRPCID(req.ID) {
		if !validRPCID(req.ID) {
			req.ID = nil
		}
		writeV1Error(w, &req, codeInvalidRequest, "invalid JSON-RPC request")
		return
	}
	switch req.Method {
	case "SendMessage":
		s.handleV1Send(w, ctx, tenantID, agentID, &req)
	case "GetTask", "CancelTask":
		var params struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(req.Params, &params) != nil || strings.TrimSpace(params.ID) == "" {
			writeV1Error(w, &req, codeInvalidParams, "task id is required")
			return
		}
		writeV1Error(w, &req, codeTaskNotFound, "task not found")
	case "SendStreamingMessage", "SubscribeToTask":
		writeV1Error(w, &req, codeUnsupportedOperation, "streaming is not supported")
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig", "ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		writeV1Error(w, &req, codePushNotSupported, "push notifications are not supported")
	default:
		writeV1Error(w, &req, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleV1Send(w http.ResponseWriter, ctx context.Context, tenantID, agentID string, req *rpcRequest) {
	var params v1SendParams
	if json.Unmarshal(req.Params, &params) != nil || params.Message == nil {
		writeV1Error(w, req, codeInvalidParams, "message is required")
		return
	}
	msg := params.Message
	// ProtoJSON parsers accept enum names and integer values. ROLE_USER = 1.
	if !isV1UserRole(msg.Role) || strings.TrimSpace(msg.MessageID) == "" {
		writeV1Error(w, req, codeInvalidParams, "user role and messageId are required")
		return
	}
	if len(msg.Parts) == 0 {
		writeV1Error(w, req, codeInvalidParams, "message has no text part")
		return
	}
	var text strings.Builder
	for _, part := range msg.Parts {
		for _, field := range []string{"raw", "url", "data", "file"} {
			if _, ok := part[field]; ok {
				writeV1Error(w, req, codeContentNotSupported, "only text parts are supported")
				return
			}
		}
		var value *string
		if json.Unmarshal(part["text"], &value) != nil || value == nil {
			writeV1Error(w, req, codeContentNotSupported, "only text parts are supported")
			return
		}
		text.WriteString(*value)
	}
	if strings.TrimSpace(text.String()) == "" {
		writeV1Error(w, req, codeInvalidParams, "message has no text part")
		return
	}
	if config := params.Configuration; config != nil {
		if len(config.TaskPushNotificationConfig) > 0 && string(config.TaskPushNotificationConfig) != "null" {
			writeV1Error(w, req, codePushNotSupported, "push notifications are not supported")
			return
		}
		if config.AcceptedOutputModes != nil {
			acceptsText := false
			for _, mode := range config.AcceptedOutputModes {
				if mode == "text/plain" {
					acceptsText = true
				}
			}
			if !acceptsText {
				writeV1Error(w, req, codeContentNotSupported, "text/plain output must be accepted")
				return
			}
		}
	}
	contextID := msg.ContextID
	if contextID == "" {
		contextID = uuid.NewString()
	}
	// Use the same synchronous runner and request context as the legacy binding.
	// returnImmediately does not affect direct Message responses (section 3.2.2).
	// Same call depth limit as the legacy binding: refuse before any agent work.
	runCtx, err := contextkeys.NextAgentHop(ctx)
	if err != nil {
		writeV1Error(w, req, codeInvalidRequest, err.Error())
		return
	}
	output, err := s.d.Runner.Run(runCtx, tenantID, agentID, text.String())
	result := v1SendResult{}
	if err != nil {
		result.Task = &v1Task{ID: uuid.NewString(), ContextID: contextID, Status: v1TaskStatus{
			State: "TASK_STATE_FAILED", Message: v1AgentMessage(v1RunFailureText, contextID), Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		}}
	} else {
		result.Message = v1AgentMessage(output, contextID)
	}
	writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Result: result})
}

func isV1UserRole(raw json.RawMessage) bool {
	var name string
	if json.Unmarshal(raw, &name) == nil && name == "ROLE_USER" {
		return true
	}
	var number int
	return json.Unmarshal(raw, &number) == nil && number == 1
}

func v1AgentMessage(text, contextID string) *v1Message {
	return &v1Message{MessageID: uuid.NewString(), Role: "ROLE_AGENT", Parts: []v1TextPart{{Text: text}}, ContextID: contextID}
}
func writeV1Error(w http.ResponseWriter, req *rpcRequest, code int, message string) {
	var id interface{}
	if req != nil && validRPCID(req.ID) {
		id = idValue(req.ID)
	}
	// JSON-RPC requires an explicit null id when no request id is recoverable.
	writeJSON(w, http.StatusOK, struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      interface{} `json:"id"`
		Error   *rpcError   `json:"error"`
	}{jsonRPCVersion, id, &rpcError{Code: code, Message: message}})
}
func validRPCID(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, float64:
		return true
	}
	return false
}
