package serve

import (
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"

	openaiapi "github.com/everstacklabs/everstack/internal/agents/openaiapi"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// withRawDeviceTokenAuth lets a route registered directly on the router accept
// the CLI login token (`evs login`) in addition to the session cookie and API
// key that router-level middleware already verify.
//
// It never grants access by itself: it only installs a verified identity for
// a valid device token, and next must still refuse a request that carries no
// verified principal. A request that is already verified is passed through
// untouched, so an API key sent as a Bearer is not re-read as a device token.
// Anything that is not a candidate device token also passes through, to be
// refused by next.
func withRawDeviceTokenAuth(next http.Handler, tokens *deviceauth.TokenManager, db *sqlx.DB) http.Handler {
	if tokens == nil || db == nil {
		return next
	}
	device := openaiapi.WithDeviceTokenAuth(next, tokens, db)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contextkeys.HasVerifiedTenantPrincipal(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		authorization := r.Header.Get(common.Authorization)
		bearer := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		if !strings.HasPrefix(authorization, "Bearer ") || bearer == "" || apikeylib.HasIssuedKeyPrefix(bearer) {
			next.ServeHTTP(w, r)
			return
		}
		device.ServeHTTP(w, r)
	})
}
