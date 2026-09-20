package tools

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) resolveMCP(o Owner, s Spec) (Grant, error) {
	if m.mcp == nil {
		return Grant{}, errorsUnavailable("MCP")
	}
	if s.ResourceID == "" || s.Name == "" {
		return Grant{}, ErrDenied
	}
	for _, tool := range m.mcp.FederatedToolsForTenant(o.InstanceID) {
		if tool.ServerID == s.ResourceID && tool.Tool.Name == s.Name {
			readOnly, _ := tool.Tool.Annotations["readOnlyHint"].(bool)
			return Grant{Kind: "mcp", ResourceID: tool.ServerID, Name: tool.Tool.Name, UpstreamName: safeName("mcp__" + tool.ServerName + "__" + tool.Tool.Name), Description: "[MCP:" + tool.ServerName + "] " + tool.Tool.Description, Parameters: tool.Tool.InputSchema, RequiresApproval: !readOnly}, nil
		}
	}
	return Grant{}, ErrDenied
}
func (m *Manager) executeMCP(ctx context.Context, o Owner, g Grant, args map[string]any) (any, error) {
	result, err := m.mcp.CallToolForTenant(ctx, o.InstanceID, g.ResourceID, g.Name, args)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return "", nil
	}
	parts := make([]string, 0, len(result.Content))
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "image":
			parts = append(parts, "[image: "+c.MimeType+"]")
		case "resource":
			parts = append(parts, "[resource]")
		}
	}
	text := strings.Join(parts, "\n")
	if result.IsError {
		return nil, fmt.Errorf("MCP tool failed: %s", text)
	}
	return text, nil
}
func errorsUnavailable(kind string) error { return fmt.Errorf("%s tools unavailable", kind) }
