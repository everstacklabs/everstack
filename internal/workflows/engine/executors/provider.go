package executors

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

const maxToolLoopIterations = 10

// ProviderExecutor handles LLM provider calls (chat completion).
type ProviderExecutor struct {
	Registry *gw.Registry
	Router   *gw.Router
	ToolLoop *toolloop.LoopManager
}

func (e *ProviderExecutor) NodeType() string { return "provider" }

func (e *ProviderExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	// Determine model and provider
	model := node.GetConfigString("model")
	if model == "" {
		model = ec.ResolvedModel
	}
	if model == "" {
		return engine.NodeResult{Error: fmt.Errorf("no model specified")}
	}

	providerType := node.GetConfigString("providerType")
	if providerType == "" {
		providerType = ec.ResolvedProvider
	}

	// Try to resolve provider from registry
	var provider gw.Provider
	if providerType != "" && e.Registry != nil {
		p, ok := e.Registry.Get(providerType)
		if ok {
			provider = p
		}
	}

	// If no provider found via direct name, try via router
	if provider == nil && e.Router != nil {
		resolvedProvider, route, err := e.Router.Resolve(model)
		if err == nil {
			provider = resolvedProvider
			if route.ModelName != "" {
				model = route.ModelName
			}
		}
	}

	if provider == nil {
		return engine.NodeResult{Error: fmt.Errorf("provider %q not found in registry", providerType)}
	}

	// Build sampling params
	sampling := ec.SamplingParams
	if maxTokens := node.GetConfigInt("maxTokens"); maxTokens > 0 {
		sampling.MaxTokens = maxTokens
	}

	// Build tool definitions from configured function names.
	// If no explicit function names are configured, auto-discover all enabled functions for the tenant.
	var tools []gw.ToolDefinition
	if e.ToolLoop != nil && e.ToolLoop.IsEnabled() {
		tenantID := contextkeys.GetTenantID(ctx)
		functionNames := node.GetConfigStringSlice("functions")

		// Auto-discover functions if none explicitly configured
		if len(functionNames) == 0 && tenantID != "" {
			discovered, err := e.ToolLoop.ListEnabledFunctionNames(ctx, tenantID)
			if err != nil {
				logger.WithFields("error", err.Error(), "tenant_id", tenantID).
					Warn("provider executor: failed to auto-discover functions")
			} else {
				functionNames = discovered
				if len(functionNames) > 0 {
					logger.WithFields("tenant_id", tenantID, "functions", len(functionNames)).
						Debug("provider executor: auto-discovered functions for tenant")
				}
			}
		}

		if len(functionNames) > 0 {
			builtTools, err := e.ToolLoop.BuildToolDefinitions(ctx, tenantID, functionNames)
			if err != nil {
				logger.WithFields("error", err.Error()).Warn("provider executor: failed to build tool definitions")
			} else {
				tools = builtTools
			}
		}
	}

	// Inject upstream data-producing node outputs into messages
	if ec.Ledger != nil {
		dataOutputs := ec.Ledger.DataOutputsSinceLastProvider()
		if len(dataOutputs) > 0 {
			contextText := engine.FormatNodeOutputsAsContext(dataOutputs)
			if contextText != "" {
				ctxMsg := gw.Message{
					Role: gw.RoleSystem,
					Content: []gw.ContentPart{
						{Type: "text", Text: &contextText},
					},
				}

				// Insert just before the last user message
				inserted := false
				for i := len(ec.Messages) - 1; i >= 0; i-- {
					if ec.Messages[i].Role == gw.RoleUser {
						ec.Messages = append(ec.Messages[:i], append([]gw.Message{ctxMsg}, ec.Messages[i:]...)...)
						inserted = true
						break
					}
				}
				if !inserted {
					ec.Messages = append(ec.Messages, ctxMsg)
				}

				logger.WithFields("data_outputs", len(dataOutputs), "messages", len(ec.Messages)).
					Info("provider executor: injected upstream node context into messages")
			}
		} else {
			logger.WithFields("ledger_entries", ec.Ledger.Len()).
				Debug("provider executor: no upstream data outputs to inject")
		}
	}

	// Build request
	req := gw.ChatCompletionRequest{
		Model:    model,
		Messages: ec.Messages,
		Sampling: sampling,
		Tools:    tools,
	}
	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}

	logger.WithFields(
		"model", model,
		"provider", provider.Name(),
		"messages", len(ec.Messages),
		"tools", len(tools),
		"streaming_enabled", ec.StreamingEnabled,
		"has_on_event", ec.OnEvent != nil,
	).Debug("provider executor: making chat request")

	ec.SetNodeData("model", model)
	ec.SetNodeData("provider", provider.Name())
	ec.SetNodeData("streaming", fmt.Sprintf("%v", ec.StreamingEnabled))

	// Streaming mode
	if ec.StreamingEnabled && ec.OnEvent != nil {
		logger.WithFields("tools", len(tools)).Debug("provider executor: taking streaming path")
		result := e.executeStreaming(ctx, node, ec, provider, req, model)
		e.populateTokenNodeData(ec)
		if result.Error == nil {
			result.Output = buildProviderOutput(ec, model, provider.Name())
		}
		return result
	}

	// Non-streaming mode with tool loop
	logger.Debug("provider executor: taking non-streaming path")
	result := e.executeNonStreaming(ctx, node, ec, provider, req, model)
	e.populateTokenNodeData(ec)
	if result.Error == nil {
		result.Output = buildProviderOutput(ec, model, provider.Name())
	}
	return result
}

// buildProviderOutput returns a typed output map for the execution ledger.
func buildProviderOutput(ec *engine.ExecutionContext, model, providerName string) map[string]interface{} {
	out := map[string]interface{}{
		"model":               model,
		"provider":            providerName,
		"messages_sent_count": len(ec.Messages),
	}
	if ec.Response != nil && len(ec.Response.Choices) > 0 {
		msg := ec.Response.Choices[0].Message
		if len(msg.Content) > 0 && msg.Content[0].Text != nil {
			out["content"] = *msg.Content[0].Text
		}
		out["usage"] = map[string]interface{}{
			"prompt_tokens":     ec.Response.Usage.PromptTokens,
			"completion_tokens": ec.Response.Usage.CompletionTokens,
			"total_tokens":      ec.Response.Usage.TotalTokens,
		}
	}
	return out
}

func (e *ProviderExecutor) populateTokenNodeData(ec *engine.ExecutionContext) {
	if ec.Response != nil {
		ec.SetNodeData("prompt_tokens", fmt.Sprintf("%d", ec.Response.Usage.PromptTokens))
		ec.SetNodeData("completion_tokens", fmt.Sprintf("%d", ec.Response.Usage.CompletionTokens))
		ec.SetNodeData("total_tokens", fmt.Sprintf("%d", ec.Response.Usage.TotalTokens))

		// Input preview: first user message
		for i := len(ec.Messages) - 1; i >= 0; i-- {
			if ec.Messages[i].Role == "user" && len(ec.Messages[i].Content) > 0 && ec.Messages[i].Content[0].Text != nil {
				preview := *ec.Messages[i].Content[0].Text
				if len(preview) > 200 {
					preview = preview[:200]
				}
				ec.SetNodeData("input_preview", preview)
				break
			}
		}

		// Output preview
		if len(ec.Response.Choices) > 0 {
			msg := ec.Response.Choices[0].Message
			if len(msg.Content) > 0 && msg.Content[0].Text != nil {
				preview := *msg.Content[0].Text
				if len(preview) > 200 {
					preview = preview[:200]
				}
				ec.SetNodeData("output_preview", preview)
			}
		}
	}
}

func (e *ProviderExecutor) executeStreaming(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext, provider gw.Provider, req gw.ChatCompletionRequest, model string) engine.NodeResult {
	if len(req.Tools) > 0 {
		// Tool calls can only be detected via the non-streaming Chat() method
		// (ChatStream doesn't deliver tool call information in any provider).
		// Run the full tool loop non-streaming, then emit the final response
		// as a single chunk. This avoids an extra LLM round-trip.
		result := e.runToolLoop(ctx, node, ec, provider, req, model)
		if result.Error != nil {
			return result
		}

		// Emit the already-obtained final response as a single chunk
		if ec.Response != nil && len(ec.Response.Choices) > 0 {
			msg := ec.Response.Choices[0].Message
			if len(msg.Content) > 0 && msg.Content[0].Text != nil {
				content := *msg.Content[0].Text
				if content != "" {
					ec.OnEvent(engine.ExecutionEvent{
						Type:         "chunk",
						NodeID:       node.ID,
						NodeType:     node.Type,
						NodeLabel:    node.Label,
						ChunkContent: content,
					})
				}
			}
		}
		return engine.NodeResult{NextHandle: "out"}
	}

	// No tools — stream directly from the LLM token-by-token
	var fullContent string
	err := provider.ChatStream(ctx, req, func(chunk gw.ChatResponseChunk) error {
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			if len(delta.Content) > 0 && delta.Content[0].Text != nil {
				tokenContent := *delta.Content[0].Text
				fullContent += tokenContent
				return ec.OnEvent(engine.ExecutionEvent{
					Type:         "chunk",
					NodeID:       node.ID,
					NodeType:     node.Type,
					NodeLabel:    node.Label,
					ChunkContent: tokenContent,
				})
			}
		}
		return nil
	})

	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("chat stream failed: %w", err)}
	}

	e.setResponse(ec, model, fullContent)
	// Append assistant message to ec.Messages so downstream provider nodes
	// see this output in their conversation context.
	if ec.Response != nil && len(ec.Response.Choices) > 0 {
		ec.Messages = append(ec.Messages, ec.Response.Choices[0].Message)
	}
	return engine.NodeResult{NextHandle: "out"}
}

func (e *ProviderExecutor) executeNonStreaming(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext, provider gw.Provider, req gw.ChatCompletionRequest, model string) engine.NodeResult {
	if len(req.Tools) > 0 {
		// The tool loop handles all iterations including the final text response.
		// ec.Response is already set when it returns.
		result := e.runToolLoop(ctx, node, ec, provider, req, model)
		if result.Error != nil {
			return result
		}
		return engine.NodeResult{NextHandle: "out"}
	}

	// No tools — direct call
	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("chat request failed: %w", err)}
	}

	ec.Response = &resp
	// Append assistant message to ec.Messages so downstream provider nodes
	// see this output in their conversation context.
	if len(resp.Choices) > 0 {
		ec.Messages = append(ec.Messages, resp.Choices[0].Message)
	}
	return engine.NodeResult{NextHandle: "out"}
}

// runToolLoop executes the tool calling loop until the LLM returns a non-tool-call response.
// It appends tool call and result messages to ec.Messages.
func (e *ProviderExecutor) runToolLoop(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext, provider gw.Provider, req gw.ChatCompletionRequest, model string) engine.NodeResult {
	if e.ToolLoop == nil || !e.ToolLoop.IsEnabled() {
		return engine.NodeResult{}
	}

	tenantID := contextkeys.GetTenantID(ctx)
	correlationID := uuid.New().String()[:8]

	for i := 0; i < maxToolLoopIterations; i++ {
		// Make chat request with tools
		chatReq := gw.ChatCompletionRequest{
			Model:      model,
			Messages:   ec.Messages,
			Sampling:   req.Sampling,
			Tools:      req.Tools,
			ToolChoice: req.ToolChoice,
		}

		resp, err := provider.Chat(ctx, chatReq)
		if err != nil {
			return engine.NodeResult{Error: fmt.Errorf("tool loop chat request failed: %w", err)}
		}

		// Check if the LLM wants to call tools
		if !e.ToolLoop.ShouldExecuteToolLoop(&resp) {
			// No tool calls — LLM gave a final text response.
			// Append the assistant response to messages so the final call
			// can use the updated conversation.
			if len(resp.Choices) > 0 {
				ec.Messages = append(ec.Messages, resp.Choices[0].Message)
			}
			ec.Response = &resp
			return engine.NodeResult{}
		}

		// Execute tool calls
		execCtx := &toolloop.ExecutionContext{
			RequestID:     uuid.New().String(),
			TenantID:      tenantID,
			CorrelationID: correlationID,
		}

		toolMessages, err := e.ToolLoop.ExecuteToolLoop(ctx, execCtx, &resp)
		if err != nil {
			logger.WithFields("error", err.Error(), "iteration", i).
				Warn("provider executor: tool loop execution had errors")
		}

		// Emit events for tool execution
		if ec.OnEvent != nil {
			for _, tm := range toolMessages {
				if tm.Role == gw.RoleTool && len(tm.Content) > 0 && tm.Content[0].Text != nil {
					ec.OnEvent(engine.ExecutionEvent{
						Type:         "chunk",
						NodeID:       node.ID,
						NodeType:     node.Type,
						NodeLabel:    node.Label,
						ChunkContent: "", // Don't emit tool results as chat content
						Data: map[string]string{
							"tool_call_id": tm.ToolCallID,
							"tool_result":  *tm.Content[0].Text,
						},
					})
				}
			}
		}

		// Append messages (assistant tool_calls message + tool result messages)
		ec.Messages = append(ec.Messages, toolMessages...)

		logger.WithFields("iteration", i+1, "tool_messages", len(toolMessages)).
			Debug("provider executor: tool loop iteration complete")
	}

	return engine.NodeResult{Error: fmt.Errorf("tool loop exceeded maximum iterations (%d)", maxToolLoopIterations)}
}

func (e *ProviderExecutor) setResponse(ec *engine.ExecutionContext, model, content string) {
	textContent := content
	ec.Response = &gw.ChatCompletionResponse{
		Model: model,
		Choices: []gw.Choice{
			{
				Index: 0,
				Message: gw.Message{
					Role: gw.RoleAssistant,
					Content: []gw.ContentPart{
						{Type: "text", Text: &textContent},
					},
				},
				FinishReason: "stop",
			},
		},
	}
}
