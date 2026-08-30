package ui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// apiLikePathPrefixes are request-path prefixes that should never fall
// through to the SPA shell handler. If a request matches one of these and
// no earlier route or static file handled it, the SPA fallback returns
// 404 instead of `index.html`.
//
// Why this matters: the prior behavior was "any unknown path → SPA shell",
// which (a) leaked the runtime env block (including POSTHOG_KEY) to
// arbitrary crawlers and scanners hitting unrelated URLs, (b) gave
// integrators a confusing "200 OK with HTML" response when they
// mistyped an RPC procedure, and (c) hid genuine routing bugs because
// everything appeared to "work" with a 200.
//
// Paths under these prefixes are unambiguously not SPA client-side
// routes — the SPA's own router never registers anything starting with
// `/everstack.`, `/api/`, `/v1/`, `/auth/`, or `/__`. Returning 404 here
// is the correct semantic.
var apiLikePathPrefixes = []string{
	"/everstack.", // Connect RPC procedures
	"/v1/",        // versioned REST API
	"/api/",       // generic REST API / auth callbacks proxied at `/api/auth/*`
	"/auth/",      // tenant gateway auth endpoints (cloud-callback, instance-signout, version probe)
	"/__",         // diagnostic/internal endpoints by convention (`__version`, `__metrics`, etc.)
}

// isAPILikePath reports whether the path should 404 if no handler
// claimed it earlier, instead of falling through to the SPA shell.
func isAPILikePath(p string) bool {
	for _, prefix := range apiLikePathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// instanceSessionCookieName is the host-only cookie set by the tenant
// gateway's cloud-callback handler after a successful relay login.
// Duplicated here intentionally — `ui` is a low-dep package, and a single
// const string is a smaller dependency surface than importing the
// controlplane package just to read the cookie name.
const instanceSessionCookieName = "es_tenant_session"

type runtimeEnv struct {
	APIBaseURL       string `json:"VITE_API_BASE_URL"`
	CloudURL         string `json:"CLOUD_URL,omitempty"`
	OrganizationSlug string `json:"ORGANIZATION_SLUG,omitempty"`
	PostHogHost      string `json:"POSTHOG_HOST,omitempty"`
	PostHogKey       string `json:"POSTHOG_KEY,omitempty"`
}

// requestLooksAuthed is a cheap presence check used to gate the
// POSTHOG_KEY injection. We do not validate the cookie value here —
// validation happens in the tenant middleware on actual RPC calls. The
// goal of this check is narrowly: don't ship the analytics key in
// responses to genuinely unauthenticated requests (random crawlers,
// scanners, the `/auth/__version` probe), while still serving it to
// users who have established a session and are loading the dashboard.
//
// An attacker can trivially spoof the cookie to retrieve the key. That's
// acceptable because the `phc_` PostHog public key is not a secret by
// PostHog's design — anyone post-auth has it too. The gating only stops
// the key from being available to entirely-anonymous probes.
func requestLooksAuthed(r *http.Request) bool {
	c, err := r.Cookie(instanceSessionCookieName)
	return err == nil && c != nil && c.Value != ""
}

// buildRuntimeEnvScript builds the `<script>window.__env = ...</script>`
// block injected into the SPA shell. Values are read from environment
// variables at request time so a single SPA bundle can target multiple
// environments (dev / staging / prod) without rebuild.
//
// POSTHOG_KEY is *only* included when the request looks authenticated.
// Other values (API base URL, cloud URL, PostHog *host*) are public and
// always included — the SPA bootstrap (login form, AuthGuard, relay
// redirect) needs them before any auth state exists.
func buildRuntimeEnvScript(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	env := runtimeEnv{
		APIBaseURL: scheme + "://" + host,
		CloudURL:   os.Getenv("EVS_CLOUD_URL"),
	}
	if tc := tenant.ConfigFromContext(r.Context()); tc != nil {
		env.OrganizationSlug = strings.TrimSpace(tc.OrgSlug)
	} else if scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
		env.OrganizationSlug = strings.TrimSpace(scope.OrganizationSlug)
	}
	env.PostHogHost = os.Getenv("EVS_POSTHOG_HOST")
	if requestLooksAuthed(r) {
		env.PostHogKey = os.Getenv("EVS_POSTHOG_KEY")
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return "<script>window.__env=window.__env||{};</script>"
	}
	return "<script>window.__env=Object.assign(window.__env||{}," + string(payload) + ");</script>"
}

// writeRuntimeHTML serves every HTML entry point through the same runtime-env
// injection path. Exact index.html requests previously bypassed injection,
// while only unknown SPA fallback routes received it.
func writeRuntimeHTML(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) error {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	html := injectRuntimeEnv(string(b), buildRuntimeEnvScript(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(html)))
	_, err = w.Write([]byte(html))
	return err
}

// injectRuntimeEnv inserts the runtime env script into the SPA HTML just
// before `</head>`, falling back to prepend when no `</head>` is found.
// Centralized so ui.go and ui_embed.go agree on placement.
func injectRuntimeEnv(html, script string) string {
	if idx := strings.Index(strings.ToLower(html), "</head>"); idx != -1 {
		return html[:idx] + script + html[idx:]
	}
	return script + html
}
