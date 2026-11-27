package httpcors

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// WriteGuardOptions configures GuardCookieWrite.
type WriteGuardOptions struct {
	// RequireJSON rejects a request whose Content-Type is not
	// application/json. Set it on routes that decode a JSON body: a browser
	// cannot send that content type cross-origin without a CORS preflight,
	// which the allowlist refuses, so form posts and text/plain fetches from
	// another site cannot reach the handler.
	RequireJSON bool
}

// GuardCookieWrite protects a state-changing REST route that authenticates
// with an ambient cookie against cross-site request forgery.
//
// The session cookie is scoped to the parent domain with SameSite=Lax. Every
// sibling subdomain counts as the same site, so the browser attaches the
// cookie to a POST from any of them, and SameSite alone does not stop a
// compromised or user-content subdomain from forging one. This check does:
//
//   - Safe methods (GET, HEAD, OPTIONS) pass. They must not change state.
//   - A request carrying no cookies passes. With no ambient credentials there
//     is nothing to forge, and the handler's own authentication decides. This
//     keeps M2M callers and cookieless cross-origin callers working.
//   - If Origin is present it must be allowlisted, or name the host the
//     request was sent to (a same-origin call through a first-party proxy).
//   - If Origin is absent, Sec-Fetch-Site must not be cross-site or
//     same-site. A request with neither header is not from a browser.
//   - With RequireJSON, the Content-Type must be application/json.
//
// Connect RPC routes do not need this: their content types already force a
// preflight. Apply it per route, never to a whole mux that also serves
// webhooks or Connect.
func (p *Policy) GuardCookieWrite(next http.Handler, opts WriteGuardOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || r.Header.Get("Cookie") == "" {
			next.ServeHTTP(w, r)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if !p.Allows(origin) && !sameOriginAsHost(origin, r.Host) && !localDevelopment(origin, r.Host) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		} else {
			switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
			case "cross-site", "same-site":
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}

		if opts.RequireJSON && !isJSONContentType(r.Header.Get("Content-Type")) {
			http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// sameOriginAsHost reports whether origin names the host the request was
// addressed to. A browser only sends an Origin it is actually on, so a match
// means the page and the endpoint share an origin. The scheme is not compared:
// behind the TLS-terminating proxy the request itself looks like plain HTTP.
func sameOriginAsHost(origin, host string) bool {
	if normalize(origin) == "" || host == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// localDevelopment allows a loopback page calling a loopback service on a
// different port, which is what the Vite dev proxy produces (it rewrites Host
// to the target). Production hosts are never loopback, and a page that can
// already run on the developer's loopback interface needs no forgery.
func localDevelopment(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHostname(u.Hostname()) && isLoopbackHostname(hostnameOnly(host))
}

func hostnameOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

func isLoopbackHostname(name string) bool {
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}
