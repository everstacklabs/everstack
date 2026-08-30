package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// SessionCreator is the interface for creating agent sessions.
// This is implemented by the agents gRPC server.
type SessionCreator interface {
	CreateChannelSession(ctx context.Context, params CreateChannelSessionParams) (sessionID string, emitter *agentrt.Emitter, err error)
	// CreateTrooperChannelSession creates a trooper session from a channel message.
	CreateTrooperChannelSession(ctx context.Context, params CreateTrooperChannelSessionParams) (sessionID string, emitter *agentrt.Emitter, err error)
	// SteerChannelSession injects a user message into an existing session.
	// If the session is idle (between turns), it re-launches the session loop
	// and returns a non-nil emitter so the caller can attach sinks.
	// If the session is already running, the steer is delivered in-band and emitter is nil.
	SteerChannelSession(ctx context.Context, sessionID, tenantID, message, userName string, options SteerChannelSessionOptions) (*agentrt.Emitter, error)
	// SubmitUserInput delivers a user's response to a pending ask_user call.
	SubmitUserInput(ctx context.Context, inputID, text string) error
}

// CreateChannelSessionParams holds parameters for creating a session from a channel.
type CreateChannelSessionParams struct {
	AgentID          string
	TenantID         string
	InitialMessage   string
	Source           string // discord, slack, telegram
	ChannelConfigID  string
	PlatformUserID   string
	PlatformUserName string
	EnableWebSearch  bool

	// Optional: connector that supports reading channel history.
	// If set, the agent gets a read_channel_history tool.
	HistoryFetcher HistoryFetcher
	ChannelRef     string // Platform channel ID for history fetching
	ThreadRef      string // Platform thread ref for history fetching
}

// CreateTrooperChannelSessionParams holds parameters for creating a trooper session from a channel.
type CreateTrooperChannelSessionParams struct {
	TrooperID        string
	TenantID         string
	InitialMessage   string
	Source           string // discord, slack, telegram
	ChannelConfigID  string
	PlatformUserID   string
	PlatformUserName string
}

// SteerChannelSessionOptions configures channel turn behavior.
type SteerChannelSessionOptions struct {
	EnableWebSearch bool
}

// ConnectorFactory creates a Connector for the given config.
type ConnectorFactory func(cfg ConnectorConfig, handler MessageHandler) (Connector, error)

// ChannelManager manages the lifecycle of all channel connectors.
// It starts/stops connectors, handles hot-reload on config changes,
// and routes inbound messages to agent sessions.
type ChannelManager struct {
	store          ChannelStore
	router         *ChannelRouter
	sessionCreator SessionCreator
	db             *sqlx.DB
	instanceID     string

	// Agent dispatcher for channels without a default agent
	dispatcher  Dispatcher
	agentLister AgentLister

	// Connector factories by platform
	factories map[Platform]ConnectorFactory

	// Active connectors by channel config ID
	mu         sync.RWMutex
	connectors map[string]*activeConnector

	// Pending ask_user inputs: sessionID → inputID
	// When a session has a pending ask_user, the next message should
	// be delivered as a SubmitUserInput instead of a steer.
	pendingInputsMu sync.RWMutex
	pendingInputs   map[string]string // sessionID → inputID

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Credentials encryption key (AES-256)
	encryptionKey []byte

	// License monitor for MESSAGES_MONTHLY. Connector events arrive on a
	// background context with no tenant config, so entitlements cannot be
	// resolved from the request the way an RPC handler does; the monitor is
	// held directly instead. Nil disables the ceiling.
	licenseMonitor enterprise.LicenseMonitor

	// Last over-quota notice per channel config, so a channel at its cap
	// posts one upgrade notice per window instead of one per message.
	quotaNoticeMu sync.Mutex
	quotaNoticeAt map[string]time.Time

	// Short-lived monthly message counts per tenant, so the allowance check
	// does not run a COUNT over the tenant's month on every inbound message.
	messageCountMu sync.Mutex
	messageCounts  map[string]*cachedMessageCount
}

// cachedMessageCount is one tenant's month-to-date message count. month is the
// UTC "2006-01" the count belongs to, so a month rollover invalidates it
// rather than carrying last month's total into the new period.
type cachedMessageCount struct {
	count     int64
	month     string
	fetchedAt time.Time
}

// quotaNoticeInterval bounds how often one channel tells its users it is over
// the monthly message allowance.
const quotaNoticeInterval = 6 * time.Hour

// messageCountTTL bounds how stale the cached monthly count may be.
const messageCountTTL = time.Minute

type activeConnector struct {
	connector   Connector
	reconnector *ReconnectingConnector
	config      ConnectorConfig
	cancel      context.CancelFunc
}

// NewChannelManager creates a new ChannelManager.
// Pass a non-nil dispatcher and agentLister to enable dynamic agent dispatch
// on channels without a default agent.
func NewChannelManager(store ChannelStore, sessionCreator SessionCreator, db *sqlx.DB, instanceID string, encryptionKey []byte, dispatcher Dispatcher, agentLister AgentLister) *ChannelManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ChannelManager{
		store:          store,
		router:         NewChannelRouter(store),
		sessionCreator: sessionCreator,
		db:             db,
		instanceID:     instanceID,
		dispatcher:     dispatcher,
		agentLister:    agentLister,
		factories:      make(map[Platform]ConnectorFactory),
		connectors:     make(map[string]*activeConnector),
		pendingInputs:  make(map[string]string),
		quotaNoticeAt:  make(map[string]time.Time),
		messageCounts:  make(map[string]*cachedMessageCount),
		ctx:            ctx,
		cancel:         cancel,
		encryptionKey:  encryptionKey,
	}
}

// SetLicenseMonitor wires the monitor used to resolve MESSAGES_MONTHLY.
// Without it the manager meters messages but enforces no ceiling.
func (m *ChannelManager) SetLicenseMonitor(monitor enterprise.LicenseMonitor) {
	m.licenseMonitor = monitor
}

// RegisterFactory registers a connector factory for a platform.
func (m *ChannelManager) RegisterFactory(platform Platform, factory ConnectorFactory) {
	m.factories[platform] = factory
}

// Start loads all enabled channel configs and starts their connectors.
func (m *ChannelManager) Start(ctx context.Context) error {
	configs, err := m.store.ListEnabledChannelConfigs(ctx)
	if err != nil {
		return fmt.Errorf("load channel configs: %w", err)
	}

	logger.WithFields("count", len(configs)).Info("channels: starting connectors")

	for _, cfg := range configs {
		// Check instance affinity
		if cfg.InstanceAffinity != "" && cfg.InstanceAffinity != m.instanceID {
			logger.WithFields("channel", cfg.Name, "affinity", cfg.InstanceAffinity, "instance", m.instanceID).
				Debug("channels: skipping connector (affinity mismatch)")
			continue
		}

		if err := m.startConnector(cfg); err != nil {
			logger.WithFields("channel", cfg.Name, "platform", cfg.Platform).WithError(err).
				Warn("channels: failed to start connector")
		}
	}

	// Start idle session reaper
	go m.reaperLoop()

	return nil
}

// Stop gracefully stops all active connectors.
func (m *ChannelManager) Stop(ctx context.Context) error {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, ac := range m.connectors {
		logger.WithFields("channel", ac.config.Name).Info("channels: stopping connector")
		ac.cancel()
		if err := ac.reconnector.Stop(ctx); err != nil {
			logger.WithFields("channel", ac.config.Name).WithError(err).Warn("channels: error stopping connector")
		}
		delete(m.connectors, id)
	}

	return nil
}

// ReloadConfig reloads a specific channel config — stops old connector, starts new one if enabled.
func (m *ChannelManager) ReloadConfig(ctx context.Context, channelConfigID, tenantID string) error {
	// Stop existing connector if running
	m.stopConnector(channelConfigID)

	// Load fresh config
	cfg, err := m.store.GetChannelConfig(ctx, channelConfigID, tenantID)
	if err != nil {
		return fmt.Errorf("load channel config: %w", err)
	}
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	// Check instance affinity
	if cfg.InstanceAffinity != "" && cfg.InstanceAffinity != m.instanceID {
		return nil
	}

	return m.startConnector(cfg)
}

// GetStatus returns the status of a channel connector.
func (m *ChannelManager) GetStatus(channelConfigID string) ConnectorStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ac, ok := m.connectors[channelConfigID]
	if !ok {
		return StatusDisconnected
	}
	return ac.connector.Status()
}

// GetConnector returns the live connector for a channel config, or nil if not running.
func (m *ChannelManager) GetConnector(channelConfigID string) Connector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ac, ok := m.connectors[channelConfigID]; ok {
		return ac.connector
	}
	return nil
}

// GetAllStatuses returns statuses for all active connectors.
func (m *ChannelManager) GetAllStatuses() map[string]ConnectorStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]ConnectorStatus, len(m.connectors))
	for id, ac := range m.connectors {
		statuses[id] = ac.connector.Status()
	}
	return statuses
}

func (m *ChannelManager) startConnector(cfg *ChannelConfigRecord) error {
	platform := Platform(cfg.Platform)
	factory, ok := m.factories[platform]
	if !ok {
		return fmt.Errorf("no factory registered for platform: %s", platform)
	}

	// Decrypt credentials
	creds, err := m.decryptCredentials(cfg.CredentialsEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}

	// Parse platform config
	var platformConfig map[string]interface{}
	if len(cfg.PlatformConfig) > 0 {
		if err := json.Unmarshal(cfg.PlatformConfig, &platformConfig); err != nil {
			return fmt.Errorf("parse platform config: %w", err)
		}
	}

	connCfg := ConnectorConfig{
		ID:              cfg.ID,
		TenantID:        cfg.TenantID,
		AgentID:         cfg.AgentID.String, // empty string when NULL (dispatcher mode)
		Platform:        platform,
		Name:            cfg.Name,
		SessionMode:     SessionMode(cfg.SessionMode),
		Credentials:     creds,
		PlatformConfig:  platformConfig,
		MaxMsgPerMinute: cfg.MaxMessagesPerMinute,
		MaxSessions:     cfg.MaxSessionsPerUser,
		ResponseFormat:  cfg.ResponseFormat,
		MaxResponseLen:  cfg.MaxResponseLength,
		MaxTokensPerDay: cfg.MaxTokensPerDay,
		IdleSessionTTL:  time.Duration(cfg.IdleSessionTTLSeconds) * time.Second,
		CoalesceWindow:  time.Duration(cfg.CoalesceWindowMs) * time.Millisecond,
	}

	// Create connector with message handler
	connector, err := factory(connCfg, m.handleMessage)
	if err != nil {
		return fmt.Errorf("create connector: %w", err)
	}

	connCtx, connCancel := context.WithCancel(m.ctx)

	// Set interaction handler for HITL approval buttons
	type interactable interface {
		SetInteractionHandler(InteractionHandler)
	}
	if ic, ok := connector.(interactable); ok {
		ic.SetInteractionHandler(m.handleInteraction)
	}

	ac := &activeConnector{
		connector: connector,
		config:    connCfg,
		cancel:    connCancel,
	}

	// Wrap with auto-reconnect. The factory propagates the interaction handler
	// to newly created connectors and updates the activeConnector reference so
	// that Send calls from the router always use the live connector.
	reconnector := NewReconnectingConnector(connector, func() (Connector, error) {
		newConn, err := factory(connCfg, m.handleMessage)
		if err != nil {
			return nil, err
		}

		// Propagate interaction handler to the new connector
		type interactable interface {
			SetInteractionHandler(InteractionHandler)
		}
		if ic, ok := newConn.(interactable); ok {
			ic.SetInteractionHandler(m.handleInteraction)
		}

		// Update the active connector reference so Send/Status use the new instance
		m.mu.Lock()
		ac.connector = newConn
		m.mu.Unlock()

		return newConn, nil
	}, DefaultReconnectConfig())

	ac.reconnector = reconnector

	m.mu.Lock()
	m.connectors[cfg.ID] = ac
	m.mu.Unlock()

	// Start in background with auto-reconnect
	go func() {
		if err := reconnector.Start(connCtx); err != nil {
			logger.WithFields("channel", cfg.Name, "platform", cfg.Platform).WithError(err).
				Error("channels: connector stopped with error")
		}
	}()

	mode := "default"
	if !cfg.AgentID.Valid {
		mode = "dispatcher"
	}
	logger.WithFields("channel", cfg.Name, "platform", cfg.Platform, "agent_id", cfg.AgentID.String, "mode", mode).
		Info("channels: connector started")

	return nil
}

func (m *ChannelManager) stopConnector(channelConfigID string) {
	m.mu.Lock()
	ac, ok := m.connectors[channelConfigID]
	if ok {
		delete(m.connectors, channelConfigID)
	}
	m.mu.Unlock()

	if ok {
		ac.cancel()
		_ = ac.reconnector.Stop(context.Background())
	}
}

// handleMessage is called by connectors when they receive a platform message.
// It resolves the message to a session and routes it to the agent runtime.
func (m *ChannelManager) handleMessage(ctx context.Context, msg InboundMessage) error {
	m.mu.RLock()
	ac, ok := m.connectors[msg.ChannelConfigID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("connector not found for channel: %s", msg.ChannelConfigID)
	}

	if !m.meterMessage(ctx, msg) {
		return nil
	}

	intent := DetectWebSearchIntent(msg.Text)
	if intent.Text != "" {
		msg.Text = intent.Text
	}
	if intent.Requested {
		msg.WebSearchRequested = true
	}

	// Web tools are default-on in the loop builder (see buildChannelSessionLoop),
	// so this channel-level policy no longer gates tool availability; it is kept
	// for connector-level intent/auto-retry signaling. The backend is a
	// self-hosted SearXNG instance (EVS_SEARXNG_URL), not the retired Brave key.
	enableWebSearch, autoRetryWebSearch := resolveWebSearchPolicy(ac.config, msg)
	if !enableWebSearch && autoRetryWebSearch && msg.WebSearchRequested && os.Getenv("EVS_SEARXNG_URL") != "" {
		enableWebSearch = true
	}

	// Resolve to session — check for an existing session BEFORE running the
	// dispatcher so that subsequent messages in an ongoing conversation skip
	// the agent-selection flow entirely.
	resolution, err := m.router.ResolveSession(ctx, msg)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}

	if resolution.IsNew {
		// ── Trooper routing: check if channel is bound to a trooper ──
		if msg.AgentID == "" && msg.TrooperID == "" && m.db != nil {
			var wsID string
			_ = m.db.GetContext(ctx, &wsID,
				`SELECT trooper_id::text FROM trooper_channel_bindings
				 WHERE channel_config_id = $1 AND enabled = true LIMIT 1`,
				msg.ChannelConfigID)
			if wsID != "" {
				msg.TrooperID = wsID
			}
		}

		// If routed to a trooper, create a trooper session
		if msg.TrooperID != "" {
			wsParams := CreateTrooperChannelSessionParams{
				TrooperID:        msg.TrooperID,
				TenantID:         msg.TenantID,
				InitialMessage:   msg.Text,
				Source:           string(msg.Platform),
				ChannelConfigID:  msg.ChannelConfigID,
				PlatformUserID:   msg.PlatformUserID,
				PlatformUserName: msg.PlatformUserName,
			}
			sessionID, emitter, err := m.sessionCreator.CreateTrooperChannelSession(ctx, wsParams)
			if err != nil {
				return fmt.Errorf("create trooper session: %w", err)
			}

			sink := NewChannelSink(ac.connector, msg.PlatformChannelRef, msg.PlatformThreadRef, ac.config)
			sink.SetPendingInputCallback(m.registerPendingInput)
			emitter.AddSinkWithReplay(sink)

			if err := m.router.RecordMapping(ctx, msg, sessionID); err != nil {
				logger.WithError(err).Warn("channels: failed to record session mapping")
			}

			logger.WithFields(
				"session_id", sessionID,
				"trooper_id", msg.TrooperID,
				"channel", ac.config.Name,
				"platform", msg.Platform,
			).Info("channels: new trooper session created from platform message")
			return nil
		}

		// ── Dispatcher: resolve agent when channel has no default ──
		// Only runs for NEW sessions — existing sessions already have an agent.
		if msg.AgentID == "" && m.dispatcher != nil && m.agentLister != nil {
			agents, err := m.agentLister.ListAgents(ctx, msg.TenantID)
			if err != nil {
				return fmt.Errorf("list agents for dispatch: %w", err)
			}

			sendFn := func(ctx context.Context, channelRef, threadRef string, outMsg OutboundMessage) (string, error) {
				return ac.connector.Send(ctx, channelRef, threadRef, outMsg)
			}

			result, err := m.dispatcher.Dispatch(ctx, msg, agents, sendFn)
			if err != nil {
				return fmt.Errorf("dispatch: %w", err)
			}
			if result.Pending {
				// HITL buttons sent, no session yet — wait for button click
				return nil
			}
			msg.AgentID = result.AgentID
		}

		// Create a new agent session
		createParams := CreateChannelSessionParams{
			AgentID:          msg.AgentID,
			TenantID:         msg.TenantID,
			InitialMessage:   msg.Text,
			Source:           string(msg.Platform),
			ChannelConfigID:  msg.ChannelConfigID,
			PlatformUserID:   msg.PlatformUserID,
			PlatformUserName: msg.PlatformUserName,
			EnableWebSearch:  enableWebSearch,
			ChannelRef:       msg.PlatformChannelRef,
			ThreadRef:        msg.PlatformThreadRef,
		}
		// If the connector supports history fetching, pass it through
		if hf, ok := ac.connector.(HistoryFetcher); ok {
			createParams.HistoryFetcher = hf
		}
		sessionID, emitter, err := m.sessionCreator.CreateChannelSession(ctx, createParams)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}

		// Attach channel sink to emitter for streaming responses back
		sink := NewChannelSink(ac.connector, msg.PlatformChannelRef, msg.PlatformThreadRef, ac.config)
		sink.SetPendingInputCallback(m.registerPendingInput)
		emitter.AddSinkWithReplay(sink)

		// Record the session mapping
		if err := m.router.RecordMapping(ctx, msg, sessionID); err != nil {
			logger.WithError(err).Warn("channels: failed to record session mapping")
		}

		logger.WithFields(
			"session_id", sessionID,
			"channel", ac.config.Name,
			"platform", msg.Platform,
			"user", msg.PlatformUserName,
		).Info("channels: new session created from platform message")
	} else {
		// Check if this session has a pending ask_user — if so, deliver as user input
		m.pendingInputsMu.RLock()
		inputID, hasPending := m.pendingInputs[resolution.SessionID]
		m.pendingInputsMu.RUnlock()

		if hasPending {
			// Clear the pending input
			m.pendingInputsMu.Lock()
			delete(m.pendingInputs, resolution.SessionID)
			m.pendingInputsMu.Unlock()

			logger.WithFields(
				"session_id", resolution.SessionID,
				"input_id", inputID,
				"user", msg.PlatformUserName,
			).Info("channels: delivering message as ask_user response")

			if err := m.sessionCreator.SubmitUserInput(ctx, inputID, msg.Text); err != nil {
				return fmt.Errorf("submit user input: %w", err)
			}
			return nil
		}

		// Steer existing session with new message
		messageText := msg.Text
		if msg.SessionMode == SessionModeShared {
			// Prefix with username for shared sessions
			messageText = fmt.Sprintf("[%s]: %s", msg.PlatformUserName, msg.Text)
		}

		emitter, err := m.sessionCreator.SteerChannelSession(ctx, resolution.SessionID, msg.TenantID, messageText, msg.PlatformUserName, SteerChannelSessionOptions{EnableWebSearch: enableWebSearch})
		if err != nil {
			return fmt.Errorf("steer session: %w", err)
		}

		// If emitter is non-nil, the session was idle and re-launched — attach channel sink
		if emitter != nil {
			sink := NewChannelSink(ac.connector, msg.PlatformChannelRef, msg.PlatformThreadRef, ac.config)
			sink.SetPendingInputCallback(m.registerPendingInput)
			emitter.AddSinkWithReplay(sink)
		}
	}

	return nil
}

func resolveWebSearchPolicy(cfg ConnectorConfig, msg InboundMessage) (bool, bool) {
	autoRetry := false
	if raw, ok := cfg.PlatformConfig["web_search_auto_retry"]; ok {
		if enabled, ok := raw.(bool); ok {
			autoRetry = enabled
		}
	}
	if !autoRetry && cfg.Platform == PlatformSlack {
		autoRetry = true
	}

	if os.Getenv("EVS_SEARXNG_URL") == "" {
		return false, autoRetry
	}

	mode := ""
	if raw, ok := cfg.PlatformConfig["web_search_mode"]; ok {
		if s, ok := raw.(string); ok {
			mode = strings.ToLower(strings.TrimSpace(s))
		}
	}

	if mode == "" {
		if raw, ok := cfg.PlatformConfig["web_search_enabled"]; ok {
			if enabled, ok := raw.(bool); ok {
				mode = ternary(enabled, "on", "off")
			}
		}
	}

	if mode == "" {
		if cfg.Platform == PlatformSlack {
			mode = "auto"
		} else {
			mode = "off"
		}
	}

	switch mode {
	case "on", "enabled", "true":
		return true, autoRetry
	case "auto":
		return msg.WebSearchRequested, autoRetry
	default:
		return false, autoRetry
	}
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// handleInteraction handles button click interactions from platforms.
// Supports both dispatch agent selection ("dispatch:id:agentID") and
// HITL approve/deny ("approve:reviewID" / "deny:reviewID").
func (m *ChannelManager) handleInteraction(ctx context.Context, interaction Interaction) error {
	// ── Dispatch interaction: "dispatch:<dispatchID>:<agentID>" ──
	if strings.HasPrefix(interaction.ActionID, "dispatch:") && m.dispatcher != nil {
		parts := strings.SplitN(interaction.ActionID, ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid dispatch action ID: %s", interaction.ActionID)
		}
		dispatchID := parts[1]
		agentID := parts[2]

		selectedAgentID, pd, err := m.dispatcher.HandleDispatchInteraction(ctx, dispatchID, agentID)
		if err != nil {
			return fmt.Errorf("handle dispatch interaction: %w", err)
		}

		logger.WithFields(
			"dispatch_id", dispatchID,
			"agent_id", selectedAgentID,
			"user", interaction.PlatformUserName,
		).Info("channels: dispatch agent selected via HITL")

		// Replay the original message with the resolved agent
		pd.Message.AgentID = selectedAgentID
		return m.handleMessage(ctx, pd.Message)
	}

	// ── HITL approve/deny: "approve:<reviewID>" or "deny:<reviewID>" ──
	parts := splitActionID(interaction.ActionID)
	if len(parts) < 2 {
		return fmt.Errorf("invalid action ID format: %s", interaction.ActionID)
	}

	action := parts[0] // "approve" or "deny"
	reviewID := parts[1]

	logger.WithFields(
		"action", action,
		"review_id", reviewID,
		"user", interaction.PlatformUserName,
		"platform", interaction.Platform,
	).Info("channels: HITL interaction received")

	// Delegate to session creator for approval resolution
	if m.sessionCreator != nil {
		logger.WithFields("review_id", reviewID, "action", action).
			Info("channels: HITL approval action received (integration pending)")
	}

	return nil
}

func splitActionID(actionID string) []string {
	// Split on first ":" to handle "approve:uuid" or "deny:uuid"
	idx := 0
	for i, c := range actionID {
		if c == ':' {
			idx = i
			break
		}
	}
	if idx == 0 {
		return []string{actionID}
	}
	return []string{actionID[:idx], actionID[idx+1:]}
}

// registerPendingInput records that a session is waiting for ask_user input.
// Called by ChannelSink when it receives EventUserInputRequested.
func (m *ChannelManager) registerPendingInput(sessionID, inputID string) {
	m.pendingInputsMu.Lock()
	m.pendingInputs[sessionID] = inputID
	m.pendingInputsMu.Unlock()

	logger.WithFields("session_id", sessionID, "input_id", inputID).
		Info("channels: registered pending ask_user input")
}

// reaperLoop periodically cleans up idle sessions and expired dispatches.
func (m *ChannelManager) reaperLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			// Clean up old session mappings (24 hours)
			cutoff := time.Now().Add(-24 * time.Hour)
			deleted, err := m.store.DeleteExpiredMappings(m.ctx, cutoff)
			if err != nil {
				logger.WithError(err).Warn("channels: reaper failed")
			} else if deleted > 0 {
				logger.WithFields("deleted", deleted).Debug("channels: reaped expired session mappings")
			}

			// Clean up expired pending dispatches
			if m.dispatcher != nil {
				m.dispatcher.CleanupExpired()
			}
		}
	}
}

func (m *ChannelManager) decryptCredentials(encrypted []byte) (map[string]interface{}, error) {
	if len(encrypted) == 0 {
		return nil, nil
	}

	// If no encryption key, treat as plain JSON (development mode)
	if len(m.encryptionKey) == 0 {
		var creds map[string]interface{}
		if err := json.Unmarshal(encrypted, &creds); err != nil {
			return nil, err
		}
		return creds, nil
	}

	// AES-GCM decryption
	plaintext, err := decryptAESGCM(m.encryptionKey, encrypted)
	if err != nil {
		return nil, err
	}

	var creds map[string]interface{}
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// EncryptCredentials encrypts credentials for storage.
func (m *ChannelManager) EncryptCredentials(creds map[string]interface{}) ([]byte, error) {
	plaintext, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}

	if len(m.encryptionKey) == 0 {
		// No encryption key — store as plain JSON (development mode)
		return plaintext, nil
	}

	return encryptAESGCM(m.encryptionKey, plaintext)
}

// SendCronNotification implements sandbox.CronNotifier.
// It sends a message to the channel/thread that originally created the cron job.
func (m *ChannelManager) SendCronNotification(ctx context.Context, channelConfigID, channelRef, threadRef, message string) error {
	m.mu.RLock()
	ac, ok := m.connectors[channelConfigID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no active connector for channel config %s", channelConfigID)
	}

	_, err := ac.connector.Send(ctx, channelRef, threadRef, OutboundMessage{
		Text: message,
	})
	return err
}

// SendRichNotification sends a structured message with embeds to a channel.
func (m *ChannelManager) SendRichNotification(ctx context.Context, channelConfigID, channelRef string, msg OutboundMessage) error {
	m.mu.RLock()
	ac, ok := m.connectors[channelConfigID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no active connector for channel config %s", channelConfigID)
	}

	_, err := ac.connector.Send(ctx, channelRef, "", msg)
	return err
}

// overageAction is what happens to a message once the tenant is past its
// monthly allowance.
type overageAction int

const (
	// meterOff: no allowance applies (self-hosted, dev, or an unlimited plan).
	meterOff overageAction = iota
	// meterWarn: past the allowance, keep delivering. Say so once and let the
	// usage dashboard carry the number.
	meterWarn
	// meterBlock: past the allowance, stop delivering until the month rolls.
	meterBlock
)

// meterMessage records one inbound message against the tenant's monthly
// allowance and reports whether it may be processed.
//
// MESSAGES_MONTHLY is the meter the channel pricing story rests on: channels
// are sold by messages, not by how many are connected. Counting is
// unconditional; what happens past the allowance is not.
//
// Anything the resolver cannot answer fails open: limits are nudges, and
// availability beats exactness here (creation_slot.go).
func (m *ChannelManager) meterMessage(ctx context.Context, msg InboundMessage) bool {
	if m.store == nil || msg.TenantID == "" {
		return true // nothing to meter against
	}

	ent := enterprise.ResolveEntitlements(ctx, m.licenseMonitor)
	limit, onExceeded := messageCeiling(ent)
	if onExceeded != meterOff {
		count, err := m.monthlyMessageCount(ctx, msg.TenantID)
		switch {
		case err != nil:
			logger.WithFields("tenant", msg.TenantID).WithError(err).
				Warn("channels: message meter unavailable, allowing message")
		case count >= limit:
			logger.WithFields("tenant", msg.TenantID, "count", count, "limit", limit,
				"tier", ent.Tier, "blocked", onExceeded == meterBlock).
				Info("channels: monthly message allowance exceeded")
			m.notifyOverQuota(ctx, msg, limit, onExceeded)
			if onExceeded == meterBlock {
				// A refused message is not metered: nobody should be counted
				// for traffic that never reached an agent.
				return false
			}
		}
	}

	if err := m.store.RecordChannelMessage(ctx, &ChannelMessageRecord{
		TenantID:        msg.TenantID,
		ChannelConfigID: msg.ChannelConfigID,
		Platform:        string(msg.Platform),
		PlatformUserID:  msg.PlatformUserID,
	}); err != nil {
		// Metering must never drop a message: an unbilled message is a far
		// smaller problem than a channel that stops answering.
		logger.WithFields("tenant", msg.TenantID).WithError(err).
			Warn("channels: failed to record message for metering")
		return true
	}
	m.bumpMessageCount(msg.TenantID)
	return true
}

// messageCeiling reports the monthly allowance that applies to a resolved
// entitlement set, and what to do with messages past it.
//
// Split out from meterMessage so the policy is testable without an edition
// build tag: ResolveEntitlements short-circuits to CE in an untagged binary,
// which would make every enforcement case unreachable from a plain go test.
func messageCeiling(ent enterprise.Entitlements) (limit int64, onExceeded overageAction) {
	limit, capped := ent.Limit(enterprise.UsageTypeMessagesMonthly)
	if !capped || limit <= 0 {
		return 0, meterOff
	}
	// Source "ce" is excluded explicitly rather than by relying on the CE
	// limit being unlimited. A self-hosted operator carries their own message
	// traffic, and that must not become enforceable through a one-line edit to
	// a defaults map.
	if ent.Source == "ce" {
		return limit, meterOff
	}
	// Paid tiers keep delivering. Messages have no overage rate, so there is
	// nothing to bill and nothing the customer could buy in the moment;
	// blocking would only mute a paying customer's bot mid-conversation.
	if ent.Tier != "free" {
		return limit, meterWarn
	}
	return limit, meterBlock
}

// monthlyMessageCount returns the tenant's message count for the month, cached
// briefly. Without the cache every inbound message would run a COUNT over the
// tenant's month, which at the top of the paid tiers is a 100k-row scan per
// message to answer a question that changes by one.
//
// The cache trades exactness for load in the direction this codebase already
// chose for limits: a tenant can cross the allowance by up to a TTL's worth of
// traffic before the meter notices. That matters for a notice, not for
// billing, which counts rows rather than this number.
func (m *ChannelManager) monthlyMessageCount(ctx context.Context, tenantID string) (int64, error) {
	month := time.Now().UTC().Format("2006-01")

	m.messageCountMu.Lock()
	if c, ok := m.messageCounts[tenantID]; ok && c.month == month && time.Since(c.fetchedAt) < messageCountTTL {
		count := c.count
		m.messageCountMu.Unlock()
		return count, nil
	}
	m.messageCountMu.Unlock()

	count, err := m.store.CountChannelMessagesThisMonth(ctx, tenantID)
	if err != nil {
		return 0, err
	}

	m.messageCountMu.Lock()
	m.messageCounts[tenantID] = &cachedMessageCount{count: count, month: month, fetchedAt: time.Now()}
	m.messageCountMu.Unlock()
	return count, nil
}

// bumpMessageCount keeps the cached count moving between refreshes so a burst
// inside one TTL window still trips the allowance.
func (m *ChannelManager) bumpMessageCount(tenantID string) {
	month := time.Now().UTC().Format("2006-01")
	m.messageCountMu.Lock()
	defer m.messageCountMu.Unlock()
	if c, ok := m.messageCounts[tenantID]; ok && c.month == month {
		c.count++
	}
}

// notifyOverQuota tells the channel it is past its monthly messages, at most
// once per quotaNoticeInterval per channel. Without the throttle a busy
// channel would answer every single message with the same notice, which reads
// as the bot spamming rather than the plan being full.
func (m *ChannelManager) notifyOverQuota(ctx context.Context, msg InboundMessage, limit int64, action overageAction) {
	m.quotaNoticeMu.Lock()
	last, seen := m.quotaNoticeAt[msg.ChannelConfigID]
	if seen && time.Since(last) < quotaNoticeInterval {
		m.quotaNoticeMu.Unlock()
		return
	}
	m.quotaNoticeAt[msg.ChannelConfigID] = time.Now()
	m.quotaNoticeMu.Unlock()

	var text string
	if action == meterBlock {
		text = fmt.Sprintf(
			"This workspace has used all %d channel messages included this month. "+
				"Messages resume when the month rolls over, or upgrade at https://everstack.ai/pricing to continue now.",
			limit,
		)
	} else {
		// Deliberately does not threaten a charge: messages have no overage
		// rate, so the only true statement is that nothing changes.
		text = fmt.Sprintf(
			"This workspace has passed the %d channel messages included this month. "+
				"Nothing is paused and your bill is unchanged; the overage shows in your usage dashboard.",
			limit,
		)
	}
	if err := m.SendCronNotification(ctx, msg.ChannelConfigID, msg.PlatformChannelRef, msg.PlatformThreadRef, text); err != nil {
		logger.WithFields("channel", msg.ChannelConfigID).WithError(err).
			Debug("channels: could not deliver over-quota notice")
	}
}
