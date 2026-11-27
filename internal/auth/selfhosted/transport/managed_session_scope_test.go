package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// managedSessionContext is what cookieSessionAuthMiddleware installs after it
// verified the user's membership of the host instance's organization in the
// platform DB.
func managedSessionContext(userID uuid.UUID, org uuid.UUID, slug, role string) context.Context {
	ctx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID:       "instance-1",
		OrganizationID:   org.String(),
		OrganizationSlug: slug,
	})
	ctx = contextkeys.WithCloudUserID(ctx, userID.String())
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return contextkeys.WithUserRole(ctx, role)
}

// No gateway-local membership row (logins no longer upsert one): the
// dashboard must still see the platform role for the host org, not "member".
func TestManagedSessionWithoutLocalRowGetsPlatformRole(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member", "viewer"} {
		t.Run(role, func(t *testing.T) {
			env := newExchangeTestEnv(t, true)
			userID := uuid.New()
			env.mock.ExpectQuery(`SELECT \* FROM everstack\.users WHERE id = \$1`).
				WithArgs(userID).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))

			user := env.handler.resolveSessionUser(managedSessionContext(userID, exchangeOrgA, "acme", role), http.Header{})
			if user == nil {
				t.Fatal("no session user")
			}
			want := domain.OrganizationMembership{ID: exchangeOrgA, Slug: "acme", Name: "acme", Role: role}
			if len(user.Organizations) != 1 || user.Organizations[0] != want {
				t.Fatalf("organizations = %+v, want [%+v]", user.Organizations, want)
			}
		})
	}
}

// A stale gateway-local owner row in another org must neither appear nor be
// organizations[0]; the session is the host org with the platform role.
func TestManagedSessionIgnoresStaleLocalMemberships(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	userID := uuid.New()
	now := time.Now()
	env.mock.ExpectQuery(`SELECT \* FROM everstack\.users WHERE id = \$1`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "external_id", "email", "name", "avatar_url", "created_at", "updated_at"}).
			AddRow(userID, "ext-dave", "dave@example.com", nil, nil, now, now))
	env.mock.ExpectQuery(`SELECT o\.id, o\.slug, o\.name, m\.role`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "name", "role"}).
			AddRow(exchangeOrgB, "globex", "Globex", "owner").
			AddRow(exchangeOrgA, "acme", "Acme Inc", "owner"))

	user := env.handler.resolveSessionUser(managedSessionContext(userID, exchangeOrgA, "acme", "member"), http.Header{})
	if user == nil {
		t.Fatal("no session user")
	}
	want := domain.OrganizationMembership{ID: exchangeOrgA, Slug: "acme", Name: "Acme Inc", Role: "member"}
	if len(user.Organizations) != 1 || user.Organizations[0] != want {
		t.Fatalf("organizations = %+v, want [%+v]", user.Organizations, want)
	}
}

// Self-hosted gateways keep their local memberships.
func TestSelfHostedSessionKeepsLocalMemberships(t *testing.T) {
	user := &domain.UserWithOrganizations{Organizations: []domain.OrganizationMembership{{ID: exchangeOrgB, Role: "owner"}}}
	newExchangeTestEnv(t, false)
	got := scopeManagedSessionToHostOrganization(managedSessionContext(uuid.New(), exchangeOrgA, "acme", "member"), user)
	if len(got.Organizations) != 1 || got.Organizations[0].ID != exchangeOrgB || got.Organizations[0].Role != "owner" {
		t.Fatalf("organizations = %+v, want local memberships unchanged", got.Organizations)
	}
}

// Unauthenticated host scope (the middleware resolves one for the pre-auth
// exchange paths) must not rewrite anything.
func TestManagedSessionScopeRequiresAuthenticatedRequest(t *testing.T) {
	newExchangeTestEnv(t, true)
	ctx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-1", OrganizationID: exchangeOrgA.String(), OrganizationSlug: "acme",
	})
	user := &domain.UserWithOrganizations{Organizations: []domain.OrganizationMembership{{ID: exchangeOrgB, Role: "viewer"}}}
	if got := scopeManagedSessionToHostOrganization(ctx, user); got.Organizations[0].ID != exchangeOrgB {
		t.Fatalf("unauthenticated scope rewrote organizations: %+v", got.Organizations)
	}
}

// cloudCallbackEnv points the launch-code back-channel at a fake auth
// service that records the instance_host it was asked to redeem for.
func cloudCallbackEnv(t *testing.T, org uuid.UUID) (*exchangeTestEnv, *[]string) {
	t.Helper()
	env := newExchangeTestEnv(t, true)
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		hosts = append(hosts, body["instance_host"])
		_ = json.NewEncoder(w).Encode(launchCodeExchangeResponse{
			UserID: "ext-erin", Email: "erin@example.com", OrganizationID: org.String(), OrganizationSlug: "slug", CodeID: "c1",
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("EVS_AUTH_SERVICE_URL", srv.URL)
	return env, &hosts
}

func cloudCallback(env *exchangeTestEnv, host string, scopeOrg *uuid.UUID, forwardedHost string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "https://"+host+"/auth/cloud-callback?code=abc&next=/agents", nil)
	if forwardedHost != "" {
		req.Header.Set("X-Forwarded-Host", forwardedHost)
	}
	if scopeOrg != nil {
		req = req.WithContext(contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
			InstanceID: "instance-1", OrganizationID: scopeOrg.String(), OrganizationSlug: "slug",
		}))
	}
	rec := httptest.NewRecorder()
	env.handler.handleCloudCallback(rec, req)
	return rec
}

// Managed gateway: a callback on a host that is not one of our instances is
// refused before the code is redeemed; a code whose organization does not own
// the host instance is refused; the host sent to the cloud is the normalized
// request host, never X-Forwarded-Host.
func TestCloudCallbackBoundToHostInstance(t *testing.T) {
	t.Run("unresolved host", func(t *testing.T) {
		env, hosts := cloudCallbackEnv(t, exchangeOrgA)
		rec := cloudCallback(env, "evil.example", nil, "")
		if !locationHas(rec, "error=invalid_code") || len(*hosts) != 0 {
			t.Fatalf("location=%q redemptions=%v, want invalid_code without redemption", rec.Header().Get("Location"), *hosts)
		}
	})
	t.Run("organization mismatch", func(t *testing.T) {
		env, _ := cloudCallbackEnv(t, exchangeOrgA)
		orgB := exchangeOrgB
		rec := cloudCallback(env, exchangeHostB, &orgB, "")
		if !locationHas(rec, "error=invalid_code") {
			t.Fatalf("location=%q, want invalid_code", rec.Header().Get("Location"))
		}
		if stmts := env.statements(); len(stmts) != 0 {
			t.Fatalf("refused callback touched the database: %v", stmts)
		}
	})
	t.Run("host sent to cloud ignores X-Forwarded-Host", func(t *testing.T) {
		env, hosts := cloudCallbackEnv(t, exchangeOrgA)
		orgA := exchangeOrgA
		cloudCallback(env, "ACME-a1b2c3.eu-gra-1.everstack.ai:443", &orgA, exchangeHostB)
		if len(*hosts) != 1 || (*hosts)[0] != exchangeHostA {
			t.Fatalf("instance_host sent = %v, want [%s]", *hosts, exchangeHostA)
		}
	})
	t.Run("matching instance signs in", func(t *testing.T) {
		env, _ := cloudCallbackEnv(t, exchangeOrgA)
		env.expectLogin(uuid.New(), "ext-erin")
		env.mock.ExpectExec(`UPDATE auth_config`).WillReturnResult(sqlmock.NewResult(0, 0))
		orgA := exchangeOrgA
		rec := cloudCallback(env, exchangeHostA, &orgA, "")
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/agents" {
			t.Fatalf("status=%d location=%q, want redirect to /agents", rec.Code, rec.Header().Get("Location"))
		}
		for _, s := range env.statements() {
			if strings.Contains(s, "organization_members") || strings.Contains(s, "cloud_organization_id") {
				t.Errorf("managed callback touched shared tenant state: %s", s)
			}
		}
	})
}
