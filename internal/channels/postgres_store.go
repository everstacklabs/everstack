package channels

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PostgresStore implements ChannelStore using PostgreSQL.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgresStore creates a new PostgresStore.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateChannelConfig(ctx context.Context, cfg *ChannelConfigRecord) error {
	query := `
		INSERT INTO channel_configs (
			id, tenant_id, agent_id, platform, name, enabled, session_mode,
			credentials_encrypted, platform_config,
			max_messages_per_minute, max_sessions_per_user,
			response_format, max_response_length, max_tokens_per_day,
			idle_session_ttl_seconds, coalesce_window_ms, instance_affinity
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9,
			$10, $11,
			$12, $13, $14,
			$15, $16, $17
		)`
	_, err := s.db.ExecContext(ctx, query,
		cfg.ID, cfg.TenantID, cfg.AgentID, cfg.Platform, cfg.Name, cfg.Enabled, cfg.SessionMode,
		cfg.CredentialsEncrypted, cfg.PlatformConfig,
		cfg.MaxMessagesPerMinute, cfg.MaxSessionsPerUser,
		cfg.ResponseFormat, cfg.MaxResponseLength, cfg.MaxTokensPerDay,
		cfg.IdleSessionTTLSeconds, cfg.CoalesceWindowMs, cfg.InstanceAffinity,
	)
	return err
}

func (s *PostgresStore) GetChannelConfig(ctx context.Context, id, tenantID string) (*ChannelConfigRecord, error) {
	if tenantID == "" {
		// Defense-in-depth: never run this query without a tenant
		// scope. Treating it as not-found prevents callers that
		// accidentally pass an empty tenant from leaking arbitrary
		// rows by id.
		return nil, nil
	}
	var cfg ChannelConfigRecord
	query := `SELECT * FROM channel_configs WHERE id = $1 AND tenant_id = $2`
	if err := s.db.GetContext(ctx, &cfg, query, id, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &cfg, nil
}

func (s *PostgresStore) UpdateChannelConfig(ctx context.Context, cfg *ChannelConfigRecord) error {
	if cfg.TenantID == "" {
		return fmt.Errorf("UpdateChannelConfig: tenant id is required")
	}
	// Tenant column is in the WHERE so a row owned by tenant A cannot
	// be modified by a request bearing tenant B's id even if the
	// caller forged the row id.
	query := `
		UPDATE channel_configs SET
			agent_id = $1, name = $2, enabled = $3, session_mode = $4,
			credentials_encrypted = $5, platform_config = $6,
			max_messages_per_minute = $7, max_sessions_per_user = $8,
			response_format = $9, max_response_length = $10, max_tokens_per_day = $11,
			idle_session_ttl_seconds = $12, coalesce_window_ms = $13,
			instance_affinity = $14, updated_at = NOW()
		WHERE id = $15 AND tenant_id = $16`
	_, err := s.db.ExecContext(ctx, query,
		cfg.AgentID, cfg.Name, cfg.Enabled, cfg.SessionMode,
		cfg.CredentialsEncrypted, cfg.PlatformConfig,
		cfg.MaxMessagesPerMinute, cfg.MaxSessionsPerUser,
		cfg.ResponseFormat, cfg.MaxResponseLength, cfg.MaxTokensPerDay,
		cfg.IdleSessionTTLSeconds, cfg.CoalesceWindowMs,
		cfg.InstanceAffinity, cfg.ID, cfg.TenantID,
	)
	return err
}

func (s *PostgresStore) DeleteChannelConfig(ctx context.Context, id, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("DeleteChannelConfig: tenant id is required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_configs WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

func (s *PostgresStore) ListChannelConfigs(ctx context.Context, tenantID string, platform *string, agentID *string, enabled *bool, limit, offset int32) ([]*ChannelConfigRecord, int32, error) {
	if tenantID == "" {
		// An empty tenant scope on a list query is the exact pattern
		// the 2026-05-06 P0 leaked through — return an empty page
		// rather than every channel config in the table.
		return nil, 0, nil
	}
	where := "WHERE tenant_id = $1"
	args := []interface{}{tenantID}
	argIdx := 2

	if platform != nil {
		where += fmt.Sprintf(" AND platform = $%d", argIdx)
		args = append(args, *platform)
		argIdx++
	}
	if agentID != nil {
		where += fmt.Sprintf(" AND agent_id = $%d", argIdx)
		args = append(args, *agentID)
		argIdx++
	}
	if enabled != nil {
		where += fmt.Sprintf(" AND enabled = $%d", argIdx)
		args = append(args, *enabled)
		argIdx++
	}

	// Count
	var total int32
	countQ := "SELECT COUNT(*) FROM channel_configs " + where
	if err := s.db.GetContext(ctx, &total, countQ, args...); err != nil {
		return nil, 0, err
	}

	// Fetch
	if limit <= 0 {
		limit = 50
	}
	fetchQ := fmt.Sprintf("SELECT * FROM channel_configs %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var configs []*ChannelConfigRecord
	if err := s.db.SelectContext(ctx, &configs, fetchQ, args...); err != nil {
		return nil, 0, err
	}
	return configs, total, nil
}

func (s *PostgresStore) ListEnabledChannelConfigs(ctx context.Context) ([]*ChannelConfigRecord, error) {
	var configs []*ChannelConfigRecord
	query := `SELECT * FROM channel_configs WHERE enabled = true ORDER BY created_at ASC`
	if err := s.db.SelectContext(ctx, &configs, query); err != nil {
		return nil, err
	}
	return configs, nil
}

// ─── Session Mapping Operations ─────────────────────────────────────

func (s *PostgresStore) FindSessionMapping(ctx context.Context, channelConfigID, platformChannelRef, platformUserID, platformThreadRef string) (*SessionMappingRecord, error) {
	var mapping SessionMappingRecord
	query := `
		SELECT * FROM channel_session_mappings
		WHERE channel_config_id = $1
		  AND platform_channel_ref = $2
		  AND platform_user_id = $3
		  AND platform_thread_ref = $4
		ORDER BY created_at DESC
		LIMIT 1`
	if err := s.db.GetContext(ctx, &mapping, query, channelConfigID, platformChannelRef, platformUserID, platformThreadRef); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &mapping, nil
}

func (s *PostgresStore) CreateSessionMapping(ctx context.Context, mapping *SessionMappingRecord) error {
	query := `
		INSERT INTO channel_session_mappings (
			id, channel_config_id, platform_channel_ref, platform_user_id,
			platform_user_name, platform_thread_ref, agent_session_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.db.ExecContext(ctx, query,
		mapping.ID, mapping.ChannelConfigID, mapping.PlatformChannelRef,
		mapping.PlatformUserID, mapping.PlatformUserName,
		mapping.PlatformThreadRef, mapping.AgentSessionID,
	)
	return err
}

func (s *PostgresStore) UpdateMappingLastMessage(ctx context.Context, mappingID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channel_session_mappings SET last_message_at = NOW() WHERE id = $1`, mappingID)
	return err
}

func (s *PostgresStore) ListSessionMappings(ctx context.Context, channelConfigID string, limit, offset int32) ([]*SessionMappingRecord, int32, error) {
	var total int32
	if err := s.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM channel_session_mappings WHERE channel_config_id = $1`, channelConfigID); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 50
	}
	var mappings []*SessionMappingRecord
	query := `SELECT * FROM channel_session_mappings WHERE channel_config_id = $1 ORDER BY last_message_at DESC LIMIT $2 OFFSET $3`
	if err := s.db.SelectContext(ctx, &mappings, query, channelConfigID, limit, offset); err != nil {
		return nil, 0, err
	}
	return mappings, total, nil
}

func (s *PostgresStore) DeleteExpiredMappings(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM channel_session_mappings WHERE last_message_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RecordChannelMessage appends one metered inbound message.
func (s *PostgresStore) RecordChannelMessage(ctx context.Context, msg *ChannelMessageRecord) error {
	if msg == nil || msg.TenantID == "" || msg.ChannelConfigID == "" {
		// A row with no tenant cannot be metered or isolated, so drop it
		// rather than write an unattributable message into the meter.
		return fmt.Errorf("channel message requires tenant and channel config")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_messages (tenant_id, channel_config_id, platform, platform_user_id)
		VALUES ($1, $2, $3, $4)`,
		msg.TenantID, msg.ChannelConfigID, msg.Platform, msg.PlatformUserID)
	return err
}

// CountChannelMessagesThisMonth returns the tenant's inbound message count for
// the current calendar month. Tenant-scoped: an unscoped count would aggregate
// every tenant's traffic in the cloud's shared schema.
func (s *PostgresStore) CountChannelMessagesThisMonth(ctx context.Context, tenantID string) (int64, error) {
	if tenantID == "" {
		return 0, fmt.Errorf("tenant id required")
	}
	var n int64
	err := s.db.GetContext(ctx, &n, `
		SELECT COUNT(*) FROM channel_messages
		WHERE tenant_id = $1 AND created_at >= date_trunc('month', NOW())`, tenantID)
	return n, err
}
