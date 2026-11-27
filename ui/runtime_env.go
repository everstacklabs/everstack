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

type runtimeEnv struct {
	APIBaseURL       string `json:"VITE_API_BASE_URL"`
	CloudURL         string `json:"CLOUD_URL,omitempty"`
	OrganizationSlug string `json:"ORGANIZATION_SLUG,omitempty"`
	// OrganizationID and InstanceID identify the managed instance this
	// console is served for. The SPA groups analytics by them; the
	// organization id is the same one Cloud groups by.
	OrganizationID string `json:"ORGANIZATION_ID,omitempty"`
	InstanceID     string `json:"INSTANCE_ID,omitempty"`
	PostHogHost    string `json:"POSTHOG_HOST,omitempty"`
	PostHogKey     string `json:"POSTHOG_KEY,omitempty"`
}

// authenticatedTenant is the tenant a request is verified for.
type authenticatedTenant struct {
	InstanceID       string
	OrganizationID   string
	OrganizationSlug string
}

// resolveAuthenticatedTenant reports whether the request carries a verified
// principal (set by the session middleware after validating the session
// cookie, or by API-key auth) and, when the host selects a managed instance,
// which one.
//
// The Host header selects the tenant on unauthenticated shell loads (/login
// is public and still gets the tenant config; the shared gateway resolves
// the host scope for every HTML route), so gating on the context alone would
// hand any visitor of a custom domain the owning org's identifiers. A
// principal for a different tenant than the host's gets nothing.
//
// Self-hosted gateways have no host-selected instance: a verified principal
// is reported with empty identifiers.
func resolveAuthenticatedTenant(r *http.Request) (authenticatedTenant, bool) {
	ctx := r.Context()
	if !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return authenticatedTenant{}, false
	}
	principal := strings.TrimSpace(contextkeys.GetTenantID(ctx))
	if tc := tenant.ConfigFromContext(ctx); tc != nil {
		if principal == strings.TrimSpace(tc.InstanceID) || principal == strings.TrimSpace(tc.OrganizationID) {
			return authenticatedTenant{
				InstanceID:       strings.TrimSpace(tc.InstanceID),
				OrganizationID:   strings.TrimSpace(tc.OrganizationID),
				OrganizationSlug: strings.TrimSpace(tc.OrgSlug),
			}, true
		}
		return authenticatedTenant{}, false
	}
	// The gateway's cookie auth installs the instance id as the tenant and
	// the verified organization in the scope; a self-hosted-style org
	// principal matches the organization.
	if scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
		if principal == strings.TrimSpace(scope.InstanceID) || principal == strings.TrimSpace(scope.OrganizationID) {
			return authenticatedTenant{
				InstanceID:       strings.TrimSpace(scope.InstanceID),
				OrganizationID:   strings.TrimSpace(scope.OrganizationID),
				OrganizationSlug: strings.TrimSpace(scope.OrganizationSlug),
			}, true
		}
		return authenticatedTenant{}, false
	}
	return authenticatedTenant{}, true
}

// servesManagedInstance reports whether the server resolved this request's
// host to a managed instance: the tenant config or host scope installed by
// the tenant and session middleware from a tenant_config lookup. Nothing the
// client sends (headers, cookies) sets either, and a host that resolves to
// no instance has neither. Self-hosted gateways have no tenant_config, so
// this is always false there.
func servesManagedInstance(r *http.Request) bool {
	ctx := r.Context()
	if tc := tenant.ConfigFromContext(ctx); tc != nil && strings.TrimSpace(tc.InstanceID) != "" {
		return true
	}
	scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx)
	return ok && strings.TrimSpace(scope.InstanceID) != ""
}

// buildRuntimeEnvScript builds the `<script>window.__env = ...</script>`
// block injected into the SPA shell. Values are read from environment
// variables at request time so a single SPA bundle can target multiple
// environments (dev / staging / prod) without rebuild.
//
// The tenant identifiers are *only* included for a verified principal of the
// tenant being served. The PostHog key goes to that principal and, on a
// managed instance host, to signed-out visitors (recorded anonymously). Other values (API base URL,
// cloud URL, PostHog *host*) are public and always included: the SPA
// bootstrap (login form, AuthGuard, relay redirect) needs them before any
// auth state exists.
//
// Analytics is off unless the operator sets both EVS_POSTHOG_KEY and
// EVS_POSTHOG_HOST. Our helm chart sets them for managed clusters; a
// self-hosted or CE gateway never phones home unless its operator opts in.
// Nothing is baked into the embedded SPA bundle.
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
	postHogHost := strings.TrimSpace(os.Getenv("EVS_POSTHOG_HOST"))
	env.PostHogHost = postHogHost
	t, authenticated := resolveAuthenticatedTenant(r)
	if authenticated {
		env.OrganizationSlug = t.OrganizationSlug
		env.OrganizationID = t.OrganizationID
		env.InstanceID = t.InstanceID
	}
	// Signed-out visitors of a managed instance are recorded too (anonymous,
	// with no tenant ids), so the sign-in handoff and bounces are visible.
	if postHogHost != "" && (authenticated || servesManagedInstance(r)) {
		env.PostHogKey = strings.TrimSpace(os.Getenv("EVS_POSTHOG_KEY"))
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
