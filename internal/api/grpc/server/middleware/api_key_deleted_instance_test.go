package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
)

// A key that resolves to a deleted managed instance is rejected even when the
// positive answer is still cached: deleting an instance must stop its keys.
func TestValidateAPIKeyRejectsKeysOfADeletedInstance(t *testing.T) {
	tenantlive.SetDeletedCheck(func(id string) bool { return id == "inst-deleted" })
	t.Cleanup(func() { tenantlive.SetDeletedCheck(nil) })

	interceptor := NewAPIKeyInterceptorWithPolicy(false, nil)
	for _, tc := range []struct {
		tenant string
		ok     bool
	}{{"inst-deleted", false}, {"inst-live", true}} {
		key := "key-" + tc.tenant
		hash, _ := apikey.HashWithSecret(key, "test-hash-secret")
		interceptor.cache.Store(hash, cacheEntry{valid: true, orgID: tc.tenant, expiresAt: time.Now().Add(time.Minute)})

		ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), "test-hash-secret")
		header := http.Header{}
		header.Set(common.EverstackApiKey, key)
		_, err := interceptor.validateAPIKey(ctx, header, "/everstack.storage.v1.ObjectStorageService/ListObjects", "corr")
		if tc.ok && err != nil {
			t.Fatalf("%s: live instance key rejected: %v", tc.tenant, err)
		}
		if !tc.ok && connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s: err = %v, want Unauthenticated", tc.tenant, err)
		}
	}
}
