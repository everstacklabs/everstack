package middleware

import (
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/tenancy"
)

type LocalTenantMiddleware struct {
	resolver *tenancy.LocalScopeResolver
}

// NewLocalTenantMiddleware builds the standalone-tenant injector. Pass
// managedMode=true for any gateway that serves other people's tenants (see
// isManagedGateway in cmd/serve): the resolver then injects nothing, because a
// single local tenant id is meaningless there and actively harmful if a
// downstream handler treats it as the owner of inbound data.
func NewLocalTenantMiddleware(db *sqlx.DB, managedMode bool) *LocalTenantMiddleware {
	return &LocalTenantMiddleware{resolver: tenancy.NewLocalScopeResolver(db, managedMode)}
}

func (m *LocalTenantMiddleware) Wrap(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m != nil && m.resolver != nil {
			r = r.WithContext(m.resolver.Inject(r.Context()))
		}
		next.ServeHTTP(w, r)
	})
}
