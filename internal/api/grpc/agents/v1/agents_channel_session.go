package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentpolicy "github.com/everstacklabs/everstack/internal/agents/policy"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	agentskills "github.com/everstacklabs/everstack/internal/agents/skills"
	channelpkg "github.com/everstacklabs/everstack/internal/channels"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/telemetry/autoscorer"
)

// Verify Server implements channels.SessionCreator at compile time.
var _ channelpkg.SessionCreator = (*Server)(nil)

// channelLoopResult holds the outputs of buildChannelSessionLoop.
type channelLoopResult struct {
	loopInput    *agentrt.LoopInput
	loopConfig   agentrt.LoopConfig
	spawnConfig  agenttools.SpawnConfig
	sandboxCfg   sandbox.SandboxConfig
	initialState *agentrt.LoopState
	hasAskUser   bool
}

// CreateChannelSession creates a new agent session from a channel message.
func (s *Server) CreateChannelSession(ctx context.Context, params channelpkg.CreateChannelSessionParams) (string, *agentrt.Emitter, error) {
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

	// 1. Create session via CQRS command
	cmd := agentscmd.NewCreateSessionCommand(params.TenantID, params.AgentID, nil, "", "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", nil, fmt.Errorf("create session command: %w", err)
	}
	sessionID := cmd.ID

	// 2. Update source tracking columns
	if s.db != nil {
		_, err := s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET source = $1, channel_config_id = $2, platform_user_id = $3, platform_user_name = $4 WHERE id = $5`,
			params.Source, params.ChannelConfigID, params.PlatformUserID, params.PlatformUserName, sessionID)
		if err != nil {
			logger.WithFields("session_id", sessionID).WithError(err).Warn("channels: failed to update session source columns")
		}
	}

	// 3. Load session + agent
	swt, err := s.loadSessionWithRetry(ctx, sys, sessionID, params.TenantID)
	if err != nil {
		return "", nil, err
	}
	agent, err := s.loadAgentWithRetry(ctx, sys, params.AgentID, params.TenantID)
	if err != nil {
		return "", nil, err
	}

	// 4. Build loop
	result, err := s.buildChannelSessionLoop(ctx, sessionID, params.TenantID, params.InitialMessage, agent, swt, params.EnableWebSearch)
	if err != nil {
		return "", nil, err
	}

	// 4b. Propagate channel context to sandbox cron handler so crons can
	// notify the originating channel when they fire.
	if params.ChannelRef != "" {
		if ti, ok := result.loopInput.Interceptor.(*agenttools.ToolInterceptor); ok {
			if cronHandler, ok := ti.Handlers["schedule_cron"].(*agenttools.SandboxCronHandler); ok {
				cronHandler.ChannelConfigID = params.ChannelConfigID
				cronHandler.ChannelRef = params.ChannelRef
				cronHandler.ThreadRef = params.ThreadRef
			}
		}
	}

	// 4c. Register channel history tool if the connector supports it
	if params.HistoryFetcher != nil {
		interceptor := result.loopInput.Interceptor
		if interceptor == nil {
			interceptor = agenttools.NewToolInterceptor(s.sessionMgr.GetToolLoop())
			result.loopInput.Interceptor = interceptor
		}
		if ti, ok := interceptor.(*agenttools.ToolInterceptor); ok {
			ti.RegisterAlwaysInclude(&agenttools.ChannelHistoryHandler{
				Fetcher:    params.HistoryFetcher,
				ChannelRef: params.ChannelRef,
				ThreadRef:  params.ThreadRef,
			})
		}

		// Augment system prompt so the agent knows it can read channel history
		result.loopInput.SystemPrompt += "\n\n## Channel Context\nYou are connected to a " + params.Source + " channel. You have a `read_channel_history` tool that lets you read recent messages from this channel or thread. Use it proactively when you need to understand the conversation context — do NOT ask the user for permission, just call the tool. When a user refers to something discussed earlier, always read the history first before responding."
	}

	// 5. Prepare + launch
	emitter, err := s.prepareAndLaunchChannelSession(ctx, sessionID, agent, result)
	if err != nil {
		return "", nil, err
	}

	logger.WithFields(
		"session_id", sessionID,
		"agent_id", params.AgentID,
		"source", params.Source,
		"platform_user", params.PlatformUserName,
	).Info("channels: created channel session")

	return sessionID, emitter, nil
}

// SteerChannelSession injects a user message into an existing session.
// If the session is idle (between turns), it re-launches the session and
// returns a non-nil emitter so the caller can attach sinks.
func (s *Server) SteerChannelSession(ctx context.Context, sessionID, tenantID, message, userName string, options channelpkg.SteerChannelSessionOptions) (*agentrt.Emitter, error) {
	var err error
	ctx, err = channelTenantContext(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if s.sessionMgr == nil {
		return nil, fmt.Errorf("agent session manager not initialized")
	}

	// Check if session runner is still active
	runner := s.sessionMgr.GetRunner(sessionID)
	if runner != nil && runner.IsRunning() {
		// Session is running — deliver steer in-band
		msg := agentrt.SteerMessage{Role: "user", Content: message}
		if err := s.sessionMgr.SteerSession(sessionID, tenantID, msg); err != nil {
			return nil, fmt.Errorf("steer session: %w", err)
		}
		logger.WithFields("session_id", sessionID, "user", userName).
			Info("channels: steered running channel session")
		return nil, nil
	}

	// Session is idle — re-launch for a new turn
	logger.WithFields("session_id", sessionID, "user", userName).
		Info("channels: session idle, re-launching for new turn")

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	// Load session + agent
	swt, err := s.loadSessionWithRetry(ctx, sys, sessionID, tenantID)
	if err != nil {
		return nil, err
	}
	agent, err := s.loadAgentWithRetry(ctx, sys, swt.Session.AgentID.String, tenantID)
	if err != nil {
		return nil, err
	}

	// Update session status to running
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE everstack.agent_sessions SET status = 'running', updated_at = NOW() WHERE id = $1`,
			sessionID)
	}

	// Build loop with the new user message
	result, err := s.buildChannelSessionLoop(ctx, sessionID, tenantID, message, agent, swt, options.EnableWebSearch)
	if err != nil {
		return nil, err
	}

	// Prepare + launch
	emitter, err := s.prepareAndLaunchChannelSession(ctx, sessionID, agent, result)
	if err != nil {
		return nil, err
	}

	logger.WithFields("session_id", sessionID, "user", userName).
		Info("channels: re-launched channel session for new turn")

	return emitter, nil
}

// ─── Helpers ────────────────────────────────────────────────────────

// channelTenantContext binds the trusted tenant carried by an internal
// channel message to the background context used by the agent runner. This is
// required for request-scoped provider resolution after the inbound HTTP
// request has ended. A conflicting existing tenant fails closed.
func channelTenantContext(ctx context.Context, tenantID string) (context.Context, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("channel tenant ID is required")
	}
	if existing := contextkeys.GetTenantID(ctx); existing != "" {
		if existing != tenantID {
			return nil, fmt.Errorf("channel tenant context mismatch")
		}
		return ctx, nil
	}
	return contextkeys.WithTenantID(ctx, tenantID), nil
}

func (s *Server) loadSessionWithRetry(ctx context.Context, sys *cqrs.System, sessionID, tenantID string) (*agentsquery.SessionWithTurns, error) {
	sessionQ := agentsquery.NewGetSessionByIDQuery(sessionID, tenantID)
	for attempt := 0; attempt < 10; attempt++ {
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		res, qErr := sys.QueryBus.Execute(queryCtx, sessionQ)
		queryCancel()
		if qErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp, ok := res.(*query.Response); ok {
			if data, ok := resp.Data.(*agentsquery.SessionWithTurns); ok {
				return data, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("session not found (id=%s)", sessionID)
}

func (s *Server) loadAgentWithRetry(ctx context.Context, sys *cqrs.System, agentID, tenantID string) (*agentsquery.AgentDefinitionReadModel, error) {
	agentQ := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	for attempt := 0; attempt < 5; attempt++ {
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		agentRes, qErr := sys.QueryBus.Execute(queryCtx, agentQ)
		queryCancel()
		if qErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp, ok := agentRes.(*query.Response); ok {
			if data, ok := resp.Data.(*agentsquery.AgentDefinitionReadModel); ok {
				return data, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("agent definition not found (id=%s)", agentID)
}

func (s *Server) buildChannelSessionLoop(
	ctx context.Context,
	sessionID, tenantID, userInput string,
	agent *agentsquery.AgentDefinitionReadModel,
	swt *agentsquery.SessionWithTurns,
	requestWebSearch bool,
) (*channelLoopResult, error) {
	if s.engine == nil {
		return nil, fmt.Errorf("agent runtime engine not initialized")
	}
	requestProviderRegistry, requestProviderRouter, err := s.engine.ProvidersForContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve channel providers: %w", err)
	}

	agentConfig := agenttools.AgentRuntimeConfig(agent)
	agentMode := strings.ToLower(strings.TrimSpace(agent.Mode))
	if agentMode == "" {
		agentMode = "primary"
	}
	taskPermissionMode := "auto"
	if pm, ok := agentConfig["task_permission_mode"].(string); ok && pm != "" {
		taskPermissionMode = pm
	}
	workingDirectory := ""
	if agent.WorkingDirectory.Valid {
		workingDirectory = strings.TrimSpace(agent.WorkingDirectory.String)
	}
	var maxSteps int32
	if agent.MaxSteps.Valid && agent.MaxSteps.Int32 > 0 {
		maxSteps = agent.MaxSteps.Int32
	}

	sampling := gw.SamplingParams{}
	if temp, ok := agentConfig["temperature"].(float64); ok {
		sampling.Temperature = temp
	}
	if maxTokens, ok := agentConfig["max_tokens"].(float64); ok {
		sampling.MaxTokens = int(maxTokens)
	}

	loopInput := &agentrt.LoopInput{
		TenantID:           tenantID,
		AgentID:            agent.ID,
		SessionID:          sessionID,
		Model:              agent.Model,
		SystemPrompt:       agent.SystemPrompt.String,
		Tools:              agent.Tools,
		Sampling:           sampling,
		UserInput:          userInput,
		AgentMode:          agentMode,
		TaskPermissionMode: taskPermissionMode,
		WorkingDirectory:   workingDirectory,
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
	hitlConfig := agentrt.ParseHITLConfig(agentConfig)
	if hitlConfig != nil {
		loopInput.HITLConfig = hitlConfig
	}

	// Spawn + sandbox config
	spawnConfig := agenttools.ParseSpawnConfig(agentConfig)
	sandboxConfig := sandbox.ParseSandboxConfig(agentConfig)
	executionMode, persistenceMode, templateConfigured := classifySessionModes(agentConfig, sandboxConfig)
	loopInput.ExecutionMode = executionMode
	loopInput.PersistenceMode = persistenceMode
	loopInput.SandboxEnabled = sandboxConfig.Enabled
	loopInput.GitRepoConfigured = strings.TrimSpace(sandboxConfig.GitRepoURL) != ""
	loopInput.TemplateConfigured = templateConfigured

	hasMemory := s.memoryStore != nil && s.memoryEmbedder != nil
	searxngURL := os.Getenv("EVS_SEARXNG_URL")
	// Web tools default-on: enabled whenever a self-hosted SearXNG instance is
	// configured (EVS_SEARXNG_URL), independent of the per-request toggle
	// (requestWebSearch is retained in the signature but no longer gates).
	enableWebSearch := searxngURL != ""
	if sandboxConfig.Enabled {
		loopInput.SystemPrompt = augmentSandboxSystemPrompt(loopInput.SystemPrompt, sandboxConfig)
	}

	// web_fetch is default-on for every agent (needs no SearXNG); web_search is
	// added only when a SearXNG instance is configured.
	loopInput.Tools = appendUnique(loopInput.Tools, "web_fetch")
	if enableWebSearch {
		loopInput.Tools = appendUnique(loopInput.Tools, "web_search")
	}

	// Check if ask_user is in tools list
	hasAskUser := false
	for _, t := range agent.Tools {
		if t == "ask_user" {
			hasAskUser = true
			break
		}
	}

	// Build interceptor — always, since web_fetch is default-on for every agent.
	var interceptor *agenttools.ToolInterceptor
	{
		interceptor = agenttools.NewToolInterceptor(s.sessionMgr.GetToolLoop())

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
			// Ensure spawn_agent is in the tools allowlist so the interceptor's
			// BuildToolDefinitions includes it for the LLM.
			loopInput.Tools = appendUnique(loopInput.Tools, "spawn_agent")

			// Phase 1 (Job): wire async job queue if spawn.async is enabled
			if spawnConfig.Async {
				jobQueue := s.sessionMgr.GetOrCreateJobQueue(sessionID, spawnConfig.MaxConcurrentJobs)
				spawnHandler.JobQueue = jobQueue
				loopInput.JobResultCh = jobQueue.ResultCh()
				interceptor.RegisterHandler(&agenttools.CheckJobHandler{JobQueue: jobQueue})
				loopInput.Tools = appendUnique(loopInput.Tools, "check_job")
			}
		}

		var sandboxCtx *agenttools.SandboxSessionContext
		if sandboxConfig.Enabled && s.sandboxMgr != nil {
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
				Manager:                 s.sandboxMgr,
				SessionID:               sessionID,
				TenantID:                tenantID,
				Config:                  clampedConfig,
				SessionStartedAt:        time.Now(),
				ExecutionMode:           executionMode,
				PersistenceMode:         persistenceMode,
				AllowedWorkingDirectory: workingDirectory,
				PortExposureBaseDomain:  s.portExposureBaseDomain,
				PortExposureTLSEnabled:  s.portExposureTLSEnabled,
				PortExposureListenPort:  s.portExposureListenPort,
				AgentID:                 agent.ID,
			}
			for _, h := range agenttools.NewSandboxHandlers(sandboxCtx) {
				interceptor.RegisterHandler(h)
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

		// Register web search/fetch handlers (standalone — not sandbox-gated).
		// web_fetch is default-on; its nil HTTPClient uses the SSRF-guarded
		// client. web_search needs a configured SearXNG.
		interceptor.RegisterHandler(&agenttools.WebFetchHandler{})
		if enableWebSearch {
			interceptor.RegisterHandler(&agenttools.WebSearchHandler{
				SearXNGURL: searxngURL,
				HTTPClient: http.DefaultClient,
			})
		}

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

		if err := s.registerProjectFunctions(
			ctx, interceptor, sandboxCtx, tenantID, agent.ID, sessionID, agentConfig, &loopInput.Tools,
		); err != nil {
			return nil, fmt.Errorf("register project functions: %w", err)
		}

		loopInput.Interceptor = interceptor
	}

	// Augment system prompt with autonomous capability guidance (spawn, etc.)
	var spawnable []SpawnableAgent
	if spawnConfig.Enabled {
		spawnable = listSpawnableAgents(ctx, tenantID)
	}
	loopInput.SystemPrompt = augmentCapabilitiesSystemPrompt(
		loopInput.SystemPrompt, spawnConfig,
		agentrt.ForkConfig{}, agentrt.MonitorConfig{},
		spawnable...,
	)
	loopInput.SystemPrompt = augmentToolCapabilitiesPrompt(loopInput.SystemPrompt, loopInput.Tools)

	// Persistent agent memory
	memoryConfig := agentmem.ParseMemoryConfig(agentConfig)
	if memoryConfig != nil && s.agentMemStore != nil {
		extractionModel := agent.Model
		if em, ok := agentConfig["memory_extraction_model"].(string); ok && em != "" {
			extractionModel = em
		}
		loopInput.MemoryProvider = agentmem.NewAgentMemoryProvider(
			s.agentMemStore, s.memoryStore, s.memoryEmbedder,
			requestProviderRouter, extractionModel, *memoryConfig,
		)
	}

	// Wire auto-scoring pipeline
	if s.scoreRecorder != nil {
		loopInput.AutoScorer = autoscorer.DefaultPipeline(s.scoreRecorder, nil)
	}

	// Wire policy evaluator
	userPolicies := agentpolicy.ParsePolicyConfig(agentConfig)
	mergedPolicies := agentpolicy.MergeWithDefaults(userPolicies)
	loopInput.PolicyEvaluator = agentpolicy.NewEvaluator(mergedPolicies)

	// Loop config
	maxHistoryMessages, sessionTokenBudget := resolveAgentLoopLimits(agentConfig)
	maxIterations := int32(0)
	if maxSteps > 0 {
		maxIterations = maxSteps
	}
	turnTimeout := 5 * time.Minute
	if hitlConfig != nil {
		approvalDuration := time.Duration(hitlConfig.TimeoutSeconds) * time.Second
		if turnTimeout < approvalDuration+time.Minute {
			turnTimeout = approvalDuration + time.Minute
		}
	}

	loopConfig := agentrt.LoopConfig{
		MaxIterations:       maxIterations,
		MaxToolCallsPerTurn: agent.MaxToolCallsPerTurn,
		MaxHistoryMessages:  maxHistoryMessages,
		EnableStreaming:     true,
		TurnTimeout:         turnTimeout,
		SessionTokenBudget:  sessionTokenBudget,
	}

	// Build initial state from previous turns
	hasSystemPrompt := agent.SystemPrompt.Valid && agent.SystemPrompt.String != ""
	streamTurns := trimTurnsToHistoryBudget(swt.Turns, maxHistoryMessages, hasSystemPrompt, 1)
	var messages []gw.Message
	if hasSystemPrompt {
		messages = append(messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(agent.SystemPrompt.String)}},
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
		hasAskUser:  hasAskUser,
		initialState: &agentrt.LoopState{
			TurnNumber:         int32(len(swt.Turns)),
			Messages:           messages,
			PriorSessionTokens: int(swt.Session.TotalTokens),
		},
	}, nil
}

func (s *Server) prepareAndLaunchChannelSession(
	ctx context.Context,
	sessionID string,
	agent *agentsquery.AgentDefinitionReadModel,
	result *channelLoopResult,
) (*agentrt.Emitter, error) {
	config := agentrt.SessionRunnerConfig{
		LoopConfig:   result.loopConfig,
		InitialState: result.initialState,
	}

	emitter, err := s.sessionMgr.PrepareSession(ctx, sessionID, agent.ID, result.loopInput.TenantID, result.loopInput, config)
	if err != nil {
		return nil, fmt.Errorf("prepare session: %w", err)
	}

	// PrepareSession may have waited for an interrupted runner to finish.
	// The async CQRS projection (INSERT into agent_session_turns) runs in a
	// goroutine and may have completed since we loaded swt.Turns. Re-query
	// the actual max turn_number to prevent collisions.
	if s.db != nil {
		s.reconcileTurnNumber(ctx, sessionID, result.initialState)
	}

	// Wire interceptor handlers that need emitter reference.
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

			// Wire web handler emitters
			if handler, ok := interceptor.Handlers["web_search"]; ok {
				if wsh, ok := handler.(*agenttools.WebSearchHandler); ok {
					wsh.Emitter = emitter
				}
			}
			if _, ok := interceptor.Handlers["web_fetch"]; ok {
				// web_fetch currently does not emit events
			}

			// Register ask_user handler for channel sessions — questions are
			// sent to the messaging platform and replies come back as messages.
			if result.hasAskUser {
				interceptor.RegisterAlwaysInclude(&agenttools.AskUserHandler{
					Emitter:    emitter,
					SessionID:  sessionID,
					RequestCh:  result.loopInput.UserInputReqCh,
					ResponseCh: result.loopInput.UserInputRespCh,
				})
				result.loopInput.Tools = appendUnique(result.loopInput.Tools, "ask_user")
			}
		}
	}

	// For persistent agents, mark lifecycle_status = 'running' on turn start.
	// CONCURRENT_RUNNING is enforced at this transition, excluding the agent
	// itself so a new turn on an already-running agent is never blocked.
	isPersistent := agent.LifecycleMode == "persistent"
	if isPersistent && s.db != nil {
		if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
			enterprise.UsageTypeConcurrentRunning,
			`SELECT COUNT(*) FROM agent_definitions WHERE tenant_id = $1 AND deleted_at IS NULL AND lifecycle_status = 'running' AND id <> $2`,
			[]interface{}{result.loopInput.TenantID, agent.ID}, 1, "concurrently running agent"); err != nil {
			return nil, err
		}
		s.db.ExecContext(ctx, `UPDATE agent_definitions SET lifecycle_status = 'running', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agent.ID, result.loopInput.TenantID)
	}

	// Launch in goroutine so caller can attach sinks before events flow
	go func() {
		if launchErr := s.sessionMgr.LaunchSession(ctx, sessionID, result.loopInput); launchErr != nil {
			logger.WithFields("session_id", sessionID).WithError(launchErr).
				Error("channels: failed to launch session")
		}
		// Revert lifecycle_status to 'idle' when the turn completes
		if isPersistent && s.db != nil {
			s.db.ExecContext(context.Background(),
				`UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND lifecycle_status = 'running' AND tenant_id = $2`,
				agent.ID, result.loopInput.TenantID)
		}
	}()

	return emitter, nil
}

// reconcileTurnNumber re-queries the actual max turn_number from the
// agent_session_turns table and updates the initial loop state if the
// projected data shows a higher turn number than what was computed from
// the (potentially stale) swt.Turns snapshot. This prevents turn_number
// collisions when a new turn starts right after an interrupted turn's
// checkpoint has been projected asynchronously.
func (s *Server) reconcileTurnNumber(ctx context.Context, sessionID string, state *agentrt.LoopState) {
	if state == nil {
		return
	}
	var dbMax int32
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(turn_number), 0) FROM agent_session_turns WHERE session_id = $1`,
		sessionID)
	if err := row.Scan(&dbMax); err != nil {
		return // best-effort
	}
	if dbMax > state.TurnNumber {
		logger.WithFields(
			"session_id", sessionID,
			"stale_turn", state.TurnNumber,
			"actual_turn", dbMax,
		).Warn("reconcileTurnNumber: corrected stale TurnNumber")
		state.TurnNumber = dbMax
	}
}

// SubmitUserInput delivers a user's response to a pending ask_user call.
func (s *Server) SubmitUserInput(ctx context.Context, inputID, text string) error {
	if s.sessionMgr == nil {
		return fmt.Errorf("agent session manager not initialized")
	}
	return s.sessionMgr.SubmitUserInput(ctx, inputID, text)
}
