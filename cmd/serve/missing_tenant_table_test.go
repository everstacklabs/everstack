package serve

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// A self-hosted gateway has no tenant_config table, so resolving a request's
// Host against it fails the same way every time. That lookup runs for every
// request with a bearer token; it must reach Postgres once per TTL window, not
// once per request.

func undefinedTable() error {
	return &pgconn.PgError{Code: "42P01", Message: `relation "everstack.tenant_config" does not exist`}
}

func TestResolveInstanceFromHost_MissingTableIsRememberedPerDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(tenantConfigLookup).WillReturnError(undefinedTable())

	// Different hosts each time: the answer is about the database, not a host.
	hosts := []string{"localhost:8080", "gateway.internal", "acme-a1b2c3.eu.everstack.ai", "localhost:8080"}
	for i, host := range hosts {
		scope, err := resolveInstanceFromHost(context.Background(), xdb, host)
		if err == nil {
			t.Fatalf("lookup %d (%s): resolved %+v from a database with no tenants", i, host, scope)
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup %d (%s): a missing table must not read as a definite host miss", i, host)
		}
		if scope.InstanceID != "" || scope.OrganizationID != "" {
			t.Fatalf("lookup %d (%s): scope must be empty, got %+v", i, host, scope)
		}
		// sqlmock answers an unexpected query with its own error, so the
		// remembered answer is what proves no second query was sent.
		if i > 0 && !errors.Is(err, errNoTenantConfigTable) {
			t.Fatalf("lookup %d (%s): went back to the database: %v", i, host, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the missing table must cost one query: %v", err)
	}

	// Another database is a separate question.
	other, otherMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherMock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug", "gateway_url"}).
			AddRow("22222222-2222-2222-2222-222222a1b2c3", "11111111-1111-1111-1111-111111111111", "acme", "https://acme-a1b2c3.eu.everstack.ai"))
	scope, err := resolveInstanceFromHost(context.Background(), sqlx.NewDb(other, "sqlmock"), "acme-a1b2c3.eu.everstack.ai")
	if err != nil || scope.InstanceID == "" {
		t.Fatalf("a database that has the table must still resolve: scope=%+v err=%v", scope, err)
	}
}

func TestResolveInstanceFromHost_MissingTableEntryExpires(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	now := time.Now()
	prevNow := hostNegativeCacheNow
	hostNegativeCacheNow = func() time.Time { return now }
	t.Cleanup(func() { hostNegativeCacheNow = prevNow })

	mock.ExpectQuery(tenantConfigLookup).WillReturnError(undefinedTable())
	mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug", "gateway_url"}).
			AddRow("22222222-2222-2222-2222-222222a1b2c3", "11111111-1111-1111-1111-111111111111", "acme", "https://acme-a1b2c3.eu.everstack.ai"))

	const host = "acme-a1b2c3.eu.everstack.ai"
	if _, err := resolveInstanceFromHost(context.Background(), xdb, host); err == nil {
		t.Fatal("first lookup must fail: the table does not exist yet")
	}
	if _, err := resolveInstanceFromHost(context.Background(), xdb, host); !errors.Is(err, errNoTenantConfigTable) {
		t.Fatalf("within the window the remembered answer is served, got %v", err)
	}

	// The table is created (a migration ran). Once the entry expires the
	// tenant resolves; nothing has to be restarted.
	now = now.Add(hostNegativeCacheTTL + time.Second)
	scope, err := resolveInstanceFromHost(context.Background(), xdb, host)
	if err != nil || scope.InstanceID != "22222222-2222-2222-2222-222222a1b2c3" {
		t.Fatalf("after expiry the lookup must reach the database again: scope=%+v err=%v", scope, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Only Postgres saying the relation is undefined is remembered. A lookup that
// failed for any other reason says nothing about the database, and must be
// retried on the next request.
func TestResolveInstanceFromHost_OtherErrorsAreNotRemembered(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(tenantConfigLookup).WillReturnError(context.DeadlineExceeded)
	mock.ExpectQuery(tenantConfigLookup).WillReturnError(&pgconn.PgError{Code: "53300", Message: "too many clients"})
	mock.ExpectQuery(tenantConfigLookup).WillReturnError(errors.New("connection reset by peer"))

	for i := 0; i < 3; i++ {
		if _, err := resolveInstanceFromHost(context.Background(), xdb, "acme-a1b2c3.eu.everstack.ai"); err == nil {
			t.Fatalf("lookup %d: expected the injected failure", i)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("each failed lookup must go back to the database: %v", err)
	}
}

// The request path: bearer-token requests on a database with no tenant table
// are served without instance scope, and all but the first without a query.
func TestBearerRequestsOnSelfHostedDatabaseQueryOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(tenantConfigLookup).WillReturnError(undefinedTable())
	// A second lookup is allowed for but must never be sent: it staying
	// unconsumed is the proof that 200 requests cost one query.
	mock.ExpectQuery(tenantConfigLookup).WillReturnError(undefinedTable())

	served := 0
	handler := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		if _, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
			t.Errorf("request %d carries an instance scope on a database with no tenants", served)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/openai/v1/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer some-api-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
	}
	if served != 200 {
		t.Fatalf("served %d of 200", served)
	}
	if err := mock.ExpectationsWereMet(); err == nil {
		t.Fatal("200 bearer requests sent more than one lookup to the database")
	}
}
