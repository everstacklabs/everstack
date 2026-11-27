package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/everstacklabs/everstack/pkg/oidc"
)

// A session whose user belongs to no organization (a member removed while a
// session survived) must not resolve to a signed-in user, and resolving it
// must never create a membership. GetSession used to make such a user the
// owner of the instance's organization. Real Postgres; requires
// EVS_SESSION_TEST_DSN.
func TestOrglessSessionIsNotSignedInAndGainsNoMembership(t *testing.T) {
	e := sessionlivetest.NewCE(t)
	ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/organizations_billing_columns_20260501234812/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	ctx := context.Background()

	e.Exec(t, `UPDATE everstack.users SET external_id = 'local:' || id::text WHERE id = $1`, e.UserID)

	// The instance has an organization with an owner; e.UserID is not in it.
	orgID, ownerID := uuid.New(), uuid.New()
	e.Exec(t, `INSERT INTO everstack.users (id, external_id, email) VALUES ($1, $2, 'owner@example.test')`, ownerID, "local:"+ownerID.String())
	e.Exec(t, `INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, 'default', 'Default Organization')`, orgID)
	e.Exec(t, `INSERT INTO everstack.organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, ownerID)
	e.Exec(t, `INSERT INTO everstack.sessions (user_id, token, expires_at) VALUES ($1, 'orphan-token', $2)`, e.UserID, time.Now().Add(time.Hour))

	cfg := &domain.InternalConfig{Session: domain.SessionConfig{CookieName: "everstack_session", MaxAge: time.Hour}}
	svc := service.NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(e.DB),
		repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB),
		repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB),
		repository.NewOrganizationRepository(e.DB),
		nil,
	)
	h := NewSelfHostedAuthHandler(cfg, svc, 0, nil)

	req := connect.NewRequest(&authv1.GetSessionRequest{})
	req.Header().Set("Cookie", "everstack_session=orphan-token")
	resp, err := h.GetSession(ctx, req)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if resp.Msg.GetAuthenticated() {
		t.Errorf("GetSession authenticated an organization-less session as %+v", resp.Msg.GetUser())
	}
	var n int
	if err := e.DB.Get(&n, `SELECT COUNT(*) FROM everstack.organization_members WHERE user_id = $1`, e.UserID); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("resolving the session created %d membership(s)", n)
	}
}

// An OIDC relying-party sign-in that names no organization must not mint a
// session on a self-hosted gateway for a user who belongs to none: that
// session would be a credential with nothing behind it. A member still gets
// one.
func TestOIDCSignInWithoutOrganizationMintsNoSession(t *testing.T) {
	e := sessionlivetest.NewCE(t)
	ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/organizations_billing_columns_20260501234812/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	e.Exec(t, `UPDATE everstack.users SET external_id = 'cloud-orgless' WHERE id = $1`, e.UserID)
	memberID, orgID := uuid.New(), uuid.New()
	e.Exec(t, `INSERT INTO everstack.users (id, external_id, email) VALUES ($1, 'cloud-member', 'member@example.test')`, memberID)
	e.Exec(t, `INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, 'default', 'Default Organization')`, orgID)
	e.Exec(t, `INSERT INTO everstack.organization_members (organization_id, user_id, role) VALUES ($1, $2, 'member')`, orgID, memberID)

	cfg := &domain.InternalConfig{Session: domain.SessionConfig{CookieName: "everstack_session", MaxAge: time.Hour}}
	svc := service.NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(e.DB), repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB), repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB), repository.NewOrganizationRepository(e.DB), nil,
	)
	m := &oidcSessionMinter{h: NewSelfHostedAuthHandler(cfg, svc, 0, nil)}
	sessions := func(id uuid.UUID) int {
		var n int
		if err := e.DB.Get(&n, `SELECT COUNT(*) FROM everstack.sessions WHERE user_id = $1`, id); err != nil {
			t.Fatal(err)
		}
		return n
	}

	orgless := &oidc.IDClaims{Email: e.UserID.String() + "@example.test"}
	orgless.Subject = "cloud-orgless"
	if err := m.Mint(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gw.example/auth/oidc/callback", nil), orgless); err == nil {
		t.Error("minted a session for a user with no organization")
	}
	if n := sessions(e.UserID); n != 0 {
		t.Fatalf("organization-less user has %d session(s)", n)
	}

	// A refused sign-in by someone the gateway has never seen creates no
	// account: otherwise two such sign-ins on a fresh instance would leave
	// users and no organization, and no one could ever register.
	var before, after int
	if err := e.DB.Get(&before, `SELECT COUNT(*) FROM everstack.users`); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"cloud-stranger-1", "cloud-stranger-2"} {
		stranger := &oidc.IDClaims{Email: sub + "@example.test"}
		stranger.Subject = sub
		if err := m.Mint(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gw.example/auth/oidc/callback", nil), stranger); err == nil {
			t.Errorf("%s: minted a session without an organization", sub)
		}
	}
	if err := e.DB.Get(&after, `SELECT COUNT(*) FROM everstack.users`); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused sign-ins created %d account(s)", after-before)
	}

	member := &oidc.IDClaims{Email: "member@example.test"}
	member.Subject = "cloud-member"
	if err := m.Mint(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gw.example/auth/oidc/callback", nil), member); err != nil {
		t.Fatalf("member sign-in: %v", err)
	}
	if n := sessions(memberID); n != 1 {
		t.Fatalf("member has %d session(s), want 1", n)
	}
}

// Every cloud sign-in path (exchange token, launch code, OIDC) admits the
// user through admitCloudSignIn before any session exists. With an
// organization the cloud vouched for, the user is attached with their cloud
// role; without one, an unknown user is refused before any account is
// created.
func TestAdmitCloudSignIn(t *testing.T) {
	ctx := context.Background()
	e := sessionlivetest.NewCE(t)
	for _, m := range []string{"organizations_billing_columns_20260501234812", "auth_config_cloud_connection_20260323150000"} {
		ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/" + m + "/up.sql")
		if err != nil {
			t.Fatal(err)
		}
		e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	}
	sep := "?"
	if strings.Contains(e.DSN, "?") {
		sep = "&"
	}
	cfgDB, err := sqlx.Open("pgx", e.DSN+sep+"search_path=everstack,public")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfgDB.Close() })
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{CookieName: "everstack_session", MaxAge: time.Hour}}
	svc := service.NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(e.DB), repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB), repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB), repository.NewOrganizationRepository(e.DB),
		repository.NewAuthConfigRepository(cfgDB),
	)
	h := NewSelfHostedAuthHandler(cfg, svc, 0, nil)
	users := func() int {
		var n int
		if err := e.DB.Get(&n, `SELECT COUNT(*) FROM everstack.users`); err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := users()
	if _, err := h.admitCloudSignIn(ctx, cloudSignIn{ExternalID: "cloud-x", Email: "x@example.test"}); !errors.Is(err, service.ErrNoOrganization) {
		t.Fatalf("no organization: err = %v, want ErrNoOrganization", err)
	}
	if users() != before {
		t.Fatal("refused cloud sign-in created an account")
	}

	orgID := uuid.New()
	user, err := h.admitCloudSignIn(ctx, cloudSignIn{
		ExternalID: "cloud-v", Email: "v@example.test",
		OrgID: orgID.String(), OrgSlug: "acme", OrgRole: "viewer",
	})
	if err != nil {
		t.Fatalf("vouched sign-in: %v", err)
	}
	var role string
	if err := e.DB.Get(&role, `SELECT role FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2`, orgID, user.ID); err != nil {
		t.Fatal(err)
	}
	if role != "viewer" {
		t.Fatalf("attached as %q, want viewer", role)
	}
}
