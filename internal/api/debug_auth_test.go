package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestRequireOrgAdmin(t *testing.T) {
	reached := false
	h := requireOrgAdmin(func(http.ResponseWriter, *http.Request) { reached = true })
	call := func(ctx context.Context) int {
		reached = false
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", "/debug/health/providers", nil).WithContext(ctx))
		return rec.Code
	}
	person := func(role string) context.Context {
		ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
		ctx = contextkeys.WithCloudUserID(ctx, "user-1")
		ctx = contextkeys.WithTenantAuthenticated(ctx)
		if role != "" {
			ctx = contextkeys.WithUserRole(ctx, role)
		}
		return ctx
	}

	refused := map[string]context.Context{
		"no credential":    context.Background(),
		"tenant id only":   contextkeys.WithTenantID(context.Background(), "tenant-a"),
		"member":           person("member"),
		"person, no role":  person(""),
		"api key, no role": contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "hash", "key-1"),
	}
	for name, ctx := range refused {
		if code := call(ctx); code != http.StatusUnauthorized || reached {
			t.Errorf("%s: want 401 without running the handler, got %d reached=%v", name, code, reached)
		}
	}
	for _, role := range []string{"owner", "admin"} {
		if code := call(person(role)); code != http.StatusOK || !reached {
			t.Errorf("%s should pass, got %d reached=%v", role, code, reached)
		}
	}
}
