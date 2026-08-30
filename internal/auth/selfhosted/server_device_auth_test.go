package selfhosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
)

func TestCreateServerSupportsDeviceAuthorization(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO device_authorization_sessions")).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "last_polled_at"}).
			AddRow(time.Now().UTC(), time.Now().UTC()))

	deviceTokens, err := deviceauth.NewTokenManager(
		[]byte(strings.Repeat("m", 32)),
		90*24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	server, err := CreateServer(sqlx.NewDb(db, "sqlmock"), &Config{
		SessionSecret:     "different-session-secret-at-least-32-bytes",
		SessionCookieName: "everstack_session",
		SessionMaxAge:     3600,
		DeviceTokens:      deviceTokens,
		ExternalURL:       "http://localhost:8089",
	}, 0)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	req := connect.NewRequest(&authv1.CreateDeviceAuthorizationRequest{
		ClientId: "evs-cli",
		Scope:    "cli:full",
	})
	req.Header().Set("X-Forwarded-Proto", "https")
	req.Header().Set("X-Forwarded-Host", "instance.example.com")

	resp, err := server.handler.CreateDeviceAuthorization(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateDeviceAuthorization() error = %v", err)
	}
	if resp.Msg.GetDeviceCode() == "" {
		t.Error("CreateDeviceAuthorization().DeviceCode is empty")
	}
	if matched, _ := regexp.MatchString(`^[A-Z]{4}-[A-Z]{4}$`, resp.Msg.GetUserCode()); !matched {
		t.Errorf("CreateDeviceAuthorization().UserCode = %q, want XXXX-XXXX", resp.Msg.GetUserCode())
	}
	if got, want := resp.Msg.GetVerificationUri(), "https://instance.example.com/device"; got != want {
		t.Errorf("CreateDeviceAuthorization().VerificationUri = %q, want %q", got, want)
	}
	if resp.Msg.GetExpiresIn() <= 0 {
		t.Errorf("CreateDeviceAuthorization().ExpiresIn = %d, want positive", resp.Msg.GetExpiresIn())
	}
	if resp.Msg.GetInterval() <= 0 {
		t.Errorf("CreateDeviceAuthorization().Interval = %d, want positive", resp.Msg.GetInterval())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations not met: %v", err)
	}
}

func TestCreateServerRegistersOAuthAuthorizationMetadata(t *testing.T) {
	t.Parallel()

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	deviceTokens, err := deviceauth.NewTokenManager(
		[]byte(strings.Repeat("m", 32)),
		90*24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	server, err := CreateServer(sqlx.NewDb(db, "sqlmock"), &Config{
		SessionSecret:     "different-session-secret-at-least-32-bytes",
		SessionCookieName: "everstack_session",
		SessionMaxAge:     3600,
		DeviceTokens:      deviceTokens,
	}, 0)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	router := mux.NewRouter()
	server.RegisterHTTPRoutes(router)
	req := httptest.NewRequest(http.MethodGet, "https://instance.example.com/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var metadata map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata["issuer"] != "https://instance.example.com" {
		t.Fatalf("issuer = %v", metadata["issuer"])
	}
	if metadata["authorization_endpoint"] != "https://instance.example.com/oauth/authorize" {
		t.Fatalf("authorization_endpoint = %v", metadata["authorization_endpoint"])
	}
	if metadata["revocation_endpoint"] != "https://instance.example.com/oauth/revoke" {
		t.Fatalf("revocation_endpoint = %v", metadata["revocation_endpoint"])
	}
}
