package otlp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// fakeResolver mirrors *mcpserverauth.APIKeyAuthenticator: a key authenticates
// only if it is in the known set, and "presented" is independent of validity.
type fakeResolver struct {
	known map[string]string // key -> tenant
}

func (f fakeResolver) Authenticate(r *http.Request) (string, bool) {
	key := f.key(r)
	if key == "" {
		return "", false
	}
	tenant, ok := f.known[key]
	return tenant, ok
}

func (f fakeResolver) PresentsCredential(r *http.Request) bool { return f.key(r) != "" }

func (f fakeResolver) key(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if k := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")); k != "" {
			return k
		}
	}
	return strings.TrimSpace(r.Header.Get("x-everstack-api-key"))
}

// spy records whether the wrapped handler ran and with which tenant.
type spy struct {
	called bool
	tenant string
	schema string
}

func (s *spy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.tenant = contextkeys.GetTenantID(r.Context())
		s.schema = database.TenantSchemaFromContext(r.Context())
	})
}

func TestValidKeyStampsItsOwnTenant(t *testing.T) {
	got := &spy{}
	auth := fakeResolver{known: map[string]string{"good": "tenant-a"}}
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()

	WithTenantAuth(got.handler(), auth).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !got.called {
		t.Fatal("handler was not called")
	}
	if got.tenant != "tenant-a" || got.schema != "tenant-a" {
		t.Fatalf("tenant/schema = %q/%q, want tenant-a on both", got.tenant, got.schema)
	}
}

// The regression this whole change exists for. A key that is presented and does
// not resolve must be rejected, NOT silently ingested under whatever tenant an
// upstream middleware left in the context.
func TestPresentedButInvalidKeyIsRejectedEvenWithContextTenant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{"bearer", "Authorization", "Bearer revoked"},
		{"everstack header", "x-everstack-api-key", "revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := &spy{}
			auth := fakeResolver{known: map[string]string{"good": "tenant-a"}}

			req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
			req.Header.Set(tc.header, tc.value)
			// An upstream middleware left a tenant behind, as LocalScopeResolver
			// does on a gateway that believes it is standalone.
			ctx := contextkeys.WithTenantID(req.Context(), "local-placeholder")
			ctx = database.WithTenantSchema(ctx, "local-placeholder")
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			WithTenantAuth(got.handler(), auth).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got.called {
				t.Fatalf("handler ran and would have ingested under %q", got.tenant)
			}
		})
	}
}

// The standalone self-hosted path: no credential at all, so the tenant the
// local resolver injected is the legitimate owner.
func TestNoCredentialFallsBackToContextTenant(t *testing.T) {
	got := &spy{}
	auth := fakeResolver{known: map[string]string{"good": "tenant-a"}}

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	ctx := contextkeys.WithTenantID(req.Context(), "local-tenant")
	ctx = database.WithTenantSchema(ctx, "local-tenant")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	WithTenantAuth(got.handler(), auth).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !got.called || got.tenant != "local-tenant" {
		t.Fatalf("called=%v tenant=%q, want true/local-tenant", got.called, got.tenant)
	}
}

// A managed gateway injects no tenant (LocalScopeResolver is disabled there), so
// an anonymous export has nothing to attribute to and must be refused rather
// than written somewhere unreadable.
func TestNoCredentialAndNoContextTenantIsRejected(t *testing.T) {
	got := &spy{}
	auth := fakeResolver{known: map[string]string{"good": "tenant-a"}}
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	rec := httptest.NewRecorder()

	WithTenantAuth(got.handler(), auth).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got.called {
		t.Fatal("handler ran on an unauthenticated export")
	}
}

// An empty Bearer is not a presented credential, so it takes the no-credential
// path rather than being rejected outright.
func TestEmptyBearerIsNotAPresentedCredential(t *testing.T) {
	got := &spy{}
	auth := fakeResolver{known: map[string]string{"good": "tenant-a"}}

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer ")
	ctx := contextkeys.WithTenantID(req.Context(), "local-tenant")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	WithTenantAuth(got.handler(), auth).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !got.called {
		t.Fatalf("status=%d called=%v, want 200/true", rec.Code, got.called)
	}
}

// With no resolver wired at all the middleware must not start rejecting
// requests it previously passed through.
func TestNilResolverStillFallsBackToContextTenant(t *testing.T) {
	got := &spy{}
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	ctx := contextkeys.WithTenantID(req.Context(), "local-tenant")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	WithTenantAuth(got.handler(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !got.called {
		t.Fatalf("status=%d called=%v, want 200/true", rec.Code, got.called)
	}
}
