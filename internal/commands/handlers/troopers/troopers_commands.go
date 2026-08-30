package troopers

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/google/uuid"
)

// CreateTrooperCommand creates a new trooper definition.
type CreateTrooperCommand struct {
	commands.BaseCommand
	TenantID              string                 `json:"tenant_id"`
	Name                  string                 `json:"name"`
	Description           string                 `json:"description"`
	Model                 string                 `json:"model"`
	SystemPrompt          string                 `json:"system_prompt"`
	Tools                 []string               `json:"tools"`
	AgentConfig           map[string]interface{} `json:"agent_config"`
	MaxTurns              int32                  `json:"max_turns"`
	MaxToolCallsPerTurn   int32                  `json:"max_tool_calls_per_turn"`
	MaxSteps              *int32                 `json:"max_steps,omitempty"`
	SoulMD                string                 `json:"soul_md"`
	IdentityMD            string                 `json:"identity_md"`
	UserMD                string                 `json:"user_md"`
	RoleMD                string                 `json:"role_md"`
	SandboxImage          string                 `json:"sandbox_image"`
	SandboxNetworkMode    string                 `json:"sandbox_network_mode"`
	SandboxCPULimit       float64                `json:"sandbox_cpu_limit"`
	SandboxMemoryMB       int64                  `json:"sandbox_memory_mb"`
	SandboxDiskMB         int64                  `json:"sandbox_disk_mb"`
	SandboxTimeoutSeconds int32                  `json:"sandbox_timeout_seconds"`
	SandboxAllowedHosts   []string               `json:"sandbox_allowed_hosts"`
	SandboxEnvVars        map[string]string      `json:"sandbox_env_vars"`
	SandboxSSHEnabled     bool                   `json:"sandbox_ssh_enabled"`
	SandboxGitRepoURL     string                 `json:"sandbox_git_repo_url"`
	SandboxGitBranch      string                 `json:"sandbox_git_branch"`
	DBSqlitePath          string                 `json:"db_sqlite_path"`
	DBLanceDBPath         string                 `json:"db_lancedb_path"`
	DBRedbPath            string                 `json:"db_redb_path"`
	MaxConcurrentWorkers  int32                  `json:"max_concurrent_workers"`
	WorkerPoolConfig      map[string]interface{} `json:"worker_pool_config"`
	Color                 *string                `json:"color,omitempty"`
	Icon                  *string                `json:"icon,omitempty"`
	AutoProvision         bool                   `json:"auto_provision"`
}

func NewCreateTrooperCommand(
	tenantID, name, description, model, systemPrompt string,
	tools []string,
	agentConfig map[string]interface{},
	maxTurns, maxToolCallsPerTurn int32,
	maxSteps *int32,
	soulMD, identityMD, userMD, roleMD string,
	sandboxImage, sandboxNetworkMode string,
	sandboxCPULimit float64,
	sandboxMemoryMB, sandboxDiskMB int64,
	sandboxTimeoutSeconds int32,
	sandboxAllowedHosts []string,
	sandboxEnvVars map[string]string,
	sandboxSSHEnabled bool,
	sandboxGitRepoURL, sandboxGitBranch string,
	dbSqlitePath, dbLanceDBPath, dbRedbPath string,
	maxConcurrentWorkers int32,
	workerPoolConfig map[string]interface{},
	color, icon *string,
	autoProvision bool,
	userID, traceID string,
) *CreateTrooperCommand {
	return &CreateTrooperCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:              tenantID,
		Name:                  name,
		Description:           description,
		Model:                 model,
		SystemPrompt:          systemPrompt,
		Tools:                 tools,
		AgentConfig:           agentConfig,
		MaxTurns:              maxTurns,
		MaxToolCallsPerTurn:   maxToolCallsPerTurn,
		MaxSteps:              maxSteps,
		SoulMD:                soulMD,
		IdentityMD:            identityMD,
		UserMD:                userMD,
		RoleMD:                roleMD,
		SandboxImage:          sandboxImage,
		SandboxNetworkMode:    sandboxNetworkMode,
		SandboxCPULimit:       sandboxCPULimit,
		SandboxMemoryMB:       sandboxMemoryMB,
		SandboxDiskMB:         sandboxDiskMB,
		SandboxTimeoutSeconds: sandboxTimeoutSeconds,
		SandboxAllowedHosts:   sandboxAllowedHosts,
		SandboxEnvVars:        sandboxEnvVars,
		SandboxSSHEnabled:     sandboxSSHEnabled,
		SandboxGitRepoURL:     sandboxGitRepoURL,
		SandboxGitBranch:      sandboxGitBranch,
		DBSqlitePath:          dbSqlitePath,
		DBLanceDBPath:         dbLanceDBPath,
		DBRedbPath:            dbRedbPath,
		MaxConcurrentWorkers:  maxConcurrentWorkers,
		WorkerPoolConfig:      workerPoolConfig,
		Color:                 color,
		Icon:                  icon,
		AutoProvision:         autoProvision,
	}
}

func (c CreateTrooperCommand) AggregateID() string { return c.ID }
func (c CreateTrooperCommand) CommandType() string  { return "CreateTrooper" }
func (c CreateTrooperCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if c.Model == "" {
		return fmt.Errorf("model cannot be empty")
	}
	return nil
}

// UpdateTrooperCommand updates an existing trooper definition.
type UpdateTrooperCommand struct {
	commands.BaseCommand
	ID                    string                 `json:"trooper_id"`
	TenantID              string                 `json:"tenant_id"`
	Name                  *string                `json:"name,omitempty"`
	Description           *string                `json:"description,omitempty"`
	Model                 *string                `json:"model,omitempty"`
	SystemPrompt          *string                `json:"system_prompt,omitempty"`
	Tools                 []string               `json:"tools,omitempty"`
	AgentConfig           map[string]interface{} `json:"agent_config,omitempty"`
	MaxTurns              *int32                 `json:"max_turns,omitempty"`
	MaxToolCallsPerTurn   *int32                 `json:"max_tool_calls_per_turn,omitempty"`
	MaxSteps              *int32                 `json:"max_steps,omitempty"`
	SoulMD                *string                `json:"soul_md,omitempty"`
	IdentityMD            *string                `json:"identity_md,omitempty"`
	UserMD                *string                `json:"user_md,omitempty"`
	RoleMD                *string                `json:"role_md,omitempty"`
	SandboxImage          *string                `json:"sandbox_image,omitempty"`
	SandboxNetworkMode    *string                `json:"sandbox_network_mode,omitempty"`
	SandboxCPULimit       *float64               `json:"sandbox_cpu_limit,omitempty"`
	SandboxMemoryMB       *int64                 `json:"sandbox_memory_mb,omitempty"`
	SandboxDiskMB         *int64                 `json:"sandbox_disk_mb,omitempty"`
	SandboxTimeoutSeconds *int32                 `json:"sandbox_timeout_seconds,omitempty"`
	SandboxSSHEnabled     *bool                  `json:"sandbox_ssh_enabled,omitempty"`
	SandboxGitRepoURL     *string                `json:"sandbox_git_repo_url,omitempty"`
	SandboxGitBranch      *string                `json:"sandbox_git_branch,omitempty"`
	DBSqlitePath          *string                `json:"db_sqlite_path,omitempty"`
	DBLanceDBPath         *string                `json:"db_lancedb_path,omitempty"`
	DBRedbPath            *string                `json:"db_redb_path,omitempty"`
	MaxConcurrentWorkers  *int32                 `json:"max_concurrent_workers,omitempty"`
	WorkerPoolConfig      map[string]interface{} `json:"worker_pool_config,omitempty"`
	Color                 *string                `json:"color,omitempty"`
	Icon                  *string                `json:"icon,omitempty"`
}

func NewUpdateTrooperCommand(id, tenantID, userID, traceID string) *UpdateTrooperCommand {
	return &UpdateTrooperCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ID:       id,
		TenantID: tenantID,
	}
}

func (c UpdateTrooperCommand) AggregateID() string { return c.ID }
func (c UpdateTrooperCommand) CommandType() string  { return "UpdateTrooper" }
func (c UpdateTrooperCommand) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("trooper id cannot be empty")
	}
	return nil
}

// DeleteTrooperCommand deletes a trooper definition.
type DeleteTrooperCommand struct {
	commands.BaseCommand
	ID       string `json:"trooper_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteTrooperCommand(id, tenantID, userID, traceID string) *DeleteTrooperCommand {
	return &DeleteTrooperCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ID:       id,
		TenantID: tenantID,
	}
}

func (c DeleteTrooperCommand) AggregateID() string { return c.ID }
func (c DeleteTrooperCommand) CommandType() string  { return "DeleteTrooper" }
func (c DeleteTrooperCommand) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("trooper id cannot be empty")
	}
	return nil
}

// CreateTrooperLinkCommand creates a link between a trooper and another resource.
type CreateTrooperLinkCommand struct {
	commands.BaseCommand
	TenantID            string                 `json:"tenant_id"`
	SourceTrooperID   string                 `json:"source_trooper_id"`
	TargetType          string                 `json:"target_type"`
	TargetID            string                 `json:"target_id"`
	TargetName          string                 `json:"target_name"`
	LinkType            string                 `json:"link_type"`
	Protocol            string                 `json:"protocol"`
	Config              map[string]interface{} `json:"config"`
}

func NewCreateTrooperLinkCommand(
	tenantID, sourceTrooperID, targetType, targetID, targetName, linkType, protocol string,
	config map[string]interface{},
	userID, traceID string,
) *CreateTrooperLinkCommand {
	return &CreateTrooperLinkCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:          tenantID,
		SourceTrooperID: sourceTrooperID,
		TargetType:        targetType,
		TargetID:          targetID,
		TargetName:        targetName,
		LinkType:          linkType,
		Protocol:          protocol,
		Config:            config,
	}
}

func (c CreateTrooperLinkCommand) AggregateID() string { return c.BaseCommand.ID }
func (c CreateTrooperLinkCommand) CommandType() string  { return "CreateTrooperLink" }
func (c CreateTrooperLinkCommand) Validate() error {
	if c.SourceTrooperID == "" {
		return fmt.Errorf("source_trooper_id cannot be empty")
	}
	if c.TargetID == "" {
		return fmt.Errorf("target_id cannot be empty")
	}
	return nil
}

// DeleteTrooperLinkCommand deletes a trooper link.
type DeleteTrooperLinkCommand struct {
	commands.BaseCommand
	ID       string `json:"link_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteTrooperLinkCommand(id, tenantID, userID, traceID string) *DeleteTrooperLinkCommand {
	return &DeleteTrooperLinkCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ID:       id,
		TenantID: tenantID,
	}
}

func (c DeleteTrooperLinkCommand) AggregateID() string { return c.ID }
func (c DeleteTrooperLinkCommand) CommandType() string  { return "DeleteTrooperLink" }
func (c DeleteTrooperLinkCommand) Validate() error      { return nil }

// BindChannelCommand binds a channel configuration to a trooper.
type BindChannelCommand struct {
	commands.BaseCommand
	TenantID        string `json:"tenant_id"`
	TrooperID     string `json:"trooper_id"`
	ChannelConfigID string `json:"channel_config_id"`
}

func NewBindChannelCommand(tenantID, trooperID, channelConfigID, userID, traceID string) *BindChannelCommand {
	return &BindChannelCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:        tenantID,
		TrooperID:     trooperID,
		ChannelConfigID: channelConfigID,
	}
}

func (c BindChannelCommand) AggregateID() string { return c.TrooperID }
func (c BindChannelCommand) CommandType() string  { return "BindChannel" }
func (c BindChannelCommand) Validate() error      { return nil }

// UnbindChannelCommand unbinds a channel configuration from a trooper.
type UnbindChannelCommand struct {
	commands.BaseCommand
	TenantID        string `json:"tenant_id"`
	TrooperID     string `json:"trooper_id"`
	ChannelConfigID string `json:"channel_config_id"`
}

func NewUnbindChannelCommand(tenantID, trooperID, channelConfigID, userID, traceID string) *UnbindChannelCommand {
	return &UnbindChannelCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:        tenantID,
		TrooperID:     trooperID,
		ChannelConfigID: channelConfigID,
	}
}

func (c UnbindChannelCommand) AggregateID() string { return c.TrooperID }
func (c UnbindChannelCommand) CommandType() string  { return "UnbindChannel" }
func (c UnbindChannelCommand) Validate() error      { return nil }
