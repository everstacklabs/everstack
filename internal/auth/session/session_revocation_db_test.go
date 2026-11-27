package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
)

const revocationProcedure = "/everstack.license.v1.LicenseService/ReleaseInstance"

// The license service's session interceptor (ReleaseInstance, instance
// listing, spend limits) must not authenticate a cookie whose Better Auth
// session is gone, although its mirror row is unexpired. With
// FallbackToNext (production) a rejected cookie falls through to M2M with
// no session claims; a store failure is Unavailable and never falls
// through. Real Postgres; requires EVS_SESSION_TEST_DSN.
func TestInterceptor_IdentitySessionRevocation(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)

	for _, fallback := range []bool{true, false} {
		name := "fallback"
		if !fallback {
			name = "no fallback"
		}
		t.Run(name, func(t *testing.T) {
			var reached bool
			var claims *SessionClaims
			interceptor := NewInterceptor(&InterceptorConfig{
				DB:                   e.DB,
				CookieName:           "es_everstack_session",
				SessionAuthEndpoints: []string{revocationProcedure},
				FallbackToNext:       fallback,
			})
			mux := http.NewServeMux()
			mux.Handle(revocationProcedure, connect.NewUnaryHandler(revocationProcedure,
				func(ctx context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
					reached = true
					claims = ClaimsFromContext(ctx)
					return connect.NewResponse(&emptypb.Empty{}), nil
				}, connect.WithInterceptors(interceptor)))
			srv := httptest.NewServer(mux)
			defer srv.Close()
			client := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+revocationProcedure)

			call := func(token string) error {
				reached, claims = false, nil
				req := connect.NewRequest(&emptypb.Empty{})
				req.Header().Set("Cookie", "es_everstack_session="+token)
				_, err := client.CallUnary(context.Background(), req)
				return err
			}
			// rejected asserts the cookie conferred no session identity.
			rejected := func(t *testing.T, label string, err error) {
				t.Helper()
				if fallback {
					if err != nil || !reached || claims != nil {
						t.Fatalf("%s: err=%v reached=%v claims=%+v, want fall-through without claims", label, err, reached, claims)
					}
					return
				}
				if connect.CodeOf(err) != connect.CodeUnauthenticated || reached {
					t.Fatalf("%s: err=%v reached=%v, want Unauthenticated", label, err, reached)
				}
			}

			id, token := e.LinkedSession(t, time.Hour)
			if err := call(token); err != nil || claims == nil || claims.UserID != e.UserID.String() || claims.SessionID == "" {
				t.Fatalf("live session: err=%v claims=%+v", err, claims)
			}
			e.RevokeIdentitySession(t, id)
			rejected(t, "signed out", call(token))

			id, token = e.LinkedSession(t, time.Hour)
			e.ExpireIdentitySession(t, id)
			rejected(t, "identity expired", call(token))

			if err := call(e.LegacySession(t)); err != nil || claims == nil {
				t.Fatalf("legacy session: err=%v claims=%+v", err, claims)
			}

			_, token = e.LinkedSession(t, time.Hour)
			restore := e.BreakIdentity(t)
			defer restore()
			if err := call(token); connect.CodeOf(err) != connect.CodeUnavailable || reached {
				t.Fatalf("identity unreachable: err=%v reached=%v, want Unavailable", err, reached)
			}
		})
	}
}
