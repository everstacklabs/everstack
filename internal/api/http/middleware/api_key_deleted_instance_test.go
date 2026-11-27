package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"github.com/everstacklabs/everstack/internal/query"
)

// A cached key of a deleted managed instance is refused with 401.
func TestAPIKeyOfADeletedInstanceIsRejected(t *testing.T) {
	tenantlive.SetDeletedCheck(func(id string) bool { return id == "inst-deleted" })
	t.Cleanup(func() { tenantlive.SetDeletedCheck(nil) })

	interceptor := NewAPIKeyInterceptor(false)
	for tenant, want := range map[string]int{"inst-deleted": http.StatusUnauthorized, "inst-live": http.StatusNoContent} {
		key := "key-" + tenant
		hash, _ := apikeylib.HashWithSecret(key, "test-hash-secret")
		interceptor.cache[hash] = apiKeyCacheEntry{valid: true, orgID: tenant, expiresAt: time.Now().Add(time.Minute)}

		handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload", nil)
		req.Header.Set(common.EverstackApiKey, key)
		ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), "test-hash-secret")
		ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: query.NewQueryBus()})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req.WithContext(ctx))
		if res.Code != want {
			t.Fatalf("%s: status = %d, want %d", tenant, res.Code, want)
		}
	}
}
