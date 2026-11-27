package mcp

import (
	"context"
	"fmt"
)

// Transport is the abstraction over MCP communication channels.
// Each transport handles JSON-RPC 2.0 framing for its wire format.
type Transport interface {
	// Send sends a JSON-RPC request and returns the response.
	Send(ctx context.Context, req *JsonRpcRequest) (*JsonRpcResponse, error)

	// Close tears down the transport (connection, child process, etc.).
	Close() error
}

// NewTransport creates a transport for the given server configuration.
func NewTransport(cfg ServerConfig) (Transport, error) {
	switch cfg.TransportType {
	case TransportSSE:
		return newSSETransport(cfg)
	case TransportStreamableHTTP:
		return newStreamableHTTPTransport(cfg)
	case TransportStdio:
		// Construction-time policy gate. Every stdio transport is built
		// here (NewClient and Registry.Register both come through this
		// switch), so a caller that skips the registry is still refused
		// on a gateway that has stdio disabled.
		if !StdioAllowed() {
			return nil, fmt.Errorf("mcp: new transport for %q: %w", cfg.Name, ErrStdioNotAllowed)
		}
		if cfg.StdioConfig == nil {
			return nil, fmt.Errorf("mcp: stdio_config required for stdio transport")
		}
		return newStdioTransport(cfg)
	default:
		return nil, fmt.Errorf("mcp: unsupported transport type: %s", cfg.TransportType)
	}
}
