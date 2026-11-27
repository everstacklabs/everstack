package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

const revocationProc = "/everstack.agents.v1.AgentService/ListAgents"

func cookieHeader(token string) http.Header {
	h := http.Header{}
	h.Set("Cookie", "es_everstack_session="+token)
	return h
}

func wantConnectCode(t *testing.T, label string, err error, want connect.Code) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != want {
		t.Fatalf("%s: err = %v, want %v", label, err, want)
	}
}

// The Connect API-key interceptor's session-cookie path must refuse a
// cookie whose Better Auth session is gone. A positive answer may be cached,
// but never for longer than sessionlive.MaxCacheTTL, so a revoked session
// stops working within that bound; a store failure is Unavailable and is
// never cached. Real Postgres; requires EVS_SESSION_TEST_DSN.
func TestAPIKeyConnectInterceptor_IdentitySessionRevocation(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	e.AddOrgMembership(t, uuid.New(), "admin")
	i := NewAPIKeyInterceptorWithSessionDB(false, nil, e.DB)
	ctx := context.Background()
	call := func(token string) (context.Context, error) {
		return i.validateAPIKey(ctx, cookieHeader(token), revocationProc, "corr")
	}

	id, token := e.LinkedSession(t, time.Hour)
	got, err := call(token)
	if err != nil || !contextkeys.IsTenantAuthenticated(got) {
		t.Fatalf("live session: err=%v authenticated=%v", err, contextkeys.IsTenantAuthenticated(got))
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
	expireSessionCache(i)
	_, err = call(token)
	wantConnectCode(t, "signed out", err, connect.CodeUnauthenticated)

	id, token = e.LinkedSession(t, time.Hour)
	e.ExpireIdentitySession(t, id)
	_, err = call(token)
	wantConnectCode(t, "identity expired", err, connect.CodeUnauthenticated)

	if got, err := call(e.LegacySession(t)); err != nil || !contextkeys.IsTenantAuthenticated(got) {
		t.Fatalf("legacy session: err=%v", err)
	}

	t.Run("identity unreachable fails closed and is not cached", func(t *testing.T) {
		_, token := e.LinkedSession(t, time.Hour)
		restore := e.BreakIdentity(t)
		_, err := call(token)
		wantConnectCode(t, "identity unreachable", err, connect.CodeUnavailable)
		if _, ok := i.sessionCache.Load(token); ok {
			t.Fatal("an Unavailable answer must not be cached")
		}
		restore()
		if _, err := call(token); err != nil {
			t.Fatalf("after recovery the live session must work at once: %v", err)
		}
	})

	t.Run("cookie middleware reported the store unavailable", func(t *testing.T) {
		flagged := contextkeys.WithSessionUnavailable(contextkeys.WithTenantAuthRejected(ctx))
		_, err := i.validateAPIKey(flagged, cookieHeader("any"), revocationProc, "corr")
		wantConnectCode(t, "flagged unavailable", err, connect.CodeUnavailable)
	})
}

func expireSessionCache(i *APIKeyConnectInterceptor) {
	i.sessionCache.Range(func(k, _ any) bool {
		i.sessionCache.Delete(k)
		return true
	})
}
