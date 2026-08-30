package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	workflowscmd "github.com/everstacklabs/everstack/internal/commands/handlers/workflows"
	"github.com/everstacklabs/everstack/internal/cqrs"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// validNodeTypes is the set of valid Studio node types.
var validNodeTypes = map[string]bool{
	"start": true, "response": true,
	"auth": true, "rateLimiter": true, "cache": true,
	"inputGuardrails": true, "outputGuardrails": true,
	"provider": true, "agent": true, "memory": true,
	"tts": true, "stt": true, "voiceClone": true,
	"router": true, "loadBalancer": true, "function": true,
	"httpRequest": true, "webhook": true, "ifElse": true,
}

// ---------------------------------------------------------------------------
// create_workflow
// ---------------------------------------------------------------------------

// CreateWorkflowHandler handles the create_workflow synthetic tool.
type CreateWorkflowHandler struct {
	TenantID  string
	AgentID   string
	ServerCtx context.Context // fallback for CQRS
}

func (h *CreateWorkflowHandler) Name() string { return "create_workflow" }

func (h *CreateWorkflowHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name: "create_workflow",
			Description: `Create a Studio workflow with nodes and edges. The workflow appears in the Studio editor where the user can further customize and run it.

Available node types:
- start: Entry point for the workflow (handles: out)
- response: Final output node (handles: in)
- provider: LLM provider call, e.g. GPT-4, Claude (handles: in, out)
- agent: Run a deployed agent as a step (handles: in, out)
- auth: API key or JWT authentication (handles: in, out)
- rateLimiter: Rate limiting middleware (handles: in, out)
- cache: Semantic or exact-match cache (handles: in, hit, miss)
- inputGuardrails: PII detection, prompt injection, content filtering (handles: in, pass, block)
- outputGuardrails: Jailbreak, hallucination, toxicity detection (handles: in, pass, block)
- router: Route requests to different providers by model (handles: in, out)
- loadBalancer: Distribute load across providers (handles: in, out)
- function: Execute a serverless function (handles: in, out)
- httpRequest: Make an HTTP request (handles: in, out)
- webhook: Send a webhook notification (handles: in, out)
- ifElse: Conditional branching (handles: in, true, false)
- memory: Store or query vector memory (handles: in, out)
- tts: Text to speech synthesis (handles: in, out)
- stt: Speech to text transcription (handles: in, out)
- voiceClone: Voice cloning synthesis (handles: in, out)

Layout conventions:
- x=250 centers nodes horizontally
- y should increment by 150 for each subsequent node vertically
- Edges connect source_handle to target_handle
- Most nodes have handles: in (top), out (bottom)
- Special handles: ifElse has true/false, cache has hit/miss, guardrails have pass/block`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name for the workflow.",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Brief description of what the workflow does.",
					},
					"nodes": map[string]interface{}{
						"type":        "array",
						"description": "Array of workflow nodes.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Unique node ID (e.g. 'node-1').",
								},
								"type": map[string]interface{}{
									"type":        "string",
									"description": "Node type from the list above.",
								},
								"label": map[string]interface{}{
									"type":        "string",
									"description": "Display label for the node.",
								},
								"position": map[string]interface{}{
									"type":        "object",
									"description": "Position {x, y} on the canvas.",
									"properties": map[string]interface{}{
										"x": map[string]interface{}{"type": "number"},
										"y": map[string]interface{}{"type": "number"},
									},
								},
								"config": map[string]interface{}{
									"type":        "object",
									"description": "Node-specific configuration. Varies by node type.",
								},
							},
							"required": []string{"id", "type"},
						},
					},
					"edges": map[string]interface{}{
						"type":        "array",
						"description": "Array of edges connecting nodes.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Unique edge ID.",
								},
								"source": map[string]interface{}{
									"type":        "string",
									"description": "Source node ID.",
								},
								"target": map[string]interface{}{
									"type":        "string",
									"description": "Target node ID.",
								},
								"source_handle": map[string]interface{}{
									"type":        "string",
									"description": "Source handle ID (e.g. 'out', 'true', 'hit', 'pass').",
								},
								"target_handle": map[string]interface{}{
									"type":        "string",
									"description": "Target handle ID (e.g. 'in').",
								},
							},
							"required": []string{"id", "source", "target"},
						},
					},
				},
				"required": []string{"name", "nodes", "edges"},
			},
		},
	}
}

func (h *CreateWorkflowHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	description, _ := args["description"].(string)

	// Parse nodes
	rawNodes, ok := args["nodes"].([]interface{})
	if !ok || len(rawNodes) == 0 {
		return "", fmt.Errorf("nodes array is required and must not be empty")
	}

	rawEdges, _ := args["edges"].([]interface{})

	// Validate and transform nodes into the proto/DB format that Studio expects.
	// Studio's convertProtoNodesToStudio wraps these into React Flow nodes,
	// so we store the actual node type (e.g. "provider"), NOT "studioNode".
	type rfPosition struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	type rfNode struct {
		ID       string      `json:"id"`
		Type     string      `json:"type"`     // actual node type, e.g. "provider"
		Label    string      `json:"label"`
		Position rfPosition  `json:"position"`
		Config   interface{} `json:"config"`
	}

	type nodeStatusEntry struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Label  string `json:"label"`
		Status string `json:"status"`
	}

	nodes := make([]rfNode, 0, len(rawNodes))
	nodeStatuses := make([]nodeStatusEntry, 0, len(rawNodes))
	yOffset := 0.0

	for _, raw := range rawNodes {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		nodeID, _ := m["id"].(string)
		nodeType, _ := m["type"].(string)
		label, _ := m["label"].(string)

		if nodeID == "" || nodeType == "" {
			continue
		}

		if !validNodeTypes[nodeType] {
			return "", fmt.Errorf("invalid node type %q", nodeType)
		}

		if label == "" {
			label = nodeType
		}

		// Position: use provided or auto-assign
		pos := rfPosition{X: 250, Y: yOffset}
		if posMap, ok := m["position"].(map[string]interface{}); ok {
			if x, ok := posMap["x"].(float64); ok {
				pos.X = x
			}
			if y, ok := posMap["y"].(float64); ok {
				pos.Y = y
			}
		}
		yOffset += 150

		// Config
		config := m["config"]
		if config == nil {
			config = map[string]interface{}{}
		}

		// Determine status heuristic
		status := "ready"
		switch nodeType {
		case "provider":
			// Needs credential/model config
			cfg, _ := config.(map[string]interface{})
			if cfg == nil || cfg["model"] == nil || cfg["model"] == "" {
				status = "needs_config"
			}
		case "agent":
			cfg, _ := config.(map[string]interface{})
			if cfg == nil || cfg["agentId"] == nil || cfg["agentId"] == "" {
				status = "needs_config"
			}
		case "function":
			cfg, _ := config.(map[string]interface{})
			if cfg == nil || cfg["functionId"] == nil || cfg["functionId"] == "" {
				status = "needs_config"
			}
		case "httpRequest":
			cfg, _ := config.(map[string]interface{})
			if cfg == nil || cfg["url"] == nil || cfg["url"] == "" {
				status = "needs_config"
			}
		case "webhook":
			cfg, _ := config.(map[string]interface{})
			if cfg == nil || cfg["url"] == nil || cfg["url"] == "" {
				status = "needs_config"
			}
		case "tts", "stt", "voiceClone":
			status = "needs_config"
		}

		nodes = append(nodes, rfNode{
			ID:       nodeID,
			Type:     nodeType,
			Label:    label,
			Position: pos,
			Config:   config,
		})

		nodeStatuses = append(nodeStatuses, nodeStatusEntry{
			ID:     nodeID,
			Type:   nodeType,
			Label:  label,
			Status: status,
		})
	}

	// Transform edges into React Flow format
	type rfEdge struct {
		ID           string `json:"id"`
		Source       string `json:"source"`
		Target       string `json:"target"`
		SourceHandle string `json:"sourceHandle,omitempty"`
		TargetHandle string `json:"targetHandle,omitempty"`
	}

	edges := make([]rfEdge, 0, len(rawEdges))
	for _, raw := range rawEdges {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		edgeID, _ := m["id"].(string)
		source, _ := m["source"].(string)
		target, _ := m["target"].(string)
		sourceHandle, _ := m["source_handle"].(string)
		targetHandle, _ := m["target_handle"].(string)

		if edgeID == "" || source == "" || target == "" {
			continue
		}

		// Default handles
		if sourceHandle == "" {
			sourceHandle = "out"
		}
		if targetHandle == "" {
			targetHandle = "in"
		}

		edges = append(edges, rfEdge{
			ID:           edgeID,
			Source:       source,
			Target:       target,
			SourceHandle: sourceHandle,
			TargetHandle: targetHandle,
		})
	}

	// Serialize for CQRS command
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return "", fmt.Errorf("failed to serialize nodes: %w", err)
	}
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return "", fmt.Errorf("failed to serialize edges: %w", err)
	}

	viewport := map[string]interface{}{"x": 0, "y": 0, "zoom": 1}
	viewportJSON, _ := json.Marshal(viewport)

	// Dispatch CQRS command
	sys, sysErr := cqrs.GetSystemFromContext(ctx)
	if sysErr != nil && h.ServerCtx != nil {
		sys, sysErr = cqrs.GetSystemFromContext(h.ServerCtx)
	}
	if sysErr != nil {
		return "", fmt.Errorf("CQRS system not available: %w", sysErr)
	}

	cmd := workflowscmd.NewCreateWorkflowCommand(
		h.TenantID,
		name,
		description,
		nodesJSON,
		edgesJSON,
		viewportJSON,
		"", // userID — synthetic tool context
		"", // traceID
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", fmt.Errorf("failed to create workflow: %w", err)
	}

	// Sleep to allow the async event bus projection to write the read model row.
	// The projection subscriber runs asynchronously; without this delay,
	// immediate queries for the workflow will return "not found".
	time.Sleep(500 * time.Millisecond)

	// Build result JSON
	result := map[string]interface{}{
		"workflow_id": cmd.ID,
		"name":        name,
		"description": description,
		"nodes":       nodeStatuses,
		"edges":       edges,
		"studio_url":  fmt.Sprintf("/deployments/studio/%s", cmd.ID),
	}

	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// ---------------------------------------------------------------------------
// nodeTypeSummary returns a brief string listing all node types for prompt use.
// ---------------------------------------------------------------------------

// WorkflowCapabilitiesPrompt is the system prompt addition for workflow creation.
const WorkflowCapabilitiesPrompt = `## Workflow Creation
You can create Studio workflows using create_workflow. Available node types:
- Input/Output: start (entry point, handles: out), response (final output, handles: in)
- AI: provider (LLM call), agent (agent execution), tts, stt, voiceClone
- Middleware: auth, rateLimiter, cache (handles: in, hit, miss), inputGuardrails (handles: in, pass, block), outputGuardrails (handles: in, pass, block)
- Processing: router, loadBalancer, function, httpRequest, webhook
- Logic: ifElse (handles: in, true, false), memory

Layout: x=250 center, y spaced by 150. Edges connect source_handle→target_handle.
Most nodes: in (top), out (bottom). ifElse: in, true, false. cache: in, hit, miss.
Guardrails: in, pass, block.

Always include a 'start' node as the entry point and a 'response' node as the final output.`
