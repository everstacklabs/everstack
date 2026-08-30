package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/previewurl"
)

// buildPortURL constructs the URL for an exposed sandbox port.
// For localhost/loopback domains, uses path-based routing (/_sandbox/{id}/port/{port}/)
// since wildcard subdomains don't resolve locally. For real domains, uses subdomain routing.
func buildPortURL(baseDomain string, tlsEnabled bool, listenPort string, subdomain string, sandboxID string, port int) string {
	return previewurl.DirectURL(previewurl.Config{
		BaseDomain: baseDomain,
		TLSEnabled: tlsEnabled,
		ListenPort: listenPort,
	}, subdomain, sandboxID, port)
}

// ============================================================================
// sandbox_expose_port — exposes a container port to external traffic
// ============================================================================

type sandboxExposePortHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxExposePortHandler) Name() string { return "sandbox_expose_port" }

func (h *sandboxExposePortHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

// GetPortURLs implements portURLProvider — delegates to the shared context.
func (h *sandboxExposePortHandler) GetPortURLs() []string {
	return h.ctx.GetPortURLs()
}

func (h *sandboxExposePortHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_expose_port",
			Description: "Expose a port from the sandbox to external traffic. Returns a public URL for the service on that port. Before exposing, ensure the target service is actually running and listening on that exact port (especially for Vite, which may auto-shift ports unless `--strictPort` is used). In multi-app sandboxes, expose the app-specific port (do not assume 5173).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "The port number to expose (e.g., 3000, 8080, 5173).",
					},
					"protocol": map[string]interface{}{
						"type":        "string",
						"description": "The protocol (default: 'tcp').",
						"enum":        []string{"tcp", "udp"},
					},
				},
				"required": []string{"port"},
			},
		},
	}
}

func (h *sandboxExposePortHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	portFloat, _ := args["port"].(float64)
	port := int(portFloat)
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}

	protocol, _ := args["protocol"].(string)
	if protocol == "" {
		protocol = "tcp"
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}

	mapping, err := h.ctx.Manager.ExposePort(ctx, h.ctx.SessionID, port, protocol)
	if err != nil && errors.Is(err, sandbox.ErrSandboxNotRunning) {
		if inst, recErr := recoverSandbox(ctx, h.ctx); recErr == nil {
			_ = inst
			mapping, err = h.ctx.Manager.ExposePort(ctx, h.ctx.SessionID, port, protocol)
		}
	}
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "deny") || strings.Contains(errMsg, "network") {
			return fmt.Sprintf("Failed to expose port %d: %s. The sandbox was created with network_mode 'deny' which has no network access. To expose ports, the sandbox must use a dev template (e.g., 'node-dev', 'python-dev') with network_mode 'whitelist' or 'allow'. A new session is needed to change the template.", port, errMsg), nil
		}
		return fmt.Sprintf("Failed to expose port %d: %s", port, err.Error()), nil
	}

	url := buildPortURL(h.ctx.PortExposureBaseDomain, h.ctx.PortExposureTLSEnabled, h.ctx.PortExposureListenPort, mapping.Subdomain, mapping.SandboxID, port)
	h.ctx.AddPortURL(url)

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxPortExpose,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"port":      port,
				"host_port": mapping.HostPort,
				"protocol":  protocol,
				"url":       url,
				"subdomain": mapping.Subdomain,
			},
		})
	}

	return fmt.Sprintf("Port %d exposed at %s", port, url), nil
}

// ============================================================================
// sandbox_unexpose_port — closes an exposed port mapping
// ============================================================================

type sandboxUnexposePortHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxUnexposePortHandler) Name() string { return "sandbox_unexpose_port" }

func (h *sandboxUnexposePortHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxUnexposePortHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_unexpose_port",
			Description: "Stop exposing a previously exposed port. The public URL will no longer be accessible.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "The port number to unexpose.",
					},
				},
				"required": []string{"port"},
			},
		},
	}
}

func (h *sandboxUnexposePortHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	portFloat, _ := args["port"].(float64)
	port := int(portFloat)
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}

	unexposeErr := h.ctx.Manager.UnexposePort(ctx, h.ctx.SessionID, port)
	if unexposeErr != nil && errors.Is(unexposeErr, sandbox.ErrSandboxNotRunning) {
		if _, recErr := recoverSandbox(ctx, h.ctx); recErr == nil {
			unexposeErr = h.ctx.Manager.UnexposePort(ctx, h.ctx.SessionID, port)
		}
	}
	if unexposeErr != nil {
		return fmt.Sprintf("Failed to unexpose port %d: %s", port, unexposeErr.Error()), nil
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxPortUnexpose,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"port": port,
			},
		})
	}

	return fmt.Sprintf("Port %d is no longer exposed", port), nil
}

// ============================================================================
// sandbox_list_ports — lists all exposed ports and their URLs
// ============================================================================

type sandboxListPortsHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxListPortsHandler) Name() string { return "sandbox_list_ports" }

func (h *sandboxListPortsHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxListPortsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_list_ports",
			Description: "List currently exposed ports and their public URLs, and include detected listening ports that are not exposed yet. Use this to decide the correct app-specific port to expose.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (h *sandboxListPortsHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if _, err := ensureSandbox(ctx, h.ctx); err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}

	mappings, err := h.ctx.Manager.ListExposedPorts(ctx, h.ctx.SessionID)
	if err != nil {
		return fmt.Sprintf("Failed to list ports: %s", err.Error()), nil
	}

	var sb strings.Builder

	if len(mappings) > 0 {
		sb.WriteString(fmt.Sprintf("%d port(s) exposed:\n", len(mappings)))
		for _, m := range mappings {
			url := buildPortURL(h.ctx.PortExposureBaseDomain, h.ctx.PortExposureTLSEnabled, h.ctx.PortExposureListenPort, m.Subdomain, m.SandboxID, m.Port)
			sb.WriteString(fmt.Sprintf("  - Port %d (%s): %s\n", m.Port, m.Protocol, url))
		}
	}

	// Also detect unexposed listening ports so the agent can proactively suggest exposing them.
	// Retry briefly because services started in detached mode may need a moment before they begin listening.
	detected, detectErr := h.detectListeningPortsWithRetry(ctx)
	if detectErr == nil && len(detected) > 0 {
		exposedSet := make(map[int]bool, len(mappings))
		for _, m := range mappings {
			exposedSet[m.Port] = true
		}

		var unexposed []string
		for _, lp := range detected {
			if exposedSet[lp.Port] {
				continue
			}
			label := fmt.Sprintf("%d", lp.Port)
			if lp.Process != "" {
				label += fmt.Sprintf(" (%s)", lp.Process)
			}
			if lp.Address == "127.0.0.1" || lp.Address == "::1" {
				label += " [localhost]"
			}
			unexposed = append(unexposed, label)
		}

		if len(unexposed) > 0 {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("%d unexposed listening port(s) detected:\n", len(unexposed)))
			for _, u := range unexposed {
				sb.WriteString(fmt.Sprintf("  - %s\n", u))
			}
			sb.WriteString("Use sandbox_expose_port to make them accessible.\n")
		}
	}

	if sb.Len() == 0 {
		return "No ports are currently exposed and no listening ports detected", nil
	}

	return sb.String(), nil
}

func (h *sandboxListPortsHandler) detectListeningPortsWithRetry(ctx context.Context) ([]sandbox.ListeningPort, error) {
	var lastErr error
	for i := 0; i < 5; i++ {
		ports, err := h.ctx.Manager.DetectListeningPorts(ctx, h.ctx.SessionID)
		if err != nil && errors.Is(err, sandbox.ErrSandboxNotRunning) {
			// Sandbox died — recover and retry detection from the top.
			if _, recErr := recoverSandbox(ctx, h.ctx); recErr != nil {
				return nil, fmt.Errorf("sandbox recovery failed during port detection: %w", recErr)
			}
			lastErr = err
			continue
		}
		if err == nil && len(ports) > 0 {
			return ports, nil
		}
		if err != nil {
			lastErr = err
		}
		if i < 4 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}
