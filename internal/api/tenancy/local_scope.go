package tenancy

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	pkgdb "github.com/everstacklabs/everstack/pkg/database"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// defaultSelfHostedTenantID is used only when system.instances.local_instance_id
// is unavailable. It must stay UUID-shaped because several CE tables use UUID
// tenant_id columns, while sandbox-era tables accept text/varchar tenant ids.
const defaultSelfHostedTenantID = "00000000-0000-0000-0000-000000000001"

// LocalScopeResolver supplies a stable single-tenant identity for standalone
// self-hosted gateways. It never injects scope in shared/cloud mode.
type LocalScopeResolver struct {
	db         *sqlx.DB
	sharedMode bool

	mu       sync.Mutex
	tenantID string
}

func NewLocalScopeResolver(db *sqlx.DB, sharedMode bool) *LocalScopeResolver {
	return &LocalScopeResolver{db: db, sharedMode: sharedMode}
}

// Inject returns ctx unchanged when tenant scope already exists, or when the
// gateway is in shared/cloud mode. In standalone mode it injects a local tenant
// id so existing tenant-scoped handlers keep filtering safely.
func (r *LocalScopeResolver) Inject(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tenantID := contextkeys.GetTenantID(ctx); tenantID != "" {
		if pkgdb.TenantSchemaFromContext(ctx) == "" {
			ctx = pkgdb.WithTenantSchema(ctx, tenantID)
		}
		return ctx
	}
	if tenant.ConfigFromContext(ctx) != nil {
		return ctx
	}
	if r == nil || r.sharedMode || sharedGatewayMode(ctx) {
		return ctx
	}
	tenantID := r.localTenantID()
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	return pkgdb.WithTenantSchema(ctx, tenantID)
}

func (r *LocalScopeResolver) localTenantID() string {
	if r == nil {
		return defaultSelfHostedTenantID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tenantID != "" {
		return r.tenantID
	}
	loaded := r.loadLocalTenantID()
	if loaded != defaultSelfHostedTenantID {
		r.tenantID = loaded
	}
	return loaded
}

func (r *LocalScopeResolver) loadLocalTenantID() string {
	if r == nil || r.db == nil {
		return defaultSelfHostedTenantID
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var localID sql.NullString
	if err := r.db.QueryRowContext(ctx, `
		SELECT local_instance_id
		FROM system.instances
		WHERE local_instance_id IS NOT NULL AND local_instance_id != ''
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(&localID); err != nil {
		return defaultSelfHostedTenantID
	}
	if localID.Valid {
		if v := strings.TrimSpace(localID.String); v != "" {
			return v
		}
	}
	return defaultSelfHostedTenantID
}

func sharedGatewayMode(ctx context.Context) bool {
	shared, _ := ctx.Value(contextkeys.SharedGatewayMode).(bool)
	return shared
}
