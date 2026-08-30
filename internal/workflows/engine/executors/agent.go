package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

// AgentExecutor handles agent loop execution within a workflow.
//
// Config fields (from frontend AgentNodeConfig):
//   - agentId: UUID of a persisted agent definition (mutually exclusive with inlineAgent)
//   - inlineAgent: inline agent definition object with name, model, systemPrompt, tools, temperature, maxTokens
//   - maxIterations: max LLM->tool cycles per turn (default: 25)
//   - maxToolCallsPerTurn: max tool calls across all iterations (default: 10)
//   - turnTimeout: per-turn timeout as a duration string, e.g. "5m" (default: "5m")
//   - contextMode: "inherit" (default) | "isolated" | "custom"
//
// Handles: "out" on success. Returns an error on execution failure.
type AgentExecutor struct {
	ServerCtx context.Context       // Server context with CQRS system
	Registry  *gw.Registry          // Provider registry
	Router    *gw.Router            // Model router
	ToolLoop  *toolloop.LoopManager // Tool loop manager

	// Memory (RAG) backend — optional, set when memory feature is enabled.
	MemoryStore    memory.VectorStore
	MemoryEmbedder memory.EmbedderInterface
	MemoryModel    string
	MemoryDim      int

	// Agent synthetic-tool dependencies. BrowserPool is the preferred hosted
	// runtime; SandboxManager remains available for sandbox tools and the local
	// browser-sidecar fallback.
	SandboxManager *sandbox.SandboxManager
	BrowserPool    *browserpool.Pool
	StorageServer  *storagesvc.Server
}

func (e *AgentExecutor) NodeType() string { return "agent" }

func (e *AgentExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	tenantID := contextkeys.GetTenantID(ctx)
	spanCtx, span := telemetry.StartGatewaySpan(ctx, "studio.agent.execute", telemetry.WithTenantID(tenantID))
	ctx = spanCtx
	startTime := time.Now()
	var execErr error
	defer func() {
		span.SetAttributes(attribute.Int64(attrs.LatencyMs, time.Since(startTime).Milliseconds()))
		if execErr != nil {
			telemetry.RecordError(span, execErr)
		}
		span.End()
	}()
	span.SetAttributes(
		attribute.String(attrs.NodeName, node.ID),
		attribute.String("workflow.execution.id", ec.ExecutionID),
		attribute.String("workflow.node.type", node.Type),
	)
	telemetry.AddSpanEvent(span, attrs.EventRequestReceived)

	// ---------------------------------------------------------------
	// 1. Load agent configuration (DB or inline)
	// ---------------------------------------------------------------
	agentDef, err := e.resolveAgentConfig(ctx, node)
	if err != nil {
		execErr = fmt.Errorf("agent executor: %w", err)
		return engine.NodeResult{Error: fmt.Errorf("agent executor: %w", err)}
	}
	span.SetAttributes(
		attribute.String(attrs.AgentID, agentDef.ID),
		attribute.String(attrs.AgentName, agentDef.Name),
		attribute.String(attrs.AgentModel, agentDef.Model),
		attribute.Int(attrs.AgentToolsCount, len(agentDef.Tools)),
	)

	// ---------------------------------------------------------------
	// 2. Build LoopConfig from node config + agent defaults
	// ---------------------------------------------------------------
	loopCfg := agentrt.DefaultLoopConfig()
	loopCfg.EnableStreaming = ec.StreamingEnabled

	if maxIter := node.GetConfigInt("maxIterations"); maxIter > 0 {
		loopCfg.MaxIterations = int32(maxIter)
	} else if agentDef.MaxTurns > 0 {
		loopCfg.MaxIterations = agentDef.MaxTurns
	}

	if maxTC := node.GetConfigInt("maxToolCallsPerTurn"); maxTC > 0 {
		loopCfg.MaxToolCallsPerTurn = int32(maxTC)
	} else if agentDef.MaxToolCallsPerTurn > 0 {
		loopCfg.MaxToolCallsPerTurn = agentDef.MaxToolCallsPerTurn
	}

	if timeoutStr := node.GetConfigString("turnTimeout"); timeoutStr != "" {
		if d, parseErr := time.ParseDuration(timeoutStr); parseErr == nil && d > 0 {
			loopCfg.TurnTimeout = d
		}
	}

	// ---------------------------------------------------------------
	// 3. Resolve system prompt template
	// ---------------------------------------------------------------
	systemPrompt := agentDef.SystemPrompt
	if systemPrompt != "" && ec.Ledger != nil {
		systemPrompt = ec.Ledger.InterpolateTemplate(systemPrompt, ec)
	}

	// ---------------------------------------------------------------
	// 4. Build sampling params: base from ec, override with agent/node config
	// ---------------------------------------------------------------
	sampling := ec.SamplingParams
	if agentDef.Temperature > 0 {
		sampling.Temperature = agentDef.Temperature
	}
	if agentDef.MaxTokens > 0 {
		sampling.MaxTokens = agentDef.MaxTokens
	}
	// Node-level overrides take precedence
	if temp := node.GetConfigFloat("temperature"); temp > 0 {
		sampling.Temperature = temp
	}
	if mt := node.GetConfigInt("maxTokens"); mt > 0 {
		sampling.MaxTokens = mt
	}

	// ---------------------------------------------------------------
	// 5. Extract user input from messages
	// ---------------------------------------------------------------
	userInput := extractLastUserInput(ec)

	// ---------------------------------------------------------------
	// 5b. Memory pre-query: inject relevant context from vector memory
	// ---------------------------------------------------------------
	memoryEnabled := node.GetConfigBool("memoryEnabled")
	var memoryResultsCount int
	span.SetAttributes(attribute.Bool("agent.memory.enabled", memoryEnabled))
	telemetry.AddSpanEvent(span, "agent.memory.pre_query.start")
	if memoryEnabled && e.MemoryStore != nil && e.MemoryEmbedder != nil && userInput != "" {
		memCollection := node.GetConfigString("memoryCollection")
		if memCollection == "" {
			memCollection = "default"
		}
		memTopK := node.GetConfigInt("memoryTopK")
		if memTopK <= 0 {
			memTopK = 5
		}
		memMinScore := float32(node.GetConfigFloat("memoryMinScore"))
		span.SetAttributes(
			attribute.String("agent.memory.collection", memCollection),
			attribute.Int("agent.memory.top_k", memTopK),
		)

		embModel := e.MemoryModel
		coll, collErr := e.MemoryStore.GetCollection(ctx, tenantID, memCollection)
		if collErr == nil && coll != nil {
			if coll.EmbeddingModel != "" {
				embModel = coll.EmbeddingModel
			}
			queryEmb, embErr := e.MemoryEmbedder.Embed(ctx, embModel, userInput)
			if embErr == nil {
				results, qErr := e.MemoryStore.Query(ctx, coll.ID, queryEmb, memory.QueryOptions{
					TopK:     memTopK,
					MinScore: memMinScore,
				})
				if qErr == nil && len(results) > 0 {
					memoryResultsCount = len(results)
					var memCtx strings.Builder
					memCtx.WriteString("Relevant context from memory:\n\n")
					for i, r := range results {
						memCtx.WriteString(fmt.Sprintf("[%d] (score: %.3f) %s\n", i+1, r.Score, r.ChunkText))
					}
					memMsg := gw.Message{
						Role: gw.RoleSystem,
						Content: []gw.ContentPart{
							{Type: "text", Text: strPtr(memCtx.String())},
						},
					}
					// Prepend memory context to ec.Messages for downstream injection
					ec.Messages = append([]gw.Message{memMsg}, ec.Messages...)

					logger.WithFields(
						"collection", memCollection,
						"results", len(results),
					).Debug("agent executor: injected memory context")
					telemetry.AddSpanEvent(span, "agent.memory.pre_query.results", attribute.Int("result_count", len(results)))
				}
			} else {
				logger.WithFields("error", embErr.Error()).
					Warn("agent executor: memory pre-query embedding failed")
				telemetry.AddSpanEvent(span, "agent.memory.pre_query.error", attribute.String("error", embErr.Error()))
			}
		}
	}
	span.SetAttributes(attribute.Int("agent.memory.pre_query.result_count", memoryResultsCount))

	// ---------------------------------------------------------------
	// 6. Build conversation state based on contextMode
	// ---------------------------------------------------------------
	contextMode := node.GetConfigString("contextMode")
	if contextMode == "" {
		contextMode = "inherit"
	}

	state := &agentrt.LoopState{}

	switch contextMode {
	case "isolated":
		// Agent gets only its system prompt; user input is appended by Loop.Run.
		// No inherited conversation history.

	case "custom":
		// System prompt template has already been resolved via InterpolateTemplate.
		// No conversation messages are inherited.

	default: // "inherit"
		// Carry over the full conversation from the workflow execution context.
		if len(ec.Messages) > 0 {
			msgs := make([]gw.Message, len(ec.Messages))
			copy(msgs, ec.Messages)
			state.Messages = msgs
		}
		if systemPrompt != "" {
			state.Messages = ensureSystemPrompt(state.Messages, systemPrompt)
		}

		// Inject upstream data-producing node outputs (same pattern as provider executor).
		if ec.Ledger != nil {
			dataOutputs := ec.Ledger.DataOutputsSinceLastProvider()
			if len(dataOutputs) > 0 {
				contextText := engine.FormatNodeOutputsAsContext(dataOutputs)
				if contextText != "" {
					ctxMsg := gw.Message{
						Role: gw.RoleSystem,
						Content: []gw.ContentPart{
							{Type: "text", Text: strPtr(contextText)},
						},
					}
					// Insert just before the last user message
					inserted := false
					for i := len(state.Messages) - 1; i >= 0; i-- {
						if state.Messages[i].Role == gw.RoleUser {
							state.Messages = append(state.Messages[:i], append([]gw.Message{ctxMsg}, state.Messages[i:]...)...)
							inserted = true
							break
						}
					}
					if !inserted {
						state.Messages = append(state.Messages, ctxMsg)
					}
					logger.WithFields("data_outputs", len(dataOutputs)).
						Debug("agent executor: injected upstream node context into messages")
				}
			}
		}
	}

	// ---------------------------------------------------------------
	// 7. Build LoopInput
	// ---------------------------------------------------------------
	sessionID := fmt.Sprintf("wfx_%s_%s", ec.ExecutionID, node.ID)

	loopInput := &agentrt.LoopInput{
		TenantID:     tenantID,
		AgentID:      agentDef.ID,
		SessionID:    sessionID,
		Model:        agentDef.Model,
		SystemPrompt: systemPrompt,
		Tools:        agentDef.Tools,
		Sampling:     sampling,
		UserInput:    userInput,
	}

	// For inherit mode, Loop.Run appends user input on its own, and only
	// prepends system prompt when state.Messages is empty. Since we already
	// copied ec.Messages (which may include a system prompt), clear
	// SystemPrompt from LoopInput when messages already contain one.
	if contextMode == "inherit" && len(state.Messages) > 0 {
		if state.Messages[0].Role == gw.RoleSystem {
			// System prompt already present in conversation; let the agent's
			// system prompt be injected as an additional system message only
			// if it differs from what is already there.
			// Loop.Run checks len(state.Messages) == 0 before adding system
			// prompt, so it won't duplicate.
		}
	}

	// ---------------------------------------------------------------
	// 8. Create Emitter with event sink adapter bridging to ec.OnEvent
	// ---------------------------------------------------------------
	emitter := agentrt.NewEmitter()
	defer emitter.Close()

	if ec.OnEvent != nil {
		adapter := &AgentEventSinkAdapter{
			ctx:         ctx,
			tenantID:    tenantID,
			executionID: ec.ExecutionID,
			nodeID:      node.ID,
			nodeType:    node.Type,
			nodeLabel:   node.Label,
			onEvent:     ec.OnEvent,
			artifacts:   e.StorageServer,
		}
		emitter.AddSink(adapter)
	}

	// Workflow agent nodes use the same synthetic browser/sandbox tools as
	// normal agent sessions. Previously the allowlist was passed to LoopInput
	// without an interceptor, so browser_* calls could be proposed by the model
	// but never executed.
	browserCtx := e.attachSyntheticTools(ctx, agentDef, loopInput, sessionID, tenantID, emitter, startTime)
	if browserCtx != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			browserCtx.Close(closeCtx)
		}()
	}

	// ---------------------------------------------------------------
	// 9. Create runtime Engine and Loop, then run synchronously
	// ---------------------------------------------------------------
	rtEngine := agentrt.NewEngine(e.Registry, e.Router, e.ToolLoop)
	loop := agentrt.NewLoop(rtEngine, e.ToolLoop, emitter, loopCfg)

	logger.WithFields(
		"agent_id", agentDef.ID,
		"agent_name", agentDef.Name,
		"model", agentDef.Model,
		"context_mode", contextMode,
		"tools", len(agentDef.Tools),
		"max_iterations", loopCfg.MaxIterations,
		"session_id", sessionID,
		"streaming", loopCfg.EnableStreaming,
	).Debug("agent executor: starting agent loop")
	telemetry.AddSpanEvent(span, attrs.EventAgentSessionStart,
		attribute.String(attrs.AgentSessionID, sessionID),
		attribute.String("agent.context_mode", contextMode),
		attribute.Bool("agent.streaming", loopCfg.EnableStreaming),
	)

	ec.SetNodeData("agent_id", agentDef.ID)
	ec.SetNodeData("agent_name", agentDef.Name)
	ec.SetNodeData("model", agentDef.Model)
	ec.SetNodeData("context_mode", contextMode)

	finalState, runErr := loop.Run(ctx, state, loopInput)
	if runErr != nil {
		logger.WithFields(
			"agent_id", agentDef.ID,
			"session_id", sessionID,
			"error", runErr.Error(),
		).Error("agent executor: loop failed")
		execErr = fmt.Errorf("agent loop failed: %w", runErr)
		return engine.NodeResult{Error: fmt.Errorf("agent loop failed: %w", runErr)}
	}
	attrs.SetAgentTurnMetrics(
		span,
		int(finalState.IterationCount),
		int(finalState.TotalToolCalls),
		int64(finalState.CumulativeUsage.PromptTokens),
		int64(finalState.CumulativeUsage.CompletionTokens),
		int64(finalState.CumulativeUsage.TotalTokens),
		0,
	)
	span.SetAttributes(
		attribute.String(attrs.AgentFinishReason, finalState.FinishReason),
		attribute.Int(attrs.AgentTotalToolCalls, int(finalState.TotalToolCalls)),
		attribute.Int(attrs.AgentTotalTokens, int(finalState.CumulativeUsage.TotalTokens)),
	)
	telemetry.AddSpanEvent(span, attrs.EventAgentSessionEnd, attribute.String(attrs.AgentFinishReason, finalState.FinishReason))

	// ---------------------------------------------------------------
	// 10. Convert final LoopState to ChatCompletionResponse
	// ---------------------------------------------------------------
	resp := buildAgentResponse(agentDef.Model, finalState)
	ec.Response = resp

	// Append assistant message to ec.Messages so downstream nodes see this output.
	if resp != nil && len(resp.Choices) > 0 {
		ec.Messages = append(ec.Messages, resp.Choices[0].Message)
	}

	// Populate node data for the execution event
	ec.SetNodeData("finish_reason", finalState.FinishReason)
	ec.SetNodeData("iterations", fmt.Sprintf("%d", finalState.IterationCount))
	ec.SetNodeData("tool_calls", fmt.Sprintf("%d", finalState.TotalToolCalls))
	if resp != nil {
		ec.SetNodeData("prompt_tokens", fmt.Sprintf("%d", resp.Usage.PromptTokens))
		ec.SetNodeData("completion_tokens", fmt.Sprintf("%d", resp.Usage.CompletionTokens))
		ec.SetNodeData("total_tokens", fmt.Sprintf("%d", resp.Usage.TotalTokens))
	}

	// Output preview
	if finalState.LastAssistantText != "" {
		preview := finalState.LastAssistantText
		if len(preview) > 200 {
			preview = preview[:200]
		}
		ec.SetNodeData("output_preview", preview)
	}

	// ---------------------------------------------------------------
	// 11. Store structured output in Variables and return
	// ---------------------------------------------------------------
	ec.SetVariable("agent_output", finalState.LastAssistantText)
	ec.SetVariable("agent_id", agentDef.ID)
	ec.SetVariable("agent_name", agentDef.Name)
	ec.SetVariable("agent_iterations", finalState.IterationCount)
	ec.SetVariable("agent_tool_calls", finalState.TotalToolCalls)
	ec.SetVariable("agent_finish_reason", finalState.FinishReason)

	output := map[string]interface{}{
		"agent_id":      agentDef.ID,
		"agent_name":    agentDef.Name,
		"model":         agentDef.Model,
		"content":       finalState.LastAssistantText,
		"finish_reason": finalState.FinishReason,
		"iterations":    finalState.IterationCount,
		"tool_calls":    finalState.TotalToolCalls,
		"usage": map[string]interface{}{
			"prompt_tokens":     finalState.CumulativeUsage.PromptTokens,
			"completion_tokens": finalState.CumulativeUsage.CompletionTokens,
			"total_tokens":      finalState.CumulativeUsage.TotalTokens,
		},
	}

	if memoryEnabled {
		ec.SetNodeData("memory_results", fmt.Sprintf("%d", memoryResultsCount))
	}

	logger.WithFields(
		"agent_id", agentDef.ID,
		"session_id", sessionID,
		"iterations", finalState.IterationCount,
		"tool_calls", finalState.TotalToolCalls,
		"finish_reason", finalState.FinishReason,
		"total_tokens", finalState.CumulativeUsage.TotalTokens,
	).Debug("agent executor: loop completed")

	// ---------------------------------------------------------------
	// 12. Memory post-store: asynchronously store the response
	// ---------------------------------------------------------------
	memoryStoreResponses := node.GetConfigBool("memoryStoreResponses")
	span.SetAttributes(attribute.Bool("agent.memory.store_responses", memoryStoreResponses))
	if memoryEnabled && memoryStoreResponses && e.MemoryStore != nil && e.MemoryEmbedder != nil && finalState.LastAssistantText != "" {
		memCollection := node.GetConfigString("memoryCollection")
		if memCollection == "" {
			memCollection = "default"
		}
		responseText := finalState.LastAssistantText
		telemetry.AddSpanEvent(span, "agent.memory.post_store.queued", attribute.String("collection", memCollection))
		storeCtx := context.WithoutCancel(ctx)
		go func() {
			embModel := e.MemoryModel
			coll, err := e.MemoryStore.GetCollection(storeCtx, tenantID, memCollection)
			if err != nil || coll == nil {
				coll, err = e.MemoryStore.CreateCollection(storeCtx, tenantID, memory.CollectionOptions{
					Name:               memCollection,
					EmbeddingModel:     embModel,
					EmbeddingDimension: e.MemoryDim,
					DistanceMetric:     memory.DistanceCosine,
				})
				if err != nil {
					logger.WithFields("error", err.Error()).
						Warn("agent executor: memory post-store create collection failed")
					return
				}
			}
			if coll.EmbeddingModel != "" {
				embModel = coll.EmbeddingModel
			}

			chunks := memory.ChunkText(responseText, 512)
			embeddings, err := e.MemoryEmbedder.EmbedBatch(storeCtx, embModel, chunks)
			if err != nil {
				logger.WithFields("error", err.Error()).
					Warn("agent executor: memory post-store embedding failed")
				return
			}

			memChunks := make([]memory.Chunk, len(chunks))
			for i, chunkText := range chunks {
				memChunks[i] = memory.Chunk{
					Text:       chunkText,
					ChunkIndex: i,
					Embedding:  embeddings[i],
					Metadata: map[string]string{
						"source":     "agent_response",
						"agent_id":   agentDef.ID,
						"agent_name": agentDef.Name,
					},
				}
			}

			if err := e.MemoryStore.Store(storeCtx, coll.ID, memChunks); err != nil {
				logger.WithFields("error", err.Error()).
					Warn("agent executor: memory post-store failed")
				return
			}

			logger.WithFields(
				"collection", memCollection,
				"chunks", len(memChunks),
			).Debug("agent executor: stored response in memory")
		}()
	}
	telemetry.AddSpanEvent(span, attrs.EventRequestComplete)

	return engine.NodeResult{NextHandle: "out", Output: output}
}

// ---------------------------------------------------------------
// Internal: agent config resolution
// ---------------------------------------------------------------

// agentConfig is a normalized representation of the agent configuration,
// regardless of whether it came from the database or an inline definition.
type agentConfig struct {
	ID                  string
	Name                string
	Model               string
	SystemPrompt        string
	Tools               []string
	Temperature         float64
	MaxTokens           int
	MaxTurns            int32
	MaxToolCallsPerTurn int32
	RuntimeConfig       map[string]interface{}
}

// resolveAgentConfig loads the agent configuration from either a persisted
// agent definition (via CQRS) or from an inline definition in the node config.
func (e *AgentExecutor) resolveAgentConfig(ctx context.Context, node *engine.GraphNode) (*agentConfig, error) {
	agentID := node.GetConfigString("agentId")
	useInline := node.GetConfigBool("useInline")

	// Studio defaults include an inlineAgent object even for persisted-agent
	// nodes. Only execute it when inline mode is selected. The agentID fallback
	// preserves older inline-only saved workflows.
	if node.Config != nil && (useInline || agentID == "") {
		if inlineRaw, ok := node.Config["inlineAgent"]; ok && inlineRaw != nil {
			return e.parseInlineAgent(inlineRaw)
		}
	}

	// Load from DB via agent ID
	if agentID == "" {
		return nil, fmt.Errorf("no agentId or inlineAgent configured")
	}

	return e.loadAgentFromDB(ctx, agentID)
}

// parseInlineAgent extracts an agent configuration from the inline definition.
func (e *AgentExecutor) parseInlineAgent(raw interface{}) (*agentConfig, error) {
	inlineMap, ok := raw.(map[string]interface{})
	if !ok {
		// Try JSON round-trip for nested structures
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid inlineAgent: %w", err)
		}
		if err := json.Unmarshal(data, &inlineMap); err != nil {
			return nil, fmt.Errorf("invalid inlineAgent: %w", err)
		}
	}

	cfg := &agentConfig{
		ID:            fmt.Sprintf("inline_%s", uuid.New().String()[:8]),
		Name:          "inline-agent",
		RuntimeConfig: inlineMap,
	}

	if name, ok := inlineMap["name"].(string); ok && name != "" {
		cfg.Name = name
	}
	if model, ok := inlineMap["model"].(string); ok && model != "" {
		cfg.Model = model
	}
	if sp, ok := inlineMap["systemPrompt"].(string); ok {
		cfg.SystemPrompt = sp
	}
	if temp, ok := inlineMap["temperature"].(float64); ok {
		cfg.Temperature = temp
	}
	if mt, ok := inlineMap["maxTokens"].(float64); ok {
		cfg.MaxTokens = int(mt)
	}

	// Parse tools array
	if toolsRaw, ok := inlineMap["tools"].([]interface{}); ok {
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				cfg.Tools = append(cfg.Tools, ts)
			}
		}
	}

	if cfg.Model == "" {
		return nil, fmt.Errorf("inlineAgent requires a model")
	}

	return cfg, nil
}

// loadAgentFromDB retrieves the agent definition via the CQRS query bus.
func (e *AgentExecutor) loadAgentFromDB(ctx context.Context, agentID string) (*agentConfig, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && e.ServerCtx != nil {
		sys, err = cqrs.GetSystemFromContext(e.ServerCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	tenantID := contextkeys.GetTenantID(ctx)

	q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent %s: %w", agentID, err)
	}
	if res == nil {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	agentDef, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		return nil, fmt.Errorf("unexpected data type for agent %s", agentID)
	}

	if !agentDef.Enabled {
		return nil, fmt.Errorf("agent %s is disabled", agentID)
	}

	cfg := &agentConfig{
		ID:                  agentDef.ID,
		Name:                agentDef.Name,
		Model:               agentDef.Model,
		Tools:               agentDef.Tools,
		MaxTurns:            agentDef.MaxTurns,
		MaxToolCallsPerTurn: agentDef.MaxToolCallsPerTurn,
	}

	if agentDef.SystemPrompt.Valid {
		cfg.SystemPrompt = agentDef.SystemPrompt.String
	}
	if agentDef.Description.Valid {
		// Description is informational, not used for execution
	}

	// Parse temperature and max_tokens from the config JSONB column
	if len(agentDef.Config) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(agentDef.Config, &configMap); err == nil {
			cfg.RuntimeConfig = configMap
			if temp, ok := configMap["temperature"].(float64); ok {
				cfg.Temperature = temp
			}
			if mt, ok := configMap["max_tokens"].(float64); ok {
				cfg.MaxTokens = int(mt)
			}
		}
	}

	return cfg, nil
}

// ---------------------------------------------------------------
// Internal: response building
// ---------------------------------------------------------------

// buildAgentResponse converts a LoopState into a ChatCompletionResponse.
func buildAgentResponse(model string, state *agentrt.LoopState) *gw.ChatCompletionResponse {
	if state == nil {
		return nil
	}

	content := state.LastAssistantText
	resp := &gw.ChatCompletionResponse{
		ID:    fmt.Sprintf("agent-%s", uuid.New().String()[:8]),
		Model: model,
		Usage: gw.Usage{
			PromptTokens:     state.CumulativeUsage.PromptTokens,
			CompletionTokens: state.CumulativeUsage.CompletionTokens,
			TotalTokens:      state.CumulativeUsage.TotalTokens,
		},
		Choices: []gw.Choice{
			{
				Index: 0,
				Message: gw.Message{
					Role: gw.RoleAssistant,
					Content: []gw.ContentPart{
						{Type: "text", Text: strPtr(content)},
					},
				},
				FinishReason: state.FinishReason,
			},
		},
	}

	return resp
}

// extractLastUserInput returns the text of the last user message in the
// execution context messages. Falls back to OriginalUserInput().
func extractLastUserInput(ec *engine.ExecutionContext) string {
	for i := len(ec.Messages) - 1; i >= 0; i-- {
		if ec.Messages[i].Role == gw.RoleUser && len(ec.Messages[i].Content) > 0 {
			if ec.Messages[i].Content[0].Text != nil {
				return *ec.Messages[i].Content[0].Text
			}
		}
	}
	return ec.OriginalUserInput()
}

// strPtr returns a pointer to a string. This is a local alias; the
// identical helper also exists in the runtime package.
func strPtr(s string) *string {
	return &s
}

func ensureSystemPrompt(messages []gw.Message, systemPrompt string) []gw.Message {
	if systemPrompt == "" {
		return messages
	}
	for _, msg := range messages {
		if msg.Role != gw.RoleSystem {
			continue
		}
		if len(msg.Content) > 0 && msg.Content[0].Text != nil && *msg.Content[0].Text == systemPrompt {
			return messages
		}
	}

	systemMsg := gw.Message{
		Role: gw.RoleSystem,
		Content: []gw.ContentPart{
			{Type: "text", Text: strPtr(systemPrompt)},
		},
	}
	return append([]gw.Message{systemMsg}, messages...)
}
