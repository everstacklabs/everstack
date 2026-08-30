package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/hosting"
)

func TestEnforceTenantQuotaSerializesAndRejectsSiteOverflow(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(3, 100))
	mock.ExpectRollback()

	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = enforceTenantQuotaTx(
		context.Background(),
		tx,
		"tenant-1",
		hosting.TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 500},
		hosting.TenantUsage{Sites: 1, StorageBytes: 10},
	)
	_ = tx.Rollback()

	var exceeded *hosting.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Resource != hosting.QuotaResourceSites {
		t.Fatalf("error = %v, want site quota error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceTenantQuotaCountsRetainedAndPendingVersionStorage(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("v.status IN \\('pending', 'finalized'\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(1, 450))
	mock.ExpectRollback()

	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = enforceTenantQuotaTx(
		context.Background(),
		tx,
		"tenant-1",
		hosting.TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 500},
		hosting.TenantUsage{StorageBytes: 51},
	)
	_ = tx.Rollback()

	var exceeded *hosting.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Resource != hosting.QuotaResourceStorage {
		t.Fatalf("error = %v, want storage quota error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceTenantQuotaCountsPendingStorageAfterSiteDeletion(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)COUNT\\(DISTINCT s.id\\) FILTER \\(WHERE s.status <> 'deleted'\\).*WHERE s.tenant_id = \\$1").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(0, 450))
	mock.ExpectRollback()

	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = enforceTenantQuotaTx(
		context.Background(),
		tx,
		"tenant-1",
		hosting.TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 500},
		hosting.TenantUsage{StorageBytes: 51},
	)
	_ = tx.Rollback()

	var exceeded *hosting.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Resource != hosting.QuotaResourceStorage {
		t.Fatalf("error = %v, want retained storage quota error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertPublishEnforcesQuotaBeforeWritingSite(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{
		db: db,
		quotaResolver: hosting.QuotaResolverFunc(func(_ context.Context, tenantID string) (hosting.TenantQuota, error) {
			if tenantID != "tenant-1" {
				t.Fatalf("tenant = %q, want tenant-1", tenantID)
			}
			return hosting.TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 500}, nil
		}),
	}

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(3, 100))
	mock.ExpectRollback()

	_, err = s.insertPublish(
		context.Background(), nil, "demo", "tenant-1", "", "public", false,
		[]manifestFile{{path: "index.html", sizeBytes: 10, contentType: "text/html"}},
		10, "finalize-hash",
	)
	var exceeded *hosting.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Resource != hosting.QuotaResourceSites {
		t.Fatalf("error = %v, want site quota error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBindClaimedSiteEnforcesDestinationTenantQuota(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	s := &Server{
		db: db,
		quotaResolver: hosting.QuotaResolverFunc(func(_ context.Context, tenantID string) (hosting.TenantQuota, error) {
			return hosting.TenantQuota{Tier: "free", MaxSites: 0, MaxStorageBytes: 500}, nil
		}),
	}

	now := time.Now().UTC()
	claimHash := hashToken("claim-token")
	mock.ExpectQuery("SELECT id, slug, tenant_id").
		WithArgs("demo").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "tenant_id", "owner_user_id", "status", "spa_fallback", "access",
			"current_version", "manifest_key", "total_bytes", "file_count", "claim_token_hash",
			"claimed_at", "kill_switch", "moderation_generation", "expires_at", "created_at", "last_published_at",
		}).AddRow(
			"site-1", "demo", nil, nil, "active", false, "noindex",
			int32(1), "sites/demo/manifest.json", 10, 1, claimHash, nil, false, 0, now.Add(time.Hour), now, &now,
		))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(total_bytes\\), 0\\)::bigint").
		WithArgs("site-1").
		WillReturnRows(sqlmock.NewRows([]string{"storage_bytes"}).AddRow(10))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("hosting-quota:tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("COUNT\\(DISTINCT s.id\\)").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"sites", "storage_bytes"}).AddRow(0, 0))
	mock.ExpectRollback()

	err = s.bindClaimedSiteLocked(context.Background(), "demo", "claim-token", "tenant-1", "user-1")
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, error = %v; want ResourceExhausted", connect.CodeOf(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
