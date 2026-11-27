package sandbox

// Background upkeep for persisted workspaces:
//
//   - Drains sandbox_workspace_purges, the outbox the terminate transition
//     (and an id reuse) writes in the same transaction as the row change,
//     so a terminated incarnation's tarballs are deleted even when the
//     best-effort delete in ExecuteTerminate failed and the id is reused.
//   - Publishes gauges for rows that need a human (restore failed, held
//     back from cleanup) and for purges that keep failing, so operators
//     see them without reading logs.

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

const (
	workspaceMaintenanceInterval = 5 * time.Minute
	workspacePurgeBatch          = 50
	// workspacePurgeLease keeps a claimed purge from being picked up by
	// another gateway while this one works on it.
	workspacePurgeLease = 10 * time.Minute
)

var (
	workspaceAttentionGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "sandbox",
		Name:      "workspace_attention_rows",
		Help:      "Sandboxes whose saved /workspace needs a human (restore failed, or held back from automatic cleanup). Label code: workspace_restore_failed or workspace_needs_attention.",
	}, []string{"code"})
	workspacePurgeBacklogGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "sandbox",
		Name:      "workspace_purges_pending",
		Help:      "Terminated workspace incarnations whose object-storage deletion has not succeeded yet.",
	})
	workspacePurgeFailingGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "sandbox",
		Name:      "workspace_purges_failing",
		Help:      "Pending workspace purges that have failed at least three times.",
	})
)

// StartWorkspaceMaintenance runs the purge outbox drain and the attention
// gauges until ctx is cancelled. It needs the database; object storage is
// optional (without it purges wait and only the gauges update).
func (m *SandboxManager) StartWorkspaceMaintenance(ctx context.Context) {
	if m.db == nil {
		return
	}
	go func() {
		t := time.NewTicker(workspaceMaintenanceInterval)
		defer t.Stop()
		for {
			m.runWorkspaceMaintenance(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (m *SandboxManager) runWorkspaceMaintenance(ctx context.Context) {
	tickCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	m.drainWorkspacePurges(tickCtx)
	m.reportWorkspaceAttention(tickCtx)
}

type workspacePurge struct {
	SandboxID   string `db:"sandbox_id"`
	Incarnation string `db:"incarnation"`
	TenantID    string `db:"tenant_id"`
	Attempts    int    `db:"attempts"`
}

// drainWorkspacePurges deletes due incarnations' tarballs and removes
// their outbox rows; failures are rescheduled with backoff.
func (m *SandboxManager) drainWorkspacePurges(ctx context.Context) {
	ws, ok := m.workspaceStore()
	if !ok || m.db == nil {
		return
	}
	var due []workspacePurge
	if err := m.db.SelectContext(ctx, &due, `
		UPDATE sandbox_workspace_purges p
		SET next_attempt_at = NOW() + make_interval(secs => $2),
		    attempts        = p.attempts + 1
		FROM (
		    SELECT sandbox_id, incarnation FROM sandbox_workspace_purges
		    WHERE next_attempt_at <= NOW()
		    ORDER BY next_attempt_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		) d
		WHERE p.sandbox_id = d.sandbox_id AND p.incarnation = d.incarnation
		RETURNING p.sandbox_id, p.incarnation, p.tenant_id, p.attempts`,
		workspacePurgeBatch, workspacePurgeLease.Seconds()); err != nil {
		logger.WithFields("error", err.Error()).Warn("sandbox_workspace_purge: claim failed")
		return
	}
	for _, p := range due {
		owner := snapshot.WorkspaceOwner{TenantID: p.TenantID, SandboxID: p.SandboxID, Incarnation: p.Incarnation}
		err := ws.DeleteIncarnation(ctx, owner)
		if err == nil {
			if _, dbErr := m.db.ExecContext(ctx,
				`DELETE FROM sandbox_workspace_purges WHERE sandbox_id = $1 AND incarnation = $2`,
				p.SandboxID, p.Incarnation); dbErr != nil {
				logger.WithFields("sandbox_id", p.SandboxID, "error", dbErr.Error()).
					Warn("sandbox_workspace_purge: deleted objects but could not clear the outbox row; it will be retried")
			}
			continue
		}
		backoff := purgeBackoff(p.Attempts)
		if _, dbErr := m.db.ExecContext(ctx, `
			UPDATE sandbox_workspace_purges
			SET last_error = $3, next_attempt_at = NOW() + make_interval(secs => $4)
			WHERE sandbox_id = $1 AND incarnation = $2`,
			p.SandboxID, p.Incarnation, truncateError(err.Error()), backoff.Seconds()); dbErr != nil {
			logger.WithFields("sandbox_id", p.SandboxID, "error", dbErr.Error()).
				Warn("sandbox_workspace_purge: could not record failure")
		}
		logger.WithFields("sandbox_id", p.SandboxID, "tenant_id", p.TenantID, "attempts", p.Attempts, "error", err.Error()).
			Warn("sandbox_workspace_purge: deleting a terminated workspace failed; will retry")
	}
}

func purgeBackoff(attempts int) time.Duration {
	d := time.Minute << min(attempts, 9) // 2m, 4m, ... capped below
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return d
}

func truncateError(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// reportWorkspaceAttention refreshes the operator-facing gauges.
func (m *SandboxManager) reportWorkspaceAttention(ctx context.Context) {
	if m.db == nil {
		return
	}
	type countRow struct {
		Code  string `db:"code"`
		Count int    `db:"n"`
	}
	var rows []countRow
	if err := m.db.SelectContext(ctx, &rows, `
		SELECT split_part(error, ':', 1) AS code, COUNT(*) AS n
		FROM sandbox_instances
		WHERE (error LIKE $1 OR error LIKE $2)
		  AND error_reason IS NOT NULL
		  AND destroyed_at IS NULL
		  AND lifecycle_state NOT IN ('terminating', 'terminated')
		GROUP BY 1`,
		WorkspaceErrorCodeRestoreFailed+":%", WorkspaceErrorCodeNeedsAttention+":%"); err != nil {
		logger.WithFields("error", err.Error()).Warn("sandbox_workspace: attention count failed")
	} else {
		counts := map[string]int{WorkspaceErrorCodeRestoreFailed: 0, WorkspaceErrorCodeNeedsAttention: 0}
		for _, r := range rows {
			counts[strings.TrimSpace(r.Code)] = r.Count
		}
		for code, n := range counts {
			workspaceAttentionGauge.WithLabelValues(code).Set(float64(n))
		}
	}

	var pending, failing int
	if err := m.db.QueryRowxContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE attempts >= 3) FROM sandbox_workspace_purges`).
		Scan(&pending, &failing); err != nil {
		logger.WithFields("error", err.Error()).Warn("sandbox_workspace: purge backlog count failed")
		return
	}
	workspacePurgeBacklogGauge.Set(float64(pending))
	workspacePurgeFailingGauge.Set(float64(failing))
}
