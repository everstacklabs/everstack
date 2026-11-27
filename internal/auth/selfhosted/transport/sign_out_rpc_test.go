package transport

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/jmoiron/sqlx"
)

// The admin app signs a self-hosted user out through the Connect SignOut
// RPC. It used to answer Unimplemented, so the session stayed valid and the
// browser kept its cookie.

func signOutRPCHandler(t *testing.T) (*SelfHostedAuthHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{
		CookieName: "everstack_session",
		HTTPOnly:   true,
		Secure:     true,
		SameSite:   "lax",
	}}
	sessions := repository.NewSessionRepository(sqlx.NewDb(db, "sqlmock"))
	svc := service.NewSelfHostedAuthService(cfg, nil, sessions, nil, nil, nil, nil, nil)
	return NewSelfHostedAuthHandler(cfg, svc, 0, nil), mock
}

func signOutRequest(t *testing.T, cookie string) (context.Context, *connect.Request[authv1.SignOutRequest]) {
	ctx, header := contextFor(t, "https://gateway.example.com/everstack.auth.v1.AuthService/SignOut", "")
	req := connect.NewRequest(&authv1.SignOutRequest{})
	for k, v := range header {
		req.Header()[k] = v
	}
	if cookie != "" {
		req.Header().Set("Cookie", cookie)
	}
	return ctx, req
}

func TestSignOutRPCRevokesSessionAndClearsCookie(t *testing.T) {
	h, mock := signOutRPCHandler(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.sessions WHERE token = $1")).
		WithArgs("live-token").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, req := signOutRequest(t, "everstack_session=live-token")
	resp, err := h.SignOut(ctx, req)
	if err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatal("SignOut success = false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("session was not revoked server-side: %v", err)
	}

	var cleared, marked bool
	for _, c := range (&http.Response{Header: resp.Header()}).Cookies() {
		switch c.Name {
		case "everstack_session":
			cleared = c.Value == "" && c.MaxAge < 0 && c.HttpOnly
		case domain.InstanceSignedOutCookie:
			marked = true
		}
	}
	if !cleared {
		t.Fatalf("session cookie not expired: %v", resp.Header().Values("Set-Cookie"))
	}
	// The marker would make the gateway ignore the session cookie the next
	// Connect Login sets, locking a self-hosted user out.
	if marked {
		t.Fatalf("SignOut set the instance signed-out marker: %v", resp.Header().Values("Set-Cookie"))
	}
}

// If the session row cannot be deleted the RPC must fail, so the UI keeps
// the user signed in and says so instead of reporting a false success.
func TestSignOutRPCFailsLoudlyWhenRevocationFails(t *testing.T) {
	h, mock := signOutRPCHandler(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.sessions WHERE token = $1")).
		WithArgs("live-token").
		WillReturnError(errors.New("connection refused"))

	ctx, req := signOutRequest(t, "everstack_session=live-token")
	resp, err := h.SignOut(ctx, req)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("SignOut err = %v, want Internal", err)
	}
	if resp != nil && resp.Msg.GetSuccess() {
		t.Fatal("SignOut reported success after a failed revocation")
	}
}

// With no session cookie there is nothing to revoke; the RPC still succeeds
// and expires any stale cookie.
func TestSignOutRPCWithoutSessionStillClearsCookie(t *testing.T) {
	h, mock := signOutRPCHandler(t)

	ctx, req := signOutRequest(t, "")
	resp, err := h.SignOut(ctx, req)
	if err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	var cleared bool
	for _, c := range (&http.Response{Header: resp.Header()}).Cookies() {
		if c.Name == "everstack_session" && c.Value == "" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("session cookie not expired: %v", resp.Header().Values("Set-Cookie"))
	}
}
