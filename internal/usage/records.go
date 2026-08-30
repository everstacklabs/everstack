package usage

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// BillingUsageRecord represents a normalized metering row used for tenant billing.
type BillingUsageRecord struct {
	IdempotencyKey string
	TenantID       string
	ResourceType   string
	ResourceID     string
	SourceType     string
	SourceRef      string
	MetricType     string
	Quantity       float64
	Unit           string
	CostUSD        float64
	Currency       string
	Status         string
	Metadata       map[string]interface{}
	PeriodStart    *time.Time
	PeriodEnd      *time.Time
}

// InsertBillingUsageRecord inserts a usage record idempotently.
func InsertBillingUsageRecord(ctx context.Context, db *sqlx.DB, rec BillingUsageRecord) error {
	if db == nil {
		return nil
	}
	return insertBillingUsageRecord(ctx, db, rec)
}

// InsertBillingUsageRecordTx is the transactional form used when a
// domain-specific usage ledger row and the normalized billing outbox row must
// commit together.
func InsertBillingUsageRecordTx(ctx context.Context, tx *sqlx.Tx, rec BillingUsageRecord) error {
	if tx == nil {
		return nil
	}
	return insertBillingUsageRecord(ctx, tx, rec)
}

func insertBillingUsageRecord(ctx context.Context, db sqlx.ExtContext, rec BillingUsageRecord) error {
	key := strings.TrimSpace(rec.IdempotencyKey)
	if key == "" {
		return nil
	}

	tenantID := strings.TrimSpace(rec.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}
	currency := strings.TrimSpace(rec.Currency)
	if currency == "" {
		currency = "USD"
	}
	status := strings.TrimSpace(rec.Status)
	if status == "" {
		status = "recorded"
	}

	metadata := rec.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	var resourceID interface{}
	if strings.TrimSpace(rec.ResourceID) != "" {
		resourceID = rec.ResourceID
	}

	const q = `
		INSERT INTO billing_usage_records (
			idempotency_key, tenant_id, resource_type, resource_id,
			source_type, source_ref, metric_type,
			quantity, unit, cost_usd, currency, status,
			metadata, period_start, period_end
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15
		)
		ON CONFLICT (idempotency_key) DO NOTHING
	`

	_, err = db.ExecContext(ctx, q,
		key, tenantID, rec.ResourceType, resourceID,
		rec.SourceType, rec.SourceRef, rec.MetricType,
		rec.Quantity, rec.Unit, rec.CostUSD, currency, status,
		metadataJSON, rec.PeriodStart, rec.PeriodEnd,
	)
	return err
}
