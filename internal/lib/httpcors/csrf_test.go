package httpcors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type writeCase struct {
	name        string
	method      string
	cookie      bool
	origin      string
	fetchSite   string
	contentType string
	host        string
	want        int
}

func runWriteGuard(t *testing.T, opts WriteGuardOptions, cases []writeCase) {
	t.Helper()
	p := NewPolicy([]string{"https://app.everstack.ai"})
	h := p.GuardCookieWrite(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), opts)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "https://auth.everstack.ai/api/auth/instance-token", nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.cookie {
				req.Header.Set("Cookie", "es_everstack_session=abc")
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

// The cookie is scoped to .everstack.ai and SameSite=Lax, so any page on a
// sibling subdomain is "same-site" and the browser attaches it to a POST.
// SameSite alone therefore does not stop a compromised or user-content
// subdomain from forging these requests; the Origin check does.
func TestGuardCookieWrite_Origin(t *testing.T) {
	runWriteGuard(t, WriteGuardOptions{}, []writeCase{
		{name: "allowlisted origin", cookie: true, origin: "https://app.everstack.ai", want: http.StatusOK},
		{name: "same-origin request to the service host", cookie: true, origin: "https://auth.everstack.ai", want: http.StatusOK},
		{name: "sibling subdomain", cookie: true, origin: "https://evil.everstack.ai", want: http.StatusForbidden},
		{name: "foreign site", cookie: true, origin: "https://evil.example", want: http.StatusForbidden},
		{name: "opaque origin", cookie: true, origin: "null", want: http.StatusForbidden},
		{name: "no origin, cross-site fetch metadata", cookie: true, fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "no origin, same-site fetch metadata", cookie: true, fetchSite: "same-site", want: http.StatusForbidden},
		{name: "no origin, same-origin fetch metadata", cookie: true, fetchSite: "same-origin", want: http.StatusOK},
		{name: "non-browser client with a cookie", cookie: true, want: http.StatusOK},
		// Local development: the Vite dev server on :3001 proxies to the
		// service on another port with changeOrigin, so Host and Origin differ
		// only by port. Both loopback, so no other site is involved.
		{name: "local dev proxy", cookie: true, origin: "http://localhost:3001", host: "localhost:8093", want: http.StatusOK},
		{name: "loopback origin against a real host", cookie: true, origin: "http://localhost:3001", want: http.StatusForbidden},
		// Without ambient credentials there is nothing to forge; the handler's
		// own authentication decides. Keeps cookieless cross-origin callers
		// (M2M, older self-hosted admin builds) working.
		{name: "cross-origin without cookies", origin: "https://customer.example", want: http.StatusOK},
		{name: "safe method is not checked", method: http.MethodGet, cookie: true, origin: "https://evil.example", want: http.StatusOK},
		{name: "delete is checked", method: http.MethodDelete, cookie: true, origin: "https://evil.example", want: http.StatusForbidden},
	})
}

// A JSON content type cannot be sent cross-origin without a CORS preflight,
// which the allowlist then refuses. Requiring it closes the form-post and
// text/plain fetch variants of the same forgery.
func TestGuardCookieWrite_RequireJSON(t *testing.T) {
	runWriteGuard(t, WriteGuardOptions{RequireJSON: true}, []writeCase{
		{name: "json", cookie: true, origin: "https://app.everstack.ai", contentType: "application/json", want: http.StatusOK},
		{name: "json with charset", cookie: true, origin: "https://app.everstack.ai", contentType: "application/json; charset=utf-8", want: http.StatusOK},
		{name: "form post", cookie: true, origin: "https://app.everstack.ai", contentType: "application/x-www-form-urlencoded", want: http.StatusUnsupportedMediaType},
		{name: "text/plain", cookie: true, origin: "https://app.everstack.ai", contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "missing", cookie: true, origin: "https://app.everstack.ai", want: http.StatusUnsupportedMediaType},
		{name: "cookieless caller is left to the handler", contentType: "text/plain", want: http.StatusOK},
	})
}
