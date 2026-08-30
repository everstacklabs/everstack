package trigger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/commands"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/google/uuid"
)

// WorkflowRunner executes a workflow by ID with a rendered input message.
// Implemented by the workflows gRPC server to decouple the trigger package
// from the full workflow execution pipeline.
type WorkflowRunner interface {
	RunWorkflow(ctx context.Context, workflowID, tenantID, input string) error
}

// Executor runs agent sessions in response to trigger activations.
type Executor struct {
	store          Store
	sessionMgr     *agentrt.SessionManager
	commandBus     commands.CommandBus
	queryBus       query.QueryBus
	cb             *CircuitBreaker
	workflowRunner WorkflowRunner // optional — set via SetWorkflowRunner
}

// NewExecutor creates a new trigger executor.
func NewExecutor(store Store, sessionMgr *agentrt.SessionManager, commandBus commands.CommandBus, queryBus query.QueryBus) *Executor {
	return &Executor{
		store:      store,
		sessionMgr: sessionMgr,
		commandBus: commandBus,
		queryBus:   queryBus,
		cb:         NewCircuitBreaker(store),
	}
}

// SetWorkflowRunner configures the optional workflow executor for workflow-targeted triggers.
func (e *Executor) SetWorkflowRunner(runner WorkflowRunner) {
	e.workflowRunner = runner
}

// Execute fires a trigger, creating an agent session with the rendered input.
// payload is the webhook body or event data (nil for cron triggers).
func (e *Executor) Execute(ctx context.Context, t *Trigger, payload json.RawMessage) {
	if t == nil {
		logger.Warn("trigger: skipped nil trigger")
		return
	}
	if t.TenantID == "" {
		logger.WithFields("trigger_id", t.ID, "name", t.Name).
			Warn("trigger: skipped because stored tenant identity is missing")
		return
	}

	// Trigger records come from tenant-scoped persistence and are the trusted
	// identity for background execution. Detach from short-lived request/tick
	// cancellation while preserving correlation values, then overwrite both
	// tenant keys so provider resolution and DB access use the stored tenant.
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	ctx = agentrt.ContextWithTenantIdentity(ctx, t.TenantID)

	// Check circuit breaker
	if !e.cb.ShouldExecute(t) {
		logger.WithFields("trigger_id", t.ID, "name", t.Name, "circuit", t.CircuitState).
			Debug("trigger: skipped (circuit open)")
		return
	}

	// Transition to half-open if reset period has elapsed
	if t.CircuitState == CircuitOpen {
		if err := e.cb.TransitionToHalfOpen(ctx, t); err != nil {
			logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to transition to half-open")
			return
		}
	}

	// Check max concurrent
	running, err := e.store.CountRunningExecutions(ctx, t.ID)
	if err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to count running executions")
		return
	}
	if running >= t.MaxConcurrent {
		logger.WithFields("trigger_id", t.ID, "name", t.Name, "running", running, "max", t.MaxConcurrent).
			Debug("trigger: skipped (max concurrent reached)")
		return
	}

	// Render input from template
	input, err := e.renderInput(t, payload)
	if err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to render input template")
		input = string(payload) // fallback to raw payload
	}

	// Branch: workflow-targeted trigger vs agent session trigger
	if t.WorkflowID != "" {
		e.executeWorkflowTrigger(ctx, t, input, payload)
		return
	}

	// Execute with retries
	e.executeWithRetries(ctx, t, input, payload)
}

func (e *Executor) executeWithRetries(ctx context.Context, t *Trigger, input string, payload json.RawMessage) {
	maxAttempts := t.MaxRetries + 1
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := e.executeSingle(ctx, t, input, payload, attempt)
		if err == nil {
			e.cb.RecordSuccess(ctx, t)
			return
		}

		logger.WithFields("trigger_id", t.ID, "name", t.Name, "attempt", attempt, "max", maxAttempts, "error", err.Error()).
			Warn("trigger: execution failed")

		if attempt < maxAttempts {
			delay := time.Duration(t.RetryDelaySeconds) * time.Second
			if delay <= 0 {
				delay = 60 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}

	// All attempts exhausted
	e.cb.RecordFailure(ctx, t)
}

func (e *Executor) executeSingle(ctx context.Context, t *Trigger, input string, payload json.RawMessage, attempt int) error {
	start := time.Now()
	sessionID := uuid.New().String()

	// Record execution as pending
	exec := &Execution{
		TenantID:       t.TenantID,
		TriggerID:      t.ID,
		SessionID:      &sessionID,
		Status:         StatusRunning,
		TriggerPayload: payload,
		InputRendered:  input,
		Attempt:        attempt,
	}
	if err := e.store.RecordExecution(ctx, exec); err != nil {
		return fmt.Errorf("record execution: %w", err)
	}

	// Load agent config via query bus
	agentCfg, err := e.loadAgentConfig(ctx, t.AgentID, t.TenantID)
	if err != nil {
		e.completeExecution(ctx, exec.ID, StatusFailed, "", err.Error(), int(time.Since(start).Milliseconds()))
		return fmt.Errorf("load agent: %w", err)
	}

	// Create session via CQRS command
	if e.commandBus != nil {
		cmd := agentscmd.NewCreateSessionCommand(t.TenantID, t.AgentID, map[string]interface{}{
			"source":     "trigger",
			"trigger_id": t.ID,
		}, "", "")
		cmd.ID = sessionID
		if err := e.commandBus.Dispatch(ctx, cmd); err != nil {
			logger.WithFields("error", err.Error(), "session_id", sessionID).Warn("trigger: failed to register session in CQRS")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Build loop input
	loopInput := &agentrt.LoopInput{
		TenantID:     t.TenantID,
		AgentID:      t.AgentID,
		SessionID:    sessionID,
		Model:        agentCfg.Model,
		SystemPrompt: agentCfg.SystemPrompt,
		Tools:        agentCfg.Tools,
		UserInput:    input,
	}

	maxTurns := agentCfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	runnerConfig := agentrt.SessionRunnerConfig{
		LoopConfig: agentrt.LoopConfig{
			MaxIterations: int32(maxTurns),
		},
	}

	// Prepare and launch session
	emitter, err := e.sessionMgr.PrepareSession(ctx, sessionID, t.AgentID, t.TenantID, loopInput, runnerConfig)
	if err != nil {
		e.completeExecution(ctx, exec.ID, StatusFailed, "", err.Error(), int(time.Since(start).Milliseconds()))
		return fmt.Errorf("prepare session: %w", err)
	}

	// Collect events
	eventCh := make(chan agentrt.Event, 256)
	emitter.AddSink(agentrt.EventSinkFunc(func(ev agentrt.Event) error {
		select {
		case eventCh <- ev:
		default:
		}
		return nil
	}))

	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := e.sessionMgr.LaunchSession(runCtx, sessionID, loopInput); err != nil {
		e.completeExecution(ctx, exec.ID, StatusFailed, "", err.Error(), int(time.Since(start).Milliseconds()))
		return fmt.Errorf("launch session: %w", err)
	}

	// Collect results
	var finalOutput string
	var status ExecutionStatus = StatusCompleted
	var errMsg string

	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				goto done
			}
			switch evt.Type {
			case agentrt.EventTurnEnd:
				if text, ok := evt.Data["assistant_text"].(string); ok && text != "" {
					finalOutput = text
				}
			case agentrt.EventSessionEnd:
				goto done
			case agentrt.EventSessionError:
				status = StatusFailed
				if msg, ok := evt.Data["error"].(string); ok {
					errMsg = msg
				}
				goto done
			}
		case <-runCtx.Done():
			status = StatusTimeout
			errMsg = "trigger execution timed out"
			goto done
		}
	}

done:
	durationMs := int(time.Since(start).Milliseconds())

	// Truncate output preview
	preview := finalOutput
	if len(preview) > 1000 {
		preview = preview[:1000] + "...(truncated)"
	}

	e.completeExecution(ctx, exec.ID, status, preview, errMsg, durationMs)

	if status == StatusFailed || status == StatusTimeout {
		return fmt.Errorf("%s: %s", status, errMsg)
	}
	return nil
}

func (e *Executor) completeExecution(ctx context.Context, execID string, status ExecutionStatus, output, errMsg string, durationMs int) {
	if err := e.store.CompleteExecution(ctx, execID, status, output, errMsg, durationMs); err != nil {
		logger.WithFields("execution_id", execID, "error", err.Error()).Warn("trigger: failed to complete execution record")
	}
}

func (e *Executor) renderInput(t *Trigger, payload json.RawMessage) (string, error) {
	if t.InputTemplate == "" {
		if len(payload) > 0 {
			return string(payload), nil
		}
		return fmt.Sprintf("Trigger '%s' activated.", t.Name), nil
	}

	tmpl, err := template.New("input").Parse(t.InputTemplate)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	data := map[string]interface{}{
		"trigger": map[string]interface{}{
			"id":   t.ID,
			"name": t.Name,
			"type": string(t.Type),
		},
	}

	if len(payload) > 0 {
		var p interface{}
		if json.Unmarshal(payload, &p) == nil {
			data["payload"] = p
		} else {
			data["payload"] = string(payload)
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// agentConfig is the subset of agent fields needed for trigger execution.
type agentConfig struct {
	Model        string   `json:"model"`
	SystemPrompt string   `json:"system_prompt"`
	Tools        []string `json:"tools"`
	MaxTurns     int      `json:"max_turns"`
}

// executeWorkflowTrigger runs a workflow instead of an agent session.
func (e *Executor) executeWorkflowTrigger(ctx context.Context, t *Trigger, input string, payload json.RawMessage) {
	if e.workflowRunner == nil {
		logger.WithFields("trigger_id", t.ID, "workflow_id", t.WorkflowID).
			Warn("trigger: workflow runner not configured, cannot execute workflow trigger")
		e.cb.RecordFailure(ctx, t)
		return
	}

	start := time.Now()

	exec := &Execution{
		TenantID:       t.TenantID,
		TriggerID:      t.ID,
		Status:         StatusRunning,
		TriggerPayload: payload,
		InputRendered:  input,
		Attempt:        1,
	}
	if err := e.store.RecordExecution(ctx, exec); err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to record workflow execution")
		return
	}

	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := e.workflowRunner.RunWorkflow(runCtx, t.WorkflowID, t.TenantID, input)
	durationMs := int(time.Since(start).Milliseconds())

	if err != nil {
		logger.WithFields("trigger_id", t.ID, "workflow_id", t.WorkflowID, "error", err.Error()).
			Warn("trigger: workflow execution failed")
		e.completeExecution(ctx, exec.ID, StatusFailed, "", err.Error(), durationMs)
		e.cb.RecordFailure(ctx, t)
		return
	}

	e.completeExecution(ctx, exec.ID, StatusCompleted, "Workflow executed successfully", "", durationMs)
	e.cb.RecordSuccess(ctx, t)
}

func (e *Executor) loadAgentConfig(ctx context.Context, agentID, tenantID string) (*agentConfig, error) {
	if e.queryBus == nil {
		return nil, fmt.Errorf("query bus not available")
	}

	q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	res, err := e.queryBus.Execute(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query agent: %w", err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	agent, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		return nil, fmt.Errorf("unexpected agent data type")
	}

	return &agentConfig{
		Model:        agent.Model,
		SystemPrompt: agent.SystemPrompt.String,
		Tools:        []string(agent.Tools),
		MaxTurns:     int(agent.MaxTurns),
	}, nil
}
