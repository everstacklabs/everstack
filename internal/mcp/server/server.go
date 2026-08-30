// Package server implements the server side of the Model Context Protocol
// (MCP) over Streamable HTTP. It lets external MCP clients — Claude Desktop,
// Cursor, Google ADK, and any other MCP-speaking agent — discover and call
// Everstack tools.
//
// This package is deliberately dependency-light: it owns the JSON-RPC 2.0
// dispatch and the ToolProvider / Authenticator seams, and nothing else. The
// concrete tenant-scoped tool catalog and the API-key authenticator live in
// sibling packages so this protocol core stays unit-testable with fakes.
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/mcp"
)

const (
	// protocolVersion is the MCP revision we advertise. Kept in lockstep with
	// the outbound client (internal/mcp.mcpProtocolVersion).
	protocolVersion = "2025-03-26"
	serverName      = "everstack-mcp-server"
	serverVersion   = "1.0.0"

	jsonRPCVersion = "2.0"

	// maxBodyBytes caps a single inbound JSON-RPC request. Tool arguments can
	// be sizable (code, documents) but a few MB is plenty and keeps a hostile
	// client from exhausting memory.
	maxBodyBytes = 4 << 20
)

// JSON-RPC 2.0 error codes (subset we emit).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ToolProvider supplies the MCP tools visible and callable for a tenant.
// Implementations MUST fail closed on an empty tenantID (return no tools / an
// error) — this is the tenant-isolation boundary.
type ToolProvider interface {
	// ListTools returns the tool catalog the tenant may see.
	ListTools(ctx context.Context, tenantID string) ([]mcp.ToolInfo, error)
	// CallTool executes a tool for the tenant. A non-nil error is surfaced to
	// the caller as an in-result tool error (isError=true), not a transport
	// fault.
	CallTool(ctx context.Context, tenantID, name string, args map[string]interface{}) (*mcp.ToolCallResult, error)
}

// Authenticator resolves an inbound HTTP request to a tenant ID. It MUST fail
// closed: return ("", false) for any request it cannot positively attribute to
// a single tenant. Crucially, no "first/only tenant in the DB" fallback is
// permitted here — this endpoint is externally reachable and such a fallback is
// exactly the cross-tenant leak pattern we have been bitten by before.
type Authenticator interface {
	Authenticate(r *http.Request) (tenantID string, ok bool)
}

// Handler is the http.Handler implementing the MCP server protocol.
type Handler struct {
	provider ToolProvider
	auth     Authenticator
	logger   *slog.Logger
	info     mcp.ImplementationInfo
}

// New constructs an MCP server handler. provider and auth are required.
func New(provider ToolProvider, auth Authenticator, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		provider: provider,
		auth:     auth,
		logger:   logger.With("component", "mcp_server"),
		info:     mcp.ImplementationInfo{Name: serverName, Version: serverVersion},
	}
}

// rpcRequest is the inbound JSON-RPC envelope. We parse with json.RawMessage
// for ID and Params so we can echo the ID back verbatim (number, string, or
// null) and decode Params into the precise param type per method.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the request is a JSON-RPC notification (no id),
// which never receives a response.
func (r *rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Streamable HTTP clients may open a GET for a server->client SSE stream.
	// Phase 1 does not push server-initiated messages, so decline cleanly
	// rather than failing opaquely.
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint does not offer a server-initiated stream; use POST", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate every request to a tenant. Fail closed.
	tenantID, ok := h.auth.Authenticate(r)
	if !ok || tenantID == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="everstack-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeResponse(w, errorResp(nil, codeParseError, "failed to read request body"))
		return
	}

	// MCP tool flows use single request objects. Batch arrays are legal
	// JSON-RPC but unused here; a leading '[' gets a clear error.
	if isBatch(body) {
		writeResponse(w, errorResp(nil, codeInvalidRequest, "batch requests are not supported"))
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponse(w, errorResp(nil, codeParseError, "invalid JSON-RPC request"))
		return
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		writeResponse(w, errorResp(req.ID, codeInvalidRequest, "invalid JSON-RPC request"))
		return
	}

	resp := h.dispatch(r.Context(), tenantID, &req)
	if resp == nil {
		// Notification: acknowledge with 202 and no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeResponse(w, resp)
}

// dispatch routes a parsed request to its method handler. Returns nil for
// notifications (no response expected).
func (h *Handler) dispatch(ctx context.Context, tenantID string, req *rpcRequest) *mcp.JsonRpcResponse {
	switch req.Method {
	case "initialize":
		return h.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "ping":
		return result(req.ID, struct{}{})
	case "tools/list":
		return h.handleToolsList(ctx, tenantID, req)
	case "tools/call":
		return h.handleToolsCall(ctx, tenantID, req)
	default:
		// Silently drop unknown notifications; error on unknown calls.
		if req.isNotification() && strings.HasPrefix(req.Method, "notifications/") {
			return nil
		}
		return errorResp(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (h *Handler) handleInitialize(req *rpcRequest) *mcp.JsonRpcResponse {
	res := mcp.InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities: mcp.ServerCapabilities{
			Tools: &mcp.ToolsCapability{ListChanged: false},
		},
		ServerInfo: h.info,
	}
	return result(req.ID, res)
}

func (h *Handler) handleToolsList(ctx context.Context, tenantID string, req *rpcRequest) *mcp.JsonRpcResponse {
	tools, err := h.provider.ListTools(ctx, tenantID)
	if err != nil {
		h.logger.Error("tools/list failed", "error", err)
		return errorResp(req.ID, codeInternalError, "failed to list tools")
	}
	if tools == nil {
		tools = []mcp.ToolInfo{}
	}
	return result(req.ID, mcp.ToolListResult{Tools: tools})
}

func (h *Handler) handleToolsCall(ctx context.Context, tenantID string, req *rpcRequest) *mcp.JsonRpcResponse {
	var params mcp.ToolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResp(req.ID, codeInvalidParams, "invalid tools/call params")
		}
	}
	if strings.TrimSpace(params.Name) == "" {
		return errorResp(req.ID, codeInvalidParams, "tool name is required")
	}

	res, err := h.provider.CallTool(ctx, tenantID, params.Name, params.Arguments)
	if err != nil {
		// Per MCP, tool-level failures are reported in the result with
		// isError=true so the calling model can see and adapt, rather than as
		// a JSON-RPC transport error.
		h.logger.Warn("tools/call failed", "tool", params.Name, "error", err)
		return result(req.ID, &mcp.ToolCallResult{
			IsError: true,
			Content: []mcp.ContentBlock{{Type: "text", Text: err.Error()}},
		})
	}
	if res == nil {
		res = &mcp.ToolCallResult{Content: []mcp.ContentBlock{}}
	}
	return result(req.ID, res)
}

// ─── helpers ────────────────────────────────────────────────────────

// isBatch reports whether the body is a JSON array (batch request).
func isBatch(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

func result(id json.RawMessage, payload interface{}) *mcp.JsonRpcResponse {
	raw, err := json.Marshal(payload)
	if err != nil {
		return errorResp(id, codeInternalError, "failed to marshal result")
	}
	return &mcp.JsonRpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(id), Result: raw}
}

func errorResp(id json.RawMessage, code int, msg string) *mcp.JsonRpcResponse {
	return &mcp.JsonRpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      idValue(id),
		Error:   &mcp.JsonRpcError{Code: code, Message: msg},
	}
}

// idValue echoes the request id back. A nil/absent id becomes JSON null (the
// correct id for errors that occur before/while parsing the id).
func idValue(id json.RawMessage) interface{} {
	if len(id) == 0 {
		return nil
	}
	return id
}

func writeResponse(w http.ResponseWriter, resp *mcp.JsonRpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	// JSON-RPC over HTTP returns 200 even for JSON-RPC-level errors; the error
	// travels in the body. (Transport-level failures like auth are handled
	// before we get here.)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
