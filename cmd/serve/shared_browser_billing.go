package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/jmoiron/sqlx"
)

// errBrowserBillingRequired refuses a hosted browser lease when the
// organization has used this period's included browser hours and has no
// credit to pay for more. The message is shown to the customer.
var errBrowserBillingRequired = errors.New("You have used this month's included browser hours. Add credit to keep running.")

// sharedBrowserUsageRecorder establishes the central-billing watermark before
// a public-plan tenant receives a hosted browser, then delegates the durable
// lease window to the gateway meter. This is the browser equivalent of the
// sandbox allocation preflight.
type sharedBrowserUsageRecorder struct {
	db       *sqlx.DB
	reporter tenantSandboxUsageReporter
	meter    browserpool.UsageRecorder
	// shadowDB is the platform DB for the G1 shadow compute admission
	// (prepaid-credits-v2 S4). Nil disables the shadow.
	shadowDB *sqlx.DB
}

func (r *sharedBrowserUsageRecorder) Start(ctx context.Context, lease *browserpool.Lease, at time.Time) error {
	if r == nil || r.meter == nil || lease == nil {
		return fmt.Errorf("shared browser usage recorder is not configured")
	}
	// Dry-run only: evaluated in the background, never changes the lease.
	billingmeter.ShadowAdmitComputeTenant(r.shadowDB, lease.TenantID, billingmeter.SurfaceBrowserLease)
	tier, access, err := resolveSharedRuntimeBillingAccess(ctx, r.db, r.reporter, lease.TenantID)
	if err != nil {
		return fmt.Errorf("resolve browser billing entitlement: %w", err)
	}
	// Browser runtime has its own plan allowance and is then paid from the
	// same prepaid wallet as sandbox compute, so central billing's browser
	// decision admits or refuses the lease. A Stripe subscription is
	// informational and never admits on its own.
	if tier != "enterprise" && !access.BrowserAllowed {
		return errBrowserBillingRequired
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
