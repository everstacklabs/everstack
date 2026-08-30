package serverauth

import (
	"context"
	"errors"
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
