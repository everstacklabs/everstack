// Package server implements an A2A (Agent2Agent) server that exposes deployed
// Everstack agents as A2A-discoverable agents. External A2A clients — notably
// Google ADK — fetch an agent's "Agent Card" and then use the agent as a remote
// sub-agent via JSON-RPC message/send.
//
// This is the inbound A2A counterpart to the MCP server: where MCP exposes
// Everstack *tools*, A2A exposes Everstack *agents* as peers. Both share the
// same tenant API-key Bearer auth and the same synchronous agent runner.
//
// Routing (one logical A2A agent per Everstack agent):
//
//	GET  /a2a/agents/{agentId}/.well-known/agent.json   -> Agent Card
//	GET  /a2a/agents/{agentId}/.well-known/agent-card.json
//	POST /a2a/agents/{agentId}                           -> JSON-RPC message/send
//
// Every request authenticates to a tenant; an agent is only visible/callable to
// the tenant that owns it.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

const (
	a2aProtocolVersion = "0.2.5"
	providerOrg        = "Everstack"
	jsonRPCVersion     = "2.0"

	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Authenticator resolves a request to a tenant id. It MUST fail closed.
type Authenticator interface {
	Authenticate(r *http.Request) (tenantID string, ok bool)
}

// Router is the subset of the API needed to mount routes (satisfied by *api.API).
type Router interface {
	HandleFunc(path string, f http.HandlerFunc)
}

// AgentLookup resolves an agent's display metadata for a tenant. found=false
// (with nil error) means the agent does not exist for that tenant.
type AgentLookup interface {
	GetAgent(ctx context.Context, tenantID, agentID string) (name, description string, found bool, err error)
}

// AgentRunner invokes a deployed agent and returns its final text. Satisfied by
// *agentrun.Runner; an interface so the server is unit-testable with a stub.
type AgentRunner interface {
	Run(ctx context.Context, tenantID, agentID, message string) (string, error)
}

// Publisher reports whether an agent is published over A2A for a tenant. When a
// Publisher is configured, A2A is opt-in: unpublished agents are invisible (404)
// and uncallable. A nil Publisher disables gating (used in tests).
type Publisher interface {
	IsAgentPublished(ctx context.Context, tenantID, agentID string) (bool, error)
}

// Deps are the A2A server's dependencies.
type Deps struct {
	Agents    AgentLookup
	Runner    AgentRunner
	Auth      Authenticator
	Publisher Publisher
	PublicURL string // optional absolute base URL override; if empty, derived from the request
}

// Server serves the A2A protocol for Everstack agents.
type Server struct {
	d Deps
}

// New constructs an A2A server.
func New(d Deps) *Server {
	d.PublicURL = strings.TrimRight(d.PublicURL, "/")
	return &Server{d: d}
}

// Mount registers the A2A routes on the given router.
func (s *Server) Mount(r Router) {
	r.HandleFunc("/a2a/agents/{agentId}/.well-known/agent.json", s.handleAgentCard)
	r.HandleFunc("/a2a/agents/{agentId}/.well-known/agent-card.json", s.handleAgentCard)
	r.HandleFunc("/a2a/agents/{agentId}", s.handleRPC)
}

// ─── Agent Card ─────────────────────────────────────────────────────

// AgentCard is the A2A discovery document describing one agent.
type AgentCard struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	URL                string            `json:"url"`
	Version            string            `json:"version"`
	ProtocolVersion    string            `json:"protocolVersion"`
	Provider           AgentProvider     `json:"provider"`
	Capabilities       AgentCapabilities `json:"capabilities"`
	DefaultInputModes  []string          `json:"defaultInputModes"`
	DefaultOutputModes []string          `json:"defaultOutputModes"`
	Skills             []AgentSkill      `json:"skills"`
}

type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type AgentCapabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Examples    []string `json:"examples,omitempty"`
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantID, ok := s.d.Auth.Authenticate(r)
	if !ok || tenantID == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="everstack-a2a"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := mux.Vars(r)["agentId"]
	if !s.isPublished(r.Context(), tenantID, agentID) {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	name, description, found, err := s.d.Agents.GetAgent(r.Context(), tenantID, agentID)
	if err != nil || !found {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	desc := name
	if description != "" {
		desc = description
	}
	base := s.baseURL(r) + "/a2a/agents/" + agentID
	card := AgentCard{
		Name:               name,
		Description:        desc,
		URL:                base,
		Version:            "1.0.0",
		ProtocolVersion:    a2aProtocolVersion,
		Provider:           AgentProvider{Organization: providerOrg, URL: s.baseURL(r)},
		Capabilities:       AgentCapabilities{Streaming: false, PushNotifications: false, StateTransitionHistory: false},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []AgentSkill{{
			ID:          "chat",
			Name:        name,
			Description: desc,
			Tags:        []string{"everstack", "agent"},
		}},
	}
	writeJSON(w, http.StatusOK, card)
}

// ─── JSON-RPC (message/send) ────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// A2A message / part / task types (subset).
type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aMessage struct {
	Role      string    `json:"role"`
	Parts     []a2aPart `json:"parts"`
	MessageID string    `json:"messageId,omitempty"`
	Kind      string    `json:"kind,omitempty"`
}

type a2aArtifact struct {
	ArtifactID string    `json:"artifactId"`
	Name       string    `json:"name,omitempty"`
	Parts      []a2aPart `json:"parts"`
}

type a2aTaskStatus struct {
	State   string      `json:"state"`
	Message *a2aMessage `json:"message,omitempty"`
}

type a2aTask struct {
	ID        string        `json:"id"`
	ContextID string        `json:"contextId"`
	Status    a2aTaskStatus `json:"status"`
	Artifacts []a2aArtifact `json:"artifacts,omitempty"`
	Kind      string        `json:"kind"`
}

// messageSendParams is the params object for the message/send method.
type messageSendParams struct {
	Message a2aMessage `json:"message"`
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantID, ok := s.d.Auth.Authenticate(r)
	if !ok || tenantID == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="everstack-a2a"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := mux.Vars(r)["agentId"]
	if !s.isPublished(r.Context(), tenantID, agentID) {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: codeParseError, Message: "invalid JSON-RPC request"}})
		return
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Error: &rpcError{Code: codeInvalidRequest, Message: "invalid JSON-RPC request"}})
		return
	}

	switch req.Method {
	case "message/send":
		s.handleMessageSend(w, r.Context(), tenantID, agentID, &req)
	case "message/stream":
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Error: &rpcError{Code: codeInvalidRequest, Message: "streaming (message/stream) is not supported; use message/send"}})
	default:
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Error: &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}})
	}
}

func (s *Server) handleMessageSend(w http.ResponseWriter, ctx context.Context, tenantID, agentID string, req *rpcRequest) {
	var params messageSendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Error: &rpcError{Code: codeInvalidParams, Message: "invalid message/send params"}})
			return
		}
	}
	text := extractText(params.Message)
	if strings.TrimSpace(text) == "" {
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Error: &rpcError{Code: codeInvalidParams, Message: "message has no text part"}})
		return
	}

	output, err := s.d.Runner.Run(ctx, tenantID, agentID, text)
	taskID := randID()
	contextID := randID()
	if err != nil {
		// Report as a failed task so A2A clients see a structured outcome.
		task := a2aTask{
			ID: taskID, ContextID: contextID, Kind: "task",
			Status: a2aTaskStatus{State: "failed", Message: agentMessage(err.Error())},
		}
		writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Result: task})
		return
	}

	task := a2aTask{
		ID: taskID, ContextID: contextID, Kind: "task",
		Status:    a2aTaskStatus{State: "completed", Message: agentMessage(output)},
		Artifacts: []a2aArtifact{{ArtifactID: randID(), Name: "response", Parts: []a2aPart{{Kind: "text", Text: output}}}},
	}
	writeRPC(w, &rpcResponse{JSONRPC: jsonRPCVersion, ID: idValue(req.ID), Result: task})
}

// ─── helpers ────────────────────────────────────────────────────────

// isPublished reports whether the agent may be exposed over A2A. With no
// Publisher configured, gating is disabled (allow). With one, A2A is opt-in.
func (s *Server) isPublished(ctx context.Context, tenantID, agentID string) bool {
	if s.d.Publisher == nil {
		return true
	}
	ok, err := s.d.Publisher.IsAgentPublished(ctx, tenantID, agentID)
	return err == nil && ok
}

func (s *Server) baseURL(r *http.Request) string {
	if s.d.PublicURL != "" {
		return s.d.PublicURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func extractText(m a2aMessage) string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Kind == "text" || p.Kind == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func agentMessage(text string) *a2aMessage {
	return &a2aMessage{Role: "agent", Kind: "message", MessageID: randID(), Parts: []a2aPart{{Kind: "text", Text: text}}}
}

func idValue(id json.RawMessage) interface{} {
	if len(id) == 0 {
		return nil
	}
	return id
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "id-0000000000000000"
	}
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRPC(w http.ResponseWriter, resp *rpcResponse) {
	writeJSON(w, http.StatusOK, resp)
}
