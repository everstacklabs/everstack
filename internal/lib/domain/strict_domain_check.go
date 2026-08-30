package domain

import (
	"fmt"
	"net/http"
	"strings"

	http_util "github.com/everstacklabs/everstack/internal/api/http"
	"github.com/gorilla/mux"
)

// EnforceExternalDomain rejects requests whose Host (or X-Forwarded-Host) does not
// match the configured external domain. Returns 403 with a clear message.
func EnforceExternalDomain(expectedHost string) mux.MiddlewareFunc {
	expectedHost = strings.TrimSpace(expectedHost)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqHost := r.Header.Get(http_util.ForwardedHost)
			if reqHost == "" {
				reqHost = r.Host
			}
			if i := strings.Index(reqHost, ":"); i >= 0 {
				reqHost = reqHost[:i]
			}
			if expectedHost != "" && !strings.EqualFold(reqHost, expectedHost) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				http.Error(w, fmt.Sprintf("Access forbidden: requests from origin '%s' are not allowed to access resource on '%s'. If you are the owner of this resource, please visit https://everstack.ai/docs/hosting/external-domains for more information or contact your administrator.", expectedHost, reqHost), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
