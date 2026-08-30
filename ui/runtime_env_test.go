package ui

import (
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

// POSTHOG_KEY must not appear in the injected env when the request looks
// unauthenticated. Other values (CLOUD_URL, POSTHOG_HOST, VITE_API_BASE_URL)
// are public and stay in. Tests use t.Setenv so they're race-safe.
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
	// PostHog *key* must NOT be present.
	if strings.Contains(got, "POSTHOG_KEY") {
		t.Errorf("unauth response must not include POSTHOG_KEY name: %s", got)
	}
	if strings.Contains(got, "phc_secret_should_not_leak") {
		t.Errorf("unauth response must not include POSTHOG_KEY value: %s", got)
	}

	// Now with the auth cookie set — POSTHOG_KEY should appear.
	authed := httptest.NewRequest(http.MethodGet, "https://inst.dev.eu-gra-1.everstack.ai/", nil)
	authed.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "anything-nonempty"})
	got = buildRuntimeEnvScript(authed)
	if !strings.Contains(got, "POSTHOG_KEY") {
		t.Errorf("authed response should include POSTHOG_KEY: %s", got)
	}
	if !strings.Contains(got, "phc_secret_should_not_leak") {
		t.Errorf("authed response should include POSTHOG_KEY value: %s", got)
	}
}

func TestBuildRuntimeEnvScriptIncludesTenantOrganizationSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance-abc123.dev.eu-gra-1.everstack.ai/", nil)
	req = req.WithContext(tenant.WithConfig(req.Context(), &tenant.Config{OrgSlug: "everstack"}))

	got := buildRuntimeEnvScript(req)
	if !strings.Contains(got, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("runtime environment missing organization slug: %s", got)
	}
}

func TestBuildRuntimeEnvScriptIncludesVerifiedHostOrganizationSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance-abc123.dev.eu-gra-1.everstack.ai/", nil)
	req = req.WithContext(contextkeys.WithRequestInstanceScope(req.Context(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: "org-1", OrganizationSlug: "everstack",
	}))

	got := buildRuntimeEnvScript(req)
	if !strings.Contains(got, `"ORGANIZATION_SLUG":"everstack"`) {
		t.Fatalf("runtime environment missing verified-host organization slug: %s", got)
	}
}

func TestBuildRuntimeEnvScriptEscapesScriptClosingTags(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://instance.example.com/", nil)
	req = req.WithContext(tenant.WithConfig(req.Context(), &tenant.Config{
		OrgSlug: `acme</script><script>alert("xss")</script>`,
	}))

	got := buildRuntimeEnvScript(req)
	if strings.Contains(got, `</script><script>`) {
		t.Fatalf("runtime environment contains an executable closing script tag: %s", got)
	}
	if !strings.Contains(got, `acme\u003c/script\u003e\u003cscript\u003e`) {
		t.Fatalf("runtime environment did not JSON-escape HTML-sensitive characters: %s", got)
	}
}

// requestLooksAuthed must reject an empty cookie value (browsers will
// sometimes send `name=` after a deletion). A "yes-this-is-authed"
// answer based on a present-but-empty cookie would re-leak the key.
func TestRequestLooksAuthed(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"no cookie", false, "", false},
		{"empty value", true, "", false},
		{"non-empty value", true, "tok", true},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "https://i.example.com/", nil)
		if tc.set {
			req.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: tc.value})
		}
		if got := requestLooksAuthed(req); got != tc.want {
			t.Errorf("%s: requestLooksAuthed = %v, want %v", tc.name, got, tc.want)
		}
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
