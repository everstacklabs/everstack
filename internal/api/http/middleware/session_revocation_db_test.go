package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// The HTTP API-key interceptor's session-cookie path must refuse a cookie
// whose Better Auth session is gone. A positive answer is cached for at most
// sessionlive.MaxCacheTTL (not the 60s API-key cacheTTL); a store failure
// denies with 503 and is never cached. Real Postgres; requires
// EVS_SESSION_TEST_DSN.
func TestAPIKeyInterceptor_IdentitySessionRevocation(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	e.AddOrgMembership(t, uuid.New(), "admin")
	i := NewAPIKeyInterceptorWithSessionDB(false, e.DB)

	call := func(ctx context.Context, token string) (int, bool) {
		var reached bool
		h := i.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = contextkeys.IsTenantAuthenticated(r.Context())
		}))
		req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/agents", nil).WithContext(ctx)
		req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, reached
	}
	ctx := context.Background()

	id, token := e.LinkedSession(t, time.Hour)
	if code, reached := call(ctx, token); code != http.StatusOK || !reached {
		t.Fatalf("live session: code=%d reached=%v", code, reached)
	}
	cached, ok := i.sessionCache.Load(token)
	if !ok {
		t.Fatal("live session answer was not cached")
	}
	if ttl := time.Until(cached.(sessionAuth).expiresAt); ttl > sessionlive.MaxCacheTTL {
		t.Fatalf("session cached for %v, must be <= %v", ttl, sessionlive.MaxCacheTTL)
	}

	e.RevokeIdentitySession(t, id)
	// Within the TTL the cached answer may still be served; once it lapses
	// the revoked session is refused.
	i.sessionCache.Range(func(k, _ any) bool { i.sessionCache.Delete(k); return true })
	if code, reached := call(ctx, token); code != http.StatusUnauthorized || reached {
		t.Fatalf("signed out: code=%d reached=%v, want 401", code, reached)
	}

	id, token = e.LinkedSession(t, time.Hour)
	e.ExpireIdentitySession(t, id)
	if code, reached := call(ctx, token); code != http.StatusUnauthorized || reached {
		t.Fatalf("identity expired: code=%d reached=%v, want 401", code, reached)
	}

	if code, reached := call(ctx, e.LegacySession(t)); code != http.StatusOK || !reached {
		t.Fatalf("legacy session: code=%d reached=%v", code, reached)
	}

	t.Run("identity unreachable fails closed and is not cached", func(t *testing.T) {
		_, token := e.LinkedSession(t, time.Hour)
		restore := e.BreakIdentity(t)
		if code, reached := call(ctx, token); code != http.StatusServiceUnavailable || reached {
			t.Fatalf("identity unreachable: code=%d reached=%v, want 503", code, reached)
		}
		if _, ok := i.sessionCache.Load(token); ok {
			t.Fatal("an Unavailable answer must not be cached")
		}
		restore()
		if code, reached := call(ctx, token); code != http.StatusOK || !reached {
			t.Fatalf("after recovery: code=%d reached=%v", code, reached)
		}
	})

	t.Run("cookie middleware reported the store unavailable", func(t *testing.T) {
		flagged := contextkeys.WithSessionUnavailable(contextkeys.WithTenantAuthRejected(ctx))
		if code, reached := call(flagged, "any"); code != http.StatusServiceUnavailable || reached {
			t.Fatalf("flagged unavailable: code=%d reached=%v, want 503", code, reached)
		}
	})
}
