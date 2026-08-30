package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/jmoiron/sqlx"
)

// sharedBrowserUsageRecorder establishes the central-billing watermark before
// a public-plan tenant receives a hosted browser, then delegates the durable
// lease window to the gateway meter. This is the browser equivalent of the
// sandbox allocation preflight.
type sharedBrowserUsageRecorder struct {
	db       *sqlx.DB
	reporter tenantSandboxUsageReporter
	meter    browserpool.UsageRecorder
}

func (r *sharedBrowserUsageRecorder) Start(ctx context.Context, lease *browserpool.Lease, at time.Time) error {
	if r == nil || r.meter == nil || lease == nil {
		return fmt.Errorf("shared browser usage recorder is not configured")
	}
	tier, access, err := resolveSharedRuntimeBillingAccess(ctx, r.db, r.reporter, lease.TenantID)
	if err != nil {
		return fmt.Errorf("resolve browser billing entitlement: %w", err)
	}
	if tier != "enterprise" && !access.BillingActive {
		return fmt.Errorf("hosted browser billing is not enabled for this organization")
	}
	return r.meter.Start(ctx, lease, at)
}

func (r *sharedBrowserUsageRecorder) Heartbeat(ctx context.Context, sessionID string, at time.Time) error {
	if r == nil || r.meter == nil {
		return nil
	}
	return r.meter.Heartbeat(ctx, sessionID, at)
}

func (r *sharedBrowserUsageRecorder) Finish(ctx context.Context, sessionID string, at time.Time, reason string) error {
	if r == nil || r.meter == nil {
		return nil
	}
	return r.meter.Finish(ctx, sessionID, at, reason)
}
