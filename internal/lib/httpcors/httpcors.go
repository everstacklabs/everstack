// Package httpcors provides a single credentialed-CORS policy for the
// cloud services (auth, billing, license) and self-hosted auth.
//
// Every one of those services shipped its own `corsMiddleware`, and all of
// them were unsafe in one of two ways:
//
//	w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
//	w.Header().Set("Access-Control-Allow-Credentials", "true")
//
// reflecting *any* origin back with credentials allowed, which lets any page
// on the internet make authenticated cross-origin requests using the victim's
// cookies and read the responses; or
//
//	w.Header().Set("Access-Control-Allow-Origin", "*")
//	w.Header().Set("Access-Control-Allow-Credentials", "true")
//
// which browsers reject outright, so the credentialed call simply fails. The
// first is a vulnerability, the second is a bug, and neither is what anyone
// intended.
//
// This package allows an explicit set of origins and nothing else. It never
// emits "*" together with credentials, and it always sets Vary: Origin so a
// shared cache cannot serve one origin's response to another.
package httpcors

import (
	"net/http"
	"os"
	"strings"
)

// allowedRequestHeaders is the union of what the REST and Connect handlers
// need. Permitting an extra request header on a preflight is not a security
// boundary; the origin check is.
const allowedRequestHeaders = "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms"

const allowedMethods = "GET, POST, PUT, DELETE, OPTIONS"

// defaultAllowedOrigins is used when nothing is configured. It matches the
// cloud app origin the chart already documents as the default. Keeping a
// default rather than failing closed is deliberate: the cloud frontend calls
// auth/billing/license on separate hosts with cookies, so an empty allowlist
// would break sign-in on any deployment that has not set the variable yet.
var defaultAllowedOrigins = []string{"https://app.everstack.ai"}

// Policy is an immutable set of origins permitted to send credentialed
// cross-origin requests.
type Policy struct {
	allowed map[string]struct{}
}

// NewPolicy builds a policy from an explicit origin list. Entries are
// normalised (trimmed, lowercased, trailing slash removed) so that a value
// like "https://app.everstack.ai/" still matches the Origin header a browser
// sends, which never has a trailing slash.
func NewPolicy(origins []string) *Policy {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if n := normalize(o); n != "" {
			allowed[n] = struct{}{}
		}
	}
	return &Policy{allowed: allowed}
}

// PolicyFromEnv builds the policy from configuration, in precedence order:
//
//  1. EVS_CORS_ALLOWED_ORIGINS, a comma-separated list. The explicit knob.
//  2. EVS_CLOUD_APP_URL, which the docker-compose cloud stack already sets,
//     so local development works without extra configuration.
//  3. EVS_SERVICES_EMAIL_CLOUD_URL, the other place the cloud app URL is
//     already threaded through the services deployment.
//  4. defaultAllowedOrigins.
func PolicyFromEnv() *Policy {
	if raw := strings.TrimSpace(os.Getenv("EVS_CORS_ALLOWED_ORIGINS")); raw != "" {
		return NewPolicy(strings.Split(raw, ","))
	}
	for _, key := range []string{"EVS_CLOUD_APP_URL", "EVS_SERVICES_EMAIL_CLOUD_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return NewPolicy([]string{v})
		}
	}
	return NewPolicy(defaultAllowedOrigins)
}

// Allows reports whether origin may make credentialed cross-origin requests.
func (p *Policy) Allows(origin string) bool {
	if p == nil {
		return false
	}
	n := normalize(origin)
	if n == "" {
		return false
	}
	_, ok := p.allowed[n]
	return ok
}

// Origins returns the configured origins. Intended for startup logging so a
// misconfiguration is visible rather than showing up later as a browser error.
func (p *Policy) Origins() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.allowed))
	for o := range p.allowed {
		out = append(out, o)
	}
	return out
}

// Middleware applies the policy.
//
// A request with no Origin header is not a cross-origin browser request, so it
// gets no CORS headers at all: emitting "*" for those was what made the old
// middlewares look permissive even before the reflection bug.
//
// A request from a disallowed origin is passed through without CORS headers.
// The browser then blocks the response, which is the correct outcome, and the
// server has not confirmed anything about the origin. Preflights from such an
// origin get 204 with no headers for the same reason.
func (p *Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Always vary, even when the request is denied or has no Origin: the
		// response body can differ per origin and a shared cache must not
		// reuse it across them.
		w.Header().Add("Vary", "Origin")

		if origin != "" && p.Allows(origin) {
			// Echo the request's origin rather than the stored form so the
			// value byte-matches what the browser sent.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowedRequestHeaders)
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func normalize(origin string) string {
	o := strings.ToLower(strings.TrimSpace(origin))
	o = strings.TrimRight(o, "/")
	if o == "null" {
		// Browsers send Origin: null for sandboxed iframes and some redirects.
		// It is not an authenticatable origin and must never be allowlisted.
		return ""
	}
	return o
}
