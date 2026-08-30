package channels

import (
	"context"
	"database/sql"
	"time"
)

// ChannelConfigRecord is the database representation of a channel config.
// AgentID is nullable — when NULL the channel operates in dispatcher mode,
// routing messages to agents via mention matching or auto-route with HITL.
type ChannelConfigRecord struct {
	ID                    string         `db:"id"`
	TenantID              string         `db:"tenant_id"`
	AgentID               sql.NullString `db:"agent_id"`
	Platform              string         `db:"platform"`
	Name                  string         `db:"name"`
	Enabled               bool           `db:"enabled"`
	SessionMode           string         `db:"session_mode"`
	CredentialsEncrypted  []byte         `db:"credentials_encrypted"`
	PlatformConfig        []byte         `db:"platform_config"`
	MaxMessagesPerMinute  int32          `db:"max_messages_per_minute"`
	MaxSessionsPerUser    int32          `db:"max_sessions_per_user"`
	ResponseFormat        string         `db:"response_format"`
	MaxResponseLength     int32          `db:"max_response_length"`
	MaxTokensPerDay       int64          `db:"max_tokens_per_day"`
	IdleSessionTTLSeconds int32          `db:"idle_session_ttl_seconds"`
	CoalesceWindowMs      int32          `db:"coalesce_window_ms"`
	InstanceAffinity      string         `db:"instance_affinity"`
	CreatedAt             time.Time      `db:"created_at"`
	UpdatedAt             time.Time      `db:"updated_at"`
}

// ChannelMessageRecord is one metered inbound channel message. It carries no
// message text on purpose: this table is a meter, not an archive, and the
// content already lives on the agent session.
type ChannelMessageRecord struct {
	ID              string    `db:"id"`
	TenantID        string    `db:"tenant_id"`
	ChannelConfigID string    `db:"channel_config_id"`
	Platform        string    `db:"platform"`
	PlatformUserID  string    `db:"platform_user_id"`
	CreatedAt       time.Time `db:"created_at"`
}

// SessionMappingRecord is the database representation of a session mapping.
type SessionMappingRecord struct {
	ID                 string    `db:"id"`
	ChannelConfigID    string    `db:"channel_config_id"`
	PlatformChannelRef string    `db:"platform_channel_ref"`
	PlatformUserID     string    `db:"platform_user_id"`
	PlatformUserName   string    `db:"platform_user_name"`
	PlatformThreadRef  string    `db:"platform_thread_ref"`
	AgentSessionID     string    `db:"agent_session_id"`
	CreatedAt          time.Time `db:"created_at"`
	LastMessageAt      time.Time `db:"last_message_at"`
}

// ChannelStore defines database operations for channel configs and session mappings.
type ChannelStore interface {
	// Channel config CRUD
	CreateChannelConfig(ctx context.Context, cfg *ChannelConfigRecord) error
	GetChannelConfig(ctx context.Context, id, tenantID string) (*ChannelConfigRecord, error)
	UpdateChannelConfig(ctx context.Context, cfg *ChannelConfigRecord) error
	DeleteChannelConfig(ctx context.Context, id, tenantID string) error
	ListChannelConfigs(ctx context.Context, tenantID string, platform *string, agentID *string, enabled *bool, limit, offset int32) ([]*ChannelConfigRecord, int32, error)
	ListEnabledChannelConfigs(ctx context.Context) ([]*ChannelConfigRecord, error)

	// Message metering. RecordChannelMessage appends one row per inbound
	// message; CountChannelMessagesThisMonth reads the tenant's current
	// calendar-month total, which is what MESSAGES_MONTHLY is measured
	// against and what the usage reporter forwards to billing.
	RecordChannelMessage(ctx context.Context, msg *ChannelMessageRecord) error
	CountChannelMessagesThisMonth(ctx context.Context, tenantID string) (int64, error)

	// Session mapping operations
	FindSessionMapping(ctx context.Context, channelConfigID, platformChannelRef, platformUserID, platformThreadRef string) (*SessionMappingRecord, error)
	CreateSessionMapping(ctx context.Context, mapping *SessionMappingRecord) error
	UpdateMappingLastMessage(ctx context.Context, mappingID string) error
	ListSessionMappings(ctx context.Context, channelConfigID string, limit, offset int32) ([]*SessionMappingRecord, int32, error)
	DeleteExpiredMappings(ctx context.Context, olderThan time.Time) (int64, error)
}
