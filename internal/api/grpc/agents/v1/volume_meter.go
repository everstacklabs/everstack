package v1

// Hourly volume storage metering sweep.
//
// Sandbox volumes are object-storage-backed and billed by measured usage (not
// provisioned capacity), with NO free allowance — the 20 GiB included is
// root-disk only. Each tick, for every volume, we measure the bytes stored
// under its prefix, bill the GiB-hours elapsed since the last measurement at
// the tenant's storage rate, and persist a usage record to the ledger
// (billing_usage_records). Stripe forwarding is wired in a later slice; this
// sweep only produces the ledger rows, exactly as sandbox compute does today.

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/volstore"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/usage"
)

const defaultVolumeMeterInterval = time.Hour

// VolumeUsageMeter periodically measures + bills sandbox volume storage.
type VolumeUsageMeter struct {
	db       *sqlx.DB
	store    storage.ObjectStore
	bucket   string
	interval time.Duration
	// rateFn resolves the per-GiB-hour storage rate for a tenant (incl. tier).
	rateFn func(ctx context.Context, tenantID string) float64
}

// NewVolumeUsageMeter builds a sweep. Returns nil when storage backing is not
// configured (nil store / empty bucket) — volumes are then metadata-only and
// there is nothing to measure.
func NewVolumeUsageMeter(
	db *sqlx.DB,
	store storage.ObjectStore,
	bucket string,
	rateFn func(ctx context.Context, tenantID string) float64,
) *VolumeUsageMeter {
	if db == nil || store == nil || bucket == "" {
		return nil
	}
	return &VolumeUsageMeter{
		db:       db,
		store:    store,
		bucket:   bucket,
		interval: defaultVolumeMeterInterval,
		rateFn:   rateFn,
	}
}

// Run blocks, sweeping every interval until ctx is cancelled.
func (m *VolumeUsageMeter) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweepOnce(ctx)
		}
	}
}

func (m *VolumeUsageMeter) sweepOnce(ctx context.Context) {
	repo := &volumeRepo{db: m.db}
	vols, err := repo.listAll()
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("volume_meter: failed to list volumes")
		return
	}
	now := time.Now().UTC()
	for i := range vols {
		m.meterVolume(ctx, repo, &vols[i], now)
	}
}

// meterVolume measures one volume, persists a usage record for the GiB-hours
// accrued since its last measurement, and updates the stored measurement.
func (m *VolumeUsageMeter) meterVolume(ctx context.Context, repo *volumeRepo, v *Volume, now time.Time) {
	// Per-tenant bucket layout: measure the volume's prefix inside the tenant's
	// own bucket. The meter's store uses account-level R2 creds, so it can list
	// any tenant bucket.
	bucket := volstore.BucketName(v.TenantID)
	prefix := volumeObjectPrefix(v.ID)
	objs, err := m.store.List(ctx, bucket, prefix)
	if err != nil {
		logger.WithFields("volume_id", v.ID, "error", err.Error()).Warn("volume_meter: measure failed")
		return
	}
	var usedBytes int64
	for _, o := range objs {
		usedBytes += o.SizeBytes
	}

	// Window start: last measurement, or creation time on the first sweep.
	start := v.CreatedAt.UTC()
	if v.UsageMeasuredAt.Valid {
		start = v.UsageMeasuredAt.Time.UTC()
	}

	// Persist the measurement regardless (so the UI shows current usage even
	// when pricing is disabled or the volume is empty).
	if err := repo.updateUsage(v.ID, usedBytes); err != nil {
		logger.WithFields("volume_id", v.ID, "error", err.Error()).Warn("volume_meter: update failed")
		return
	}

	if !now.After(start) || usedBytes <= 0 {
		return
	}
	hours := now.Sub(start).Hours()
	usedGiB := float64(usedBytes) / (1024.0 * 1024.0 * 1024.0)
	gibHours := usedGiB * hours
	if gibHours <= 0 {
		return
	}

	rate := 0.0
	if m.rateFn != nil {
		rate = m.rateFn(ctx, v.TenantID)
	}
	costUSD := gibHours * rate

	periodStart := start
	periodEnd := now
	rec := usage.BillingUsageRecord{
		IdempotencyKey: fmt.Sprintf("volume:%s:%d", v.ID, periodEnd.Unix()),
		TenantID:       v.TenantID,
		ResourceType:   "volume",
		ResourceID:     v.ID,
		SourceType:     "volume.usage",
		SourceRef:      v.ID,
		MetricType:     "volume.storage_gib_hours",
		Quantity:       gibHours,
		Unit:           "gib_hours",
		CostUSD:        costUSD,
		Currency:       "USD",
		Metadata: map[string]interface{}{
			"used_bytes":  usedBytes,
			"used_gib":    usedGiB,
			"hours":       hours,
			"rate_usd":    rate,
			"volume_name": v.Name,
		},
		PeriodStart: &periodStart,
		PeriodEnd:   &periodEnd,
	}
	if err := usage.InsertBillingUsageRecord(ctx, m.db, rec); err != nil {
		logger.WithFields("volume_id", v.ID, "error", err.Error()).Warn("volume_meter: failed to persist usage record")
	}
}
