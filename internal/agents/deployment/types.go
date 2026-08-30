package deployment

import (
	"time"

	"github.com/lib/pq"
)

// DeploymentStatus represents the lifecycle state of a deployment.
type DeploymentStatus string

const (
	StatusActive  DeploymentStatus = "active"
	StatusPaused  DeploymentStatus = "paused"
	StatusRetired DeploymentStatus = "retired"
)

// InvocationStatus represents the state of an invocation.
type InvocationStatus string

const (
	InvocationRunning   InvocationStatus = "running"
	InvocationCompleted InvocationStatus = "completed"
	InvocationFailed    InvocationStatus = "failed"
	InvocationTimeout   InvocationStatus = "timeout"
)

// Deployment represents a versioned, published agent config with its own auth and limits.
type Deployment struct {
	ID       string           `db:"id" json:"id"`
	TenantID string           `db:"tenant_id" json:"tenant_id"`
	AgentID  string           `db:"agent_id" json:"agent_id"`
	Name     string           `db:"name" json:"name"`
	Version  int              `db:"version" json:"version"`
	Status   DeploymentStatus `db:"status" json:"status"`

	// Frozen config at deploy time
	AgentConfigSnapshot []byte `db:"agent_config_snapshot" json:"agent_config_snapshot"`

	// Limits
	RateLimitRPM          *int `db:"rate_limit_rpm" json:"rate_limit_rpm,omitempty"`
	RateLimitBurst        *int `db:"rate_limit_burst" json:"rate_limit_burst,omitempty"`
	SpendLimitDailyCents  *int `db:"spend_limit_daily_cents" json:"spend_limit_daily_cents,omitempty"`
	MaxConcurrentSessions int  `db:"max_concurrent_sessions" json:"max_concurrent_sessions"`
	MaxTurnsPerSession    *int `db:"max_turns_per_session" json:"max_turns_per_session,omitempty"`
	SessionTimeoutSeconds int  `db:"session_timeout_seconds" json:"session_timeout_seconds"`

	// Session tracking: when false, API invocations skip creating agent_sessions entries
	TrackSessions bool `db:"track_sessions" json:"track_sessions"`

	// CORS
	AllowedOrigins pq.StringArray `db:"allowed_origins" json:"allowed_origins"`

	// Metadata
	Description string `db:"description" json:"description,omitempty"`
	Changelog   string `db:"changelog" json:"changelog,omitempty"`
	DeployedBy  string `db:"deployed_by" json:"deployed_by,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// DeploymentKey represents a per-deployment API key.
type DeploymentKey struct {
	ID           string     `db:"id" json:"id"`
	TenantID     string     `db:"tenant_id" json:"tenant_id"`
	DeploymentID string     `db:"deployment_id" json:"deployment_id"`
	KeyHash      string     `db:"key_hash" json:"-"`
	KeyPrefix    string     `db:"key_prefix" json:"key_prefix"`
	Name         string     `db:"name" json:"name,omitempty"`
	ExpiresAt    *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	IsActive     bool       `db:"is_active" json:"is_active"`
	LastUsedAt   *time.Time `db:"last_used_at" json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
}

// Invocation represents a single API invocation of a deployment.
type Invocation struct {
	ID               string     `db:"id" json:"id"`
	TenantID         string     `db:"tenant_id" json:"tenant_id"`
	DeploymentID     string     `db:"deployment_id" json:"deployment_id"`
	SessionID        *string    `db:"session_id" json:"session_id,omitempty"`
	KeyID            *string    `db:"key_id" json:"key_id,omitempty"`
	Status           string     `db:"status" json:"status"`
	InputPreview     string     `db:"input_preview" json:"input_preview,omitempty"`
	OutputPreview    string     `db:"output_preview" json:"output_preview,omitempty"`
	Turns            int        `db:"turns" json:"turns"`
	PromptTokens     int        `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int        `db:"completion_tokens" json:"completion_tokens"`
	DurationMs       int        `db:"duration_ms" json:"duration_ms"`
	ErrorMessage     string     `db:"error_message" json:"error_message,omitempty"`
	ClientIP         string     `db:"client_ip" json:"client_ip,omitempty"`
	UserAgent        string     `db:"user_agent" json:"user_agent,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	CompletedAt      *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// InvokeRequest is the HTTP API request body for invoking a deployment.
type InvokeRequest struct {
	Message        string                 `json:"message"`
	Context        map[string]interface{} `json:"context,omitempty"`
	SessionID      string                 `json:"session_id,omitempty"`
	Version        *int                   `json:"version,omitempty"`
	MaxTurns       *int                   `json:"max_turns,omitempty"`
	TimeoutSeconds *int                   `json:"timeout_seconds,omitempty"`
}

// InvokeResponse is the HTTP API response for a sync invocation.
type InvokeResponse struct {
	SessionID  string        `json:"session_id"`
	Status     string        `json:"status"`
	Output     string        `json:"output"`
	Turns      int           `json:"turns"`
	Tokens     InvokeTokens  `json:"tokens"`
	DurationMs int           `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
}

// InvokeTokens holds token usage for an invocation.
type InvokeTokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}
