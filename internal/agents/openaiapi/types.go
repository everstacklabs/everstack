package openaiapi

import (
	"encoding/json"
	"time"
)

const Provider = "openai_agents"

type Capabilities struct {
	Enabled              bool     `json:"enabled"`
	Provider             string   `json:"provider"`
	EnvironmentModes     []string `json:"environment_modes"`
	ToolTypes            []string `json:"tool_types"`
	FileOperations       []string `json:"file_operations"`
	ExecutorVersion      string   `json:"executor_version,omitempty"`
	Readiness            string   `json:"readiness"`
	CanManageConnections bool     `json:"can_manage_connections"`
	CanRunSessions       bool     `json:"can_run_sessions"`
	CanResolveApprovals  bool     `json:"can_resolve_approvals"`
}

type Ownership struct {
	OrganizationID string `json:"-"`
	InstanceID     string `json:"-"`
	PrincipalID    string `json:"-"`
	Role           string `json:"-"`
}

type Connection struct {
	ID                 string    `db:"id" json:"id"`
	Name               string    `db:"name" json:"name"`
	Provider           string    `db:"provider" json:"provider"`
	ProjectID          string    `db:"openai_project_id" json:"project_id,omitempty"`
	OpenAIOrganization string    `db:"openai_organization_id" json:"organization_id,omitempty"`
	OpenAIPrincipal    string    `db:"openai_principal_id" json:"principal_id,omitempty"`
	AllowedModels      []string  `db:"-" json:"allowed_models,omitempty"`
	Status             string    `db:"status" json:"status"`
	LastError          string    `db:"last_error" json:"last_error,omitempty"`
	CreatedAt          time.Time `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time `db:"updated_at" json:"updated_at"`
	credentialRef      string
	executorRef        string
}

type ConnectionSecret struct {
	APIKey         string `json:"api_key"`
	ExecutorKey    string `json:"executor_key,omitempty"`
	ProjectID      string `json:"-"`
	OrganizationID string `json:"-"`
}

type CreateConnectionRequest struct {
	Name           string   `json:"name"`
	APIKey         string   `json:"api_key"`
	ExecutorKey    string   `json:"executor_key,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	OrganizationID string   `json:"organization_id,omitempty"`
	PrincipalID    string   `json:"principal_id,omitempty"`
	AllowedModels  []string `json:"allowed_models,omitempty"`
}
type UpdateConnectionRequest struct {
	Name           *string   `json:"name,omitempty"`
	APIKey         *string   `json:"api_key,omitempty"`
	ExecutorKey    *string   `json:"executor_key,omitempty"`
	ProjectID      *string   `json:"project_id,omitempty"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	PrincipalID    *string   `json:"principal_id,omitempty"`
	AllowedModels  *[]string `json:"allowed_models,omitempty"`
}

type EnvironmentSpec struct {
	Type               string `json:"type"`
	WorkspaceDirectory string `json:"workspace_directory,omitempty"`
}

type ToolSpec struct {
	Type       string `json:"type"`
	ResourceID string `json:"resource_id,omitempty"`
	Name       string `json:"name,omitempty"`
}
type Approval struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	TurnID       string          `json:"turn_id"`
	CallID       string          `json:"call_id"`
	ToolName     string          `json:"tool_name"`
	ResourceKind string          `json:"resource_kind,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	Status       string          `json:"status"`
	RequestedBy  string          `json:"requested_by"`
	Arguments    json.RawMessage `json:"arguments"`
	RequestedAt  time.Time       `json:"requested_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

type CreateSessionRequest struct {
	ConnectionID   string          `json:"connection_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Model          string          `json:"model"`
	Instructions   string          `json:"instructions,omitempty"`
	Environment    EnvironmentSpec `json:"environment"`
	Input          json.RawMessage `json:"input,omitempty"`
	Tools          []ToolSpec      `json:"tools,omitempty"`
	resolvedTools  []json.RawMessage
}

type Session struct {
	ID                       string          `db:"id" json:"id"`
	Provider                 string          `db:"provider" json:"provider"`
	ConnectionID             string          `db:"connection_id" json:"connection_id"`
	ConversationID           string          `db:"conversation_id" json:"conversation_id,omitempty"`
	UpstreamSessionID        string          `db:"upstream_session_id" json:"upstream_session_id,omitempty"`
	UpstreamEnvironmentID    string          `db:"upstream_environment_id" json:"upstream_environment_id,omitempty"`
	SandboxID                string          `db:"sandbox_id" json:"sandbox_id,omitempty"`
	Model                    string          `db:"model" json:"model"`
	Environment              EnvironmentSpec `db:"-" json:"environment"`
	Status                   string          `db:"status" json:"status"`
	Readiness                string          `db:"readiness" json:"readiness"`
	CleanupStatus            string          `db:"cleanup_status" json:"cleanup_status"`
	EnvironmentCleanupStatus string          `db:"environment_cleanup_status" json:"environment_cleanup_status,omitempty"`
	LastError                string          `db:"last_error" json:"last_error,omitempty"`
	CreatedAt                time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt                time.Time       `db:"updated_at" json:"updated_at"`
	IdempotencyKey           string          `db:"idempotency_key" json:"-"`
	RequestFingerprint       string          `db:"request_fingerprint" json:"-"`
}

type InputRequest struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Input          json.RawMessage `json:"input"`
}

type Event struct {
	Cursor         int64           `db:"cursor" json:"cursor"`
	EventID        string          `db:"event_id" json:"event_id"`
	Type           string          `db:"event_type" json:"type"`
	Version        string          `db:"event_version" json:"version"`
	UpstreamItemID string          `db:"upstream_item_id" json:"upstream_item_id,omitempty"`
	Payload        json.RawMessage `db:"payload" json:"payload"`
	ToolFailed     bool            `db:"tool_failed" json:"tool_failed,omitempty"`
	CreatedAt      time.Time       `db:"created_at" json:"created_at"`
}

type UpstreamSession struct {
	ID                 string
	EnvironmentID      string
	RemoteURL          string
	Status             string
	Readiness          string
	HasRequiredActions bool
	Raw                json.RawMessage
}

type UpstreamEvent struct {
	ID            string
	Type          string
	Version       string
	ItemID        string
	Payload       json.RawMessage
	ActionPayload json.RawMessage
	Failed        bool
	CreatedAt     time.Time
}
type ToolResult struct {
	TurnID, CallID       string
	Success              bool
	Output, Error, State string
}
