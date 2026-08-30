package mcp

import (
	"encoding/json"
	"time"
)

// ─── JSON-RPC 2.0 ──────────────────────────────────────────────────

const jsonRPCVersion = "2.0"

// JsonRpcRequest is a JSON-RPC 2.0 request envelope.
type JsonRpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// JsonRpcResponse is a JSON-RPC 2.0 response envelope.
type JsonRpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

// JsonRpcError represents a JSON-RPC 2.0 error object.
type JsonRpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JsonRpcError) Error() string { return e.Message }

// ─── MCP Protocol Types ────────────────────────────────────────────

// ServerCapabilities describes capabilities advertised by an MCP server.
type ServerCapabilities struct {
	Tools     *ToolsCapability    `json:"tools,omitempty"`
	Resources *ResourceCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability  `json:"prompts,omitempty"`
	Logging   *LoggingCapability  `json:"logging,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourceCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type LoggingCapability struct{}

// InitializeParams are sent from client to server for session initialization.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

// ClientCapabilities describes capabilities of the MCP client.
type ClientCapabilities struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type SamplingCapability struct{}

// ImplementationInfo identifies client/server name and version.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned by the server after initialization.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
}

// ─── Tool Types ─────────────────────────────────────────────────────

// ToolInfo describes a single tool exposed by an MCP server.
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

// ToolListResult is the result of tools/list.
type ToolListResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// ToolCallParams are the parameters for tools/call.
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// ToolCallResult is the result of tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a content item in a tool call result.
type ContentBlock struct {
	Type     string `json:"type"` // "text", "image", "resource"
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// PingResult is the result of the ping method.
type PingResult struct{}

// ─── Transport Configuration ────────────────────────────────────────

// TransportType identifies how to communicate with an MCP server.
type TransportType string

const (
	TransportSSE            TransportType = "sse"
	TransportStreamableHTTP TransportType = "streamable_http"
	TransportStdio          TransportType = "stdio"
)

// StdioConfig holds process spawn settings for stdio transports.
type StdioConfig struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
}

// AuthType identifies the authentication method for an MCP server.
type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeAPIKey AuthType = "api_key"
	AuthTypeBearer AuthType = "bearer"
	AuthTypeOAuth2 AuthType = "oauth2"
)

// AuthConfig holds authentication credentials for an MCP server.
type AuthConfig struct {
	Type         AuthType  `json:"type"`
	Token        string    `json:"token,omitempty"`          // Bearer token or API key value
	APIKeyHeader string    `json:"api_key_header,omitempty"` // Custom header name for API key (default: "Authorization")
	AccessToken  string    `json:"access_token,omitempty"`   // OAuth2 access token
	RefreshToken string    `json:"refresh_token,omitempty"`  // OAuth2 refresh token
	ExpiresAt    time.Time `json:"expires_at,omitempty"`     // OAuth2 token expiry
	TokenURL     string    `json:"token_url,omitempty"`      // OAuth2 token endpoint
	ClientID     string    `json:"client_id,omitempty"`      // OAuth2 client ID
	ClientSecret string    `json:"client_secret,omitempty"`  // OAuth2 client secret (confidential clients)
}

// ServerConfig holds all configuration needed to connect to an MCP server.
type ServerConfig struct {
	ID string
	// TenantID is the owning tenant. Required for tenant isolation in the
	// registry; entries without TenantID can be enumerated by anyone via
	// FederatedToolsForTenant.
	TenantID      string
	Name          string
	URL           string
	TransportType TransportType
	Headers       map[string]string
	StdioConfig   *StdioConfig
	AuthConfig    *AuthConfig

	// OnTokenUpdate is called when OAuth2 tokens are refreshed, allowing
	// the caller to persist the updated tokens.
	OnTokenUpdate func(*AuthConfig)
}
