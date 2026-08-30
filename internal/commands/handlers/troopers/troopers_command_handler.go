package troopers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// TroopersCommandHandler handles trooper CRUD, link, and channel binding commands.
type TroopersCommandHandler struct{}

func NewTroopersCommandHandler() *TroopersCommandHandler { return &TroopersCommandHandler{} }

func (h *TroopersCommandHandler) CommandType() string {
	return "CreateTrooper|UpdateTrooper|DeleteTrooper|CreateTrooperLink|DeleteTrooperLink|BindChannel|UnbindChannel"
}

func (h *TroopersCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateTrooperCommand:
		return h.handleCreateTrooper(ctx, c)
	case *UpdateTrooperCommand:
		return h.handleUpdateTrooper(ctx, c)
	case *DeleteTrooperCommand:
		return h.handleDeleteTrooper(ctx, c)
	case *CreateTrooperLinkCommand:
		return h.handleCreateTrooperLink(ctx, c)
	case *DeleteTrooperLinkCommand:
		return h.handleDeleteTrooperLink(ctx, c)
	case *BindChannelCommand:
		return h.handleBindChannel(ctx, c)
	case *UnbindChannelCommand:
		return h.handleUnbindChannel(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

// requireTenant returns the tenant ID or an error if it is empty.
func requireTenant(tenantID string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenant_id is required")
	}
	return tenantID, nil
}

func (h *TroopersCommandHandler) handleCreateTrooper(ctx context.Context, cmd *CreateTrooperCommand) ([]database.Event, error) {
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
		"command_id", cmd.BaseCommand.ID,
		"name", cmd.Name,
		"model", cmd.Model,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper create command")

	maxTurns := cmd.MaxTurns
	if maxTurns < 0 {
		maxTurns = 0
	}
	maxToolCalls := cmd.MaxToolCallsPerTurn

	agentConfigJSON, _ := json.Marshal(cmd.AgentConfig)
	if cmd.AgentConfig == nil {
		agentConfigJSON = []byte("{}")
	}

	envVarsJSON, _ := json.Marshal(cmd.SandboxEnvVars)
	if cmd.SandboxEnvVars == nil {
		envVarsJSON = []byte("{}")
	}

	workerPoolConfigJSON, _ := json.Marshal(cmd.WorkerPoolConfig)
	if cmd.WorkerPoolConfig == nil {
		workerPoolConfigJSON = []byte("{}")
	}

	payload := map[string]interface{}{
		"id":                       uuid.New().String(),
		"tenant_id":                tenantID,
		"name":                     cmd.Name,
		"description":              cmd.Description,
		"model":                    cmd.Model,
		"system_prompt":            cmd.SystemPrompt,
		"tools":                    cmd.Tools,
		"agent_config":             json.RawMessage(agentConfigJSON),
		"max_turns":                maxTurns,
		"max_tool_calls_per_turn":  maxToolCalls,
		"max_steps":                cmd.MaxSteps,
		"soul_md":                  cmd.SoulMD,
		"identity_md":              cmd.IdentityMD,
		"user_md":                  cmd.UserMD,
		"role_md":                  cmd.RoleMD,
		"sandbox_image":            cmd.SandboxImage,
		"sandbox_network_mode":     cmd.SandboxNetworkMode,
		"sandbox_cpu_limit":        cmd.SandboxCPULimit,
		"sandbox_memory_mb":        cmd.SandboxMemoryMB,
		"sandbox_disk_mb":          cmd.SandboxDiskMB,
		"sandbox_timeout_seconds":  cmd.SandboxTimeoutSeconds,
		"sandbox_allowed_hosts":    cmd.SandboxAllowedHosts,
		"sandbox_env_vars":         json.RawMessage(envVarsJSON),
		"sandbox_ssh_enabled":      cmd.SandboxSSHEnabled,
		"sandbox_git_repo_url":     cmd.SandboxGitRepoURL,
		"sandbox_git_branch":       cmd.SandboxGitBranch,
		"db_sqlite_path":           cmd.DBSqlitePath,
		"db_lancedb_path":          cmd.DBLanceDBPath,
		"db_redb_path":             cmd.DBRedbPath,
		"max_concurrent_workers":   cmd.MaxConcurrentWorkers,
		"worker_pool_config":       json.RawMessage(workerPoolConfigJSON),
		"color":                    cmd.Color,
		"icon":                     cmd.Icon,
		"auto_provision":           cmd.AutoProvision,
		"created_at":               now.Format(time.RFC3339),
		"updated_at":               now.Format(time.RFC3339),
		"correlation_id":           correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.created",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleUpdateTrooper(ctx context.Context, cmd *UpdateTrooperCommand) ([]database.Event, error) {
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
		"command_id", cmd.BaseCommand.ID,
		"trooper_id", cmd.ID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper update command")

	payload := map[string]interface{}{
		"id":             cmd.ID,
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
	if cmd.AgentConfig != nil {
		agentConfigJSON, _ := json.Marshal(cmd.AgentConfig)
		payload["agent_config"] = json.RawMessage(agentConfigJSON)
	}
	if cmd.MaxTurns != nil {
		payload["max_turns"] = *cmd.MaxTurns
	}
	if cmd.MaxToolCallsPerTurn != nil {
		payload["max_tool_calls_per_turn"] = *cmd.MaxToolCallsPerTurn
	}
	if cmd.MaxSteps != nil {
		payload["max_steps"] = *cmd.MaxSteps
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
	if cmd.SandboxNetworkMode != nil {
		payload["sandbox_network_mode"] = *cmd.SandboxNetworkMode
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
		workerPoolConfigJSON, _ := json.Marshal(cmd.WorkerPoolConfig)
		payload["worker_pool_config"] = json.RawMessage(workerPoolConfigJSON)
	}
	if cmd.Color != nil {
		payload["color"] = *cmd.Color
	}
	if cmd.Icon != nil {
		payload["icon"] = *cmd.Icon
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.updated",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleDeleteTrooper(ctx context.Context, cmd *DeleteTrooperCommand) ([]database.Event, error) {
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
		"command_id", cmd.BaseCommand.ID,
		"trooper_id", cmd.ID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper delete command")

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.deleted",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleCreateTrooperLink(ctx context.Context, cmd *CreateTrooperLinkCommand) ([]database.Event, error) {
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
		"command_id", cmd.BaseCommand.ID,
		"source_trooper_id", cmd.SourceTrooperID,
		"target_id", cmd.TargetID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper link create command")

	configJSON, _ := json.Marshal(cmd.Config)
	if cmd.Config == nil {
		configJSON = []byte("{}")
	}

	payload := map[string]interface{}{
		"id":                   uuid.New().String(),
		"tenant_id":            tenantID,
		"source_trooper_id":  cmd.SourceTrooperID,
		"target_type":          cmd.TargetType,
		"target_id":            cmd.TargetID,
		"target_name":          cmd.TargetName,
		"link_type":            cmd.LinkType,
		"protocol":             cmd.Protocol,
		"config":               json.RawMessage(configJSON),
		"created_at":           now.Format(time.RFC3339),
		"correlation_id":       correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.link.created",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleDeleteTrooperLink(ctx context.Context, cmd *DeleteTrooperLinkCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.BaseCommand.ID,
		"link_id", cmd.ID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper link delete command")

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.link.deleted",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleBindChannel(ctx context.Context, cmd *BindChannelCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.BaseCommand.ID,
		"trooper_id", cmd.TrooperID,
		"channel_config_id", cmd.ChannelConfigID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper channel bind command")

	payload := map[string]interface{}{
		"tenant_id":         tenantID,
		"trooper_id":      cmd.TrooperID,
		"channel_config_id": cmd.ChannelConfigID,
		"bound_at":          now.Format(time.RFC3339),
		"correlation_id":    correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.channel.bound",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *TroopersCommandHandler) handleUnbindChannel(ctx context.Context, cmd *UnbindChannelCommand) ([]database.Event, error) {
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := requireTenant(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.BaseCommand.ID,
		"trooper_id", cmd.TrooperID,
		"channel_config_id", cmd.ChannelConfigID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing trooper channel unbind command")

	payload := map[string]interface{}{
		"tenant_id":         tenantID,
		"trooper_id":      cmd.TrooperID,
		"channel_config_id": cmd.ChannelConfigID,
		"unbound_at":        now.Format(time.RFC3339),
		"correlation_id":    correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "trooper.channel.unbound",
		Stream:    "troopers",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
