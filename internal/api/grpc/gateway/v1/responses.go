package v1

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cacheexec"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// responseStore is an in-memory store for responses.
// In production, this should be backed by a database.
type responseStore struct {
	mu        sync.RWMutex
	responses map[string]*gw.CreateResponseResponse
}

var globalResponseStore = &responseStore{
	responses: make(map[string]*gw.CreateResponseResponse),
}

func (rs *responseStore) Set(id string, resp *gw.CreateResponseResponse) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.responses[id] = resp
}

func (rs *responseStore) Get(id string) (*gw.CreateResponseResponse, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	resp, ok := rs.responses[id]
	return resp, ok
}

func (rs *responseStore) Delete(id string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.responses[id]; ok {
		delete(rs.responses, id)
		return true
	}
	return false
}

func (rs *responseStore) List(limit int, after, before string) []*gw.CreateResponseResponse {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	var results []*gw.CreateResponseResponse
	for _, resp := range rs.responses {
		results = append(results, resp)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results
}

// CreateResponse implements the Responses API create endpoint.
func (s *Server) CreateResponse(ctx context.Context, req *connect.Request[gatewaypb.CreateResponseRequest], stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	logger.Debug("gateway: processing create response request")

	// Convert proto to gateway request
	gwReq := convertProtoToResponseRequest(req.Msg)

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for response request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("response: no providers configured for this tenant"))
	}
	s.requestDefaultsForModel(ctx, gwReq.Model).applyResponse(&gwReq)

	// Find a provider that supports the Responses API
	provider, providerName, ok := bundle.reg.FindResponsesProvider()
	if ok {
		logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing response request to native provider")

		// Stamp the resolved provider on the trace span so Responses-API traces
		// attribute a provider instead of rendering as "N/A".
		if sp := trace.SpanFromContext(ctx); sp != nil {
			sp.SetAttributes(attribute.String("provider", providerName))
		}

		// Use native Responses API if provider supports it
		if gwReq.Stream {
			return s.handleStreamingResponse(ctx, provider, providerName, gwReq, stream)
		}
		return s.handleUnaryResponse(ctx, provider, providerName, gwReq, stream)
	}

	// Fallback: Implement agentic loop using Chat Completions API
	logger.WithFields("model", req.Msg.Model).Debug("gateway: using chat-based agentic loop for response")
	return s.handleAgenticResponse(ctx, gwReq, stream)
}

// handleStreamingResponse handles streaming Responses API using native provider.
func (s *Server) handleStreamingResponse(ctx context.Context, provider gw.ResponsesProvider, providerName string, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	var streamUsage *gw.ResponseUsage
	var streamModel string
	err := provider.CreateResponseStream(ctx, req, func(resp gw.CreateResponseResponse) error {
		if resp.Usage != nil {
			streamUsage = resp.Usage
		}
		if resp.Model != "" {
			streamModel = resp.Model
		}
		protoResp := convertResponseToProto(&resp)
		if err := stream.Send(protoResp); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Record usage metrics from the final streaming chunk. Use the provider that
	// actually served this Responses request (chosen by capability), not a
	// model->provider re-resolve.
	if streamUsage != nil {
		costDetails := metrics.CalculateCost(providerName, streamModel, streamUsage.InputTokens, streamUsage.OutputTokens, 0)
		s.recordUsageMetrics(
			int64(streamUsage.InputTokens),
			int64(streamUsage.OutputTokens),
			costDetails.EstimatedUSD,
			0, false,
		)
	}
	return nil
}

// handleUnaryResponse handles non-streaming Responses API.
func (s *Server) handleUnaryResponse(ctx context.Context, provider gw.ResponsesProvider, providerName string, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	resp, cacheOutcome, err := cacheexec.ExecuteResponseWithCacheOutcome(ctx, req,
		func(callCtx context.Context, callReq gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return provider.CreateResponse(callCtx, callReq)
		},
	)
	annotateResponsesCacheTrace(ctx, cacheOutcome, req.Model)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	cacheType := "miss"
	if cacheOutcome.Hit {
		cacheType = cacheOutcome.HitType
	}
	logger.WithFields(
		"model", resp.Model,
		"cache_hit", cacheOutcome.Hit,
		"cache_type", cacheType,
		"cache_stored", cacheOutcome.Stored,
		"semantic_stored", cacheOutcome.SemanticStored,
	).Debug("gateway: responses unary cache outcome")

	// Record usage metrics. Use the provider that actually served this Responses
	// request (chosen by capability), not a model->provider re-resolve.
	if resp.Usage != nil {
		costDetails := metrics.CalculateCost(providerName, resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens, 0)
		estimatedCost := costDetails.EstimatedUSD
		cacheSavings := 0.0
		if cacheOutcome.Hit {
			estimatedCost = 0
			cacheSavings = costDetails.EstimatedUSD
		}
		s.recordUsageMetrics(
			int64(resp.Usage.InputTokens),
			int64(resp.Usage.OutputTokens),
			estimatedCost,
			cacheSavings, cacheOutcome.Hit,
		)
	}

	// Store the response
	globalResponseStore.Set(resp.ID, &resp)

	// Send the response
	protoResp := convertResponseToProto(&resp)
	return stream.Send(protoResp)
}

// handleAgenticResponse implements the agentic loop using Chat Completions API.
func (s *Server) handleAgenticResponse(ctx context.Context, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	// Generate a unique response ID
	responseID := generateResponseID()
	createdAt := time.Now().Unix()

	// Convert inputs to chat messages
	messages := s.convertInputsToMessages(req)

	// If previous_response_id is provided, hydrate prior assistant messages so
	// the fallback loop can continue the same thread.
	if req.PreviousResponseID != "" {
		if prev, ok := globalResponseStore.Get(req.PreviousResponseID); ok && prev != nil {
			previousMessages := responseOutputToMessages(prev.Output)
			if len(previousMessages) > 0 {
				messages = append(previousMessages, messages...)
			}
		} else {
			logger.WithFields("previous_response_id", req.PreviousResponseID).
				Debug("gateway: previous response not found in in-memory store")
		}
	}

	// Add system instructions if provided
	if req.Instructions != "" {
		systemMsg := gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: stringPtr(req.Instructions)}},
		}
		messages = append([]gw.Message{systemMsg}, messages...)
	}

	// Convert builtin tools and custom tools to chat tools
	tools := s.convertToToolDefinitions(req)

	// Prepare chat request
	chatReq := gw.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Sampling: gw.SamplingParams{
			Temperature:           req.Temperature,
			TemperatureConfigured: req.TemperatureConfigured,
			TopP:                  req.TopP,
			TopPConfigured:        req.TopPConfigured,
			MaxTokens:             req.MaxOutputTokens,
			ReasoningEffort:       reasoningEffort(req.Reasoning),
		},
		// The fallback loop uses unary provider.Chat calls internally.
		Stream:     false,
		ToolChoice: req.ToolChoice,
	}

	// Apply configurable conversation truncation for the fallback agentic loop.
	// This prevents unbounded context growth when tools append extra messages.
	maxHistoryMessages := resolveResponseHistoryLimit(req.Truncation)

	// Create initial response
	response := &gw.CreateResponseResponse{
		ID:                 responseID,
		Object:             "response",
		CreatedAt:          createdAt,
		Status:             "in_progress",
		Model:              req.Model,
		Output:             []gw.ResponseOutputItem{},
		Metadata:           req.Metadata,
		Temperature:        req.Temperature,
		TopP:               req.TopP,
		MaxOutputTokens:    req.MaxOutputTokens,
		PreviousResponseID: req.PreviousResponseID,
		Truncation:         req.Truncation,
		Reasoning:          req.Reasoning,
	}

	// Send initial in_progress event
	if err := stream.Send(convertResponseToProto(response)); err != nil {
		return err
	}

	// Execute the agentic loop
	maxIterations := 10 // Prevent infinite loops
	for i := 0; i < maxIterations; i++ {
		if maxHistoryMessages > 0 {
			chatReq.Messages = truncateChatHistory(chatReq.Messages, maxHistoryMessages)
		}

		// Call the chat completion
		chatResp, cacheHit, err := s.processChatCompletionInternal(ctx, chatReq)
		if err != nil {
			response.Status = "failed"
			response.Error = &gw.ResponseError{
				Code:    "chat_error",
				Message: err.Error(),
			}
			globalResponseStore.Set(responseID, response)
			return stream.Send(convertResponseToProto(response))
		}

		// Record usage metrics for this agentic loop iteration
		if chatResp.Usage.PromptTokens > 0 || chatResp.Usage.CompletionTokens > 0 {
			iterProvider := s.getProviderForModel(ctx, chatResp.Model)
			iterCost := metrics.CalculateCost(iterProvider, chatResp.Model, chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens, 0)
			estimatedCost := iterCost.EstimatedUSD
			cacheSavings := 0.0
			if cacheHit {
				estimatedCost = 0
				cacheSavings = iterCost.EstimatedUSD
			}
			s.recordUsageMetrics(
				int64(chatResp.Usage.PromptTokens),
				int64(chatResp.Usage.CompletionTokens),
				estimatedCost,
				cacheSavings, cacheHit,
			)
		}

		// Check if we have tool calls to execute
		if len(chatResp.Choices) > 0 {
			choice := chatResp.Choices[0]

			// Add assistant message to output
			outputItem := gw.ResponseOutputItem{
				ID:      generateItemID(),
				Type:    "message",
				Status:  "completed",
				Role:    gw.RoleAssistant,
				Content: choice.Message.Content,
			}
			response.Output = append(response.Output, outputItem)

			// Check for tool calls
			if len(choice.Message.ToolCalls) > 0 {
				// Add tool call items to output
				for _, tc := range choice.Message.ToolCalls {
					toolCallItem := gw.ResponseOutputItem{
						ID:        tc.ID,
						Type:      "function_call",
						Status:    "completed",
						CallID:    tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					}
					response.Output = append(response.Output, toolCallItem)
				}

				// Execute tool calls using the tool loop manager
				if s.toolLoop != nil && s.toolLoop.IsEnabled() {
					toolMessages, err := s.toolLoop.ExecuteToolLoop(ctx, &toolloop.ExecutionContext{
						RequestID:     responseID,
						TenantID:      s.GetOrganizationID(),
						CorrelationID: responseID,
					}, &chatResp)

					if err != nil {
						logger.WithFields("error", err.Error()).Warn("gateway: tool execution failed")
					}

					// ExecuteToolLoop returns messages in correct order:
					// [0] = assistant message with tool_calls
					// [1..n] = tool result messages
					// Add all of them to the chat history
					for _, tm := range toolMessages {
						chatReq.Messages = append(chatReq.Messages, tm)

						// Add tool output to response (only for tool messages, not assistant)
						if tm.Role == gw.RoleTool && tm.ToolCallID != "" {
							for _, content := range tm.Content {
								if content.Text != nil {
									toolOutputItem := gw.ResponseOutputItem{
										ID:     generateItemID(),
										Type:   "function_call_output",
										Status: "completed",
										CallID: tm.ToolCallID,
										Output: *content.Text,
									}
									response.Output = append(response.Output, toolOutputItem)
								}
							}
						}
					}

					// Continue the loop to get the model's response to the tool results
					continue
				}
			}

			// No tool calls or tool execution disabled - we're done
			if choice.FinishReason == "stop" || choice.FinishReason == "end_turn" || len(choice.Message.ToolCalls) == 0 {
				response.Status = "completed"
				response.Usage = &gw.ResponseUsage{
					InputTokens:  chatResp.Usage.PromptTokens,
					OutputTokens: chatResp.Usage.CompletionTokens,
					TotalTokens:  chatResp.Usage.TotalTokens,
				}
				globalResponseStore.Set(responseID, response)
				return stream.Send(convertResponseToProto(response))
			}
		}

		// Add the assistant response to messages for next iteration
		if len(chatResp.Choices) > 0 {
			chatReq.Messages = append(chatReq.Messages, chatResp.Choices[0].Message)
		}
	}

	// Max iterations reached
	response.Status = "incomplete"
	response.IncompleteDetails = &gw.IncompleteDetails{
		Reason: "max_iterations_reached",
	}
	globalResponseStore.Set(responseID, response)
	return stream.Send(convertResponseToProto(response))
}

func reasoningEffort(reasoning *gw.ReasoningConfig) string {
	if reasoning == nil {
		return ""
	}
	return reasoning.Effort
}

// processChatCompletionInternal is a wrapper to call the internal chat completion.
func (s *Server) processChatCompletionInternal(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, bool, error) {
	resp, cacheOutcome, err := cacheexec.ExecuteChatWithCacheOutcome(ctx, req,
		func(callCtx context.Context, callReq gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			router := s.routerFor(callCtx)
			if router == nil {
				return gw.ChatCompletionResponse{}, gw.ErrNotImplemented("processChatCompletionInternal: no providers configured for this tenant")
			}
			provider, route, resolveErr := router.ResolveWithContext(callCtx, callReq.Model)
			if resolveErr != nil {
				return gw.ChatCompletionResponse{}, resolveErr
			}

			// Update model to actual model if aliased.
			if route.ModelName != "" {
				callReq.Model = route.ModelName
			}

			return provider.Chat(callCtx, callReq)
		},
	)
	annotateResponsesCacheTrace(ctx, cacheOutcome, req.Model)
	if err != nil {
		return gw.ChatCompletionResponse{}, false, err
	}
	cacheType := "miss"
	if cacheOutcome.Hit {
		cacheType = cacheOutcome.HitType
	}
	logger.WithFields(
		"model", resp.Model,
		"cache_hit", cacheOutcome.Hit,
		"cache_type", cacheType,
		"cache_stored", cacheOutcome.Stored,
		"semantic_stored", cacheOutcome.SemanticStored,
	).Debug("gateway: responses fallback chat cache outcome")
	return resp, cacheOutcome.Hit, nil
}

func annotateResponsesCacheTrace(ctx context.Context, outcome cacheexec.CacheOutcome, model string) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.SpanContext().IsValid() {
		return
	}

	telemetry.AddSpanEvent(span, attrs.EventCacheLookupStart,
		attribute.String(attrs.LLMRequestModel, model),
	)

	cacheType := "none"
	if outcome.Hit {
		cacheType = outcome.HitType
	}
	attrs.SetCacheMetadata(span, outcome.Hit, "")
	attrs.SetCacheLookup(span, cacheType, outcome.Hit, 0, 0)

	if outcome.Hit {
		telemetry.AddSpanEvent(span, attrs.EventCacheLookupHit,
			attribute.String(attrs.CacheType, cacheType),
			attribute.Bool(attrs.CacheHit, true),
		)
	} else {
		telemetry.AddSpanEvent(span, attrs.EventCacheLookupMiss,
			attribute.Bool(attrs.CacheHit, false),
		)
	}

	if outcome.Stored {
		telemetry.AddSpanEvent(span, attrs.EventCacheStoreStart,
			attribute.String(attrs.CacheType, "exact"),
		)
		attrs.SetCacheStore(span, "exact", 0, 0, 0)
		telemetry.AddSpanEvent(span, attrs.EventCacheStoreComplete,
			attribute.String(attrs.CacheType, "exact"),
		)
		if outcome.SemanticStored {
			telemetry.AddSpanEvent(span, attrs.EventCacheStoreStart,
				attribute.String(attrs.CacheType, "semantic"),
			)
			telemetry.AddSpanEvent(span, attrs.EventCacheStoreComplete,
				attribute.String(attrs.CacheType, "semantic"),
			)
		}
	}
}

func resolveResponseHistoryLimit(truncation *gw.TruncationStrategy) int {
	// Default guard for fallback loop.
	limit := 100
	if truncation == nil {
		return limit
	}

	if truncation.Type == "disabled" {
		return 0
	}
	if truncation.LastMessages > 0 {
		return truncation.LastMessages
	}
	return limit
}

func truncateChatHistory(messages []gw.Message, maxMessages int) []gw.Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}

	tail := messages[len(messages)-maxMessages+1:]
	if len(messages) > 0 && messages[0].Role == gw.RoleSystem {
		truncated := make([]gw.Message, 0, maxMessages)
		truncated = append(truncated, messages[0])
		truncated = append(truncated, tail...)
		return truncated
	}
	return messages[len(messages)-maxMessages:]
}

func responseOutputToMessages(items []gw.ResponseOutputItem) []gw.Message {
	msgs := make([]gw.Message, 0, len(items))
	for _, item := range items {
		if item.Type != "message" || len(item.Content) == 0 {
			continue
		}
		role := item.Role
		if role == "" {
			role = gw.RoleAssistant
		}
		msgs = append(msgs, gw.Message{
			Role:    role,
			Content: item.Content,
		})
	}
	return msgs
}

// convertInputsToMessages converts ResponseInputs to chat Messages.
func (s *Server) convertInputsToMessages(req gw.CreateResponseRequest) []gw.Message {
	var messages []gw.Message
	for _, input := range req.Input {
		if input.Type == "message" {
			msg := gw.Message{
				Role:    input.Role,
				Content: input.Content,
			}
			messages = append(messages, msg)
		}
	}
	return messages
}

// convertToToolDefinitions converts builtin and custom tools to ToolDefinitions.
func (s *Server) convertToToolDefinitions(req gw.CreateResponseRequest) []gw.ToolDefinition {
	var tools []gw.ToolDefinition

	// Add custom function tools
	tools = append(tools, req.Tools...)

	// Builtin tools are handled by the tool loop manager
	// They don't need to be sent to the model as tool definitions

	return tools
}

// GetResponse retrieves a response by ID.
func (s *Server) GetResponse(ctx context.Context, req *connect.Request[gatewaypb.GetResponseRequest]) (*connect.Response[gatewaypb.GetResponseResponse], error) {
	resp, ok := globalResponseStore.Get(req.Msg.ResponseId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, gw.ErrNotImplemented("response not found"))
	}

	protoResp := &gatewaypb.GetResponseResponse{
		Response: convertResponseToProto(resp),
	}

	return connect.NewResponse(protoResp), nil
}

// CancelResponse cancels an in-progress response.
func (s *Server) CancelResponse(ctx context.Context, req *connect.Request[gatewaypb.CancelResponseRequest]) (*connect.Response[gatewaypb.CancelResponseResponse], error) {
	resp, ok := globalResponseStore.Get(req.Msg.ResponseId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, gw.ErrNotImplemented("response not found"))
	}

	// Update status to cancelled
	resp.Status = "cancelled"
	globalResponseStore.Set(req.Msg.ResponseId, resp)

	return connect.NewResponse(&gatewaypb.CancelResponseResponse{
		Success:  true,
		Response: convertResponseToProto(resp),
	}), nil
}

// DeleteResponse deletes a response.
func (s *Server) DeleteResponse(ctx context.Context, req *connect.Request[gatewaypb.DeleteResponseRequest]) (*connect.Response[gatewaypb.DeleteResponseResponse], error) {
	deleted := globalResponseStore.Delete(req.Msg.ResponseId)

	return connect.NewResponse(&gatewaypb.DeleteResponseResponse{
		Id:      req.Msg.ResponseId,
		Object:  "response.deleted",
		Deleted: deleted,
	}), nil
}

// ListResponses lists stored responses.
func (s *Server) ListResponses(ctx context.Context, req *connect.Request[gatewaypb.ListResponsesRequest]) (*connect.Response[gatewaypb.ListResponsesResponse], error) {
	limit := int(req.Msg.Limit)
	if limit == 0 {
		limit = 20
	}

	responses := globalResponseStore.List(limit, req.Msg.After, req.Msg.Before)

	protoResp := &gatewaypb.ListResponsesResponse{
		Object: "list",
	}

	for _, resp := range responses {
		protoResp.Data = append(protoResp.Data, convertResponseToProto(resp))
	}

	if len(protoResp.Data) > 0 {
		protoResp.FirstId = protoResp.Data[0].Id
		protoResp.LastId = protoResp.Data[len(protoResp.Data)-1].Id
	}
	protoResp.HasMore = false

	return connect.NewResponse(protoResp), nil
}

// Helper functions

func convertProtoToResponseRequest(msg *gatewaypb.CreateResponseRequest) gw.CreateResponseRequest {
	req := gw.CreateResponseRequest{
		Model:                 msg.Model,
		Instructions:          msg.Instructions,
		ParallelToolCalls:     msg.ParallelToolCalls,
		MaxOutputTokens:       int(msg.GetMaxOutputTokens()),
		MaxOutputConfigured:   msg.MaxOutputTokens != nil,
		Temperature:           float64(msg.GetTemperature()),
		TemperatureConfigured: msg.Temperature != nil,
		TopP:                  float64(msg.GetTopP()),
		TopPConfigured:        msg.TopP != nil,
		Store:                 msg.Store,
		PreviousResponseID:    msg.PreviousResponseId,
		Stream:                msg.Stream,
		Metadata:              msg.Metadata,
	}

	// Convert inputs
	for _, input := range msg.Input {
		gwInput := gw.ResponseInput{
			Type:   input.Type,
			Role:   convertProtoRole(input.Role),
			ItemID: input.ItemId,
		}
		for _, content := range input.Content {
			gwInput.Content = append(gwInput.Content, convertProtoContentPart(content))
		}
		req.Input = append(req.Input, gwInput)
	}

	// Convert tools
	for _, tool := range msg.Tools {
		req.Tools = append(req.Tools, gw.ToolDefinition{
			Type: tool.Type,
			Function: gw.ToolFunctionDef{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  protoStructToMap(tool.Function.Parameters),
			},
		})
	}

	// Convert builtin tools
	for _, bt := range msg.BuiltinTools {
		gwBT := gw.BuiltInToolConfig{
			Type: bt.Type,
		}
		if bt.WebSearch != nil {
			gwBT.WebSearch = &gw.WebSearchConfig{
				MaxResults:        int(bt.WebSearch.MaxResults),
				SearchContextSize: bt.WebSearch.SearchContextSize,
			}
			if bt.WebSearch.UserLocation != nil {
				gwBT.WebSearch.UserLocation = &gw.WebSearchLocation{
					Type:     bt.WebSearch.UserLocation.Type,
					City:     bt.WebSearch.UserLocation.City,
					Region:   bt.WebSearch.UserLocation.Region,
					Country:  bt.WebSearch.UserLocation.Country,
					Timezone: bt.WebSearch.UserLocation.Timezone,
				}
			}
		}
		if bt.FileSearch != nil {
			gwBT.FileSearch = &gw.FileSearchConfig{
				VectorStoreIDs: bt.FileSearch.VectorStoreIds,
				MaxNumResults:  int(bt.FileSearch.MaxNumResults),
			}
			if bt.FileSearch.RankingOptions != nil {
				gwBT.FileSearch.RankingOptions = &gw.FileSearchRankingOptions{
					Ranker:         bt.FileSearch.RankingOptions.Ranker,
					ScoreThreshold: float64(bt.FileSearch.RankingOptions.ScoreThreshold),
				}
			}
		}
		if bt.CodeInterpreter != nil && bt.CodeInterpreter.Container != nil {
			gwBT.CodeInterpreter = &gw.CodeInterpreterConfig{
				Container: &gw.CodeInterpreterContainer{
					Type:    bt.CodeInterpreter.Container.Type,
					FileIDs: bt.CodeInterpreter.Container.FileIds,
				},
			}
		}
		if bt.ComputerUse != nil {
			gwBT.ComputerUse = &gw.ComputerUseConfig{
				Environment:   bt.ComputerUse.Environment,
				DisplayWidth:  int(bt.ComputerUse.DisplayWidth),
				DisplayHeight: int(bt.ComputerUse.DisplayHeight),
			}
		}
		if bt.Mcp != nil {
			gwBT.MCP = &gw.McpServerConfig{
				URL:             bt.Mcp.Url,
				Name:            bt.Mcp.Name,
				RequireApproval: bt.Mcp.RequireApproval,
				AllowedTools:    bt.Mcp.AllowedTools,
				Headers:         bt.Mcp.Headers,
			}
			if bt.Mcp.ToolFilter != nil {
				gwBT.MCP.ToolFilter = &gw.McpToolFilter{
					Type:  bt.Mcp.ToolFilter.Type,
					Tools: bt.Mcp.ToolFilter.Tools,
				}
			}
		}
		req.BuiltinTools = append(req.BuiltinTools, gwBT)
	}

	// Convert text config
	if msg.Text != nil && msg.Text.Format != nil {
		req.Text = &gw.ResponseTextConfig{
			Format: &gw.ResponseFormatConfig{
				Type:       msg.Text.Format.Type,
				JSONSchema: protoStructToMap(msg.Text.Format.JsonSchema),
			},
		}
	}

	// Convert reasoning config
	if msg.Reasoning != nil {
		req.Reasoning = &gw.ReasoningConfig{
			Effort:          msg.Reasoning.Effort,
			GenerateSummary: msg.Reasoning.GenerateSummary,
		}
	}

	// Convert truncation
	if msg.Truncation != nil {
		req.Truncation = &gw.TruncationStrategy{
			Type:         msg.Truncation.Type,
			LastMessages: int(msg.Truncation.LastMessages),
		}
	}

	return req
}

func convertResponseToProto(resp *gw.CreateResponseResponse) *gatewaypb.CreateResponseResponse {
	protoResp := &gatewaypb.CreateResponseResponse{
		Id:                 resp.ID,
		Object:             resp.Object,
		CreatedAt:          resp.CreatedAt,
		Status:             resp.Status,
		Model:              resp.Model,
		Metadata:           resp.Metadata,
		Temperature:        float32(resp.Temperature),
		TopP:               float32(resp.TopP),
		MaxOutputTokens:    int32(resp.MaxOutputTokens),
		PreviousResponseId: resp.PreviousResponseID,
	}

	// Convert error
	if resp.Error != nil {
		protoResp.Error = &gatewaypb.ResponseError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	// Convert incomplete details
	if resp.IncompleteDetails != nil {
		protoResp.IncompleteDetails = &gatewaypb.IncompleteDetails{
			Reason: resp.IncompleteDetails.Reason,
		}
	}

	// Convert output items
	for _, item := range resp.Output {
		protoItem := &gatewaypb.ResponseOutputItem{
			Id:        item.ID,
			Type:      item.Type,
			Status:    item.Status,
			Role:      convertRoleToProto(item.Role),
			CallId:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Output:    item.Output,
		}
		for _, content := range item.Content {
			protoItem.Content = append(protoItem.Content, convertContentPartToProto(content))
		}
		if item.WebSearch != nil {
			protoItem.WebSearchResults = &gatewaypb.WebSearchResults{
				Query: item.WebSearch.Query,
			}
			for _, r := range item.WebSearch.Results {
				protoItem.WebSearchResults.Results = append(protoItem.WebSearchResults.Results, &gatewaypb.WebSearchResult{
					Title:   r.Title,
					Url:     r.URL,
					Snippet: r.Snippet,
				})
			}
		}
		if item.FileSearch != nil {
			protoItem.FileSearchResults = &gatewaypb.FileSearchResults{
				Query: item.FileSearch.Query,
			}
			for _, r := range item.FileSearch.Results {
				protoItem.FileSearchResults.Results = append(protoItem.FileSearchResults.Results, &gatewaypb.FileSearchResult{
					FileId:   r.FileID,
					FileName: r.FileName,
					Content:  r.Content,
					Score:    float32(r.Score),
				})
			}
		}
		protoResp.Output = append(protoResp.Output, protoItem)
	}

	// Convert usage
	if resp.Usage != nil {
		protoResp.Usage = &gatewaypb.ResponseUsage{
			InputTokens:  int32(resp.Usage.InputTokens),
			OutputTokens: int32(resp.Usage.OutputTokens),
			TotalTokens:  int32(resp.Usage.TotalTokens),
		}
		if resp.Usage.InputTokensDetails != nil {
			protoResp.Usage.InputTokensDetails = &gatewaypb.ResponseUsageDetails{
				CachedTokens:    int32(resp.Usage.InputTokensDetails.CachedTokens),
				TextTokens:      int32(resp.Usage.InputTokensDetails.TextTokens),
				AudioTokens:     int32(resp.Usage.InputTokensDetails.AudioTokens),
				ImageTokens:     int32(resp.Usage.InputTokensDetails.ImageTokens),
				ReasoningTokens: int32(resp.Usage.InputTokensDetails.ReasoningTokens),
			}
		}
		if resp.Usage.OutputTokensDetails != nil {
			protoResp.Usage.OutputTokensDetails = &gatewaypb.ResponseUsageDetails{
				CachedTokens:    int32(resp.Usage.OutputTokensDetails.CachedTokens),
				TextTokens:      int32(resp.Usage.OutputTokensDetails.TextTokens),
				AudioTokens:     int32(resp.Usage.OutputTokensDetails.AudioTokens),
				ImageTokens:     int32(resp.Usage.OutputTokensDetails.ImageTokens),
				ReasoningTokens: int32(resp.Usage.OutputTokensDetails.ReasoningTokens),
			}
		}
	}

	// Convert reasoning config
	if resp.Reasoning != nil {
		protoResp.Reasoning = &gatewaypb.ReasoningConfig{
			Effort:          resp.Reasoning.Effort,
			GenerateSummary: resp.Reasoning.GenerateSummary,
		}
	}

	// Convert truncation
	if resp.Truncation != nil {
		protoResp.Truncation = &gatewaypb.TruncationStrategy{
			Type:         resp.Truncation.Type,
			LastMessages: int32(resp.Truncation.LastMessages),
		}
	}

	return protoResp
}

func convertProtoRole(role gatewaypb.Role) gw.MessageRole {
	switch role {
	case gatewaypb.Role_ROLE_SYSTEM:
		return gw.RoleSystem
	case gatewaypb.Role_ROLE_USER:
		return gw.RoleUser
	case gatewaypb.Role_ROLE_ASSISTANT:
		return gw.RoleAssistant
	case gatewaypb.Role_ROLE_FUNCTION:
		return gw.RoleFunction
	case gatewaypb.Role_ROLE_TOOL:
		return gw.RoleTool
	default:
		return gw.RoleUser
	}
}

func convertRoleToProto(role gw.MessageRole) gatewaypb.Role {
	switch role {
	case gw.RoleSystem:
		return gatewaypb.Role_ROLE_SYSTEM
	case gw.RoleUser:
		return gatewaypb.Role_ROLE_USER
	case gw.RoleAssistant:
		return gatewaypb.Role_ROLE_ASSISTANT
	case gw.RoleFunction:
		return gatewaypb.Role_ROLE_FUNCTION
	case gw.RoleTool:
		return gatewaypb.Role_ROLE_TOOL
	default:
		return gatewaypb.Role_ROLE_UNSPECIFIED
	}
}

func convertProtoContentPart(cp *gatewaypb.ContentPart) gw.ContentPart {
	part := gw.ContentPart{
		Type: cp.Type,
	}
	switch data := cp.Data.(type) {
	case *gatewaypb.ContentPart_Text:
		part.Text = &data.Text
	case *gatewaypb.ContentPart_ImageUrl:
		part.ImageURL = &data.ImageUrl
	case *gatewaypb.ContentPart_FileId:
		part.FileID = &data.FileId
	case *gatewaypb.ContentPart_ToolCallId:
		part.ToolCallID = &data.ToolCallId
	}
	return part
}

func convertContentPartToProto(cp gw.ContentPart) *gatewaypb.ContentPart {
	protoCP := &gatewaypb.ContentPart{
		Type: cp.Type,
	}
	if cp.Text != nil {
		protoCP.Data = &gatewaypb.ContentPart_Text{Text: *cp.Text}
	} else if cp.ImageURL != nil {
		protoCP.Data = &gatewaypb.ContentPart_ImageUrl{ImageUrl: *cp.ImageURL}
	} else if cp.FileID != nil {
		protoCP.Data = &gatewaypb.ContentPart_FileId{FileId: *cp.FileID}
	} else if cp.ToolCallID != nil {
		protoCP.Data = &gatewaypb.ContentPart_ToolCallId{ToolCallId: *cp.ToolCallID}
	}
	return protoCP
}

var responseIDCounter int64
var itemIDCounter int64
var idMutex sync.Mutex

func generateResponseID() string {
	idMutex.Lock()
	defer idMutex.Unlock()
	responseIDCounter++
	return fmt.Sprintf("resp_%s_%d", time.Now().Format("20060102150405"), responseIDCounter)
}

func generateItemID() string {
	idMutex.Lock()
	defer idMutex.Unlock()
	itemIDCounter++
	return fmt.Sprintf("item_%s_%d", time.Now().Format("20060102150405"), itemIDCounter)
}

func stringPtr(s string) *string {
	return &s
}
