package transport

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
)

type capturingMailer struct {
	mu    sync.Mutex
	links []string
}

func (m *capturingMailer) SendMagicLink(_ context.Context, _, link, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links = append(m.links, link)
	return nil
}

func (m *capturingMailer) SendInvitation(context.Context, string, string, string, string, string, string, time.Time) error {
	return nil
}

// The self-hosted magic-link email pointed at /auth/verify-magic-link, which
// nothing serves, and VerifyMagicLink created a session without ever handing
// it to the browser. Following the emailed link must now sign the user in:
// the link opens the admin app's /verify-magic-link page with the token in
// the fragment (never sent to a server or a Referer), and the verification
// call that page makes sets the session cookie. Real Postgres; requires
// EVS_SESSION_TEST_DSN.
func TestMagicLinkEmailSignsTheUserIn(t *testing.T) {
	e := sessionlivetest.NewCE(t)
	ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/organizations_billing_columns_20260501234812/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	e.Exec(t, `UPDATE everstack.users SET external_id = 'local:' || id::text WHERE id = $1`, e.UserID)
	orgID := uuid.New()
	e.Exec(t, `INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, 'default', 'Default Organization')`, orgID)
	e.Exec(t, `INSERT INTO everstack.organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, e.UserID)
	email := e.UserID.String() + "@example.test"

	cfg := &domain.InternalConfig{Session: domain.SessionConfig{CookieName: "everstack_session", MaxAge: time.Hour, HTTPOnly: true, SameSite: "lax"}}
	mailer := &capturingMailer{}
	svc := service.NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(e.DB), repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB), repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB), repository.NewOrganizationRepository(e.DB), nil,
	).WithEmail(mailer, "https://gateway.example.test/")
	h := NewSelfHostedAuthHandler(cfg, svc, 0, nil)
	ctx := context.Background()

	if _, err := h.RequestMagicLink(ctx, connect.NewRequest(&authv1.RequestMagicLinkRequest{Email: email})); err != nil {
		t.Fatalf("RequestMagicLink: %v", err)
	}
	if len(mailer.links) != 1 {
		t.Fatalf("mailed %d links, want 1", len(mailer.links))
	}
	link, err := url.Parse(mailer.links[0])
	if err != nil {
		t.Fatalf("mailed link %q does not parse: %v", mailer.links[0], err)
	}
	if link.Scheme != "https" || link.Host != "gateway.example.test" || link.Path != "/verify-magic-link" {
		t.Fatalf("mailed link goes to %s://%s%s, want https://gateway.example.test/verify-magic-link (the admin route that verifies it)", link.Scheme, link.Host, link.Path)
	}
	if link.RawQuery != "" {
		t.Fatalf("mailed link carries a query %q; the token belongs in the fragment so it never reaches server logs", link.RawQuery)
	}
	fragment, err := url.ParseQuery(link.Fragment)
	if err != nil {
		t.Fatalf("fragment %q does not parse: %v", link.Fragment, err)
	}
	token := fragment.Get("token")
	if token == "" {
		t.Fatalf("mailed link has no token in its fragment: %q", link.Fragment)
	}

	resp, err := h.VerifyMagicLink(ctx, connect.NewRequest(&authv1.VerifyMagicLinkRequest{Token: token}))
	if err != nil {
		t.Fatalf("VerifyMagicLink: %v", err)
	}
	if resp.Msg.GetSessionToken() != "" {
		t.Fatal("VerifyMagicLink put the session token in the response body")
	}
	var sessionCookie *http.Cookie
	for _, c := range (&http.Response{Header: http.Header{"Set-Cookie": resp.Header().Values("Set-Cookie")}}).Cookies() {
		if c.Name == "everstack_session" && c.Value != "" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("VerifyMagicLink set no session cookie (Set-Cookie: %q), so the browser stays signed out", resp.Header().Values("Set-Cookie"))
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}

	sessionReq := connect.NewRequest(&authv1.GetSessionRequest{})
	sessionReq.Header().Set("Cookie", "everstack_session="+sessionCookie.Value)
	session, err := h.GetSession(ctx, sessionReq)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !session.Msg.GetAuthenticated() || !strings.EqualFold(session.Msg.GetUser().GetUser().GetEmail(), email) {
		t.Fatalf("the cookie from VerifyMagicLink does not sign %s in: %+v", email, session.Msg)
	}

	// The link is single-use.
	if _, err := h.VerifyMagicLink(ctx, connect.NewRequest(&authv1.VerifyMagicLinkRequest{Token: token})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("second VerifyMagicLink error = %v, want Unauthenticated", err)
	}
}
