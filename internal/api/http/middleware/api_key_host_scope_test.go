package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// An API key is bound to one tenant. On a managed gateway the Host names a
// tenant too (stamped by tenantstatus.HostGuard, or the verified
// RequestInstanceScope for bearer requests). A key presented on another
// tenant's hostname must be refused exactly like an unknown key, so the
// response never tells the caller that the key is valid somewhere else.

const (
	hostScopeSecret = "host-scope-test-secret"
	tenantAKey      = "sk-mf-svcacct-api01-tenant-a-key"
	tenantA         = "aaaaaaaa-0000-0000-0000-00000000000a"
	tenantB         = "bbbbbbbb-0000-0000-0000-00000000000b"
	orgB            = "0b0b0b0b-0000-0000-0000-0000000000b0"
)

type oneKeyStore struct {
	key   string
	read  apikeyq.APIKeyReadModel
	calls int
}

func (s *oneKeyStore) QueryType() string { return "GetApiKeyByHash" }

func (s *oneKeyStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	s.calls++
	if h, _ := apikeylib.HashWithSecret(s.key, hostScopeSecret); h == q.(*apikeyq.GetApiKeyByHashQuery).Hash {
		return s.read, nil
	}
	return nil, nil
}

func strp(s string) *string { return &s }

type hostScopeHarness struct {
	interceptor *APIKeyInterceptor
	store       *oneKeyStore
	reached     bool
	tenant      string
}

func newHostScopeHarness(read apikeyq.APIKeyReadModel) *hostScopeHarness {
	return &hostScopeHarness{
		interceptor: NewAPIKeyInterceptor(false),
		store:       &oneKeyStore{key: tenantAKey, read: read},
	}
}

func (h *hostScopeHarness) serve(t *testing.T, key string, scope func(context.Context) context.Context) *httptest.ResponseRecorder {
	t.Helper()
	h.reached, h.tenant = false, ""
	handler := h.interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.reached = true
		h.tenant = contextkeys.GetTenantID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(common.EverstackApiKey, key)
	bus := query.NewQueryBus()
	bus.RegisterHandler(h.store)
	ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), hostScopeSecret)
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
	if scope != nil {
		ctx = scope(ctx)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func hostOf(instance, org string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return contextkeys.WithHostSelectedTenant(ctx, contextkeys.RequestInstanceScope{InstanceID: instance, OrganizationID: org})
	}
}

func verifiedHostOf(instance, org string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{InstanceID: instance, OrganizationID: org})
	}
}

// Cloud keys carry the instance id (tenant id IS the instance id there).
var cloudKeyOfA = apikeyq.APIKeyReadModel{OrgID: strp(tenantA)}

func TestAPIKeyOnItsOwnHostIsAccepted(t *testing.T) {
	h := newHostScopeHarness(cloudKeyOfA)
	// First request takes the DB path, the second the warm cache.
	for i := 0; i < 2; i++ {
		rec := h.serve(t, tenantAKey, hostOf(tenantA, "0a0a0a0a-0000-0000-0000-0000000000a0"))
		if rec.Code != http.StatusNoContent || !h.reached || h.tenant != tenantA {
			t.Fatalf("request %d: status %d, reached %t, tenant %q", i, rec.Code, h.reached, h.tenant)
		}
	}
	if h.store.calls != 1 {
		t.Fatalf("key store consulted %d times, want 1 (second request must hit the cache)", h.store.calls)
	}
}

func TestAPIKeyWithNoHostScopeIsAccepted(t *testing.T) {
	// Self-hosted, localhost, dev and the shared apex host resolve to no
	// tenant, so nothing changes for them.
	h := newHostScopeHarness(cloudKeyOfA)
	rec := h.serve(t, tenantAKey, nil)
	if rec.Code != http.StatusNoContent || h.tenant != tenantA {
		t.Fatalf("status %d, tenant %q", rec.Code, h.tenant)
	}
}

func TestAPIKeyOnAnotherTenantsHostIsRefusedLikeAnInvalidKey(t *testing.T) {
	for name, scope := range map[string]func(context.Context) context.Context{
		"host-selected tenant":   hostOf(tenantB, orgB),
		"verified request scope": verifiedHostOf(tenantB, orgB),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHostScopeHarness(cloudKeyOfA)
			invalid := h.serve(t, "sk-mf-svcacct-api01-never-issued", scope)

			// Cold (DB path), then warm (cache path).
			for i := 0; i < 2; i++ {
				rec := h.serve(t, tenantAKey, scope)
				if h.reached {
					t.Fatalf("request %d: tenant A's key reached the handler on tenant B's host", i)
				}
				if rec.Code != invalid.Code || rec.Code != http.StatusUnauthorized {
					t.Fatalf("request %d: status %d, invalid key gets %d", i, rec.Code, invalid.Code)
				}
				if rec.Body.String() != invalid.Body.String() {
					t.Fatalf("request %d: body differs from an invalid key's:\n got  %s\n want %s", i, rec.Body.String(), invalid.Body.String())
				}
				if rec.Header().Get("Content-Type") != invalid.Header().Get("Content-Type") {
					t.Fatalf("request %d: content type %q, invalid key gets %q", i, rec.Header().Get("Content-Type"), invalid.Header().Get("Content-Type"))
				}
			}

			// The key itself is valid: a refusal on the wrong host must not
			// poison the cache and lock it out of its own host.
			if rec := h.serve(t, tenantAKey, hostOf(tenantA, "")); rec.Code != http.StatusNoContent || h.tenant != tenantA {
				t.Fatalf("key refused on its own host after a cross-host attempt: status %d", rec.Code)
			}
		})
	}
}

func TestLegacyOrgScopedKeyIsAcceptedOnItsOrganizationsHosts(t *testing.T) {
	// A key with no instance id is scoped to its organization; it keeps
	// working on that organization's instances and nowhere else.
	h := newHostScopeHarness(apikeyq.APIKeyReadModel{OrgID: strp(orgB)})
	if rec := h.serve(t, tenantAKey, hostOf(tenantB, orgB)); rec.Code != http.StatusNoContent {
		t.Fatalf("own organization's host: status %d", rec.Code)
	}
	if rec := h.serve(t, tenantAKey, hostOf(tenantA, "0a0a0a0a-0000-0000-0000-0000000000a0")); rec.Code != http.StatusUnauthorized || h.reached {
		t.Fatalf("another organization's host: status %d", rec.Code)
	}
}

func TestInstanceScopedKeyIsRefusedOnASiblingInstance(t *testing.T) {
	// A key bound to instance A of organization B is not valid on instance
	// B of the same organization.
	h := newHostScopeHarness(apikeyq.APIKeyReadModel{InstanceID: strp(tenantA), OrgID: strp(orgB)})
	if rec := h.serve(t, tenantAKey, hostOf(tenantB, orgB)); rec.Code != http.StatusUnauthorized || h.reached {
		t.Fatalf("sibling instance host: status %d", rec.Code)
	}
	if rec := h.serve(t, tenantAKey, hostOf(tenantA, orgB)); rec.Code != http.StatusNoContent {
		t.Fatalf("own instance host: status %d", rec.Code)
	}
}
