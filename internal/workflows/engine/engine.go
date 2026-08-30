package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// EngineDeps holds external dependencies needed by the engine.
type EngineDeps struct {
	Registry *gw.Registry
	Router   *gw.Router
	Ctx      context.Context // Server context with CQRS system
}

// Engine orchestrates the execution of a workflow DAG.
type Engine struct {
	deps      *EngineDeps
	executors map[string]NodeExecutor
}

// NewEngine creates a new workflow execution engine and registers all node executors.
func NewEngine(deps *EngineDeps) *Engine {
	e := &Engine{
		deps:      deps,
		executors: make(map[string]NodeExecutor),
	}
	return e
}

// RegisterExecutor registers a node executor for a given node type.
func (e *Engine) RegisterExecutor(executor NodeExecutor) {
	e.executors[executor.NodeType()] = executor
}

// Execute traverses the workflow DAG starting from the start node,
// executing each node and emitting events via the execution context's OnEvent callback.
func (e *Engine) Execute(ctx context.Context, graph *Graph, ec *ExecutionContext) error {
	if graph.StartID == "" {
		return fmt.Errorf("workflow has no start node")
	}

	// Standard cross-module session: a workflow's runs share one session (the
	// workflow definition is the logical group), so every invocation groups
	// together in the Sessions view. A caller-supplied session_id/user_id in
	// request metadata overrides. Stamped before the root span so the run span
	// and all node spans inherit trace.session_id / trace.user_id.
	wfSession := ec.WorkflowID
	if s := ec.Metadata["session_id"]; s != "" {
		wfSession = s
	}
	ctx = telemetry.WithSession(ctx, wfSession, ec.Metadata["user_id"])

	// Workflow-run root span (traces-module-replan M1-T1). Node spans nest under
	// it via runCtx; the per-node Ledger/NodeTimings already kept below now also
	// surface as a real trace tree.
	runCtx, runSpan := telemetry.StartWorkflowRunSpan(ctx, ec.WorkflowID, ec.ExecutionID, ec.TenantID)
	defer runSpan.End()
	ctx = runCtx

	emitEvent := func(evt ExecutionEvent) {
		evt.Timestamp = time.Now()
		if ec.OnEvent != nil {
			if err := ec.OnEvent(evt); err != nil {
				logger.WithFields("event_type", evt.Type, "node_id", evt.NodeID, "error", err.Error()).
					Warn("failed to emit execution event")
			}
		}
	}

	visited := make(map[string]bool)
	currentNode := graph.Nodes[graph.StartID]

	for currentNode != nil {
		select {
		case <-ctx.Done():
			emitEvent(ExecutionEvent{
				Type:  "error",
				Error: "execution cancelled",
			})
			return ctx.Err()
		default:
		}

		// Cycle detection
		if visited[currentNode.ID] {
			emitEvent(ExecutionEvent{
				Type:   "error",
				NodeID: currentNode.ID,
				Error:  fmt.Sprintf("cycle detected at node %s", currentNode.ID),
			})
			return fmt.Errorf("cycle detected at node %s", currentNode.ID)
		}
		visited[currentNode.ID] = true

		// Find executor for this node type
		executor, ok := e.executors[currentNode.Type]
		if !ok {
			emitEvent(ExecutionEvent{
				Type:      "node.error",
				NodeID:    currentNode.ID,
				NodeType:  currentNode.Type,
				NodeLabel: currentNode.Label,
				Error:     fmt.Sprintf("no executor for node type: %s", currentNode.Type),
			})
			return fmt.Errorf("no executor for node type: %s", currentNode.Type)
		}

		// Emit node.started
		emitEvent(ExecutionEvent{
			Type:      "node.started",
			NodeID:    currentNode.ID,
			NodeType:  currentNode.Type,
			NodeLabel: currentNode.Label,
		})

		// Execute the node (wrapped in a per-node span that nests under the run).
		nodeCtx, nodeSpan := telemetry.StartWorkflowNodeSpan(ctx, currentNode.ID, currentNode.Type, currentNode.Label)
		ec.ClearNodeData()
		nodeStart := time.Now()
		result := executor.Execute(nodeCtx, currentNode, ec)
		durationMs := time.Since(nodeStart).Milliseconds()
		ec.NodeTimings[currentNode.ID] = durationMs
		nodeSpan.SetAttributes(attribute.Int64(attrs.LatencyMs, durationMs))

		// Record node output in the execution ledger
		outputData := result.Output
		if outputData == nil {
			outputData = make(map[string]interface{})
		}
		outputStatus := "success"
		if result.Error != nil {
			outputStatus = "error"
		}
		ec.Ledger.Record(&NodeOutput{
			NodeID:     currentNode.ID,
			NodeType:   currentNode.Type,
			NodeLabel:  currentNode.Label,
			Status:     outputStatus,
			Handle:     result.NextHandle,
			Data:       outputData,
			StartedAt:  nodeStart,
			DurationMs: durationMs,
		})

		if result.Error != nil {
			nodeData := copyMap(ec.NodeData)
			emitEvent(ExecutionEvent{
				Type:       "node.error",
				NodeID:     currentNode.ID,
				NodeType:   currentNode.Type,
				NodeLabel:  currentNode.Label,
				Error:      result.Error.Error(),
				DurationMs: durationMs,
				Data:       nodeData,
			})

			logger.WithCategory(logger.CategoryOperational).
				WithLogEvent(logger.EventWorkflowNodeError).
				WithPayload(
					logger.NewPayload().
						WithWorkflowExecution(ec.ExecutionID, ec.WorkflowID, ec.CorrelationID, ec.TenantID).
						WithWorkflowNode(currentNode.ID, currentNode.Type, currentNode.Label, durationMs, nodeData).
						Build(),
				).
				Info("workflow node error")

			telemetry.RecordError(nodeSpan, result.Error)
			nodeSpan.End()
			return fmt.Errorf("node %s (%s) failed: %w", currentNode.ID, currentNode.Type, result.Error)
		}

		// Emit node.completed
		nodeData := copyMap(ec.NodeData)
		emitEvent(ExecutionEvent{
			Type:       "node.completed",
			NodeID:     currentNode.ID,
			NodeType:   currentNode.Type,
			NodeLabel:  currentNode.Label,
			DurationMs: durationMs,
			Data:       nodeData,
		})

		logger.WithCategory(logger.CategoryOperational).
			WithLogEvent(logger.EventWorkflowNodeCompleted).
			WithPayload(
				logger.NewPayload().
					WithWorkflowExecution(ec.ExecutionID, ec.WorkflowID, ec.CorrelationID, ec.TenantID).
					WithWorkflowNode(currentNode.ID, currentNode.Type, currentNode.Label, durationMs, nodeData).
					Build(),
			).
			Info("workflow node completed")

		nodeSpan.End()

		// After a provider node completes, async-write to cache if there was a cache miss
		if (currentNode.Type == "provider" || currentNode.Type == "agent") && ec.CacheMiss && ec.Response != nil {
			e.asyncCacheWrite(ctx, ec)
			ec.CacheMiss = false // Prevent duplicate writes
		}

		// Response node is terminal
		if currentNode.Type == "response" {
			break
		}

		// Resolve next node based on the result handle
		nextNode := graph.ResolveNextNode(currentNode.ID, result.NextHandle)
		if nextNode == nil {
			// No outgoing edge for this handle -- end of path
			logger.WithFields(
				"node_id", currentNode.ID,
				"handle", result.NextHandle,
			).Debug("no next node found, ending execution")
			break
		}

		currentNode = nextNode
	}

	// Extract response content for the done event and fallback chunk emission.
	var responseContent string
	if ec.Response != nil && len(ec.Response.Choices) > 0 {
		msg := ec.Response.Choices[0].Message
		if len(msg.Content) > 0 && msg.Content[0].Text != nil {
			responseContent = *msg.Content[0].Text
		}
	}

	// If streaming was not used (non-streaming path), the response content was
	// never sent as chunk events. Emit it now so the frontend always receives it.
	if !ec.StreamingEnabled && responseContent != "" {
		emitEvent(ExecutionEvent{
			Type:         "chunk",
			ChunkContent: responseContent,
		})
	}

	// Emit done event
	totalDuration := time.Since(ec.StartTime).Milliseconds()
	doneData := map[string]string{
		"total_duration_ms": fmt.Sprintf("%d", totalDuration),
	}
	if ec.Response != nil {
		doneData["total_tokens"] = fmt.Sprintf("%d", ec.Response.Usage.TotalTokens)
		doneData["prompt_tokens"] = fmt.Sprintf("%d", ec.Response.Usage.PromptTokens)
		doneData["completion_tokens"] = fmt.Sprintf("%d", ec.Response.Usage.CompletionTokens)
	}
	// Always include response content in done data as a fallback for the frontend.
	if responseContent != "" {
		doneData["response_content"] = responseContent
	}

	emitEvent(ExecutionEvent{
		Type:       "done",
		Data:       doneData,
		DurationMs: totalDuration,
	})

	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventWorkflowExecutionDone).
		WithPayload(
			logger.NewPayload().
				WithWorkflowExecution(ec.ExecutionID, ec.WorkflowID, ec.CorrelationID, ec.TenantID).
				Build(),
		).
		Info("workflow execution done")

	return nil
}

// asyncCacheWrite stores the provider response in the cache after a cache miss.
// It runs asynchronously so the workflow execution is not blocked.
func (e *Engine) asyncCacheWrite(ctx context.Context, ec *ExecutionContext) {
	fpEngine := fastpath.GetGlobalEngine()
	if fpEngine == nil || !fpEngine.IsEnabled() {
		return
	}

	respBytes, err := json.Marshal(ec.Response)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("engine: failed to marshal response for cache write")
		return
	}

	cached := &cache.CachedResponse{
		Response:     respBytes,
		CreatedAt:    time.Now(),
		Model:        ec.Response.Model,
		InputTokens:  ec.Response.Usage.PromptTokens,
		OutputTokens: ec.Response.Usage.CompletionTokens,
	}

	cacheType := ec.CacheType
	cacheQuery := ec.CacheQuery

	go func() {
		// Use a detached context so the write completes even if the request ctx is cancelled
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		switch cacheType {
		case "exact":
			req := &exactCacheWriteRequest{
				model:       ec.ResolvedModel,
				messages:    ec.Messages,
				temperature: ec.SamplingParams.Temperature,
				maxTokens:   ec.SamplingParams.MaxTokens,
				topP:        ec.SamplingParams.TopP,
				stream:      ec.StreamingEnabled,
			}
			fpEngine.CacheResponseWithContext(writeCtx, req, cached)
			logger.Debug("engine: async exact cache write completed")
		case "semantic":
			if cacheQuery != "" {
				fpEngine.CacheSemanticResponseWithContext(writeCtx, cacheQuery, cached)
				logger.Debug("engine: async semantic cache write completed")
			}
		}
	}()
}

// exactCacheWriteRequest implements cache.ChatRequest for the async cache write.
// It captures values by copy so the goroutine doesn't race on the ExecutionContext.
type exactCacheWriteRequest struct {
	model       string
	messages    []gw.Message
	temperature float64
	maxTokens   int
	topP        float64
	stream      bool
}

func (r *exactCacheWriteRequest) GetModel() string        { return r.model }
func (r *exactCacheWriteRequest) GetTemperature() float64 { return r.temperature }
func (r *exactCacheWriteRequest) GetMaxTokens() int       { return r.maxTokens }
func (r *exactCacheWriteRequest) GetTopP() float64        { return r.topP }
func (r *exactCacheWriteRequest) GetStream() bool         { return r.stream }

func (r *exactCacheWriteRequest) GetMessages() []cache.Message {
	msgs := make([]cache.Message, 0, len(r.messages))
	for i := range r.messages {
		msgs = append(msgs, &engineMessageAdapter{msg: &r.messages[i]})
	}
	return msgs
}

// engineMessageAdapter implements cache.Message by wrapping a gw.Message.
type engineMessageAdapter struct {
	msg *gw.Message
}

func (m *engineMessageAdapter) GetRole() string { return string(m.msg.Role) }

func (m *engineMessageAdapter) GetContent() string {
	if len(m.msg.Content) > 0 && m.msg.Content[0].Text != nil {
		return *m.msg.Content[0].Text
	}
	return ""
}

// copyMap returns a shallow copy of a string map to avoid aliasing.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
