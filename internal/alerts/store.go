package alerts

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// ─── Record Types ────────────────────────────────────────────────────

type AlertRuleRecord struct {
	ID              string         `db:"id"`
	TenantID        string         `db:"tenant_id"`
	Name            string         `db:"name"`
	Description     string         `db:"description"`
	Category        string         `db:"category"`
	Severity        string         `db:"severity"`
	BuiltinKey      sql.NullString `db:"builtin_key"`
	Metric          string         `db:"metric"`
	Operator        string         `db:"operator"`
	Threshold       float64        `db:"threshold"`
	DurationSeconds int32          `db:"duration_seconds"`
	Filters         []byte         `db:"filters"`
	Enabled         bool           `db:"enabled"`
	MutedUntil      sql.NullTime   `db:"muted_until"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
}

type NotificationTargetRecord struct {
	ID                 string         `db:"id"`
	TenantID           string         `db:"tenant_id"`
	Name               string         `db:"name"`
	TargetType         string         `db:"target_type"`
	ChannelConfigID    sql.NullString `db:"channel_config_id"`
	PlatformChannelRef sql.NullString `db:"platform_channel_ref"`
	WebhookURL         sql.NullString `db:"webhook_url"`
	WebhookHeaders     []byte         `db:"webhook_headers"`
	EmailAddresses     pq.StringArray `db:"email_addresses"`
	MinSeverity        string         `db:"min_severity"`
	Enabled            bool           `db:"enabled"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

type AlertEventRecord struct {
	ID                string         `db:"id"`
	TenantID          string         `db:"tenant_id"`
	AlertRuleID       string         `db:"alert_rule_id"`
	Status            string         `db:"status"`
	MetricValue       float64        `db:"metric_value"`
	Threshold         float64        `db:"threshold"`
	FiredAt           time.Time      `db:"fired_at"`
	AcknowledgedAt    sql.NullTime   `db:"acknowledged_at"`
	AcknowledgedBy    sql.NullString `db:"acknowledged_by"`
	ResolvedAt        sql.NullTime   `db:"resolved_at"`
	LastNotifiedAt    sql.NullTime   `db:"last_notified_at"`
	NotificationCount int32          `db:"notification_count"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
}

// AlertRuleWithTargets is the rule record plus its linked target IDs.
type AlertRuleWithTargets struct {
	AlertRuleRecord
	TargetIDs []string
}

// AlertEventWithRule is the event record plus rule metadata for display.
type AlertEventWithRule struct {
	AlertEventRecord
	AlertRuleName string `db:"alert_rule_name"`
	Severity      string `db:"rule_severity"`
}

// AlertsSummary provides aggregate counts.
type AlertsSummary struct {
	FiringCount       int32 `db:"firing_count"`
	AcknowledgedCount int32 `db:"acknowledged_count"`
	TotalRules        int32 `db:"total_rules"`
	EnabledRules      int32 `db:"enabled_rules"`
}

// ─── Store Interface ─────────────────────────────────────────────────

type AlertStore interface {
	// AlertRule CRUD
	CreateAlertRule(ctx context.Context, rule *AlertRuleRecord) error
	GetAlertRule(ctx context.Context, id, tenantID string) (*AlertRuleRecord, error)
	UpdateAlertRule(ctx context.Context, rule *AlertRuleRecord) error
	DeleteAlertRule(ctx context.Context, id, tenantID string) error
	ListAlertRules(ctx context.Context, tenantID string, category *string, enabled *bool, limit, offset int32) ([]*AlertRuleRecord, int32, error)
	GetAlertRuleByBuiltinKey(ctx context.Context, tenantID, builtinKey string) (*AlertRuleRecord, error)

	// Rule-Target associations
	SetRuleTargets(ctx context.Context, ruleID string, targetIDs []string) error
	GetRuleTargetIDs(ctx context.Context, ruleID string) ([]string, error)

	// NotificationTarget CRUD
	CreateNotificationTarget(ctx context.Context, target *NotificationTargetRecord) error
	GetNotificationTarget(ctx context.Context, id, tenantID string) (*NotificationTargetRecord, error)
	UpdateNotificationTarget(ctx context.Context, target *NotificationTargetRecord) error
	DeleteNotificationTarget(ctx context.Context, id, tenantID string) error
	ListNotificationTargets(ctx context.Context, tenantID string, targetType *string, limit, offset int32) ([]*NotificationTargetRecord, int32, error)

	// AlertEvent operations
	CreateAlertEvent(ctx context.Context, event *AlertEventRecord) error
	GetActiveEventForRule(ctx context.Context, ruleID string) (*AlertEventRecord, error)
	UpdateAlertEvent(ctx context.Context, event *AlertEventRecord) error
	ListAlertEvents(ctx context.Context, tenantID string, ruleID *string, status *string, limit, offset int32) ([]*AlertEventWithRule, int32, error)

	// Targets for a rule (for notifier)
	GetTargetsForRule(ctx context.Context, ruleID string) ([]*NotificationTargetRecord, error)

	// Summary
	GetAlertsSummary(ctx context.Context, tenantID string) (*AlertsSummary, error)

	// For evaluator: list enabled rules across all tenants
	ListAllEnabledRules(ctx context.Context) ([]*AlertRuleRecord, error)
}
