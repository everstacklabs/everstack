package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/everstacklabs/everstack/internal/telemetry"
)

const (
	mcpProtocolVersion = "2025-03-26"
	clientName         = "everstack-mcp-client"
	clientVersion      = "1.0.0"
)

// Client is a stateful MCP protocol client that manages a connection to a
// single MCP server. It handles initialization, tool discovery, tool
// execution, and health pings.
type Client struct {
	transport  Transport
	config     ServerConfig
	serverInfo *InitializeResult
	logger     *slog.Logger
}

// NewClient creates a new MCP client for the given server configuration.
// If the config has OAuth2 auth, the transport is wrapped with automatic
// token refresh logic.
func NewClient(cfg ServerConfig, logger *slog.Logger) (*Client, error) {
	t, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}

	// Wrap with auth refresh for OAuth2
	if cfg.AuthConfig != nil && cfg.AuthConfig.Type == AuthTypeOAuth2 {
		t = newAuthRefreshTransport(t, cfg.AuthConfig, cfg.OnTokenUpdate)
	}

	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		transport: t,
		config:    cfg,
		logger:    logger.With("mcp_server", cfg.Name),
	}, nil
}

// Initialize performs the MCP initialize handshake. Must be called before
// any other protocol method.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities: ClientCapabilities{
			Roots: &RootsCapability{ListChanged: false},
		},
		ClientInfo: ImplementationInfo{
			Name:    clientName,
			Version: clientVersion,
		},
	}

	resp, err := c.call(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal initialize result: %w", err)
	}
	c.serverInfo = &result

	// Send initialized notification (no ID = notification)
	notif := &JsonRpcRequest{
		JSONRPC: jsonRPCVersion,
		Method:  "notifications/initialized",
	}
	// Notifications don't expect responses; best-effort send.
	_, _ = c.transport.Send(ctx, notif)

	c.logger.Info("mcp server initialized",
		"protocol_version", result.ProtocolVersion,
		"server_name", result.ServerInfo.Name,
	)
	return &result, nil
}

// ListTools calls tools/list and returns all available tools. Handles
// pagination automatically.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var allTools []ToolInfo
	var cursor string

	for {
		params := map[string]interface{}{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		resp, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}

		var result ToolListResult
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, fmt.Errorf("mcp: unmarshal tools/list result: %w", err)
		}

		allTools = append(allTools, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return allTools, nil
}

// CallTool invokes a tool on the MCP server and returns the result.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*ToolCallResult, error) {
	ctx, span := telemetry.StartMCPToolSpan(ctx, c.config.ID, name)
	defer span.End()

	params := ToolCallParams{
		Name:      name,
		Arguments: arguments,
	}

	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, fmt.Errorf("mcp: tools/call %q: %w", name, err)
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp, &result); err != nil {
		telemetry.RecordError(span, err)
		return nil, fmt.Errorf("mcp: unmarshal tools/call result: %w", err)
	}
	return &result, nil
}

// Ping sends a ping to the MCP server to check connectivity.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	if err != nil {
		return fmt.Errorf("mcp: ping: %w", err)
	}
	return nil
}

// ServerInfo returns the server's initialization result, if available.
func (c *Client) ServerInfo() *InitializeResult {
	return c.serverInfo
}

// Close shuts down the transport connection.
func (c *Client) Close() error {
	return c.transport.Close()
}

// call is the low-level RPC helper that sends a request and checks for errors.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	req := &JsonRpcRequest{
		Method: method,
		Params: params,
	}

	resp, err := c.transport.Send(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}
