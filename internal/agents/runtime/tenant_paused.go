package runtime

import (
	"context"
	"sync/atomic"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// FinishReasonInstancePaused ends a run whose managed instance was paused
// (over its plan's instance limit, or stopped) while it was running
// (docs/design/instance-over-limit.md).
const FinishReasonInstancePaused = "instance_paused"

// instancePausedMessage is what the run reports as its last text when it is
// stopped for that reason.
const instancePausedMessage = "Stopped: this instance is paused. Resume it to continue."

// tenantActiveFn reports whether a tenant may keep running. Package-level
// because loops are built in many places; the gateway installs it once.
var tenantActiveFn atomic.Pointer[func(ctx context.Context, tenantID string) bool]

// SetTenantActiveFn installs the paused-instance check consulted at every
// iteration boundary of every agent loop. nil disables it.
func SetTenantActiveFn(fn func(ctx context.Context, tenantID string) bool) {
	if fn == nil {
		tenantActiveFn.Store(nil)
		return
	}
	tenantActiveFn.Store(&fn)
}

func tenantActive(ctx context.Context, tenantID string) bool {
	p := tenantActiveFn.Load()
	if p == nil || tenantID == "" {
		return true
	}
	return (*p)(ctx, tenantID)
}

// loopTenantID is the tenant a loop runs for: the input's, else the
// context's.
func loopTenantID(ctx context.Context, input *LoopInput) string {
	if input != nil && input.TenantID != "" {
		return input.TenantID
	}
	return contextkeys.ExtractTenantID(ctx)
}
