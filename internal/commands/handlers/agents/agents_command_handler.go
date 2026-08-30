package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// AgentsCommandHandler handles agent CRUD and session lifecycle commands.
type AgentsCommandHandler struct {
	db *sqlx.DB
}

func NewAgentsCommandHandler(databases ...*sqlx.DB) *AgentsCommandHandler {
	var db *sqlx.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	return &AgentsCommandHandler{db: db}
}

func (h *AgentsCommandHandler) CommandType() string {
	return "CreateAgent|UpdateAgent|DeleteAgent|CreateAgentSession|RunAgentTurn|CancelAgentSession|CompleteAgentSession|CompleteAgentTurn|RequestApproval|ProvisionAgent|SleepAgent|WakeAgent|CreateAgentLink|DeleteAgentLink|BindAgentChannel|UnbindAgentChannel"
}

func (h *AgentsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateAgentCommand:
		return h.handleCreateAgent(ctx, c)
	case *UpdateAgentCommand:
		return h.handleUpdateAgent(ctx, c)
	case *DeleteAgentCommand:
		return h.handleDeleteAgent(ctx, c)
	case *CreateSessionCommand:
		return h.handleCreateSession(ctx, c)
	case *RunTurnCommand:
		return h.handleRunTurn(ctx, c)
	case *CancelSessionCommand:
		return h.handleCancelSession(ctx, c)
	case *CompleteSessionCommand:
		return h.handleCompleteSession(ctx, c)
	case *CompleteTurnCommand:
		return h.handleCompleteTurn(ctx, c)
	case *RequestApprovalCommand:
		return h.handleRequestApproval(ctx, c)
	case *ProvisionAgentCommand:
		return h.handleProvisionAgent(ctx, c)
	case *SleepAgentCommand:
		return h.handleSleepAgent(ctx, c)
	case *WakeAgentCommand:
		return h.handleWakeAgent(ctx, c)
	case *CreateAgentLinkCommand:
		return h.handleCreateAgentLink(ctx, c)
	case *DeleteAgentLinkCommand:
		return h.handleDeleteAgentLink(ctx, c)
	case *BindAgentChannelCommand:
		return h.handleBindAgentChannel(ctx, c)
	case *UnbindAgentChannelCommand:
		return h.handleUnbindAgentChannel(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

// requireTenant returns the tenant ID or an error if it is empty.
// Tenant ID is required for all agent operations to prevent data co-mingling.
func requireTenant(tenantID string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenant_id is required")
	}
	return tenantID, nil
}

func normalizeAgentMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "subagent":
		return "subagent"
	default:
		return "primary"
	}
}

func normalizeTaskPermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "always":
		return "always"
	case "deny":
		return "deny"
	default:
		return "ask"
	}
}

func (h *AgentsCommandHandler) handleCreateAgent(ctx context.Context, cmd *CreateAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"model", cmd.Model,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing agent create command")

	maxTurns := cmd.MaxTurns
	// 0 means unlimited — only apply a default when explicitly negative
	if maxTurns < 0 {
		maxTurns = 0
	}
	// 0 means unlimited tool calls per turn
	maxToolCalls := cmd.MaxToolCallsPerTurn
	mode := normalizeAgentMode(cmd.Mode)
	taskPermissionMode := normalizeTaskPermissionMode(cmd.TaskPermissionMode)
	lifecycleMode := strings.ToLower(strings.TrimSpace(cmd.LifecycleMode))
	if lifecycleMode == "" {
		lifecycleMode = "ephemeral"
	}

	configJSON, _ := json.Marshal(cmd.Config)
	if cmd.Config == nil {
		configJSON = []byte("{}")
	}

	payload := map[string]interface{}{
		"id":                      cmd.ID,
		"tenant_id":               tenantID,
		"name":                    cmd.Name,
		"description":             cmd.Description,
		"model":                   cmd.Model,
		"system_prompt":           cmd.SystemPrompt,
		"tools":                   cmd.Tools,
		"config":                  json.RawMessage(configJSON),
		"max_turns":               maxTurns,
		"max_tool_calls_per_turn": maxToolCalls,
		"mode":                    mode,
		"max_steps":               cmd.MaxSteps,
		"task_permission_mode":    taskPermissionMode,
		"hidden":                  cmd.Hidden,
		"color":                   cmd.Color,
		"working_directory":       cmd.WorkingDirectory,
		"mention_alias":           cmd.MentionAlias,
		"lifecycle_mode":          lifecycleMode,
		"lifecycle_status":        "created",
		"icon":                    cmd.Icon,
		"soul_md":                 cmd.SoulMD,
		"identity_md":             cmd.IdentityMD,
		"user_md":                 cmd.UserMD,
		"role_md":                 cmd.RoleMD,
		"sandbox_image":           cmd.SandboxImage,
		"sandbox_cpu_limit":       cmd.SandboxCPULimit,
		"sandbox_memory_mb":       cmd.SandboxMemoryMB,
		"sandbox_disk_mb":         cmd.SandboxDiskMB,
		"sandbox_timeout_seconds": cmd.SandboxTimeoutSeconds,
		"sandbox_network_mode":    cmd.SandboxNetworkMode,
		"sandbox_allowed_hosts":   cmd.SandboxAllowedHosts,
		"sandbox_env_vars":        cmd.SandboxEnvVars,
		"sandbox_ssh_enabled":     cmd.SandboxSSHEnabled,
		"sandbox_git_repo_url":    cmd.SandboxGitRepoURL,
		"sandbox_git_branch":      cmd.SandboxGitBranch,
		"db_sqlite_path":          cmd.DBSqlitePath,
		"db_lancedb_path":         cmd.DBLanceDBPath,
		"db_redb_path":            cmd.DBRedbPath,
		"max_concurrent_workers":  cmd.MaxConcurrentWorkers,
		"worker_pool_config":      cmd.WorkerPoolConfig,
		"auto_provision":          cmd.AutoProvision,
		"enabled":                 true,
		"created_at":              now.Format(time.RFC3339),
		"updated_at":              now.Format(time.RFC3339),
		"correlation_id":          correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.created",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleUpdateAgent(ctx context.Context, cmd *UpdateAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"agent_id", cmd.AgentID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing agent update command")

	payload := map[string]interface{}{
		"id":             cmd.AgentID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	if cmd.Name != nil {
		payload["name"] = *cmd.Name
	}
	if cmd.Description != nil {
		payload["description"] = *cmd.Description
	}
	if cmd.Model != nil {
		payload["model"] = *cmd.Model
	}
	if cmd.SystemPrompt != nil {
		payload["system_prompt"] = *cmd.SystemPrompt
	}
	if cmd.Tools != nil {
		payload["tools"] = cmd.Tools
	}
	if cmd.Config != nil {
		configJSON, _ := json.Marshal(cmd.Config)
		payload["config"] = json.RawMessage(configJSON)
	}
	if cmd.MaxTurns != nil {
		payload["max_turns"] = *cmd.MaxTurns
	}
	if cmd.MaxToolCallsPerTurn != nil {
		payload["max_tool_calls_per_turn"] = *cmd.MaxToolCallsPerTurn
	}
	if cmd.Enabled != nil {
		payload["enabled"] = *cmd.Enabled
	}
	if cmd.Mode != nil {
		payload["mode"] = normalizeAgentMode(*cmd.Mode)
	}
	if cmd.MaxSteps != nil {
		payload["max_steps"] = *cmd.MaxSteps
	}
	if cmd.TaskPermissionMode != nil {
		payload["task_permission_mode"] = normalizeTaskPermissionMode(*cmd.TaskPermissionMode)
	}
	if cmd.Hidden != nil {
		payload["hidden"] = *cmd.Hidden
	}
	if cmd.Color != nil {
		payload["color"] = *cmd.Color
	}
	if cmd.WorkingDirectory != nil {
		payload["working_directory"] = *cmd.WorkingDirectory
	}
	if cmd.MentionAlias != nil {
		payload["mention_alias"] = *cmd.MentionAlias
	}
	if cmd.LifecycleMode != nil {
		payload["lifecycle_mode"] = *cmd.LifecycleMode
	}
	if cmd.Icon != nil {
		payload["icon"] = *cmd.Icon
	}
	if cmd.SoulMD != nil {
		payload["soul_md"] = *cmd.SoulMD
	}
	if cmd.IdentityMD != nil {
		payload["identity_md"] = *cmd.IdentityMD
	}
	if cmd.UserMD != nil {
		payload["user_md"] = *cmd.UserMD
	}
	if cmd.RoleMD != nil {
		payload["role_md"] = *cmd.RoleMD
	}
	if cmd.SandboxImage != nil {
		payload["sandbox_image"] = *cmd.SandboxImage
	}
	if cmd.SandboxCPULimit != nil {
		payload["sandbox_cpu_limit"] = *cmd.SandboxCPULimit
	}
	if cmd.SandboxMemoryMB != nil {
		payload["sandbox_memory_mb"] = *cmd.SandboxMemoryMB
	}
	if cmd.SandboxDiskMB != nil {
		payload["sandbox_disk_mb"] = *cmd.SandboxDiskMB
	}
	if cmd.SandboxTimeoutSeconds != nil {
		payload["sandbox_timeout_seconds"] = *cmd.SandboxTimeoutSeconds
	}
	if cmd.SandboxNetworkMode != nil {
		payload["sandbox_network_mode"] = *cmd.SandboxNetworkMode
	}
	if cmd.SandboxAllowedHosts != nil {
		payload["sandbox_allowed_hosts"] = cmd.SandboxAllowedHosts
	}
	if cmd.SandboxEnvVars != nil {
		payload["sandbox_env_vars"] = cmd.SandboxEnvVars
	}
	if cmd.SandboxSSHEnabled != nil {
		payload["sandbox_ssh_enabled"] = *cmd.SandboxSSHEnabled
	}
	if cmd.SandboxGitRepoURL != nil {
		payload["sandbox_git_repo_url"] = *cmd.SandboxGitRepoURL
	}
	if cmd.SandboxGitBranch != nil {
		payload["sandbox_git_branch"] = *cmd.SandboxGitBranch
	}
	if cmd.DBSqlitePath != nil {
		payload["db_sqlite_path"] = *cmd.DBSqlitePath
	}
	if cmd.DBLanceDBPath != nil {
		payload["db_lancedb_path"] = *cmd.DBLanceDBPath
	}
	if cmd.DBRedbPath != nil {
		payload["db_redb_path"] = *cmd.DBRedbPath
	}
	if cmd.MaxConcurrentWorkers != nil {
		payload["max_concurrent_workers"] = *cmd.MaxConcurrentWorkers
	}
	if cmd.WorkerPoolConfig != nil {
		wpJSON, _ := json.Marshal(cmd.WorkerPoolConfig)
		payload["worker_pool_config"] = json.RawMessage(wpJSON)
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.updated",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleDeleteAgent(ctx context.Context, cmd *DeleteAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"agent_id", cmd.AgentID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing agent delete command")

	payload := map[string]interface{}{
		"id":             cmd.AgentID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.deleted",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleCreateSession(ctx context.Context, cmd *CreateSessionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"agent_id", cmd.AgentID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing session create command")

	metadataJSON, _ := json.Marshal(cmd.Metadata)
	if cmd.Metadata == nil {
		metadataJSON = []byte("{}")
	}

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"agent_id":       cmd.AgentID,
		"status":         "created",
		"turn_count":     0,
		"total_tokens":   0,
		"metadata":       json.RawMessage(metadataJSON),
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	if cmd.AgentID != "" && h.db != nil {
		var revisionID sql.NullString
		err := h.db.GetContext(ctx, &revisionID, `
			SELECT active_revision_id
			FROM agent_definitions
			WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, cmd.AgentID, tenantID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("capture active agent revision: %w", err)
		}
		if revisionID.Valid && revisionID.String != "" {
			payload["revision_id"] = revisionID.String
		}
	}
	if cmd.TrooperID != "" {
		payload["trooper_id"] = cmd.TrooperID
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.session.created",
		Stream:    "agent-sessions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleRunTurn(_ context.Context, cmd *RunTurnCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	// RunTurn is dispatched but the actual turn execution happens in the gRPC handler
	// which calls the runtime engine. The turn result is persisted via CompleteTurnCommand.
	// This event simply marks the session as running.
	now := time.Now()
	payload := map[string]interface{}{
		"session_id": cmd.SessionID,
		"status":     "running",
		"updated_at": now.Format(time.RFC3339),
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.session.running",
		Stream:    "agent-sessions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleCancelSession(_ context.Context, cmd *CancelSessionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"session_id":   cmd.SessionID,
		"tenant_id":    tenantID,
		"status":       "cancelled",
		"updated_at":   now.Format(time.RFC3339),
		"completed_at": now.Format(time.RFC3339),
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.session.cancelled",
		Stream:    "agent-sessions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleCompleteSession(_ context.Context, cmd *CompleteSessionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"session_id":   cmd.SessionID,
		"tenant_id":    tenantID,
		"status":       "completed",
		"updated_at":   now.Format(time.RFC3339),
		"completed_at": now.Format(time.RFC3339),
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.session.completed",
		Stream:    "agent-sessions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleCompleteTurn(_ context.Context, cmd *CompleteTurnCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	now := time.Now()

	turnStatus := "completed"
	if cmd.Error != "" {
		turnStatus = "failed"
	}

	turnPayload := map[string]interface{}{
		"id":                       uuid.New().String(),
		"session_id":               cmd.SessionID,
		"turn_number":              cmd.TurnNumber,
		"expected_turn_count":      cmd.ExpectedTurnCount,
		"status":                   turnStatus,
		"user_input":               cmd.UserInput,
		"assistant_output":         cmd.AssistantOutput,
		"tool_calls":               json.RawMessage(cmd.ToolCalls),
		"prompt_tokens":            cmd.PromptTokens,
		"completion_tokens":        cmd.CompletionTokens,
		"total_tokens":             cmd.TotalTokens,
		"cache_read_input_tokens":  cmd.CacheReadInputTokens,
		"cache_write_input_tokens": cmd.CacheWriteInputTokens,
		"latency_ms":               cmd.LatencyMs,
		"error":                    cmd.Error,
		"created_at":               now.Format(time.RFC3339),
		"completed_at":             now.Format(time.RFC3339),
	}
	// Only carry the timeline when present — an empty RawMessage would fail to
	// marshal. A missing key persists as NULL and the UI falls back to the
	// flat fields.
	if cmd.Timeline != "" {
		turnPayload["timeline"] = json.RawMessage(cmd.Timeline)
	}

	turnData, _ := json.Marshal(turnPayload)
	events := []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.turn.completed",
		Stream:    "agent-sessions",
		Payload:   turnData,
		CreatedAt: now.Unix(),
	}}

	// If session status changed, emit session event
	if cmd.SessionStatus == "completed" || cmd.SessionStatus == "failed" || cmd.SessionStatus == "waiting_for_input" {
		sessionPayload := map[string]interface{}{
			"session_id":   cmd.SessionID,
			"status":       cmd.SessionStatus,
			"updated_at":   now.Format(time.RFC3339),
			"completed_at": now.Format(time.RFC3339),
		}
		sessionData, _ := json.Marshal(sessionPayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "agent.session.completed",
			Stream:    "agent-sessions",
			Payload:   sessionData,
			CreatedAt: now.Unix(),
		})
	}

	return events, nil
}

func (h *AgentsCommandHandler) handleRequestApproval(_ context.Context, cmd *RequestApprovalCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	now := time.Now()

	payload := map[string]interface{}{
		"review_id":      cmd.ReviewID,
		"session_id":     cmd.SessionID,
		"tenant_id":      cmd.TenantID,
		"agent_id":       cmd.AgentID,
		"turn_number":    cmd.TurnNumber,
		"iteration":      cmd.Iteration,
		"tool_calls":     cmd.ToolCalls,
		"default_action": cmd.DefaultAction,
		"expires_at":     cmd.ExpiresAt.Format(time.RFC3339),
		"requested_at":   now.Format(time.RFC3339),
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.approval.requested",
		Stream:    "agent-sessions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleProvisionAgent(ctx context.Context, cmd *ProvisionAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"id":               cmd.AgentID,
		"tenant_id":        cmd.TenantID,
		"lifecycle_status": "provisioning",
		"updated_at":       now.Format(time.RFC3339),
		"correlation_id":   correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.provisioning",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleSleepAgent(ctx context.Context, cmd *SleepAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"id":               cmd.AgentID,
		"tenant_id":        cmd.TenantID,
		"lifecycle_status": "sleeping",
		"updated_at":       now.Format(time.RFC3339),
		"correlation_id":   correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.sleeping",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleWakeAgent(ctx context.Context, cmd *WakeAgentCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"id":               cmd.AgentID,
		"tenant_id":        cmd.TenantID,
		"lifecycle_status": "waking",
		"updated_at":       now.Format(time.RFC3339),
		"correlation_id":   correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.waking",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleCreateAgentLink(ctx context.Context, cmd *CreateAgentLinkCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	configJSON, _ := json.Marshal(cmd.Config)
	if cmd.Config == nil {
		configJSON = []byte("{}")
	}

	payload := map[string]interface{}{
		"id":              uuid.New().String(),
		"tenant_id":       cmd.TenantID,
		"source_agent_id": cmd.SourceAgentID,
		"target_type":     cmd.TargetType,
		"target_id":       cmd.TargetID,
		"target_name":     cmd.TargetName,
		"link_type":       cmd.LinkType,
		"protocol":        cmd.Protocol,
		"status":          "active",
		"config":          json.RawMessage(configJSON),
		"created_at":      now.Format(time.RFC3339),
		"updated_at":      now.Format(time.RFC3339),
		"correlation_id":  correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.link.created",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleDeleteAgentLink(ctx context.Context, cmd *DeleteAgentLinkCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"link_id":        cmd.LinkID,
		"tenant_id":      cmd.TenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.link.deleted",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleBindAgentChannel(ctx context.Context, cmd *BindAgentChannelCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"id":                uuid.New().String(),
		"tenant_id":         cmd.TenantID,
		"agent_id":          cmd.AgentID,
		"channel_config_id": cmd.ChannelConfigID,
		"enabled":           true,
		"created_at":        now.Format(time.RFC3339),
		"correlation_id":    correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.channel.bound",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AgentsCommandHandler) handleUnbindAgentChannel(ctx context.Context, cmd *UnbindAgentChannelCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	payload := map[string]interface{}{
		"tenant_id":         cmd.TenantID,
		"agent_id":          cmd.AgentID,
		"channel_config_id": cmd.ChannelConfigID,
		"deleted_at":        now.Format(time.RFC3339),
		"correlation_id":    correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "agent.channel.unbound",
		Stream:    "agents",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
