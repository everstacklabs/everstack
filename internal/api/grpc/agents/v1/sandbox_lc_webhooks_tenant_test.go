package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/sandbox/lcwebhooks"
)

func TestListLCWebhooksUsesThePrincipalTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{lcWebhookRepo: lcwebhooks.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM sandbox_lifecycle_webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC")).
		WithArgs(ownerTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "url", "events", "secret", "enabled", "created_at", "updated_at"}))

	req := httptest.NewRequest(http.MethodGet, "/v1/sandbox-webhooks", nil)
	req = req.WithContext(sessionPrincipal(ownerTenant))
	recorder := httptest.NewRecorder()
	s.HandleListLCWebhooks(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateLCWebhookUsesThePrincipalTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{lcWebhookRepo: lcwebhooks.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(`
		INSERT INTO sandbox_lifecycle_webhook_endpoints (id, tenant_id, url, events, secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING *`)).
		WithArgs(sqlmock.AnyArg(), ownerTenant, "https://example.invalid/hook", []byte(`["sandbox.started"]`), "secret").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "url", "events", "secret", "enabled", "created_at", "updated_at"}).
			AddRow("webhook-1", ownerTenant, "https://example.invalid/hook", []byte(`["sandbox.started"]`), "secret", true, now, now))

	body := []byte(`{"url":"https://example.invalid/hook","events":["sandbox.started"],"secret":"secret","tenant_id":"tenant-from-body"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox-webhooks", bytes.NewReader(body))
	req = req.WithContext(sessionPrincipal(ownerTenant))
	recorder := httptest.NewRecorder()
	s.HandleCreateLCWebhook(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTestLCWebhookUsesThePrincipalTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{lcWebhookRepo: lcwebhooks.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM sandbox_lifecycle_webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC")).
		WithArgs(ownerTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "url", "events", "secret", "enabled", "created_at", "updated_at"}).
			AddRow("webhook-1", ownerTenant, "https://example.invalid/hook", []byte(`["sandbox.started"]`), "secret", true, now, now))

	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox-webhooks/webhook-1/test", nil)
	req = req.WithContext(sessionPrincipal(ownerTenant))
	req = mux.SetURLVars(req, map[string]string{"webhook_id": "webhook-1"})
	recorder := httptest.NewRecorder()
	s.HandleTestLCWebhook(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response struct {
		Payload lcwebhooks.Payload `json:"payload"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Payload.TenantID != ownerTenant {
		t.Fatalf("payload tenant = %q, want %q", response.Payload.TenantID, ownerTenant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListLCWebhookDeliveriesRequiresOwnedEndpoint(t *testing.T) {
	endpointQuery := regexp.QuoteMeta(`SELECT * FROM sandbox_lifecycle_webhook_endpoints WHERE id = $1 AND tenant_id = $2`)

	t.Run("foreign endpoint is not found before deliveries are read", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer rawDB.Close()
		s := &Server{lcWebhookRepo: lcwebhooks.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}
		mock.ExpectQuery(endpointQuery).
			WithArgs("webhook-1", "tenant-b").
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		req := httptest.NewRequest(http.MethodGet, "/v1/sandbox-webhooks/webhook-1/deliveries", nil)
		req = req.WithContext(sessionPrincipal("tenant-b"))
		req = mux.SetURLVars(req, map[string]string{"webhook_id": "webhook-1"})
		recorder := httptest.NewRecorder()
		s.HandleListLCWebhookDeliveries(recorder, req)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("SQL expectations: %v", err)
		}
	})

	t.Run("owner reads deliveries", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer rawDB.Close()
		s := &Server{lcWebhookRepo: lcwebhooks.NewRepository(sqlx.NewDb(rawDB, "sqlmock"))}
		now := time.Now().UTC()
		mock.ExpectQuery(endpointQuery).
			WithArgs("webhook-1", ownerTenant).
			WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "url", "events", "secret", "enabled", "created_at", "updated_at"}).
				AddRow("webhook-1", ownerTenant, "https://example.invalid", []byte(`["sandbox.started"]`), "secret", true, now, now))
		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT * FROM sandbox_lifecycle_webhook_deliveries
		WHERE endpoint_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC LIMIT 100`)).
			WithArgs("webhook-1", ownerTenant).
			WillReturnRows(sqlmock.NewRows([]string{"id", "endpoint_id", "tenant_id", "event", "payload", "status_code", "error", "duration_ms", "created_at"}))

		req := httptest.NewRequest(http.MethodGet, "/v1/sandbox-webhooks/webhook-1/deliveries", nil)
		req = req.WithContext(sessionPrincipal(ownerTenant))
		req = mux.SetURLVars(req, map[string]string{"webhook_id": "webhook-1"})
		recorder := httptest.NewRecorder()
		s.HandleListLCWebhookDeliveries(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
		}
		var response struct {
			Total int `json:"total"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Total != 0 {
			t.Fatalf("total = %d, want 0", response.Total)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("SQL expectations: %v", err)
		}
	})
}
