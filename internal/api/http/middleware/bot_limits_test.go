package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	keyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestInferenceBotLimitColdAndWarm(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		old := apikey.SetRuntime(apikey.NewRuntime(authratelimit.NewMemoryStore(), nil))
		rpm := 1
		surfaces := []string{"inference"}
		if ordinary {
			surfaces = nil
		}
		store := &oneKeyStore{key: tenantAKey, read: keyq.APIKeyReadModel{ID: "key", OrgID: strp(tenantA), Surfaces: surfaces, RateLimitRPM: &rpm}}
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
		i := NewAPIKeyInterceptor(false)
		h := i.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ordinary && !apikey.Processed(r.Context(), "key", apikey.SurfaceInference) {
				t.Error("missing hop marker")
			}
			w.WriteHeader(204)
		}))
		for n := 0; n < 2; n++ {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			r.Header.Set(common.EverstackApiKey, tenantAKey)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r.WithContext(ctx))
			want := 204
			if n == 1 && !ordinary {
				want = 429
			}
			if w.Code != want {
				t.Fatalf("ordinary=%v n=%d: %d %s", ordinary, n, w.Code, w.Body)
			}
			if want == 429 && (w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"code":"rate_limit_exceeded"`)) {
				t.Fatalf("bad inference error: %v %s", w.Header(), w.Body)
			}
		}
		if !ordinary {
			raised := 3
			store.read.RateLimitRPM = &raised
			apikey.InvalidateCaches("", "key")
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			r.Header.Set(common.EverstackApiKey, tenantAKey)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r.WithContext(ctx))
			if w.Code != 204 {
				t.Fatalf("limit change stuck in cache: %d %s", w.Code, w.Body)
			}
		}
		apikey.SetRuntime(old)
	}
}
