package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// API-shaped paths must short-circuit to 404 instead of falling through
// to the SPA shell. The previous behavior leaked the runtime env block
// (including the PostHog key) to any visitor hitting an unknown URL on
// the gateway host, and silently 200'd responses to typo'd RPC paths.
func TestIsAPILikePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Should 404 — API-shaped.
		{"/everstack.providers.v1.ProvidersService/List", true},
		{"/everstack.auth.v1.AuthService/GetSession", true},
		{"/api/auth/signin", true},
		{"/api/auth/callback", true},
		{"/v1/agents", true},
		{"/auth/cloud-callback", true},
		{"/auth/instance-signout", true},
		{"/auth/__version", true},
		{"/__version", true},
		{"/__metrics", true},

		// Should fall through to SPA — these are real client-side routes.
		{"/", false},
		{"/login", false},
		{"/dashboard", false},
		{"/deployments/agents", false},
		{"/settings/api-keys", false},
		{"/observability/traces", false},
		// Asset paths are handled earlier by the static file server; the
		// API-like check would also let them through anyway.
		{"/assets/index.js", false},
		{"/favicon.ico", false},
	}
	for _, tc := range cases {
		if got := isAPILikePath(tc.path); got != tc.want {
			t.Errorf("isAPILikePath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// POSTHOG_KEY must not appear in the injected env unless the request carries
// a verified principal for the tenant it is served on. Other values
// (CLOUD_URL, POSTHOG_HOST, VITE_API_BASE_URL) are public and stay in. Tests
// use t.Setenv so they're race-safe.
func TestBuildRuntimeEnvScript_GatesPosthogKey(t *testing.T) {
	t.Setenv("EVS_CLOUD_URL", "https://app-dev.everstack.ai")
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.dev.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_secret_should_not_leak")

	unauth := httptest.NewRequest(http.MethodGet, "https://inst.dev.eu-gra-1.everstack.ai/", nil)
	got := buildRuntimeEnvScript(unauth)

	// Public values: present.
	for _, must := range []string{"VITE_API_BASE_URL", "CLOUD_URL", "POSTHOG_HOST", "app-dev.everstack.ai", "ph.dev.everstack.ai"} {
		if !strings.Contains(got, must) {
			t.Errorf("unauth response missing %q: %s", must, got)
		}
	}
	assertNoPosthogKey(t, "unauth", got)

	// A verified member of the host-selected managed instance gets the key.
	authed := managedInstanceRequest(authedAs(context.Background(), "instance-1"))
	got = buildRuntimeEnvScript(authed)
	if !strings.Contains(got, `"POSTHOG_KEY":"phc_secret_should_not_leak"`) {
		t.Errorf("verified instance member should receive POSTHOG_KEY: %s", got)
	}
}

// Regression: the key used to be gated on the presence of an
// `es_tenant_session` cookie, which instance auth stopped setting in May
// 2026. Real users never carried it, so managed instances recorded no
// analytics at all, while anyone could spoof the dead cookie to get the key.
// Cookie presence must no longer matter; only verified identity does.
func TestBuildRuntimeEnvScript_LegacyCookieDoesNotUnlockPosthogKey(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_secret_should_not_leak")

	for _, name := range []string{"es_tenant_session", "es_everstack_session", "everstack_session"} {
		req := httptest.NewRequest(http.MethodGet, "https://inst.eu-gra-1.everstack.ai/", nil)
		req.AddCookie(&http.Cookie{Name: name, Value: "unverified"})
		assertNoPosthogKey(t, name, buildRuntimeEnvScript(req))
	}
}

// The organization and instance ids used to group events must not reach an
// anonymous visitor (the /login shell still gets the host-selected tenant in
// context), a member of a different organization, or a context whose auth
// flag lacks a verified identity.
func TestBuildRuntimeEnvScript_GroupingIdsRequireMatchingPrincipal(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_secret_should_not_leak")

	cfg := &tenant.Config{InstanceID: "instance-1", OrganizationID: "org-1", OrgSlug: "victim-org"}
	cases := map[string]*http.Request{
		"anonymous, host scope":          managedInstanceRequest(context.Background()),
		"anonymous, tenant config":       requestWithContext(tenant.WithConfig(context.Background(), cfg)),
		"other org, host scope":          managedInstanceRequest(authedAs(context.Background(), "org-2")),
		"other org, tenant config":       requestWithContext(tenant.WithConfig(authedAs(context.Background(), "org-2"), cfg)),
		"flag without verified identity": managedInstanceRequest(contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "instance-1"))),
		"rejected session":               managedInstanceRequest(contextkeys.WithTenantAuthRejected(context.Background())),
	}
	for name, req := range cases {
		got := buildRuntimeEnvScript(req)
		for _, leaked := range []string{"ORGANIZATION_ID", "INSTANCE_ID", "ORGANIZATION_SLUG", "instance-1", "org-1", "victim-org"} {
			if strings.Contains(got, leaked) {
				t.Errorf("%s: %q leaked: %s", name, leaked, got)
			}
		}
	}
}

// Signed-out visitors of a managed instance are recorded too, so the sign-in
// handoff and any bounce are visible. "Managed" is the server-resolved host
// scope (or tenant config): a Host that resolves to no instance gets
// nothing, and nothing the client sends can make it resolve.
func TestBuildRuntimeEnvScript_AnonymousOnManagedInstanceGetsKeyOnly(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_cloud_project")

	cfg := &tenant.Config{InstanceID: "instance-1", OrganizationID: "org-1", OrgSlug: "victim-org"}
	for name, req := range map[string]*http.Request{
		"anonymous, host scope":    managedInstanceRequest(context.Background()),
		"anonymous, tenant config": requestWithContext(tenant.WithConfig(context.Background(), cfg)),
		"rejected session":         managedInstanceRequest(contextkeys.WithTenantAuthRejected(context.Background())),
		"other org":                managedInstanceRequest(authedAs(context.Background(), "org-2")),
	} {
		if got := buildRuntimeEnvScript(req); !strings.Contains(got, `"POSTHOG_KEY":"phc_cloud_project"`) {
			t.Errorf("%s: signed-out visitor of a managed instance should get the key: %s", name, got)
		}
	}
}

// Unknown hosts (no instance resolved) and self-hosted gateways never send
// the key to a visitor who is not signed in, whatever the request carries.
func TestBuildRuntimeEnvScript_AnonymousWithoutManagedInstanceGetsNothing(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_secret_should_not_leak")

	plain := httptest.NewRequest(http.MethodGet, "https://unknown.eu-gra-1.everstack.ai/login", nil)
	spoofed := httptest.NewRequest(http.MethodGet, "https://unknown.eu-gra-1.everstack.ai/login", nil)
	spoofed.Header.Set("X-Forwarded-Host", "prod-a1b2c3.eu-gra-1.everstack.ai")
	spoofed.Header.Set("X-Tenant-Id", "instance-1")
	spoofed.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "x"})
	for name, req := range map[string]*http.Request{
		"unknown host":           plain,
		"client-supplied tenant": spoofed,
		"self-hosted, rejected":  requestWithContext(contextkeys.WithTenantAuthRejected(context.Background())),
		"auth flag, no identity": requestWithContext(contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "org-1"))),
	} {
		assertNoPosthogKey(t, name, buildRuntimeEnvScript(req))
	}
}

// Managed instances group events by organization and instance, using the
// same organization id Cloud groups by. The ids come from the verified host
// scope, not from the user's first membership, so a user in several
// organizations is attributed to the organization that owns this instance.
func TestBuildRuntimeEnvScript_ExposesVerifiedTenantIdsForGrouping(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://ph.everstack.ai")
	t.Setenv("EVS_POSTHOG_KEY", "phc_project")

	got := buildRuntimeEnvScript(managedInstanceRequest(authedAs(context.Background(), "instance-1")))
	for _, want := range []string{`"ORGANIZATION_ID":"org-1"`, `"INSTANCE_ID":"instance-1"`, `"ORGANIZATION_SLUG":"everstack"`, `"POSTHOG_KEY":"phc_project"`} {
		if !strings.Contains(got, want) {
			t.Errorf("runtime environment missing %s: %s", want, got)
		}
	}
}

// Self-hosted and CE gateways never phone home unless the operator opts in by
// setting EVS_POSTHOG_KEY. Our helm chart sets it only for managed clusters.
// The host must be configured too: the SPA would otherwise fall back to a
// default host, routing an opted-in operator's events through our proxy.
func TestBuildRuntimeEnvScript_PosthogRequiresOperatorConfig(t *testing.T) {
	cases := map[string]struct{ key, host string }{
		"no key, no host": {"", ""},
		"host only":       {"", "https://ph.everstack.ai"},
		"key only":        {"phc_project", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("EVS_POSTHOG_KEY", tc.key)
			t.Setenv("EVS_POSTHOG_HOST", tc.host)
			assertNoPosthogKey(t, name, buildRuntimeEnvScript(managedInstanceRequest(authedAs(context.Background(), "instance-1"))))
		})
	}
}

// A self-hosted operator who opted in gets the key for verified users. There
// is no host-selected instance, so no grouping ids are emitted and the SPA
// falls back to the session's organization.
func TestBuildRuntimeEnvScript_SelfHostedOptIn(t *testing.T) {
	t.Setenv("EVS_POSTHOG_HOST", "https://posthog.internal.example")
	t.Setenv("EVS_POSTHOG_KEY", "phc_operator_project")

	got := buildRuntimeEnvScript(requestWithContext(authedAs(context.Background(), "org-7")))
	if !strings.Contains(got, `"POSTHOG_KEY":"phc_operator_project"`) {
		t.Errorf("opted-in self-hosted user should receive POSTHOG_KEY: %s", got)
	}
	if strings.Contains(got, "INSTANCE_ID") || strings.Contains(got, "ORGANIZATION_ID") {
		t.Errorf("self-hosted response must not carry grouping ids: %s", got)
	}
}

func managedInstanceRequest(ctx context.Context) *http.Request {
	return requestWithContext(contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "everstack",
	}))
}

func requestWithContext(ctx context.Context) *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://instance-abc123.eu-gra-1.everstack.ai/", nil).WithContext(ctx)
}

func assertNoPosthogKey(t *testing.T, name, got string) {
	t.Helper()
	if strings.Contains(got, "POSTHOG_KEY") || strings.Contains(got, "phc_") {
		t.Errorf("%s: response must not include POSTHOG_KEY: %s", name, got)
	}
}

func TestBuildRuntimeEnvScriptIncludesTenantOrganizationSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance-abc123.dev.eu-gra-1.everstack.ai/", nil)
	ctx := authedAs(req.Context(), "instance-1")
	req = req.WithContext(tenant.WithConfig(ctx, &tenant.Config{InstanceID: "instance-1", OrganizationID: "org-1", OrgSlug: "everstack"}))

	got := buildRuntimeEnvScript(req)
	if !strings.Contains(got, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("runtime environment missing organization slug: %s", got)
	}
}

// authedAs mirrors the session middleware: a verified user whose request
// tenant is tenantID.
func authedAs(ctx context.Context, tenantID string) context.Context {
	ctx = contextkeys.WithCloudUserID(ctx, "user-1")
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	return contextkeys.WithTenantAuthenticated(ctx)
}

// The Host header selects the tenant on unauthenticated shell loads: /login
// on a managed instance still receives the tenant config, and the shared
// gateway resolves the host scope for every HTML route. Neither may put the
// owning organization's slug into HTML served to an anonymous visitor (a
// custom domain does not otherwise reveal it), nor to a user signed in as a
// different organization.
func TestBuildRuntimeEnvScriptOmitsOrganizationSlugWithoutMatchingPrincipal(t *testing.T) {
	cfg := &tenant.Config{InstanceID: "instance-1", OrganizationID: "org-1", OrgSlug: "victim-org"}
	scope := contextkeys.RequestInstanceScope{InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "victim-org"}
	cases := map[string]context.Context{
		"anonymous, tenant config":       tenant.WithConfig(context.Background(), cfg),
		"anonymous, host scope":          contextkeys.WithRequestInstanceScope(context.Background(), scope),
		"other org, tenant config":       tenant.WithConfig(authedAs(context.Background(), "org-2"), cfg),
		"other org, host scope":          contextkeys.WithRequestInstanceScope(authedAs(context.Background(), "org-2"), scope),
		"flag without verified identity": tenant.WithConfig(contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "instance-1")), cfg),
	}
	for name, ctx := range cases {
		req := httptest.NewRequest(http.MethodGet, "https://app.victim.example/login", nil).WithContext(ctx)
		if got := buildRuntimeEnvScript(req); strings.Contains(got, "victim-org") || strings.Contains(got, "ORGANIZATION_SLUG") {
			t.Errorf("%s: organization slug leaked: %s", name, got)
		}
	}
}

func TestBuildRuntimeEnvScriptIncludesVerifiedHostOrganizationSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance-abc123.dev.eu-gra-1.everstack.ai/", nil)
	req = req.WithContext(contextkeys.WithRequestInstanceScope(authedAs(req.Context(), "instance-1"), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "everstack",
	}))

	got := buildRuntimeEnvScript(req)
	if !strings.Contains(got, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("runtime environment missing verified-host organization slug: %s", got)
	}
}

func TestBuildRuntimeEnvScriptEscapesScriptClosingTags(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance.example.com/", nil)
	req = req.WithContext(tenant.WithConfig(authedAs(req.Context(), "instance-1"), &tenant.Config{
		InstanceID: "instance-1",
		OrgSlug:    `acme</script><script>alert("xss")</script>`,
	}))

	got := buildRuntimeEnvScript(req)
	if strings.Contains(got, `</script><script>`) {
		t.Fatalf("runtime environment contains an executable closing script tag: %s", got)
	}
	if !strings.Contains(got, `acme\u003c/script\u003e\u003cscript\u003e`) {
		t.Fatalf("runtime environment did not JSON-escape HTML-sensitive characters: %s", got)
	}
}

// injectRuntimeEnv places the script just before </head> when present so
// it executes before the SPA's own scripts. Falls back to prepend so a
// malformed template still gets the env (and the SPA can still bootstrap).
func TestInjectRuntimeEnv(t *testing.T) {
	const tmpl = "<html><head><title>x</title></head><body></body></html>"
	out := injectRuntimeEnv(tmpl, "<script>X</script>")
	if !strings.Contains(out, "<script>X</script></head>") {
		t.Errorf("script must be injected just before </head>, got: %s", out)
	}

	const noHead = "<html><body>x</body></html>"
	out = injectRuntimeEnv(noHead, "<script>X</script>")
	if !strings.HasPrefix(out, "<script>X</script>") {
		t.Errorf("when </head> absent, script must be prepended, got: %s", out)
	}
}
