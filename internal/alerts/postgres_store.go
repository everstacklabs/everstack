package alerts

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// PostgresStore implements AlertStore using PostgreSQL.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgresStore creates a new PostgresStore.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// ─── AlertRule CRUD ──────────────────────────────────────────────────

func (s *PostgresStore) CreateAlertRule(ctx context.Context, rule *AlertRuleRecord) error {
	query := `
		INSERT INTO alert_rules (
			id, tenant_id, name, description, category, severity, builtin_key,
			metric, operator, threshold, duration_seconds, filters, enabled, muted_until
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14
		)`
	_, err := s.db.ExecContext(ctx, query,
		rule.ID, rule.TenantID, rule.Name, rule.Description, rule.Category, rule.Severity, rule.BuiltinKey,
		rule.Metric, rule.Operator, rule.Threshold, rule.DurationSeconds, rule.Filters, rule.Enabled, rule.MutedUntil,
	)
	return err
}

func (s *PostgresStore) GetAlertRule(ctx context.Context, id, tenantID string) (*AlertRuleRecord, error) {
	var rule AlertRuleRecord
	query := `SELECT * FROM alert_rules WHERE id = $1 AND tenant_id = $2`
	if err := s.db.GetContext(ctx, &rule, query, id, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

func (s *PostgresStore) UpdateAlertRule(ctx context.Context, rule *AlertRuleRecord) error {
	query := `
		UPDATE alert_rules SET
			name = $1, description = $2, category = $3, severity = $4,
			metric = $5, operator = $6, threshold = $7, duration_seconds = $8,
			filters = $9, enabled = $10, muted_until = $11, updated_at = NOW()
		WHERE id = $12 AND tenant_id = $13`
	_, err := s.db.ExecContext(ctx, query,
		rule.Name, rule.Description, rule.Category, rule.Severity,
		rule.Metric, rule.Operator, rule.Threshold, rule.DurationSeconds,
		rule.Filters, rule.Enabled, rule.MutedUntil,
		rule.ID, rule.TenantID,
	)
	return err
}

func (s *PostgresStore) DeleteAlertRule(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

func (s *PostgresStore) ListAlertRules(ctx context.Context, tenantID string, category *string, enabled *bool, limit, offset int32) ([]*AlertRuleRecord, int32, error) {
	where := "WHERE tenant_id = $1"
	args := []interface{}{tenantID}
	argIdx := 2

	if category != nil {
		where += fmt.Sprintf(" AND category = $%d", argIdx)
		args = append(args, *category)
		argIdx++
	}
	if enabled != nil {
		where += fmt.Sprintf(" AND enabled = $%d", argIdx)
		args = append(args, *enabled)
		argIdx++
	}

	var total int32
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM alert_rules "+where, args...); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 50
	}
	fetchQ := fmt.Sprintf("SELECT * FROM alert_rules %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var rules []*AlertRuleRecord
	if err := s.db.SelectContext(ctx, &rules, fetchQ, args...); err != nil {
		return nil, 0, err
	}
	return rules, total, nil
}

func (s *PostgresStore) GetAlertRuleByBuiltinKey(ctx context.Context, tenantID, builtinKey string) (*AlertRuleRecord, error) {
	var rule AlertRuleRecord
	query := `SELECT * FROM alert_rules WHERE tenant_id = $1 AND builtin_key = $2`
	if err := s.db.GetContext(ctx, &rule, query, tenantID, builtinKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

func (s *PostgresStore) ListAllEnabledRules(ctx context.Context) ([]*AlertRuleRecord, error) {
	var rules []*AlertRuleRecord
	query := `SELECT * FROM alert_rules WHERE enabled = true AND (muted_until IS NULL OR muted_until < NOW())`
	if err := s.db.SelectContext(ctx, &rules, query); err != nil {
		return nil, err
	}
	return rules, nil
}

// ─── Rule-Target Associations ────────────────────────────────────────

func (s *PostgresStore) SetRuleTargets(ctx context.Context, ruleID string, targetIDs []string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rule_targets WHERE alert_rule_id = $1`, ruleID); err != nil {
		return err
	}

	for _, tid := range targetIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_rule_targets (alert_rule_id, target_id) VALUES ($1, $2)`, ruleID, tid); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStore) GetRuleTargetIDs(ctx context.Context, ruleID string) ([]string, error) {
	var ids []string
	if err := s.db.SelectContext(ctx, &ids, `SELECT target_id FROM alert_rule_targets WHERE alert_rule_id = $1`, ruleID); err != nil {
		return nil, err
	}
	return ids, nil
}

// ─── NotificationTarget CRUD ─────────────────────────────────────────

func (s *PostgresStore) CreateNotificationTarget(ctx context.Context, target *NotificationTargetRecord) error {
	query := `
		INSERT INTO alert_notification_targets (
			id, tenant_id, name, target_type, channel_config_id, platform_channel_ref,
			webhook_url, webhook_headers, email_addresses, min_severity, enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err := s.db.ExecContext(ctx, query,
		target.ID, target.TenantID, target.Name, target.TargetType, target.ChannelConfigID,
		target.PlatformChannelRef, target.WebhookURL, target.WebhookHeaders,
		target.EmailAddresses, target.MinSeverity, target.Enabled,
	)
	return err
}

func (s *PostgresStore) GetNotificationTarget(ctx context.Context, id, tenantID string) (*NotificationTargetRecord, error) {
	var target NotificationTargetRecord
	query := `SELECT * FROM alert_notification_targets WHERE id = $1 AND tenant_id = $2`
	if err := s.db.GetContext(ctx, &target, query, id, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &target, nil
}

func (s *PostgresStore) UpdateNotificationTarget(ctx context.Context, target *NotificationTargetRecord) error {
	query := `
		UPDATE alert_notification_targets SET
			name = $1, target_type = $2, channel_config_id = $3, platform_channel_ref = $4,
			webhook_url = $5, webhook_headers = $6, email_addresses = $7,
			min_severity = $8, enabled = $9, updated_at = NOW()
		WHERE id = $10 AND tenant_id = $11`
	_, err := s.db.ExecContext(ctx, query,
		target.Name, target.TargetType, target.ChannelConfigID, target.PlatformChannelRef,
		target.WebhookURL, target.WebhookHeaders, target.EmailAddresses,
		target.MinSeverity, target.Enabled,
		target.ID, target.TenantID,
	)
	return err
}

func (s *PostgresStore) DeleteNotificationTarget(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_notification_targets WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

func (s *PostgresStore) ListNotificationTargets(ctx context.Context, tenantID string, targetType *string, limit, offset int32) ([]*NotificationTargetRecord, int32, error) {
	where := "WHERE tenant_id = $1"
	args := []interface{}{tenantID}
	argIdx := 2

	if targetType != nil {
		where += fmt.Sprintf(" AND target_type = $%d", argIdx)
		args = append(args, *targetType)
		argIdx++
	}

	var total int32
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM alert_notification_targets "+where, args...); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 50
	}
	fetchQ := fmt.Sprintf("SELECT * FROM alert_notification_targets %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var targets []*NotificationTargetRecord
	if err := s.db.SelectContext(ctx, &targets, fetchQ, args...); err != nil {
		return nil, 0, err
	}
	return targets, total, nil
}

// ─── AlertEvent Operations ───────────────────────────────────────────

func (s *PostgresStore) CreateAlertEvent(ctx context.Context, event *AlertEventRecord) error {
	query := `
		INSERT INTO alert_events (
			id, tenant_id, alert_rule_id, status, metric_value, threshold,
			fired_at, last_notified_at, notification_count
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.db.ExecContext(ctx, query,
		event.ID, event.TenantID, event.AlertRuleID, event.Status, event.MetricValue,
		event.Threshold, event.FiredAt, event.LastNotifiedAt, event.NotificationCount,
	)
	return err
}

func (s *PostgresStore) GetActiveEventForRule(ctx context.Context, ruleID string) (*AlertEventRecord, error) {
	var event AlertEventRecord
	query := `SELECT * FROM alert_events WHERE alert_rule_id = $1 AND status IN ('firing', 'acknowledged') ORDER BY fired_at DESC LIMIT 1`
	if err := s.db.GetContext(ctx, &event, query, ruleID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

func (s *PostgresStore) UpdateAlertEvent(ctx context.Context, event *AlertEventRecord) error {
	query := `
		UPDATE alert_events SET
			status = $1, acknowledged_at = $2, acknowledged_by = $3,
			resolved_at = $4, last_notified_at = $5, notification_count = $6,
			updated_at = NOW()
		WHERE id = $7`
	_, err := s.db.ExecContext(ctx, query,
		event.Status, event.AcknowledgedAt, event.AcknowledgedBy,
		event.ResolvedAt, event.LastNotifiedAt, event.NotificationCount,
		event.ID,
	)
	return err
}

func (s *PostgresStore) ListAlertEvents(ctx context.Context, tenantID string, ruleID *string, status *string, limit, offset int32) ([]*AlertEventWithRule, int32, error) {
	where := "WHERE e.tenant_id = $1"
	args := []interface{}{tenantID}
	argIdx := 2

	if ruleID != nil {
		where += fmt.Sprintf(" AND e.alert_rule_id = $%d", argIdx)
		args = append(args, *ruleID)
		argIdx++
	}
	if status != nil {
		where += fmt.Sprintf(" AND e.status = $%d", argIdx)
		args = append(args, *status)
		argIdx++
	}

	var total int32
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM alert_events e %s", where)
	if err := s.db.GetContext(ctx, &total, countQ, args...); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 50
	}
	fetchQ := fmt.Sprintf(`
		SELECT e.*, r.name as alert_rule_name, r.severity as rule_severity
		FROM alert_events e
		LEFT JOIN alert_rules r ON r.id = e.alert_rule_id
		%s ORDER BY e.fired_at DESC LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var events []*AlertEventWithRule
	if err := s.db.SelectContext(ctx, &events, fetchQ, args...); err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func (s *PostgresStore) GetTargetsForRule(ctx context.Context, ruleID string) ([]*NotificationTargetRecord, error) {
	var targets []*NotificationTargetRecord
	query := `
		SELECT t.* FROM alert_notification_targets t
		INNER JOIN alert_rule_targets rt ON rt.target_id = t.id
		WHERE rt.alert_rule_id = $1 AND t.enabled = true`
	if err := s.db.SelectContext(ctx, &targets, query, ruleID); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *PostgresStore) GetAlertsSummary(ctx context.Context, tenantID string) (*AlertsSummary, error) {
	var summary AlertsSummary
	query := `
		SELECT
			COALESCE((SELECT COUNT(*) FROM alert_events WHERE tenant_id = $1 AND status = 'firing'), 0) as firing_count,
			COALESCE((SELECT COUNT(*) FROM alert_events WHERE tenant_id = $1 AND status = 'acknowledged'), 0) as acknowledged_count,
			COALESCE((SELECT COUNT(*) FROM alert_rules WHERE tenant_id = $1), 0) as total_rules,
			COALESCE((SELECT COUNT(*) FROM alert_rules WHERE tenant_id = $1 AND enabled = true), 0) as enabled_rules`
	if err := s.db.GetContext(ctx, &summary, query, tenantID); err != nil {
		return nil, err
	}
	return &summary, nil
}
