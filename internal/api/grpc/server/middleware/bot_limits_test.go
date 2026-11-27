package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type inferenceRecorder struct{ calls int }

func (r *inferenceRecorder) Record(string, string, apikey.Surface) { r.calls++ }
func TestConnectBotLimitAndSignedHop(t *testing.T) {
	recorder := &inferenceRecorder{}
	old := apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), recorder))
	defer apikey.SetRuntime(old)
	rpm := 1
	proc := "/everstack.gateway.v1.GatewayService/ListModels"
	i := NewAPIKeyInterceptor(false)
	ctx := contextkeys.WithAPIKeyRateLimit(contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant", "verified", "one"), []string{"inference"}), &rpm)
	admitted, err := i.validateAPIKey(ctx, http.Header{}, proc, "")
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	header.Set(internalauth.GatewayIdentityHeader, internalauth.SignGatewayIdentity(internalauth.GatewayIdentity{TenantID: "tenant", KeyID: "one", Hash: "verified", Surfaces: []string{"inference"}, RateLimitRPM: &rpm, Processed: apikey.Processed(admitted, "one", apikey.SurfaceInference)}))
	if _, err = i.validateAPIKey(context.Background(), header, proc, ""); err != nil {
		t.Fatalf("REST hop charged twice: %v", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("REST hop recorded twice: %d", recorder.calls)
	}
	_, err = i.validateAPIKey(ctx, http.Header{}, proc, "")
	ce := new(connect.Error)
	if !errors.As(err, &ce) || ce.Code() != connect.CodeResourceExhausted {
		t.Fatalf("over: %v", err)
	}
	if n, _ := strconv.Atoi(ce.Meta().Get("Retry-After")); n < 1 {
		t.Fatalf("missing retry: %v", ce.Meta())
	}
	other := contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant", "other-digest", "two")
	if _, err = i.validateAPIKey(other, http.Header{}, proc, ""); err != nil {
		t.Fatalf("key isolation: %v", err)
	}
}

func TestConnectCachedBotRateLimit(t *testing.T) {
	old := apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), nil))
	defer apikey.SetRuntime(old)
	i := NewAPIKeyInterceptor(false)
	rpm := 1
	store := &cacheSurfaceStore{surfaces: []string{"inference"}, rpm: &rpm}
	ctx := contextkeys.WithAPIKeyHashSecret(cacheContext(store), "test-secret")
	h := http.Header{}
	h.Set(common.EverstackApiKey, "key")
	for n := 0; n < 2; n++ {
		_, err := i.validateAPIKey(ctx, h, "/everstack.gateway.v1.GatewayService/ListModels", "")
		if n == 0 && err != nil || n == 1 && connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatal(err)
		}
	}
}

func TestConnectRateLimitInvalidation(t *testing.T) {
	first, second := 1, 2
	store := &cacheSurfaceStore{surfaces: []string{"inference"}, rpm: &first}
	ctx := cacheContext(store)
	i := NewAPIKeyInterceptor(false)
	_, _, _, rpm, ok, _ := i.validateCached(ctx, "key", "digest")
	if !ok || rpm == nil || *rpm != first {
		t.Fatal("initial limit missing")
	}
	store.rpm = &second
	apikey.InvalidateCaches("", "cache-test-key")
	_, _, _, rpm, ok, _ = i.validateCached(ctx, "key", "digest")
	if !ok || rpm == nil || *rpm != second {
		t.Fatal("rate change stuck in cache")
	}
}
