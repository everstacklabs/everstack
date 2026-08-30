package agents

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// CreateAgentCommand creates a new agent definition.
type CreateAgentCommand struct {
	commands.BaseCommand
	TenantID            string                 `json:"tenant_id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	Model               string                 `json:"model"`
	SystemPrompt        string                 `json:"system_prompt"`
	Tools               []string               `json:"tools"`
	Config              map[string]interface{} `json:"config"`
	MaxTurns            int32                  `json:"max_turns"`
	MaxToolCallsPerTurn int32                  `json:"max_tool_calls_per_turn"`
	Mode                string                 `json:"mode"`
	MaxSteps            *int32                 `json:"max_steps,omitempty"`
	TaskPermissionMode  string                 `json:"task_permission_mode"`
	Hidden              bool                   `json:"hidden"`
	Color               *string                `json:"color,omitempty"`
	WorkingDirectory    *string                `json:"working_directory,omitempty"`
	MentionAlias        *string                `json:"mention_alias,omitempty"`
	// Persistent agent fields
	LifecycleMode         string                 `json:"lifecycle_mode"`
	Icon                  *string                `json:"icon,omitempty"`
	SoulMD                string                 `json:"soul_md"`
	IdentityMD            string                 `json:"identity_md"`
	UserMD                string                 `json:"user_md"`
	RoleMD                string                 `json:"role_md"`
	SandboxImage          string                 `json:"sandbox_image"`
	SandboxCPULimit       float64                `json:"sandbox_cpu_limit"`
	SandboxMemoryMB       int64                  `json:"sandbox_memory_mb"`
	SandboxDiskMB         int64                  `json:"sandbox_disk_mb"`
	SandboxTimeoutSeconds int32                  `json:"sandbox_timeout_seconds"`
	SandboxNetworkMode    string                 `json:"sandbox_network_mode"`
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
	AutoProvision         bool                   `json:"auto_provision"`
}

func NewCreateAgentCommand(
	tenantID, name, description, model, systemPrompt string,
	tools []string,
	config map[string]interface{},
	maxTurns, maxToolCallsPerTurn int32,
	mode string,
	maxSteps *int32,
	taskPermissionMode string,
	hidden bool,
	color, workingDirectory, mentionAlias *string,
	lifecycleMode string,
	icon *string,
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
	autoProvision bool,
	userID, traceID string,
) *CreateAgentCommand {
	return &CreateAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:            tenantID,
		Name:                name,
		Description:         description,
		Model:               model,
		SystemPrompt:        systemPrompt,
		Tools:               tools,
		Config:              config,
		MaxTurns:            maxTurns,
		MaxToolCallsPerTurn: maxToolCallsPerTurn,
		Mode:                mode,
		MaxSteps:            maxSteps,
		TaskPermissionMode:  taskPermissionMode,
		Hidden:              hidden,
		Color:               color,
		WorkingDirectory:    workingDirectory,
		MentionAlias:            mentionAlias,
		LifecycleMode:           lifecycleMode,
		Icon:                    icon,
		SoulMD:                  soulMD,
		IdentityMD:              identityMD,
		UserMD:                  userMD,
		RoleMD:                  roleMD,
		SandboxImage:            sandboxImage,
		SandboxCPULimit:         sandboxCPULimit,
		SandboxMemoryMB:         sandboxMemoryMB,
		SandboxDiskMB:           sandboxDiskMB,
		SandboxTimeoutSeconds:   sandboxTimeoutSeconds,
		SandboxNetworkMode:      sandboxNetworkMode,
		SandboxAllowedHosts:     sandboxAllowedHosts,
		SandboxEnvVars:          sandboxEnvVars,
		SandboxSSHEnabled:       sandboxSSHEnabled,
		SandboxGitRepoURL:       sandboxGitRepoURL,
		SandboxGitBranch:        sandboxGitBranch,
		DBSqlitePath:            dbSqlitePath,
		DBLanceDBPath:           dbLanceDBPath,
		DBRedbPath:              dbRedbPath,
		MaxConcurrentWorkers:    maxConcurrentWorkers,
		WorkerPoolConfig:        workerPoolConfig,
		AutoProvision:           autoProvision,
	}
}

func (c CreateAgentCommand) AggregateID() string { return c.ID }
func (c CreateAgentCommand) CommandType() string  { return "CreateAgent" }
func (c CreateAgentCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if c.Model == "" {
		return fmt.Errorf("model cannot be empty")
	}
	return nil
}

// UpdateAgentCommand updates an existing agent definition.
type UpdateAgentCommand struct {
	commands.BaseCommand
	AgentID             string                 `json:"agent_id"`
	TenantID            string                 `json:"tenant_id"`
	Name                *string                `json:"name,omitempty"`
	Description         *string                `json:"description,omitempty"`
	Model               *string                `json:"model,omitempty"`
	SystemPrompt        *string                `json:"system_prompt,omitempty"`
	Tools               []string               `json:"tools,omitempty"`
	Config              map[string]interface{} `json:"config,omitempty"`
	MaxTurns            *int32                 `json:"max_turns,omitempty"`
	MaxToolCallsPerTurn *int32                 `json:"max_tool_calls_per_turn,omitempty"`
	Enabled             *bool                  `json:"enabled,omitempty"`
	Mode                *string                `json:"mode,omitempty"`
	MaxSteps            *int32                 `json:"max_steps,omitempty"`
	TaskPermissionMode  *string                `json:"task_permission_mode,omitempty"`
	Hidden              *bool                  `json:"hidden,omitempty"`
	Color               *string                `json:"color,omitempty"`
	WorkingDirectory    *string                `json:"working_directory,omitempty"`
	MentionAlias        *string                `json:"mention_alias,omitempty"`
	// Persistent agent fields
	LifecycleMode         *string                `json:"lifecycle_mode,omitempty"`
	Icon                  *string                `json:"icon,omitempty"`
	SoulMD                *string                `json:"soul_md,omitempty"`
	IdentityMD            *string                `json:"identity_md,omitempty"`
	UserMD                *string                `json:"user_md,omitempty"`
	RoleMD                *string                `json:"role_md,omitempty"`
	SandboxImage          *string                `json:"sandbox_image,omitempty"`
	SandboxCPULimit       *float64               `json:"sandbox_cpu_limit,omitempty"`
	SandboxMemoryMB       *int64                 `json:"sandbox_memory_mb,omitempty"`
	SandboxDiskMB         *int64                 `json:"sandbox_disk_mb,omitempty"`
	SandboxTimeoutSeconds *int32                 `json:"sandbox_timeout_seconds,omitempty"`
	SandboxNetworkMode    *string                `json:"sandbox_network_mode,omitempty"`
	SandboxAllowedHosts   []string               `json:"sandbox_allowed_hosts,omitempty"`
	SandboxEnvVars        map[string]string      `json:"sandbox_env_vars,omitempty"`
	SandboxSSHEnabled     *bool                  `json:"sandbox_ssh_enabled,omitempty"`
	SandboxGitRepoURL     *string                `json:"sandbox_git_repo_url,omitempty"`
	SandboxGitBranch      *string                `json:"sandbox_git_branch,omitempty"`
	DBSqlitePath          *string                `json:"db_sqlite_path,omitempty"`
	DBLanceDBPath         *string                `json:"db_lancedb_path,omitempty"`
	DBRedbPath            *string                `json:"db_redb_path,omitempty"`
	MaxConcurrentWorkers  *int32                 `json:"max_concurrent_workers,omitempty"`
	WorkerPoolConfig      map[string]interface{} `json:"worker_pool_config,omitempty"`
}

func NewUpdateAgentCommand(agentID, tenantID, userID, traceID string) *UpdateAgentCommand {
	return &UpdateAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		AgentID:  agentID,
		TenantID: tenantID,
	}
}

func (c UpdateAgentCommand) AggregateID() string { return c.AgentID }
func (c UpdateAgentCommand) CommandType() string  { return "UpdateAgent" }
func (c UpdateAgentCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	return nil
}

// DeleteAgentCommand deletes an agent definition.
type DeleteAgentCommand struct {
	commands.BaseCommand
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteAgentCommand(agentID, tenantID, userID, traceID string) *DeleteAgentCommand {
	return &DeleteAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		AgentID:  agentID,
		TenantID: tenantID,
	}
}

func (c DeleteAgentCommand) AggregateID() string { return c.AgentID }
func (c DeleteAgentCommand) CommandType() string  { return "DeleteAgent" }
func (c DeleteAgentCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	return nil
}

// CreateSessionCommand creates a new agent session.
// Exactly one of AgentID or TrooperID must be set.
type CreateSessionCommand struct {
	commands.BaseCommand
	TenantID    string                 `json:"tenant_id"`
	AgentID     string                 `json:"agent_id"`
	TrooperID string                 `json:"trooper_id,omitempty"`
	Metadata    map[string]interface{} `json:"metadata"`
}

func NewCreateSessionCommand(tenantID, agentID string, metadata map[string]interface{}, userID, traceID string) *CreateSessionCommand {
	return &CreateSessionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID: tenantID,
		AgentID:  agentID,
		Metadata: metadata,
	}
}

func (c CreateSessionCommand) AggregateID() string { return c.ID }
func (c CreateSessionCommand) CommandType() string  { return "CreateAgentSession" }
func (c CreateSessionCommand) Validate() error {
	if c.AgentID == "" && c.TrooperID == "" {
		return fmt.Errorf("agent_id is required")
	}
	return nil
}

// RunTurnCommand runs a single turn in an agent session.
type RunTurnCommand struct {
	commands.BaseCommand
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
	UserInput string `json:"user_input"`
}

func NewRunTurnCommand(tenantID, sessionID, userInput, userID, traceID string) *RunTurnCommand {
	return &RunTurnCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:  tenantID,
		SessionID: sessionID,
		UserInput: userInput,
	}
}

func (c RunTurnCommand) AggregateID() string { return c.SessionID }
func (c RunTurnCommand) CommandType() string  { return "RunAgentTurn" }
func (c RunTurnCommand) Validate() error {
	if c.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	if c.UserInput == "" {
		return fmt.Errorf("user_input cannot be empty")
	}
	return nil
}

// CancelSessionCommand cancels a running session.
type CancelSessionCommand struct {
	commands.BaseCommand
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
}

func NewCancelSessionCommand(tenantID, sessionID, userID, traceID string) *CancelSessionCommand {
	return &CancelSessionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:  tenantID,
		SessionID: sessionID,
	}
}

func (c CancelSessionCommand) AggregateID() string { return c.SessionID }
func (c CancelSessionCommand) CommandType() string  { return "CancelAgentSession" }
func (c CancelSessionCommand) Validate() error {
	if c.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// CompleteTurnCommand records a completed turn (used internally by the runtime engine).
//
// PromptTokens is inclusive of cached input (matching gw.Usage semantics).
// CacheReadInputTokens and CacheWriteInputTokens are non-overlapping
// subsets of PromptTokens so consumers can show the fresh / cache-read /
// cache-write breakdown and apply per-bucket billing rates.
type CompleteTurnCommand struct {
	commands.BaseCommand
	SessionID             string `json:"session_id"`
	TurnNumber            int32  `json:"turn_number"`
	ExpectedTurnCount     int32  `json:"expected_turn_count"` // Optimistic concurrency: expected session turn_count before this turn
	UserInput             string `json:"user_input"`
	AssistantOutput       string `json:"assistant_output"`
	ToolCalls             string `json:"tool_calls"` // JSON
	Timeline              string `json:"timeline"`   // JSON: ordered interleaved turn timeline (set post-construction)
	PromptTokens          int32  `json:"prompt_tokens"`
	CompletionTokens      int32  `json:"completion_tokens"`
	TotalTokens           int32  `json:"total_tokens"`
	CacheReadInputTokens  int32  `json:"cache_read_input_tokens"`
	CacheWriteInputTokens int32  `json:"cache_write_input_tokens"`
	LatencyMs             int64  `json:"latency_ms"`
	Error                 string `json:"error"`
	SessionStatus         string `json:"session_status"`
}

func NewCompleteTurnCommand(sessionID string, turnNumber, expectedTurnCount int32, userInput, assistantOutput, toolCalls string,
	promptTokens, completionTokens, totalTokens int32, latencyMs int64, errMsg, sessionStatus string,
) *CompleteTurnCommand {
	return &CompleteTurnCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		SessionID:         sessionID,
		TurnNumber:        turnNumber,
		ExpectedTurnCount: expectedTurnCount,
		UserInput:         userInput,
		AssistantOutput:   assistantOutput,
		ToolCalls:         toolCalls,
		PromptTokens:      promptTokens,
		CompletionTokens:  completionTokens,
		TotalTokens:       totalTokens,
		LatencyMs:         latencyMs,
		Error:             errMsg,
		SessionStatus:     sessionStatus,
	}
}

func (c CompleteTurnCommand) AggregateID() string { return c.SessionID }
func (c CompleteTurnCommand) CommandType() string  { return "CompleteAgentTurn" }
func (c CompleteTurnCommand) Validate() error {
	if c.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// CompleteSessionCommand explicitly completes a session (user/API requested).
type CompleteSessionCommand struct {
	commands.BaseCommand
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
}

func NewCompleteSessionCommand(tenantID, sessionID, userID, traceID string) *CompleteSessionCommand {
	return &CompleteSessionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:  tenantID,
		SessionID: sessionID,
	}
}

func (c CompleteSessionCommand) AggregateID() string { return c.SessionID }
func (c CompleteSessionCommand) CommandType() string  { return "CompleteAgentSession" }
func (c CompleteSessionCommand) Validate() error {
	if c.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// RequestApprovalCommand creates a HITL approval review for tool calls.
type RequestApprovalCommand struct {
	commands.BaseCommand
	ReviewID      string          `json:"review_id"`
	SessionID     string          `json:"session_id"`
	TenantID      string          `json:"tenant_id"`
	AgentID       string          `json:"agent_id"`
	TurnNumber    int32           `json:"turn_number"`
	Iteration     int32           `json:"iteration"`
	ToolCalls     json.RawMessage `json:"tool_calls"`
	DefaultAction string          `json:"default_action"`
	ExpiresAt     time.Time       `json:"expires_at"`
}

func NewRequestApprovalCommand(
	reviewID, sessionID, tenantID, agentID string,
	turnNumber, iteration int32,
	toolCalls json.RawMessage,
	defaultAction string,
	expiresAt time.Time,
) *RequestApprovalCommand {
	return &RequestApprovalCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		ReviewID:      reviewID,
		SessionID:     sessionID,
		TenantID:      tenantID,
		AgentID:       agentID,
		TurnNumber:    turnNumber,
		Iteration:     iteration,
		ToolCalls:     toolCalls,
		DefaultAction: defaultAction,
		ExpiresAt:     expiresAt,
	}
}

func (c RequestApprovalCommand) AggregateID() string { return c.ReviewID }
func (c RequestApprovalCommand) CommandType() string  { return "RequestApproval" }
func (c RequestApprovalCommand) Validate() error {
	if c.ReviewID == "" {
		return fmt.Errorf("review_id cannot be empty")
	}
	if c.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// ProvisionAgentCommand provisions a persistent agent's sandbox.
type ProvisionAgentCommand struct {
	commands.BaseCommand
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

func NewProvisionAgentCommand(agentID, tenantID, userID, traceID string) *ProvisionAgentCommand {
	return &ProvisionAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		AgentID:  agentID,
		TenantID: tenantID,
	}
}

func (c ProvisionAgentCommand) AggregateID() string { return c.AgentID }
func (c ProvisionAgentCommand) CommandType() string  { return "ProvisionAgent" }
func (c ProvisionAgentCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	return nil
}

// SleepAgentCommand puts a persistent agent to sleep.
type SleepAgentCommand struct {
	commands.BaseCommand
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

func NewSleepAgentCommand(agentID, tenantID, userID, traceID string) *SleepAgentCommand {
	return &SleepAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		AgentID:  agentID,
		TenantID: tenantID,
	}
}

func (c SleepAgentCommand) AggregateID() string { return c.AgentID }
func (c SleepAgentCommand) CommandType() string  { return "SleepAgent" }
func (c SleepAgentCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	return nil
}

// WakeAgentCommand wakes a sleeping persistent agent.
type WakeAgentCommand struct {
	commands.BaseCommand
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

func NewWakeAgentCommand(agentID, tenantID, userID, traceID string) *WakeAgentCommand {
	return &WakeAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		AgentID:  agentID,
		TenantID: tenantID,
	}
}

func (c WakeAgentCommand) AggregateID() string { return c.AgentID }
func (c WakeAgentCommand) CommandType() string  { return "WakeAgent" }
func (c WakeAgentCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	return nil
}

// CreateAgentLinkCommand creates a link between agents.
type CreateAgentLinkCommand struct {
	commands.BaseCommand
	TenantID      string                 `json:"tenant_id"`
	SourceAgentID string                 `json:"source_agent_id"`
	TargetType    string                 `json:"target_type"`
	TargetID      string                 `json:"target_id"`
	TargetName    string                 `json:"target_name"`
	LinkType      string                 `json:"link_type"`
	Protocol      string                 `json:"protocol"`
	Config        map[string]interface{} `json:"config"`
}

func NewCreateAgentLinkCommand(tenantID, sourceAgentID, targetType, targetID, targetName, linkType, protocol string, config map[string]interface{}, userID, traceID string) *CreateAgentLinkCommand {
	return &CreateAgentLinkCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:      tenantID,
		SourceAgentID: sourceAgentID,
		TargetType:    targetType,
		TargetID:      targetID,
		TargetName:    targetName,
		LinkType:      linkType,
		Protocol:      protocol,
		Config:        config,
	}
}

func (c CreateAgentLinkCommand) AggregateID() string { return c.ID }
func (c CreateAgentLinkCommand) CommandType() string  { return "CreateAgentLink" }
func (c CreateAgentLinkCommand) Validate() error {
	if c.SourceAgentID == "" {
		return fmt.Errorf("source_agent_id cannot be empty")
	}
	if c.TargetID == "" {
		return fmt.Errorf("target_id cannot be empty")
	}
	return nil
}

// DeleteAgentLinkCommand deletes an agent link.
type DeleteAgentLinkCommand struct {
	commands.BaseCommand
	LinkID   string `json:"link_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteAgentLinkCommand(linkID, tenantID, userID, traceID string) *DeleteAgentLinkCommand {
	return &DeleteAgentLinkCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		LinkID:   linkID,
		TenantID: tenantID,
	}
}

func (c DeleteAgentLinkCommand) AggregateID() string { return c.LinkID }
func (c DeleteAgentLinkCommand) CommandType() string  { return "DeleteAgentLink" }
func (c DeleteAgentLinkCommand) Validate() error {
	if c.LinkID == "" {
		return fmt.Errorf("link_id cannot be empty")
	}
	return nil
}

// BindAgentChannelCommand binds a channel to an agent.
type BindAgentChannelCommand struct {
	commands.BaseCommand
	TenantID        string `json:"tenant_id"`
	AgentID         string `json:"agent_id"`
	ChannelConfigID string `json:"channel_config_id"`
}

func NewBindAgentChannelCommand(tenantID, agentID, channelConfigID, userID, traceID string) *BindAgentChannelCommand {
	return &BindAgentChannelCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:        tenantID,
		AgentID:         agentID,
		ChannelConfigID: channelConfigID,
	}
}

func (c BindAgentChannelCommand) AggregateID() string { return c.ID }
func (c BindAgentChannelCommand) CommandType() string  { return "BindAgentChannel" }
func (c BindAgentChannelCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	if c.ChannelConfigID == "" {
		return fmt.Errorf("channel_config_id cannot be empty")
	}
	return nil
}

// UnbindAgentChannelCommand unbinds a channel from an agent.
type UnbindAgentChannelCommand struct {
	commands.BaseCommand
	TenantID        string `json:"tenant_id"`
	AgentID         string `json:"agent_id"`
	ChannelConfigID string `json:"channel_config_id"`
}

func NewUnbindAgentChannelCommand(tenantID, agentID, channelConfigID, userID, traceID string) *UnbindAgentChannelCommand {
	return &UnbindAgentChannelCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:        tenantID,
		AgentID:         agentID,
		ChannelConfigID: channelConfigID,
	}
}

func (c UnbindAgentChannelCommand) AggregateID() string { return c.AgentID }
func (c UnbindAgentChannelCommand) CommandType() string  { return "UnbindAgentChannel" }
func (c UnbindAgentChannelCommand) Validate() error {
	if c.AgentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}
	if c.ChannelConfigID == "" {
		return fmt.Errorf("channel_config_id cannot be empty")
	}
	return nil
}
