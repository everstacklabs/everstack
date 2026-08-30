package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// ParallelTasksHandler implements the parallel_tasks synthetic tool.
// It provides structured fan-out/fan-in with a barrier: multiple child loops
// run concurrently, and the parent blocks until all complete or timeout.
type ParallelTasksHandler struct {
	ServerCtx   context.Context
	Registry    *gw.Registry
	Router      *gw.Router
	ToolLoop    *toolloop.LoopManager
	Tracker     *SpawnTracker
	Emitter     *agentrt.Emitter
	Interceptor *ToolInterceptor
	ParentInput *agentrt.LoopInput
	BranchStore *agentrt.BranchStore
}

// Name returns the tool name.
func (h *ParallelTasksHandler) Name() string { return "parallel_tasks" }

// Definition returns the tool definition for LLM function calling.
func (h *ParallelTasksHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "parallel_tasks",
			Description: "Execute multiple tasks in parallel and wait for all to complete. Each task runs as an independent sub-agent with access to tools. Results are returned as a combined response. Use this when you need to do several independent things concurrently and synthesize the results.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tasks": map[string]interface{}{
						"type":        "array",
						"description": "Array of tasks to execute in parallel (2-10 tasks).",
						"minItems":    2,
						"maxItems":    10,
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"instruction": map[string]interface{}{
									"type":        "string",
									"description": "A clear description of the task for the sub-agent.",
								},
								"tools": map[string]interface{}{
									"type":        "array",
									"description": "Optional: subset of parent's tools for this task. If omitted, inherits all parent tools.",
									"items":       map[string]interface{}{"type": "string"},
								},
							},
							"required": []string{"instruction"},
						},
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Per-batch timeout in seconds (default: 300, max: 600).",
					},
				},
				"required": []string{"tasks"},
			},
		},
	}
}

// taskSpec is a single task parsed from the tool arguments.
type taskSpec struct {
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools,omitempty"`
}

// taskResult holds the outcome of a single parallel child.
type taskResult struct {
	Index       int
	Instruction string
	Status      string // "completed", "failed", "timeout"
	Result      string
	DurationMs  int64
	Usage       agentrt.UsageDelta
	Messages    json.RawMessage // full trace for persistence
	ToolCalls   int32
}

// Execute runs the parallel_tasks tool call.
func (h *ParallelTasksHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	parentInput := h.ParentInput
	if parentInput == nil {
		return "", fmt.Errorf("parallel_tasks: parent input not set")
	}

	// Parse tasks array
	tasksRaw, ok := args["tasks"]
	if !ok {
		return "", fmt.Errorf("tasks is required")
	}
	tasksJSON, err := json.Marshal(tasksRaw)
	if err != nil {
		return "", fmt.Errorf("invalid tasks argument: %w", err)
	}
	var tasks []taskSpec
	if err := json.Unmarshal(tasksJSON, &tasks); err != nil {
		return "", fmt.Errorf("invalid tasks format: %w", err)
	}

	if len(tasks) < 2 {
		return "parallel_tasks requires at least 2 tasks.", nil
	}
	if len(tasks) > 10 {
		return "parallel_tasks supports at most 10 tasks.", nil
	}

	// Validate all instructions are non-empty
	for i, t := range tasks {
		if strings.TrimSpace(t.Instruction) == "" {
			return fmt.Sprintf("Task %d has an empty instruction.", i+1), nil
		}
	}

	// Check spawn limits (each task counts as a spawn)
	for range tasks {
		if err := h.Tracker.CanSpawn(); err != nil {
			return fmt.Sprintf("Cannot run parallel tasks: %s. Please handle tasks directly.", err.Error()), nil
		}
		h.Tracker.SpawnCount.Add(1)
	}

	// Parse timeout
	timeoutSec := 300
	if ts, ok := args["timeout_seconds"].(float64); ok && ts >= 10 && ts <= 600 {
		timeoutSec = int(ts)
	}
	timeout := time.Duration(timeoutSec) * time.Second

	batchID := uuid.New().String()

	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSpawnStart,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"parallel_tasks": true,
				"batch_id":       batchID,
				"task_count":     len(tasks),
				"timeout":        timeoutSec,
			},
		})
	}

	// Run all tasks concurrently with barrier
	batchCtx, batchCancel := context.WithTimeout(ctx, timeout)
	defer batchCancel()

	results := make([]taskResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, spec taskSpec) {
			defer wg.Done()
			results[idx] = h.runChild(batchCtx, idx, spec, parentInput, batchID)
		}(i, task)
	}

	wg.Wait()

	// Build combined response
	var sb strings.Builder
	succeeded := 0
	for _, r := range results {
		if r.Status == "completed" {
			succeeded++
		}
	}

	sb.WriteString(fmt.Sprintf("Parallel execution completed (%d/%d tasks succeeded).\n", succeeded, len(tasks)))

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\n[Task %d: %q] (%s, %.1fs)\n",
			r.Index+1, truncateStr(r.Instruction, 80), r.Status, float64(r.DurationMs)/1000))
		sb.WriteString(fmt.Sprintf("Result: %s\n", r.Result))
	}

	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSpawnEnd,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"parallel_tasks": true,
				"batch_id":       batchID,
				"succeeded":      succeeded,
				"total":          len(tasks),
			},
		})
	}

	return sb.String(), nil
}

// runChild executes a single child task and returns the result.
func (h *ParallelTasksHandler) runChild(
	ctx context.Context,
	idx int,
	spec taskSpec,
	parentInput *agentrt.LoopInput,
	batchID string,
) taskResult {
	startedAt := time.Now()
	instruction := strings.TrimSpace(spec.Instruction)

	result := taskResult{
		Index:       idx,
		Instruction: instruction,
	}

	// Determine tools for this child
	childTools := parentInput.Tools
	if len(spec.Tools) > 0 {
		// When user specifies a subset, still merge parent's runtime-injected
		// synthetic tools so the child doesn't lose web_search, etc.
		childTools = spec.Tools
		if h.Interceptor != nil {
			specSet := make(map[string]struct{}, len(spec.Tools))
			for _, t := range spec.Tools {
				specSet[t] = struct{}{}
			}
			for name := range h.Interceptor.Handlers {
				if name == "spawn_agent" || name == "parallel_tasks" {
					continue
				}
				if _, exists := specSet[name]; !exists {
					for _, pt := range parentInput.Tools {
						if pt == name {
							childTools = append(childTools, name)
							break
						}
					}
				}
			}
		}
	}

	// Create child runtime components
	childEngine := agentrt.NewEngine(h.Registry, h.Router, h.ToolLoop)
	childEmitter := agentrt.NewEmitter()
	defer childEmitter.Close()

	// Forward child events to parent emitter with batch metadata
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSpawnStart,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"parallel_task":    true,
				"batch_id":         batchID,
				"task_index":       idx,
				"task_instruction": truncateStr(instruction, 200),
			},
		})
		childEmitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
			e.Data = ensureDataMap(e.Data)
			e.Data["parallel_task"] = true
			e.Data["batch_id"] = batchID
			e.Data["task_index"] = idx
			h.Emitter.Emit(e)
			return nil
		}))
	}

	childConfig := agentrt.LoopConfig{
		MaxIterations:       25,
		MaxToolCallsPerTurn: 0, // unlimited
		MaxHistoryMessages:  100,
		EnableStreaming:     false,
		TurnTimeout:         h.Tracker.Config.ChildTimeout,
	}

	childLoop := agentrt.NewLoop(childEngine, h.ToolLoop, childEmitter, childConfig)

	nodeID := uuid.New().String()

	childInput := &agentrt.LoopInput{
		TenantID:     parentInput.TenantID,
		AgentID:      parentInput.AgentID,
		SessionID:    fmt.Sprintf("parallel_%s_%d", batchID[:8], idx),
		Model:        parentInput.Model,
		SystemPrompt: parentInput.SystemPrompt,
		Tools:        childTools,
		Sampling:     parentInput.Sampling,
		UserInput:    instruction,
		AgentMode:    "subagent",
	}

	// Wire child interceptor (same pattern as spawn_agent)
	if h.Interceptor != nil {
		childInterceptor := NewToolInterceptor(h.ToolLoop)
		for name, handler := range h.Interceptor.Handlers {
			if name == "spawn_agent" {
				// Create a dedicated spawn handler for the child with incremented depth
				if spawnH, ok := handler.(*SpawnAgentHandler); ok {
					childSpawnHandler := &SpawnAgentHandler{
						ServerCtx:          h.ServerCtx,
						Registry:           h.Registry,
						Router:             h.Router,
						ToolLoop:           h.ToolLoop,
						Tracker:            h.Tracker.ChildTracker(nodeID),
						ParentEmitter:      childEmitter,
						ParentInput:        childInput,
						DB:                 spawnH.DB,
						TaskPermissionMode: spawnH.TaskPermissionMode,
						ParentMode:         "subagent",
						BranchStore:        h.BranchStore,
						ParentSandboxCtx:   spawnH.ParentSandboxCtx,
						RevisionStore:      spawnH.RevisionStore,
						ProjectRuntime:     spawnH.ProjectRuntime,
						SandboxManager:     spawnH.SandboxManager,
						BrowserPool:        spawnH.BrowserPool,
					}
					childInterceptor.RegisterHandler(childSpawnHandler)
				}
			} else if name == "parallel_tasks" {
				// Create a child parallel_tasks handler with incremented depth
				childPTHandler := &ParallelTasksHandler{
					ServerCtx:   h.ServerCtx,
					Registry:    h.Registry,
					Router:      h.Router,
					ToolLoop:    h.ToolLoop,
					Tracker:     h.Tracker.ChildTracker(nodeID),
					Emitter:     childEmitter,
					Interceptor: nil, // will be set below
					ParentInput: childInput,
					BranchStore: h.BranchStore,
				}
				childInterceptor.RegisterHandler(childPTHandler)
			} else {
				// Share sandbox, memory, ask_user, and other handlers
				childInterceptor.RegisterHandler(handler)
			}
		}
		childInput.Interceptor = childInterceptor

		// Wire self-reference for nested parallel_tasks
		if ptHandler, ok := childInterceptor.Handlers["parallel_tasks"]; ok {
			if ptH, ok := ptHandler.(*ParallelTasksHandler); ok {
				ptH.Interceptor = childInterceptor
			}
		}
	}

	childState := &agentrt.LoopState{
		Messages: []gw.Message{},
	}

	logger.WithFields(
		"batch_id", batchID,
		"task_index", idx,
		"instruction", truncateStr(instruction, 100),
	).Debug("parallel_tasks: launching child")

	finalState, err := childLoop.Run(ctx, childState, childInput)
	duration := time.Since(startedAt)
	result.DurationMs = duration.Milliseconds()

	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}

	if err != nil {
		result.Status = "failed"
		if ctx.Err() != nil {
			result.Status = "timeout"
		}
		result.Result = fmt.Sprintf("Error: %s", err.Error())

		// Persist failed branch
		h.persistBranch(ctx, nodeID, batchID, parentInput, instruction, result)

		return result
	}

	result.Status = "completed"
	result.Result = finalState.LastAssistantText
	if result.Result == "" {
		result.Result = "(no output)"
	}
	result.Usage = agentrt.UsageDelta{
		PromptTokens:     finalState.CumulativeUsage.PromptTokens,
		CompletionTokens: finalState.CumulativeUsage.CompletionTokens,
		TotalTokens:      finalState.CumulativeUsage.TotalTokens,
	}
	result.ToolCalls = finalState.TotalToolCalls

	// Serialize messages for persistence
	if msgBytes, err := json.Marshal(finalState.Messages); err == nil {
		result.Messages = msgBytes
	}

	// Track token usage on shared tracker
	h.Tracker.TokensUsed.Add(int64(finalState.CumulativeUsage.TotalTokens))

	// Persist successful branch
	h.persistBranch(ctx, nodeID, batchID, parentInput, instruction, result)

	return result
}

// persistBranch saves a branch record to the database.
func (h *ParallelTasksHandler) persistBranch(
	ctx context.Context,
	branchID, batchID string,
	parentInput *agentrt.LoopInput,
	instruction string,
	result taskResult,
) {
	if h.BranchStore == nil {
		return
	}

	batchIDPtr := &batchID
	h.BranchStore.SaveBranch(ctx, &agentrt.BranchRecord{
		ID:               branchID,
		SessionID:        parentInput.SessionID,
		BatchID:          batchIDPtr,
		TenantID:         parentInput.TenantID,
		AgentID:          parentInput.AgentID,
		Source:           "parallel_task",
		Instruction:      instruction,
		Conclusion:       result.Result,
		Status:           result.Status,
		Messages:         result.Messages,
		ToolCallsCount:   int(result.ToolCalls),
		PromptTokens:     int(result.Usage.PromptTokens),
		CompletionTokens: int(result.Usage.CompletionTokens),
		TotalTokens:      int(result.Usage.TotalTokens),
		DurationMs:       result.DurationMs,
	})
}
