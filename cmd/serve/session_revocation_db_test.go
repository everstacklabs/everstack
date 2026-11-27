package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
)

type cookieAuthObservation struct {
	cloudUserID        string
	authenticated      bool
	rejected           bool
	sessionUnavailable bool
}

// The gateway's cookie middleware must give no identity to a cookie whose
// Better Auth session is gone, although its everstack.sessions mirror row is
// unexpired, and must mark the request rejected so the API-key interceptors
// do not re-authenticate the same cookie. When identity data is unreachable
// the request is denied and flagged unavailable (the interceptors answer
// 503). Real Postgres; requires EVS_SESSION_TEST_DSN.
func TestCookieSessionAuth_IdentitySessionRevocation(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	e.AddOrgMembership(t, uuid.New(), "admin")
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return e.DB })

	call := func(token string) cookieAuthObservation {
		var obs cookieAuthObservation
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			obs = cookieAuthObservation{
				cloudUserID:        contextkeys.CloudUserIDFromContext(ctx),
				authenticated:      contextkeys.IsTenantAuthenticated(ctx),
				rejected:           contextkeys.IsTenantAuthRejected(ctx),
				sessionUnavailable: contextkeys.IsSessionUnavailable(ctx),
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "http://localhost/everstack.agents.v1.AgentService/ListAgents", nil)
		req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: token})
		h.ServeHTTP(httptest.NewRecorder(), req)
		return obs
	}
	denied := func(t *testing.T, label string, obs cookieAuthObservation, wantUnavailable bool) {
		t.Helper()
		if obs.cloudUserID != "" || obs.authenticated || !obs.rejected || obs.sessionUnavailable != wantUnavailable {
			t.Fatalf("%s: got %+v, want rejected with no identity (unavailable=%v)", label, obs, wantUnavailable)
		}
	}

	id, token := e.LinkedSession(t, time.Hour)
	if obs := call(token); obs.cloudUserID != e.UserID.String() || !obs.authenticated || obs.rejected {
		t.Fatalf("live session: got %+v", obs)
	}
	e.RevokeIdentitySession(t, id)
	denied(t, "signed out", call(token), false)

	id, token = e.LinkedSession(t, time.Hour)
	e.ExpireIdentitySession(t, id)
	denied(t, "identity expired", call(token), false)

	if obs := call(e.LegacySession(t)); obs.cloudUserID != e.UserID.String() || !obs.authenticated {
		t.Fatalf("legacy session: got %+v", obs)
	}

	_, token = e.LinkedSession(t, time.Hour)
	restore := e.BreakIdentity(t)
	defer restore()
	denied(t, "identity unreachable", call(token), true)
}

// The paused-tenant refusal names the org's billing page only to a verified
// member; a cookie whose Better Auth session is gone must not count, and an
// unreachable identity store means "not verified". Real Postgres; requires
// EVS_SESSION_TEST_DSN.
func TestPausedTenantViewerCheck_IdentitySessionRevocation(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	orgID := uuid.New()
	e.AddOrgMembership(t, orgID, "member")
	check := pausedTenantViewerCheck(func() *sqlx.DB { return e.DB })
	tenant := tenantstatus.Tenant{InstanceID: "instance-1", OrganizationID: orgID.String()}
	viewer := func(token string) bool {
		r := httptest.NewRequest(http.MethodGet, "https://acme-a1b2c3.eu-gra-1.everstack.ai/", nil)
		r.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: token})
		return check(r, tenant)
	}

	id, token := e.LinkedSession(t, time.Hour)
	if !viewer(token) {
		t.Fatal("live member session must be a verified viewer")
	}
	if viewer(e.LegacySession(t)) != true {
		t.Fatal("legacy member session must be a verified viewer")
	}
	e.RevokeIdentitySession(t, id)
	if viewer(token) {
		t.Fatal("signed-out session must not be a verified viewer")
	}
	id, token = e.LinkedSession(t, time.Hour)
	e.ExpireIdentitySession(t, id)
	if viewer(token) {
		t.Fatal("expired identity session must not be a verified viewer")
	}
	_, token = e.LinkedSession(t, time.Hour)
	restore := e.BreakIdentity(t)
	defer restore()
	if viewer(token) {
		t.Fatal("unverifiable session must not be a verified viewer")
	}
}
