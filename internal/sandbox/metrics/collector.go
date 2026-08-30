// Package metrics implements lightweight time-series collection for
// per-sandbox resource usage (CPU %, memory, disk).
//
// The Collector runs as a background goroutine, polls Stats() for every
// running sandbox every 30s, writes a row to sandbox_metrics_history,
// and prunes rows older than 2 hours to cap table growth.
package metrics

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

const (
	collectionInterval = 30 * time.Second
	retentionPeriod    = 2 * time.Hour
)

// Snapshot is a single resource usage sample.
type Snapshot struct {
	ID          int64     `db:"id"           json:"id"`
	SandboxID   string    `db:"sandbox_id"   json:"sandbox_id"`
	TenantID    string    `db:"tenant_id"    json:"tenant_id"`
	CPUPercent  float64   `db:"cpu_percent"  json:"cpu_percent"`
	MemoryUsage int64     `db:"memory_usage" json:"memory_usage"`
	MemoryLimit int64     `db:"memory_limit" json:"memory_limit"`
	DiskUsedMB  int64     `db:"disk_used_mb" json:"disk_used_mb"`
	CollectedAt time.Time `db:"collected_at" json:"collected_at"`
}

// Collector collects sandbox metrics periodically and stores them.
type Collector struct {
	db  *sqlx.DB
	mgr SandboxStater
}

// SandboxStater is the minimal interface the Collector needs from SandboxManager.
type SandboxStater interface {
	ListInstances() []*sandbox.Instance
	Stats(ctx context.Context, sessionID string) (*sandbox.ContainerStats, error)
}

// NewCollector creates a Collector.
func NewCollector(db *sqlx.DB, mgr SandboxStater) *Collector {
	return &Collector{db: db, mgr: mgr}
}

// Run starts the collection loop. Blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	logger.Info("sandbox_metrics_collector: starting")
	ticker := time.NewTicker(collectionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
			c.prune(ctx)
		}
	}
}

func (c *Collector) collect(ctx context.Context) {
	instances := c.mgr.ListInstances()
	for _, inst := range instances {
		if inst == nil || inst.Status != sandbox.StatusRunning {
			continue
		}
		collectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stats, err := c.mgr.Stats(collectCtx, inst.Config.SessionID)
		cancel()
		if err != nil || stats == nil {
			continue
		}
		_, _ = c.db.ExecContext(ctx,
			`INSERT INTO sandbox_metrics_history (sandbox_id, tenant_id, cpu_percent, memory_usage, memory_limit, disk_used_mb, collected_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
			inst.ID, inst.Config.TenantID,
			stats.CPUPercent, stats.MemoryUsage, stats.MemoryLimit,
			0, // disk not yet populated by all backends
		)
	}
}

func (c *Collector) prune(ctx context.Context) {
	// Keep last 2h worth of data to bound table growth.
	_, _ = c.db.ExecContext(ctx,
		`DELETE FROM sandbox_metrics_history WHERE collected_at < NOW() - INTERVAL '2 hours'`)
}

// Repository handles metric history queries.
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a Repository.
func NewRepository(db *sqlx.DB) *Repository { return &Repository{db: db} }

// History returns the last N snapshots for a sandbox in ascending time order.
func (r *Repository) History(ctx context.Context, sandboxID string, limit int) ([]Snapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 120 // default: last hour at 30s intervals
	}
	const q = `
		SELECT * FROM (
			SELECT * FROM sandbox_metrics_history
			WHERE sandbox_id = $1
			ORDER BY collected_at DESC LIMIT $2
		) t ORDER BY collected_at ASC`
	var snaps []Snapshot
	if err := r.db.SelectContext(ctx, &snaps, q, sandboxID, limit); err != nil {
		return nil, err
	}
	if snaps == nil {
		snaps = []Snapshot{}
	}
	return snaps, nil
}

// LatestBatch returns the most recent snapshot for each of the given sandbox IDs.
func (r *Repository) LatestBatch(ctx context.Context, sandboxIDs []string) ([]Snapshot, error) {
	if len(sandboxIDs) == 0 {
		return []Snapshot{}, nil
	}
	// Use a lateral join to get the latest row per sandbox efficiently.
	query, args, err := sqlx.In(
		`SELECT DISTINCT ON (sandbox_id) * FROM sandbox_metrics_history
		 WHERE sandbox_id IN (?) ORDER BY sandbox_id, collected_at DESC`,
		sandboxIDs,
	)
	if err != nil {
		return nil, err
	}
	query = r.db.Rebind(query)
	var snaps []Snapshot
	if err := r.db.SelectContext(ctx, &snaps, query, args...); err != nil {
		return nil, err
	}
	if snaps == nil {
		snaps = []Snapshot{}
	}
	return snaps, nil
}
