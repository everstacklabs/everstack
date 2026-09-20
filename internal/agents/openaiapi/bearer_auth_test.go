package openaiapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/jmoiron/sqlx"
)

const (
	bearerTestOrg      = "11111111-1111-1111-1111-111111111111"
	bearerTestInstance = "22222222-2222-2222-2222-222222222222"
)

func TestDeviceBearerResolvesVerifiedIdentityLink(t *testing.T) {
	db, mock := newBearerAuthMock(t)
	mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").
		WithArgs("external-subject", bearerTestOrg).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("JOIN everstack.identity_links il").
		WithArgs(bearerTestOrg, bearerTestInstance, "person@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("admin", "stable-user"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM everstack.tenant_config").
		WithArgs(bearerTestInstance, bearerTestOrg).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	status, reachedUser := serveBearer(t, db, deviceauth.Identity{UserID: "external-subject", Email: "person@example.com", OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance})
	if status != http.StatusNoContent || reachedUser != "stable-user" {
		t.Fatalf("status=%d downstream user=%q", status, reachedUser)
	}
}

func TestDeviceBearerDirectMemberStillWorks(t *testing.T) {
	db, mock := newBearerAuthMock(t)
	mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").
		WithArgs("stable-user", bearerTestOrg).
		WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("member", "stable-user"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM everstack.tenant_config").
		WithArgs(bearerTestInstance, bearerTestOrg).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	status, reachedUser := serveBearer(t, db, deviceauth.Identity{UserID: "stable-user", OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance})
	if status != http.StatusNoContent || reachedUser != "stable-user" {
		t.Fatalf("status=%d downstream user=%q", status, reachedUser)
	}
}

func TestDeviceBearerIdentityLinkDenialAndDatabaseFailure(t *testing.T) {
	t.Run("signed instance mismatches host", func(t *testing.T) {
		db, _ := newBearerAuthMock(t)
		tokens, err := deviceauth.NewTokenManager([]byte("01234567890123456789012345678901"), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		token, err := tokens.Issue(deviceauth.Identity{UserID: "stable-user", OrganizationID: bearerTestOrg, InstanceID: "foreign-instance"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "http://instance.test/v1/openai-agents/connections", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req = req.WithContext(contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance}))
		rr := httptest.NewRecorder()
		WithDeviceTokenAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("foreign instance reached handler") }), tokens, db).ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", rr.Code)
		}
	})
	t.Run("unverified or mismatched link", func(t *testing.T) {
		db, mock := newBearerAuthMock(t)
		mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("JOIN everstack.identity_links il").WillReturnError(sql.ErrNoRows)
		status, _ := serveBearer(t, db, deviceauth.Identity{UserID: "external-subject", Email: "person@example.com", OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance})
		if status != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", status)
		}
	})
	t.Run("database failure", func(t *testing.T) {
		db, mock := newBearerAuthMock(t)
		mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").WillReturnError(errors.New("database unavailable"))
		status, _ := serveBearer(t, db, deviceauth.Identity{UserID: "stable-user", OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance})
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", status)
		}
	})
	t.Run("tenant lookup database failure", func(t *testing.T) {
		db, mock := newBearerAuthMock(t)
		mock.ExpectQuery("SELECT COALESCE\\(role,''\\) AS role, user_id::text AS user_id").
			WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("member", "stable-user"))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM everstack.tenant_config").
			WillReturnError(errors.New("database unavailable"))
		status, _ := serveBearer(t, db, deviceauth.Identity{UserID: "stable-user", OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance})
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", status)
		}
	})
}

func newBearerAuthMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = raw.Close()
	})
	return sqlx.NewDb(raw, "sqlmock"), mock
}

func serveBearer(t *testing.T, db *sqlx.DB, identity deviceauth.Identity) (int, string) {
	t.Helper()
	tokens, err := deviceauth.NewTokenManager([]byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(identity)
	if err != nil {
		t.Fatal(err)
	}
	reachedUser := ""
	handler := WithDeviceTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedUser = contextkeys.GetUserID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), tokens, db)
	req := httptest.NewRequest(http.MethodGet, "http://instance.test/v1/openai-agents/connections", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{OrganizationID: bearerTestOrg, InstanceID: bearerTestInstance}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Code, reachedUser
}
