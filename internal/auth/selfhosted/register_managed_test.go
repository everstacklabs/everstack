package selfhosted

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/enterprise"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
)

func withManagedGateway(t *testing.T, managed bool) {
	t.Helper()
	prev := enterprise.ManagedGateway()
	enterprise.SetManagedGateway(managed)
	t.Cleanup(func() { enterprise.SetManagedGateway(prev) })
}

// Register makes the caller the owner of a new "default" organization when
// the gateway database holds no users. On a managed gateway that database is
// shared by every tenant and Cloud users arrive through the identity flow, so
// an anonymous caller who reached Register on a fresh or emptied managed
// gateway would have become its first admin. It must be refused before any
// read or write, over both the Connect procedure and POST /auth/register.
func TestRegisterIsRefusedOnManagedGateway(t *testing.T) {
	withManagedGateway(t, true)

	server, mock := newTeamScopeServer(t)
	req := connect.NewRequest(&authv1.RegisterRequest{Email: "first@example.com", Password: "Str0ng!Passw0rd-x"})
	_, err := server.handler.Register(context.Background(), req)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Connect Register on a managed gateway: err = %v, want FailedPrecondition", err)
	}

	router := mux.NewRouter()
	server.RegisterHTTPRoutes(router)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"email":"first@example.com","password":"Str0ng!Passw0rd-x"}`))
	router.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /auth/register on a managed gateway: status = %d (%s), want 403", rec.Code, rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Self-hosted first-run setup still reaches the user check.
func TestRegisterStillRunsFirstUserSetupWhenSelfHosted(t *testing.T) {
	withManagedGateway(t, false)

	server, mock := newTeamScopeServer(t)
	mock.ExpectQuery(regexp.QuoteMeta("auth_config")).WillReturnError(errors.New("boom"))
	req := connect.NewRequest(&authv1.RegisterRequest{Email: "first@example.com", Password: "Str0ng!Passw0rd-x"})
	_, err := server.handler.Register(context.Background(), req)
	if connect.CodeOf(err) == connect.CodeFailedPrecondition {
		t.Fatalf("self-hosted Register refused as managed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("self-hosted Register never reached the first-user check: %v", err)
	}
}
