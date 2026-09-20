//go:build !enterprise

package serve

import (
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
)

// registerTenantReadiness is a no-op in the Community Edition.
//
// The readiness probe reports whether a managed instance serves a given
// tenant, resolved against the platform tenant registry. The Community
// Edition is single-tenant and does not carry internal/controlplane, so
// there is nothing to mount. Keeping this stub, rather than a build tag on
// the call site, keeps start_api.go identical across editions. The
// enterprise file is stripped from the public projection by path, because
// go mod tidy ignores build tags when it resolves imports.
func registerTenantReadiness(*mux.Router, *sqlx.DB, *sqlx.DB, bool) {}
