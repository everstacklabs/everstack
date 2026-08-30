// Deprecated: This file contains the legacy Trooper session handlers.
// All Trooper session endpoints are deprecated in favor of the unified Agent
// session flow which handles persistent agents transparently via
// lifecycle_mode detection. These handlers remain as shims during migration.
// See runTurnStreamInternal in agents.go for the unified session handling.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentpolicy "github.com/everstacklabs/everstack/internal/agents/policy"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	agentskills "github.com/everstacklabs/everstack/internal/agents/skills"
	channelpkg "github.com/everstacklabs/everstack/internal/channels"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	wsquery "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/everstacklabs/everstack/internal/telemetry/autoscorer"
	"go.opentelemetry.io/otel/trace"
)

// ─── Trooper Session Handlers (DEPRECATED) ────────────────────────

// handleCreateTrooperSessionStream creates a new trooper session and streams
// SSE events back to the client. This is the REST equivalent of creating an
// agent session but for troopers.
// Route: POST /v1/troopers/{trooper_id}/sessions/stream
func (s *Server) handleCreateTrooperSessionStream(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	trooperID := pathParams["trooper_id"]
	if trooperID == "" {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"trooper_id is required"}}`, http.StatusBadRequest)
		return
	}

	var body struct {
		TenantID        string `json:"tenant_id"`
		UserInput       string `json:"user_input"`
		EnableStreaming bool   `json:"enable_streaming"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}

	logger.WithFields("trooper_id", trooperID, "tenant_id", body.TenantID).
		Info("handleCreateTrooperSessionStream: request received")

	ctx := r.Context()
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"CQRS system not available"}}`, http.StatusInternalServerError)
		return
	}

	if s.sessionMgr == nil {
		http.Error(w, `{"error":{"code":"internal","message":"session manager not initialized"}}`, http.StatusServiceUnavailable)
		return
	}

	tenantID, err := s.resolveTenantID(ctx, body.TenantID)
	if err != nil {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"tenant_id is required"}}`, http.StatusBadRequest)
		return
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)

	// Load trooper (auto-wake if sleeping).
	ws, err := s.ensureTrooperReadyForMessage(ctx, sys, trooperID, tenantID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"code":"not_found","message":"%s"}}`, err.Error()), http.StatusNotFound)
		return
	}
	if ws.Status != "running" {
		http.Error(w, fmt.Sprintf(`{"error":{"code":"failed_precondition","message":"trooper is not ready (status=%s)"}}`, ws.Status), http.StatusPreconditionFailed)
		return
	}

	// Create session via CQRS
	cmd := agentscmd.NewCreateSessionCommand(tenantID, "", nil, "", "")
	cmd.TrooperID = trooperID
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		logger.WithFields("trooper_id", trooperID).WithError(err).Error("trooper session: create command failed")
		http.Error(w, `{"error":{"code":"internal","message":"failed to create session"}}`, http.StatusInternalServerError)
		return
	}
	sessionID := cmd.ID

	// Update source column
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET source = 'trooper', trooper_id = $1 WHERE id = $2`,
			trooperID, sessionID)
	}

	// Load session with retry (eventual consistency)
	swt, err := s.loadSessionWithRetry(ctx, sys, sessionID, tenantID)
	if err != nil {
		logger.WithFields("session_id", sessionID).WithError(err).Error("trooper session: failed to load session")
		http.Error(w, `{"error":{"code":"internal","message":"failed to load session"}}`, http.StatusInternalServerError)
		return
	}

	// Build loop input from trooper config
	result, err := s.buildTrooperLoopInput(ctx, sessionID, tenantID, body.UserInput, ws, swt)
	if err != nil {
		logger.WithFields("trooper_id", trooperID).WithError(err).Error("trooper session: failed to build loop input")
		http.Error(w, `{"error":{"code":"internal","message":"failed to build session config"}}`, http.StatusInternalServerError)
		return
	}

	// Prepare session (creates runner but does NOT start it)
	emitter, err := s.prepareAndLaunchTrooperSession(ctx, sessionID, ws, result)
	if err != nil {
		logger.WithFields("session_id", sessionID).WithError(err).Error("trooper session: failed to prepare")
		http.Error(w, `{"error":{"code":"internal","message":"failed to launch session"}}`, http.StatusInternalServerError)
		return
	}

	// Set up SSE response headers and attach event sink BEFORE launching
	// so no events are missed (mirrors the agent steer flow).
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":{"code":"internal","message":"streaming not supported"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	sender := &sseEventSender{w: w, flusher: flusher}

	// Attach event channel sink before launching to capture all events.
	// Use done channel as safety net if end event can't fit in the channel.
	eventCh := make(chan agentrt.Event, 128)
	doneCh := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(doneCh) }) }

	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		isEnd := e.Type == agentrt.EventSessionEnd || e.Type == agentrt.EventSessionError
		select {
		case eventCh <- e:
			if isEnd {
				closeDone()
			}
			return nil
		default:
			if isEnd {
				closeDone()
			}
			return errors.New("event channel full")
		}
	}))

	// Now launch the session
	if err := s.launchTrooperSession(ctx, sessionID, result.loopInput); err != nil {
		logger.WithFields("session_id", sessionID).WithError(err).Error("trooper session: failed to launch")
		errJSON := fmt.Sprintf(`{"error":{"code":"internal","message":"%s"}}`, err.Error())
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errJSON)
		flusher.Flush()
		return
	}

	// Stream events to client
	for {
		select {
		case <-r.Context().Done():
			return
		case <-doneCh:
			for {
				select {
				case event := <-eventCh:
					protoEvent := runtimeEventToProto(&event)
					_ = sender.Send(protoEvent, event.Data)
				default:
					return
				}
			}
		case event, chanOk := <-eventCh:
			if !chanOk {
				return
			}
			protoEvent := runtimeEventToProto(&event)
			if err := sender.Send(protoEvent, event.Data); err != nil {
				return
			}
			if event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError {
				return
			}
		}
	}
}

// handleSteerTrooperSessionStream sends a follow-up message to an existing trooper session.
// Route: POST /v1/troopers/sessions/{session_id}/turns/stream
func (s *Server) handleSteerTrooperSessionStream(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	sessionID := pathParams["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"session_id is required"}}`, http.StatusBadRequest)
		return
	}

	var body struct {
		TenantID  string `json:"tenant_id"`
		UserInput string `json:"user_input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}

	logger.WithFields("session_id", sessionID, "tenant_id", body.TenantID).
		Info("handleSteerTrooperSessionStream: request received")

	ctx := r.Context()
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"CQRS system not available"}}`, http.StatusInternalServerError)
		return
	}

	if s.sessionMgr == nil {
		http.Error(w, `{"error":{"code":"internal","message":"session manager not initialized"}}`, http.StatusServiceUnavailable)
		return
	}

	tenantID, err := s.resolveTenantID(ctx, body.TenantID)
	if err != nil {
		http.Error(w, `{"error":{"code":"invalid_argument","message":"tenant_id is required"}}`, http.StatusBadRequest)
		return
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)

	// Load session
	swt, err := s.loadSessionWithRetry(ctx, sys, sessionID, tenantID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"code":"not_found","message":"%s"}}`, err.Error()), http.StatusNotFound)
		return
	}

	// Extract trooper_id from session
	trooperID := ""
	if swt.Session.TrooperID.Valid {
		trooperID = swt.Session.TrooperID.String
	}
	if trooperID == "" {
		http.Error(w, `{"error":{"code":"failed_precondition","message":"session is not a trooper session"}}`, http.StatusBadRequest)
		return
	}

	// Load trooper (auto-wake if sleeping).
	ws, err := s.ensureTrooperReadyForMessage(ctx, sys, trooperID, tenantID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"code":"not_found","message":"%s"}}`, err.Error()), http.StatusNotFound)
		return
	}
	if ws.Status != "running" {
		http.Error(w, fmt.Sprintf(`{"error":{"code":"failed_precondition","message":"trooper is not ready (status=%s)"}}`, ws.Status), http.StatusPreconditionFailed)
		return
	}

	// Update session status to running
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET status = 'running', updated_at = NOW() WHERE id = $1`,
			sessionID)
	}

	// Build loop input with new user message + previous turns
	result, err := s.buildTrooperLoopInput(ctx, sessionID, tenantID, body.UserInput, ws, swt)
	if err != nil {
		logger.WithFields("trooper_id", trooperID).WithError(err).Error("trooper steer: failed to build loop input")
		http.Error(w, `{"error":{"code":"internal","message":"failed to build session config"}}`, http.StatusInternalServerError)
		return
	}

	// Prepare session (creates runner but does NOT start it)
	emitter, err := s.prepareAndLaunchTrooperSession(ctx, sessionID, ws, result)
	if err != nil {
		logger.WithFields("session_id", sessionID).WithError(err).Error("trooper steer: failed to prepare")
		http.Error(w, `{"error":{"code":"internal","message":"failed to launch session"}}`, http.StatusInternalServerError)
		return
	}

	// Set up SSE and attach sink BEFORE launching
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":{"code":"internal","message":"streaming not supported"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	sender := &sseEventSender{w: w, flusher: flusher}

	eventCh := make(chan agentrt.Event, 128)
	steerDoneCh := make(chan struct{})
	var steerDoneOnce sync.Once
	closeSteerDone := func() { steerDoneOnce.Do(func() { close(steerDoneCh) }) }

	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		isEnd := e.Type == agentrt.EventSessionEnd || e.Type == agentrt.EventSessionError
		select {
		case eventCh <- e:
			if isEnd {
				closeSteerDone()
			}
			return nil
		default:
			if isEnd {
				closeSteerDone()
			}
			return errors.New("event channel full")
		}
	}))

	// Now launch
	if err := s.launchTrooperSession(ctx, sessionID, result.loopInput); err != nil {
		logger.WithFields("session_id", sessionID).WithError(err).Error("trooper steer: failed to launch")
		errJSON := fmt.Sprintf(`{"error":{"code":"internal","message":"%s"}}`, err.Error())
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errJSON)
		flusher.Flush()
		return
	}

	// Transition persistent agent back to idle when the turn ends.
	setIdleOnTurnEnd := func() {
		if s.db != nil && trooperID != "" {
			s.db.ExecContext(context.Background(),
				`UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND lifecycle_status = 'running' AND tenant_id = $2`,
				trooperID, tenantID)
		}
	}

	// Stream events
	for {
		select {
		case <-r.Context().Done():
			setIdleOnTurnEnd()
			return
		case <-steerDoneCh:
			for {
				select {
				case event := <-eventCh:
					protoEvent := runtimeEventToProto(&event)
					_ = sender.Send(protoEvent, event.Data)
				default:
					setIdleOnTurnEnd()
					return
				}
			}
		case event, chanOk := <-eventCh:
			if !chanOk {
				setIdleOnTurnEnd()
				return
			}
			protoEvent := runtimeEventToProto(&event)
			if err := sender.Send(protoEvent, event.Data); err != nil {
				setIdleOnTurnEnd()
				return
			}
			if event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError {
				setIdleOnTurnEnd()
				return
			}
		}
	}
}

// runTrooperTurnInternal is called by runTurnStreamInternal when it detects a trooper
// session (no agent_id, has trooper_id). This allows the unified agent steer endpoint
// to handle trooper sessions transparently, so SessionTimeline's startTurn works for both.
func (s *Server) runTrooperTurnInternal(
	ctx context.Context,
	sys *cqrs.System,
	swt *agentsquery.SessionWithTurns,
	tenantID, userInput string,
	stream agentEventSender,
	span trace.Span,
) error {
	trooperID := swt.Session.TrooperID.String

	logger.WithFields("session_id", swt.Session.ID, "trooper_id", trooperID, "turns", len(swt.Turns)).
		Info("runTrooperTurnInternal: starting")

	// Load trooper (auto-wake if sleeping).
	ws, err := s.ensureTrooperReadyForMessage(ctx, sys, trooperID, tenantID)
	if err != nil {
		logger.WithFields("trooper_id", trooperID).WithError(err).Error("runTrooperTurnInternal: trooper not found")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("trooper not found: %w", err))
	}

	logger.WithFields("session_id", swt.Session.ID, "trooper_status", ws.Status).
		Info("runTrooperTurnInternal: trooper loaded")

	if ws.Status != "running" {
		err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("trooper is not ready (status=%s)", ws.Status))
		telemetry.RecordError(span, err)
		return err
	}

	// Update session status to running
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET status = 'running', updated_at = NOW() WHERE id = $1`,
			swt.Session.ID)
	}

	// Build loop input from trooper config
	logger.WithFields("session_id", swt.Session.ID).Info("runTrooperTurnInternal: building loop input")
	result, err := s.buildTrooperLoopInput(ctx, swt.Session.ID, tenantID, userInput, ws, swt)
	if err != nil {
		logger.WithFields("trooper_id", trooperID).WithError(err).
			Error("runTrooperTurnInternal: failed to build loop input")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to build session config: %w", err))
	}

	// Prepare (creates runner, does NOT start)
	logger.WithFields("session_id", swt.Session.ID).Info("runTrooperTurnInternal: preparing session")
	emitter, err := s.prepareAndLaunchTrooperSession(ctx, swt.Session.ID, ws, result)
	if err != nil {
		logger.WithFields("session_id", swt.Session.ID).WithError(err).
			Error("runTrooperTurnInternal: failed to prepare")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to prepare session: %w", err))
	}

	// Attach event sink BEFORE launching so no events are missed.
	// Use a done channel as a safety net: if the end event can't fit into
	// eventCh (channel full), the done channel ensures the loop terminates
	// rather than hanging forever.
	eventCh := make(chan agentrt.Event, 128)
	doneCh := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(doneCh) }) }

	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		isEnd := e.Type == agentrt.EventSessionEnd || e.Type == agentrt.EventSessionError
		select {
		case eventCh <- e:
			if isEnd {
				closeDone()
			}
			return nil
		default:
			if isEnd {
				// End event MUST signal completion even if channel is full
				closeDone()
			}
			logger.WithFields("session_id", swt.Session.ID, "event_type", string(e.Type)).
				Warn("runTrooperTurnInternal: event channel full, dropping event")
			return errors.New("event channel full")
		}
	}))

	// Now launch
	logger.WithFields("session_id", swt.Session.ID).Info("runTrooperTurnInternal: launching session")
	if err := s.launchTrooperSession(ctx, swt.Session.ID, result.loopInput); err != nil {
		logger.WithFields("session_id", swt.Session.ID).WithError(err).
			Error("runTrooperTurnInternal: failed to launch")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to launch session: %w", err))
	}

	logger.WithFields("session_id", swt.Session.ID).Info("runTrooperTurnInternal: streaming events")

	// Transition persistent agent back to idle when the turn ends.
	setIdleOnTurnEnd := func() {
		if s.db != nil && trooperID != "" {
			s.db.ExecContext(context.Background(),
				`UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND lifecycle_status = 'running' AND tenant_id = $2`,
				trooperID, tenantID)
		}
	}

	for {
		select {
		case <-ctx.Done():
			setIdleOnTurnEnd()
			return nil
		case <-doneCh:
			// Runner finished — drain any remaining events in the channel
			for {
				select {
				case event := <-eventCh:
					protoEvent := runtimeEventToProto(&event)
					_ = stream.Send(protoEvent, event.Data)
				default:
					setIdleOnTurnEnd()
					return nil
				}
			}
		case event, ok := <-eventCh:
			if !ok {
				setIdleOnTurnEnd()
				return nil
			}
			protoEvent := runtimeEventToProto(&event)
			if err := stream.Send(protoEvent, event.Data); err != nil {
				setIdleOnTurnEnd()
				telemetry.RecordError(span, err)
				return err
			}
			if event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError {
				setIdleOnTurnEnd()
				return nil
			}
		}
	}
}

// ─── Channel Integration ────────────────────────────────────────────

// CreateTrooperChannelSession creates a new trooper session from a channel message.
func (s *Server) CreateTrooperChannelSession(ctx context.Context, params channelpkg.CreateTrooperChannelSessionParams) (string, *agentrt.Emitter, error) {
	var err error
	ctx, err = channelTenantContext(ctx, params.TenantID)
	if err != nil {
		return "", nil, err
	}
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return "", nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	if s.sessionMgr == nil {
		return "", nil, fmt.Errorf("agent session manager not initialized")
	}

	// Load trooper (auto-wake if sleeping).
	ws, err := s.ensureTrooperReadyForMessage(ctx, sys, params.TrooperID, params.TenantID)
	if err != nil {
		return "", nil, err
	}
	if ws.Status != "running" {
		return "", nil, fmt.Errorf("trooper %s is not ready (status=%s)", params.TrooperID, ws.Status)
	}

	// Create session via CQRS
	cmd := agentscmd.NewCreateSessionCommand(params.TenantID, "", nil, "", "")
	cmd.TrooperID = params.TrooperID
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", nil, fmt.Errorf("create session command: %w", err)
	}
	sessionID := cmd.ID

	// Update source tracking
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET source = $1, trooper_id = $2, channel_config_id = $3, platform_user_id = $4, platform_user_name = $5 WHERE id = $6`,
			params.Source, params.TrooperID, params.ChannelConfigID, params.PlatformUserID, params.PlatformUserName, sessionID)
	}

	// Load session
	swt, err := s.loadSessionWithRetry(ctx, sys, sessionID, params.TenantID)
	if err != nil {
		return "", nil, err
	}

	// Build loop
	result, err := s.buildTrooperLoopInput(ctx, sessionID, params.TenantID, params.InitialMessage, ws, swt)
	if err != nil {
		return "", nil, err
	}

	// Prepare (don't launch yet — caller will attach sinks first)
	emitter, err := s.prepareAndLaunchTrooperSession(ctx, sessionID, ws, result)
	if err != nil {
		return "", nil, err
	}

	// Channel sessions: launch immediately since the caller (channel manager)
	// wires its own sink to the emitter after this returns.
	// The emitter replays buffered events for late subscribers.
	if err := s.launchTrooperSession(ctx, sessionID, result.loopInput); err != nil {
		return "", nil, fmt.Errorf("launch trooper session: %w", err)
	}

	logger.WithFields(
		"session_id", sessionID,
		"trooper_id", params.TrooperID,
		"source", params.Source,
	).Info("trooper: created channel session")

	return sessionID, emitter, nil
}

// ─── Core: Build LoopInput from Trooper ───────────────────────────

func (s *Server) buildTrooperLoopInput(
	ctx context.Context,
	sessionID, tenantID, userInput string,
	ws *wsquery.TrooperReadModel,
	swt *agentsquery.SessionWithTurns,
) (*channelLoopResult, error) {
	if s.engine == nil {
		return nil, fmt.Errorf("agent runtime engine not initialized")
	}
	requestProviderRegistry, requestProviderRouter, err := s.engine.ProvidersForContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve trooper providers: %w", err)
	}

	var agentConfig map[string]interface{}
	if len(ws.AgentConfig) > 0 {
		_ = json.Unmarshal(ws.AgentConfig, &agentConfig)
	}
	if agentConfig == nil {
		agentConfig = make(map[string]interface{})
	}

	agentMode := "primary"
	taskPermissionMode := "auto"
	if pm, ok := agentConfig["task_permission_mode"].(string); ok && pm != "" {
		taskPermissionMode = pm
	}

	var maxSteps int32
	if ws.MaxSteps.Valid && ws.MaxSteps.Int32 > 0 {
		maxSteps = ws.MaxSteps.Int32
	}

	sampling := gw.SamplingParams{}
	if temp, ok := agentConfig["temperature"].(float64); ok {
		sampling.Temperature = temp
	}
	if maxTokens, ok := agentConfig["max_tokens"].(float64); ok {
		sampling.MaxTokens = int(maxTokens)
	}

	// Build system prompt with identity context
	systemPrompt := ""
	if ws.SystemPrompt.Valid {
		systemPrompt = ws.SystemPrompt.String
	}
	identityBlock := buildTrooperIdentityPrompt(ws)
	if identityBlock != "" {
		systemPrompt = systemPrompt + "\n\n" + identityBlock
	}

	loopInput := &agentrt.LoopInput{
		TenantID:           tenantID,
		AgentID:            "",
		TrooperID:          ws.ID,
		SessionID:          sessionID,
		Model:              ws.Model,
		SystemPrompt:       systemPrompt,
		Tools:              ws.Tools,
		Sampling:           sampling,
		UserInput:          userInput,
		AgentMode:          agentMode,
		TaskPermissionMode: taskPermissionMode,
		MaxSteps:           maxSteps,
	}

	// Fallback config
	fallbackConfig := agentrt.ParseFallbackConfig(agentConfig)
	if fallbackConfig == nil {
		fallbackConfig = agentrt.FallbackConfigFromGateway(context.Background())
		if fallbackConfig == nil && s.ctx != nil {
			fallbackConfig = agentrt.FallbackConfigFromGateway(s.ctx)
		}
	}
	if fallbackConfig != nil {
		loopInput.FallbackConfig = fallbackConfig
	}
	loopInput.APIType = agentrt.ParseAPIType(agentConfig)

	// HITL config
	if hitlConfig := agentrt.ParseHITLConfig(agentConfig); hitlConfig != nil {
		loopInput.HITLConfig = hitlConfig
	}

	// Sandbox config: always enabled for troopers, persistent, reuses provisioned sandbox
	spawnConfig := agenttools.ParseSpawnConfig(agentConfig)
	sandboxConfig := sandbox.SandboxConfig{
		Enabled:        true,
		Persistent:     true,
		Image:          ws.SandboxImage,
		CPULimit:       ws.SandboxCPULimit,
		MemoryMB:       int64(ws.SandboxMemoryMB),
		DiskMB:         int64(ws.SandboxDiskMB),
		NetworkMode:    ws.SandboxNetworkMode,
		AllowedHosts:   ws.SandboxAllowedHosts,
		TimeoutSeconds: int(ws.SandboxTimeoutSeconds),
	}
	if ws.SandboxGitRepoURL.Valid {
		sandboxConfig.GitRepoURL = ws.SandboxGitRepoURL.String
	}
	if ws.SandboxGitBranch.Valid {
		sandboxConfig.GitBranch = ws.SandboxGitBranch.String
	}

	loopInput.ExecutionMode = "sandbox"
	loopInput.PersistenceMode = "persistent"
	loopInput.SandboxEnabled = true
	loopInput.GitRepoConfigured = strings.TrimSpace(sandboxConfig.GitRepoURL) != ""
	loopInput.SystemPrompt = augmentSandboxSystemPrompt(loopInput.SystemPrompt, sandboxConfig)

	// web_fetch is default-on for every agent (needs no SearXNG); web_search is
	// default-on only when a self-hosted SearXNG instance is configured.
	searxngURL := os.Getenv("EVS_SEARXNG_URL")
	enableWebSearch := searxngURL != ""
	loopInput.Tools = appendUnique(loopInput.Tools, "web_fetch")
	if enableWebSearch {
		loopInput.Tools = appendUnique(loopInput.Tools, "web_search")
	}

	hasMemory := s.memoryStore != nil && s.memoryEmbedder != nil

	// Build interceptor
	interceptor := agenttools.NewToolInterceptor(s.sessionMgr.GetToolLoop())

	if spawnConfig.Enabled {
		spawnTracker := agenttools.NewSpawnTracker(sessionID, spawnConfig)
		spawnHandler := &agenttools.SpawnAgentHandler{
			ServerCtx:          s.ctx,
			Registry:           requestProviderRegistry,
			Router:             requestProviderRouter,
			ToolLoop:           s.sessionMgr.GetToolLoop(),
			Tracker:            spawnTracker,
			ParentInput:        loopInput,
			DB:                 s.db,
			TaskPermissionMode: taskPermissionMode,
			ParentMode:         agentMode,
			RevisionStore:      s.revisionStore,
			ProjectRuntime:     s.projectRuntime,
			SandboxManager:     s.sandboxMgr,
			BrowserPool:        s.browserPool,
		}
		interceptor.RegisterHandler(spawnHandler)
		loopInput.Tools = appendUnique(loopInput.Tools, "spawn_agent")

		if spawnConfig.Async {
			jobQueue := s.sessionMgr.GetOrCreateJobQueue(sessionID, spawnConfig.MaxConcurrentJobs)
			spawnHandler.JobQueue = jobQueue
			loopInput.JobResultCh = jobQueue.ResultCh()
			interceptor.RegisterHandler(&agenttools.CheckJobHandler{JobQueue: jobQueue})
			loopInput.Tools = appendUnique(loopInput.Tools, "check_job")
		}
	}

	// Sandbox handlers — use the trooper's persistent sandbox (session ID = "trp-" + trooperID)
	var sandboxCtx *agenttools.SandboxSessionContext
	if s.sandboxMgr != nil {
		clampedConfig := s.sandboxMgr.ClampToGlobalLimitsForTenant(sandboxConfig, tenantID)
		s.injectPortExposureDomain(&clampedConfig)
		// Attach browser sidecar if browser automation is enabled
		browserCfg := sandbox.ParseBrowserConfig(agentConfig)
		// Gate headed mode behind browser_headed feature flag
		if !browserCfg.Headless && !s.sandboxMgr.IsBrowserHeadedEnabled(tenantID) {
			browserCfg.Headless = true
		}
		clampedConfig.BrowserSidecar = browserCfg.ToSidecarConfig()
		sandboxCtx = &agenttools.SandboxSessionContext{
			Manager:                s.sandboxMgr,
			SessionID:              "trp-" + ws.ID, // reuse provisioned sandbox
			TenantID:               tenantID,
			Config:                 clampedConfig,
			SessionStartedAt:       time.Now(),
			ExecutionMode:          "sandbox",
			PersistenceMode:        "persistent",
			PortExposureBaseDomain: s.portExposureBaseDomain,
			PortExposureTLSEnabled: s.portExposureTLSEnabled,
			PortExposureListenPort: s.portExposureListenPort,
			AgentID:                ws.ID,
		}
		handlers := agenttools.NewSandboxHandlers(sandboxCtx)
		for _, h := range handlers {
			interceptor.RegisterHandler(h)
		}
		// Ensure sandbox tool names are in the tools list so the LLM sees them
		for _, h := range handlers {
			loopInput.Tools = appendUnique(loopInput.Tools, h.Name())
		}
		// Persistent child routing — see agents.go for rationale.
		if existing, ok := interceptor.Handlers["spawn_agent"].(*agenttools.SpawnAgentHandler); ok {
			existing.ParentSandboxCtx = sandboxCtx
		}

		// Register browser automation handlers (requires sandbox).
		// Re-use browserCfg parsed above (already has headed mode gated).
		if browserCfg.Enabled {
			browserCtx := &agenttools.BrowserSessionContext{
				SandboxCtx: sandboxCtx,
				Config:     browserCfg,
				Pool:       s.browserPool,
			}
			for _, h := range agenttools.NewBrowserHandlers(browserCtx) {
				interceptor.RegisterHandler(h)
			}
			loopInput.Tools = appendUnique(loopInput.Tools, agenttools.BrowserToolNames()...)
		}
	}

	// Web search/fetch handlers. web_fetch is default-on; its nil HTTPClient
	// uses the SSRF-guarded client. web_search needs a configured SearXNG.
	interceptor.RegisterHandler(&agenttools.WebFetchHandler{})
	if enableWebSearch {
		interceptor.RegisterHandler(&agenttools.WebSearchHandler{
			SearXNGURL: searxngURL,
			HTTPClient: http.DefaultClient,
		})
	}

	// Memory handlers
	if hasMemory {
		interceptor.RegisterHandler(&agenttools.MemoryStoreHandler{
			Store:                     s.memoryStore,
			Embedder:                  s.memoryEmbedder,
			TenantID:                  tenantID,
			DefaultEmbeddingModel:     s.memoryEmbeddingModel,
			DefaultEmbeddingDimension: s.memoryEmbeddingDimension,
		})
		interceptor.RegisterHandler(&agenttools.MemoryQueryHandler{
			Store:    s.memoryStore,
			Embedder: s.memoryEmbedder,
			TenantID: tenantID,
		})
	}

	loopInput.Interceptor = interceptor

	// Augment system prompt with spawn capabilities
	var spawnable []SpawnableAgent
	if spawnConfig.Enabled && s.ctx != nil {
		spawnable = listSpawnableAgents(s.ctx, tenantID)
	}
	loopInput.SystemPrompt = augmentCapabilitiesSystemPrompt(
		loopInput.SystemPrompt, spawnConfig,
		agentrt.ForkConfig{}, agentrt.MonitorConfig{},
		spawnable...,
	)

	// Persistent agent memory
	memoryConfig := agentmem.ParseMemoryConfig(agentConfig)
	if memoryConfig != nil && s.agentMemStore != nil {
		extractionModel := ws.Model
		if em, ok := agentConfig["memory_extraction_model"].(string); ok && em != "" {
			extractionModel = em
		}
		loopInput.MemoryProvider = agentmem.NewAgentMemoryProvider(
			s.agentMemStore, s.memoryStore, s.memoryEmbedder,
			requestProviderRouter, extractionModel, *memoryConfig,
		)
	}

	// Wire skills: user-installed + built-in defaults for enabled tools
	{
		installedNames := make(map[string]struct{})
		if skillDefs := agentskills.ParseSkillsConfig(agentConfig); len(skillDefs) > 0 {
			for _, sd := range skillDefs {
				loopInput.Skills = append(loopInput.Skills, agentrt.SkillEntry{
					Name:        sd.Name,
					Description: sd.Description,
					Content:     sd.Content,
				})
				installedNames[sd.Name] = struct{}{}
			}
		}
		// Add built-in skills for enabled tools (skip duplicates)
		for _, bs := range agentskills.ResolveBuiltinSkills(loopInput.Tools) {
			if _, exists := installedNames[bs.Name]; exists {
				continue
			}
			loopInput.Skills = append(loopInput.Skills, agentrt.SkillEntry{
				Name:        bs.Name,
				Description: bs.Description,
				Content:     bs.Content,
			})
		}
		// Register use_skill synthetic tool and provision skills to sandbox
		if len(loopInput.Skills) > 0 && sandboxCtx != nil {
			sandboxCtx.SkillEntries = loopInput.Skills
			interceptor.RegisterAlwaysInclude(&agenttools.UseSkillHandler{
				SandboxCtx:      sandboxCtx,
				SessionID:       sessionID,
				AvailableSkills: loopInput.Skills,
			})
			loopInput.Tools = appendUnique(loopInput.Tools, "use_skill")
		}
	}

	// Wire auto-scoring pipeline
	if s.scoreRecorder != nil {
		loopInput.AutoScorer = autoscorer.DefaultPipeline(s.scoreRecorder, nil)
	}

	// Wire policy evaluator
	userPolicies := agentpolicy.ParsePolicyConfig(agentConfig)
	mergedPolicies := agentpolicy.MergeWithDefaults(userPolicies)
	loopInput.PolicyEvaluator = agentpolicy.NewEvaluator(mergedPolicies)

	// Augment system prompt with tool capabilities summary so the LLM knows what it can do
	loopInput.SystemPrompt = augmentToolCapabilitiesPrompt(loopInput.SystemPrompt, loopInput.Tools)

	// Loop config
	maxHistoryMessages, sessionTokenBudget := resolveAgentLoopLimits(agentConfig)
	maxIterations := int32(0)
	if maxSteps > 0 {
		maxIterations = maxSteps
	}
	turnTimeout := 5 * time.Minute
	if loopInput.HITLConfig != nil {
		approvalDuration := time.Duration(loopInput.HITLConfig.TimeoutSeconds) * time.Second
		if turnTimeout < approvalDuration+time.Minute {
			turnTimeout = approvalDuration + time.Minute
		}
	}

	loopConfig := agentrt.LoopConfig{
		MaxIterations:       maxIterations,
		MaxToolCallsPerTurn: ws.MaxToolCallsPerTurn,
		MaxHistoryMessages:  maxHistoryMessages,
		EnableStreaming:     true,
		TurnTimeout:         turnTimeout,
		SessionTokenBudget:  sessionTokenBudget,
	}

	// Build initial state from previous turns
	hasSystemPrompt := systemPrompt != ""
	streamTurns := trimTurnsToHistoryBudget(swt.Turns, maxHistoryMessages, hasSystemPrompt, 1)
	var messages []gw.Message
	if hasSystemPrompt {
		messages = append(messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(systemPrompt)}},
		})
	}
	for _, t := range streamTurns {
		if t.UserInput.Valid && t.UserInput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.UserInput.String)}},
			})
		}
		if t.AssistantOutput.Valid && t.AssistantOutput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.AssistantOutput.String)}},
			})
		}
	}

	// Drain pending steers
	if pendingSteers := s.sessionMgr.DrainPendingSteers(sessionID, tenantID); len(pendingSteers) > 0 {
		for _, ps := range pendingSteers {
			role := gw.RoleSystem
			if ps.Role == "user" {
				role = gw.RoleUser
			}
			messages = append(messages, gw.Message{
				Role:    role,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(ps.Content)}},
			})
		}
	}

	return &channelLoopResult{
		loopInput:   loopInput,
		loopConfig:  loopConfig,
		spawnConfig: spawnConfig,
		sandboxCfg:  sandboxConfig,
		initialState: &agentrt.LoopState{
			TurnNumber:         int32(len(swt.Turns)),
			Messages:           messages,
			PriorSessionTokens: int(swt.Session.TotalTokens),
		},
	}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────

func buildTrooperIdentityPrompt(ws *wsquery.TrooperReadModel) string {
	var parts []string
	if ws.SoulMD != "" {
		parts = append(parts, "## SOUL.md\n"+ws.SoulMD)
	}
	if ws.IdentityMD != "" {
		parts = append(parts, "## IDENTITY.md\n"+ws.IdentityMD)
	}
	if ws.UserMD != "" {
		parts = append(parts, "## USER.md\n"+ws.UserMD)
	}
	if ws.RoleMD != "" {
		parts = append(parts, "## ROLE.md\n"+ws.RoleMD)
	}
	if len(parts) == 0 {
		return ""
	}
	return "<identity>\n" + strings.Join(parts, "\n") + "\n</identity>"
}

func (s *Server) loadTrooperWithRetry(ctx context.Context, sys *cqrs.System, trooperID, tenantID string) (*wsquery.TrooperReadModel, error) {
	q := wsquery.NewGetTrooperByIDQuery(trooperID, tenantID)
	for attempt := 0; attempt < 5; attempt++ {
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		res, qErr := sys.QueryBus.Execute(queryCtx, q)
		queryCancel()
		if qErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp, ok := res.(*query.Response); ok {
			if data, ok := resp.Data.(*wsquery.TrooperReadModel); ok {
				return data, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("trooper not found (id=%s)", trooperID)
}

// ensureTrooperReadyForMessage ensures a trooper is running before processing
// inbound UI/channel messages. If the trooper is sleeping, it triggers wake and
// waits briefly for status to transition to running.
func (s *Server) ensureTrooperReadyForMessage(ctx context.Context, sys *cqrs.System, trooperID, tenantID string) (*wsquery.TrooperReadModel, error) {
	ws, err := s.loadTrooperWithRetry(ctx, sys, trooperID, tenantID)
	if err != nil {
		return nil, err
	}
	if ws.Status == "running" {
		return ws, nil
	}

	if ws.Status == "sleeping" && s.trooperMgr != nil {
		if wakeErr := s.trooperMgr.Wake(ctx, trooperID); wakeErr != nil {
			logger.WithFields("trooper_id", trooperID, "error", wakeErr.Error()).
				Warn("trooper session: auto-wake failed")
		}
	}

	// Poll read model briefly so callers can proceed without a second request.
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			return ws, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}

		latest, loadErr := s.loadTrooperWithRetry(ctx, sys, trooperID, tenantID)
		if loadErr != nil {
			continue
		}
		ws = latest
		if ws.Status == "running" {
			return ws, nil
		}
	}

	return ws, nil
}

func (s *Server) prepareAndLaunchTrooperSession(
	ctx context.Context,
	sessionID string,
	ws *wsquery.TrooperReadModel,
	result *channelLoopResult,
) (*agentrt.Emitter, error) {
	config := agentrt.SessionRunnerConfig{
		LoopConfig:   result.loopConfig,
		InitialState: result.initialState,
	}

	emitter, err := s.sessionMgr.PrepareSession(ctx, sessionID, "", result.loopInput.TenantID, result.loopInput, config)
	if err != nil {
		return nil, fmt.Errorf("prepare session: %w", err)
	}

	// Wire interceptor handlers that need emitter reference
	if result.loopInput.Interceptor != nil {
		if interceptor, ok := result.loopInput.Interceptor.(*agenttools.ToolInterceptor); ok {
			if result.spawnConfig.Enabled {
				if handler, ok := interceptor.Handlers["spawn_agent"]; ok {
					if spawnHandler, ok := handler.(*agenttools.SpawnAgentHandler); ok {
						spawnHandler.ParentEmitter = emitter
					}
				}
			}
			if result.sandboxCfg.Enabled && s.sandboxMgr != nil {
				agenttools.WireSandboxEmitter(interceptor, emitter)
				agenttools.WireBrowserEmitter(interceptor, emitter)
			}
			if handler, ok := interceptor.Handlers["web_search"]; ok {
				if wsh, ok := handler.(*agenttools.WebSearchHandler); ok {
					wsh.Emitter = emitter
				}
			}
		}
	}

	return emitter, nil
}

// launchTrooperSession launches a prepared trooper session. Call this AFTER
// attaching any event sinks/subscribers to the emitter so no events are missed.
func (s *Server) launchTrooperSession(ctx context.Context, sessionID string, input *agentrt.LoopInput) error {
	return s.sessionMgr.LaunchSession(ctx, sessionID, input)
}
