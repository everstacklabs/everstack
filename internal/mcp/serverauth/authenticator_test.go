package serverauth

import (
	"context"
	"errors"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/everstacklabs/everstack/internal/query"
	apikey "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestMain(m *testing.M) {
	// A configured HMAC secret is a precondition for hashing; set one so
	// apikeylib.HashFromContext succeeds in tests (otherwise auth fails closed
	// before the lookup, which is itself covered by TestNoSecretFailsClosed-style
	// behavior in production).
	_ = os.Setenv("EVS_API_KEY_HASH_SECRET", "test-secret-for-mcp-auth")
	os.Exit(m.Run())
}

type fakeLookup struct {
	result interface{}
	err    error
}

func (f fakeLookup) Handle(_ context.Context, _ query.Query) (interface{}, error) {
	return f.result, f.err
}

func strptr(s string) *string { return &s }

func req(t *testing.T, header, value string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if header != "" {
		r.Header.Set(header, value)
	}
	return r
}

func TestNoKeyFailsClosed(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{OrgID: strptr("tenant-A")}}, nil)
	if tid, ok := a.Authenticate(req(t, "", "")); ok || tid != "" {
		t.Fatalf("expected no auth without a key, got (%q,%v)", tid, ok)
	}
}

func TestBearerKeyResolvesInstanceID(t *testing.T) {
	// instance_id wins over org_id.
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{
		InstanceID: strptr("inst-123"),
		OrgID:      strptr("org-should-not-win"),
	}}, nil)
	tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test"))
	if !ok || tid != "inst-123" {
		t.Fatalf("want inst-123, got (%q,%v)", tid, ok)
	}
}

func TestBearerKeyResolvesOrgIDWhenNoInstance(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{OrgID: strptr("org-A")}}, nil)
	tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test"))
	if !ok || tid != "org-A" {
		t.Fatalf("want org-A, got (%q,%v)", tid, ok)
	}
}

func TestEverstackApiKeyHeaderAccepted(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{OrgID: strptr("org-A")}}, nil)
	tid, ok := a.Authenticate(req(t, "x-everstack-api-key", "sk-test"))
	if !ok || tid != "org-A" {
		t.Fatalf("want org-A via api-key header, got (%q,%v)", tid, ok)
	}
}

func TestUnknownKeyFailsClosed(t *testing.T) {
	// Lookup returns nil (no row) => unknown/revoked key.
	a := newWithLookup(fakeLookup{result: nil}, nil)
	if tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-bad")); ok || tid != "" {
		t.Fatalf("expected fail-closed for unknown key, got (%q,%v)", tid, ok)
	}
}

func TestLookupErrorFailsClosed(t *testing.T) {
	a := newWithLookup(fakeLookup{err: errors.New("db down")}, nil)
	if tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test")); ok || tid != "" {
		t.Fatalf("expected fail-closed on lookup error, got (%q,%v)", tid, ok)
	}
}

func TestNeitherOrgNorInstanceFailsClosed(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{}}, nil)
	if tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test")); ok || tid != "" {
		t.Fatalf("expected fail-closed when key has no tenant, got (%q,%v)", tid, ok)
	}
}

func TestEmptyBearerFailsClosed(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{OrgID: strptr("org-A")}}, nil)
	if tid, ok := a.Authenticate(req(t, "Authorization", "Bearer ")); ok || tid != "" {
		t.Fatalf("expected fail-closed for empty bearer, got (%q,%v)", tid, ok)
	}
}

// A key of a deleted managed instance does not authenticate.
func TestKeyOfADeletedInstanceFailsClosed(t *testing.T) {
	tenantlive.SetDeletedCheck(func(id string) bool { return id == "inst-deleted" })
	t.Cleanup(func() { tenantlive.SetDeletedCheck(nil) })
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{InstanceID: strptr("inst-deleted")}}, nil)
	if tid, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test")); ok || tid != "" {
		t.Fatalf("deleted instance key authenticated as (%q,%v)", tid, ok)
	}
}

// MCP and OTLP ingest authenticate keys here, outside the API-key
// interceptors: a key bound to one tenant is refused on another tenant's
// hostname exactly like an unknown key, and keeps working on its own host and
// where the Host names no tenant.
func TestKeyOnAnotherTenantsHostFailsClosed(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{OrgID: strptr("inst-A")}}, nil)
	onHost := func(instance string) *http.Request {
		r := req(t, "Authorization", "Bearer sk-test")
		if instance == "" {
			return r
		}
		return r.WithContext(contextkeys.WithHostSelectedTenant(r.Context(), contextkeys.RequestInstanceScope{InstanceID: instance, OrganizationID: "org-of-" + instance}))
	}
	if tid, ok := a.Authenticate(onHost("inst-B")); ok || tid != "" {
		t.Fatalf("key of inst-A authenticated on inst-B's host as (%q,%v)", tid, ok)
	}
	for _, host := range []string{"inst-A", ""} {
		if tid, ok := a.Authenticate(onHost(host)); !ok || tid != "inst-A" {
			t.Fatalf("host %q: want inst-A, got (%q,%v)", host, tid, ok)
		}
	}
}

// The key's id comes back with its tenant, for spend attribution.
func TestAuthenticateKeyReturnsKeyID(t *testing.T) {
	a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{ID: "key-123", OrgID: strptr("org-A")}}, nil)
	tid, keyID, ok := a.AuthenticateKey(req(t, "Authorization", "Bearer sk-test"))
	if !ok || tid != "org-A" || keyID != "key-123" {
		t.Fatalf("got (%q,%q,%v), want (org-A,key-123,true)", tid, keyID, ok)
	}
	if tid, keyID, ok := a.AuthenticateKey(req(t, "", "")); ok || tid != "" || keyID != "" {
		t.Fatalf("no key must fail closed with no id: (%q,%q,%v)", tid, keyID, ok)
	}
}

// Every protocol view must declare its own surface, including AuthenticateKey.
func TestDeclaredSurfaces(t *testing.T) {
	for _, held := range []string{"mcp", "a2a", "telemetry", "inference"} {
		a := newWithLookup(fakeLookup{result: apikey.APIKeyReadModel{ID: "key", OrgID: strptr("tenant"), Surfaces: []string{held}}}, nil)
		for _, declared := range []string{"", "mcp", "a2a", "telemetry", "inference"} {
			view := a.ForSurface(apikeylib.Surface(declared))
			tenant, id, ok := view.AuthenticateKey(req(t, "Authorization", "Bearer sk-test"))
			if ok != (held == declared) {
				t.Fatalf("key=%s view=%s allowed=%v", held, declared, ok)
			}
			if !ok && (tenant != "" || id != "") {
				t.Fatal("identity escaped on refusal")
			}
			if _, ok := view.Authenticate(req(t, "Authorization", "Bearer sk-test")); ok != (held == declared) {
				t.Fatal("Authenticate differed")
			}
		}
		if _, ok := a.Authenticate(req(t, "Authorization", "Bearer sk-test")); ok {
			t.Fatal("undeclared authenticator accepted bot")
		}
	}
}
