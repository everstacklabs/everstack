package serve

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// EVS-324 item 7: every request carrying a session cookie or bearer token
// resolves its Host against tenant_config with a predicate no index serves.
// Unknown hosts must not cost a table scan each: they are remembered briefly
// and answered the same way as the first miss.

const tenantConfigLookup = `FROM everstack\.tenant_config AS tc`

func TestResolveInstanceFromHost_UnknownHostIsNegativelyCached(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(tenantConfigLookup).WithArgs("nosuch-abcdef").WillReturnError(sql.ErrNoRows)

	for i := 0; i < 5; i++ {
		_, err := resolveInstanceFromHost(context.Background(), xdb, "NoSuch-abcdef.eu.everstack.ai:443")
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup %d: want sql.ErrNoRows (uniform miss), got %v", i, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unknown host must hit the database once: %v", err)
	}
}

func TestResolveInstanceFromHost_NegativeEntryExpires(t *testing.T) {
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

	mock.ExpectQuery(tenantConfigLookup).WithArgs("fresh-123456").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(tenantConfigLookup).WithArgs("fresh-123456").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111", "acme"))

	if _, err := resolveInstanceFromHost(context.Background(), xdb, "fresh-123456.example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("first lookup: %v", err)
	}
	// A tenant provisioned right after the miss becomes routable once the
	// short negative TTL passes.
	now = now.Add(hostNegativeCacheTTL + time.Second)
	scope, err := resolveInstanceFromHost(context.Background(), xdb, "fresh-123456.example.com")
	if err != nil || scope.InstanceID == "" {
		t.Fatalf("after TTL the tenant must resolve: scope=%+v err=%v", scope, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Transient failures are never cached: a DB blip must not make a real tenant
// unroutable.
func TestResolveInstanceFromHost_TransientErrorsAreNotCached(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(tenantConfigLookup).WithArgs("real-654321").WillReturnError(errors.New("timeout"))
	mock.ExpectQuery(tenantConfigLookup).WithArgs("real-654321").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111", "acme"))

	if _, err := resolveInstanceFromHost(context.Background(), xdb, "real-654321.example.com"); err == nil {
		t.Fatal("want the transient error")
	}
	if scope, err := resolveInstanceFromHost(context.Background(), xdb, "real-654321.example.com"); err != nil || scope.InstanceID == "" {
		t.Fatalf("transient error was cached: scope=%+v err=%v", scope, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
