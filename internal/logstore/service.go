package logstore

import (
	"context"

	"github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/quota"
)

type UsageStorer[T LogRecord[T]] interface {
	LogEmitter[T]
	QuotaUnit() quota.Unit
}
type Service[T LogRecord[T]] struct {
	queries          Queries
	usageStorer      UsageStorer[T]
	enabledSinks     []*emitter[T]
	sinkEnabled      bool
	reportingEnabled bool
}

type Queries interface {
	GetRemainingQuotaUsage(ctx context.Context, instanceID string, unit quota.Unit) (remaining *uint64, err error)
}
