package license_monitor

import (
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// managedGateway records whether this process is a managed (Cloud) gateway.
// Cloud sells prepaid credit: admission is the wallet, and the invoice-fed
// spend limit must never block there (it is no longer fed, so any block would
// be stale state nobody can clear from the Cloud UI). Self-hosted gateways keep
// enforcing their spend limits. Set once at startup from cmd/serve, alongside
// enterprise.SetManagedGateway.
var managedGateway atomic.Bool

// SetManagedGateway marks this process as a managed (Cloud) gateway, making
// every spend-limit enforcement point advisory (allow + log).
func SetManagedGateway(v bool) { managedGateway.Store(v) }

func isManagedGateway() bool { return managedGateway.Load() }

// ignoredSpendBlockLogInterval bounds the "ignoring spend block" log line to
// one per interval per process, instead of one per request.
const ignoredSpendBlockLogInterval = time.Minute

var lastIgnoredSpendBlockLog atomic.Int64

func logIgnoredSpendBlock(reason string) {
	now := time.Now().UnixNano()
	last := lastIgnoredSpendBlockLog.Load()
	if now-last < int64(ignoredSpendBlockLogInterval) {
		return
	}
	if !lastIgnoredSpendBlockLog.CompareAndSwap(last, now) {
		return
	}
	logger.Warnf("license_monitor: managed gateway ignoring spend block (prepaid credit gates admission instead): %s", reason)
}
