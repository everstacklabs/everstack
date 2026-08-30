package authzhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/pkg/authz"
)

func TestBatchCheck(t *testing.T) {
	store := authz.NewMemStore()
	eng := authz.NewEngine(store, authz.EverstackSchema().WithResourceTypes("dataset"))
	_ = store.Write(authz.ContextWithTenant(context.Background(), "acme"),
		authz.OrgMembership("acme", "alice", authz.RoleOwner),
		authz.OrgMembership("acme", "carol", authz.RoleViewer),
	)
	// tenant func returns "acme" so reads land in the same tenant namespace the
	// tuples were written under; a different tenant would see nothing.
	h := New(eng,
		func(ctx context.Context) string { return ctx.Value("uid").(string) },
		func(context.Context) string { return "acme" },
		nil, // org-scoped checks resolve via persisted tuples; no session role needed
	)

	body := `{"checks":[
		{"permission":"org:manage_members","object":"organization:acme"},
		{"permission":"org:manage_billing","object":"organization:acme"},
		{"permission":"org:view","object":"organization:acme"}
	]}`

	// Owner: gets manage_members + manage_billing + view.
	req := httptest.NewRequest(http.MethodPost, "/authz/batch-check", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "uid", "alice"))
	rec := httptest.NewRecorder()
	h.BatchCheck(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp batchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Granted) != 3 {
		t.Fatalf("owner should be granted 3, got %v", resp.Granted)
	}

	// Viewer: only org:view.
	req2 := httptest.NewRequest(http.MethodPost, "/authz/batch-check", strings.NewReader(body))
	req2 = req2.WithContext(context.WithValue(req2.Context(), "uid", "carol"))
	rec2 := httptest.NewRecorder()
	h.BatchCheck(rec2, req2)
	var resp2 batchResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if len(resp2.Granted) != 1 || resp2.Granted[0] != "org:view@organization:acme" {
		t.Fatalf("viewer should get only org:view, got %v", resp2.Granted)
	}

	// Unauthenticated -> 401.
	req3 := httptest.NewRequest(http.MethodPost, "/authz/batch-check", strings.NewReader(body))
	req3 = req3.WithContext(context.WithValue(req3.Context(), "uid", ""))
	rec3 := httptest.NewRecorder()
	h.BatchCheck(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec3.Code)
	}
}

// TestBatchCheckResourceViaBridge exercises the per-resource path: a dataset has
// only its parent tuple, and access resolves from the caller's session role via
// the BridgeStore (member edits, viewer denied, owner deletes).
func TestBatchCheckResourceViaBridge(t *testing.T) {
	const tenant = "acme"
	store := authz.NewMemStore()
	eng := authz.NewEngine(authz.NewBridgeStore(store), authz.EverstackSchema().WithResourceTypes("dataset"))
	if err := store.Write(authz.ContextWithTenant(context.Background(), tenant),
		authz.ResourceParent("dataset", "d1", tenant),
	); err != nil {
		t.Fatal(err)
	}
	roles := map[string]authz.Role{"mike": authz.RoleMember, "val": authz.RoleViewer, "alice": authz.RoleOwner}
	h := New(eng,
		func(ctx context.Context) string { return ctx.Value("uid").(string) },
		func(context.Context) string { return tenant },
		func(ctx context.Context) string { return string(roles[ctx.Value("uid").(string)]) },
	)
	body := `{"checks":[
		{"permission":"resource:edit","object":"dataset:d1"},
		{"permission":"resource:delete","object":"dataset:d1"}
	]}`
	call := func(uid string) []string {
		req := httptest.NewRequest(http.MethodPost, "/authz/batch-check", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), "uid", uid))
		rec := httptest.NewRecorder()
		h.BatchCheck(rec, req)
		var resp batchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return resp.Granted
	}
	if g := call("mike"); len(g) != 1 || g[0] != "resource:edit@dataset:d1" {
		t.Fatalf("member should get edit only, got %v", g)
	}
	if g := call("val"); len(g) != 0 {
		t.Fatalf("viewer should get nothing, got %v", g)
	}
	if g := call("alice"); len(g) != 2 {
		t.Fatalf("owner should get edit+delete, got %v", g)
	}
}
