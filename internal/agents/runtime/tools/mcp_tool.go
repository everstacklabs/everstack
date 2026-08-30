package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
)

// McpToolHandler is a SyntheticToolHandler that delegates tool calls to a
// federated MCP server via the MCP registry. Each handler wraps a single
// MCP tool discovered from a remote server.
//
// When the agent runtime builds tool definitions, one McpToolHandler is
// registered per federated tool. The agent sees them as regular tools,
// unaware they are proxied to external MCP servers.
type McpToolHandler struct {
	registry   *mcp.Registry
	tenantID   string
	serverID   string
	serverName string
	tool       mcp.ToolInfo
	// toolName may be prefixed (e.g., "mcp_<server>_<tool>") to avoid
	// collisions with native tools.
	toolName string
}

// NewMcpToolHandlers creates SyntheticToolHandlers for federated tools owned
// by the given tenant. Empty tenantID returns no handlers (fail-closed).
// Call this when setting up the ToolInterceptor for an agent session.
func NewMcpToolHandlers(registry *mcp.Registry, tenantID string) []SyntheticToolHandler {
	if tenantID == "" {
		return nil
	}
	federated := registry.FederatedToolsForTenant(tenantID)
	handlers := make([]SyntheticToolHandler, 0, len(federated))
	for _, ft := range federated {
		handlers = append(handlers, &McpToolHandler{
			registry:   registry,
			tenantID:   tenantID,
			serverID:   ft.ServerID,
			serverName: ft.ServerName,
			tool:       ft.Tool,
			toolName:   mcpToolName(ft.ServerName, ft.Tool.Name),
		})
	}
	return handlers
}

// Name implements SyntheticToolHandler.
func (h *McpToolHandler) Name() string { return h.toolName }

// Definition implements SyntheticToolHandler.
func (h *McpToolHandler) Definition() gw.ToolDefinition {
	desc := h.tool.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %q from server %q", h.tool.Name, h.serverName)
	} else {
		desc = fmt.Sprintf("[MCP:%s] %s", h.serverName, desc)
	}

	params := h.tool.InputSchema
	if params == nil {
		params = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        h.toolName,
			Description: desc,
			Parameters:  params,
		},
	}
}

// Execute implements SyntheticToolHandler. Routes the tool call through the
// MCP registry to the appropriate backend server.
func (h *McpToolHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	logger.WithFields("tool", h.toolName, "server", h.serverName).Debug("executing MCP tool")

	result, err := h.registry.CallToolForTenant(ctx, h.tenantID, h.serverID, h.tool.Name, args)
	if err != nil {
		return "", fmt.Errorf("mcp tool %q: %w", h.toolName, err)
	}

	if result.IsError {
		errText := extractText(result)
		return fmt.Sprintf("Error from MCP tool: %s", errText), nil
	}

	return extractText(result), nil
}

// extractText concatenates text content blocks from a tool call result.
func extractText(r *mcp.ToolCallResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "image":
			parts = append(parts, fmt.Sprintf("[image: %s]", c.MimeType))
		case "resource":
			parts = append(parts, fmt.Sprintf("[resource: %s]", c.URI))
		default:
			// Best-effort: marshal the block
			if b, err := json.Marshal(c); err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	return strings.Join(parts, "\n")
}

// mcpToolName produces a namespaced tool name to prevent collisions.
// Example: server "github", tool "list_repos" → "mcp__github__list_repos"
func mcpToolName(serverName, toolName string) string {
	// Sanitize server name: replace non-alphanumeric chars with underscores
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, serverName)
	return fmt.Sprintf("mcp__%s__%s", safe, toolName)
}
