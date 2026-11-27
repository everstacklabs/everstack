package provider_api_keys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func mockRepository(t *testing.T) (*PostgresRepository, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		_ = rawDB.Close()
	})
	return &PostgresRepository{db: sqlx.NewDb(rawDB, "postgres")}, mock
}

func configKeyFor(secret string) *ProviderAPIKey {
	return &ProviderAPIKey{
		ProviderConfigID: "11111111-1111-1111-1111-111111111111",
		KeyName:          "Config API Key",
		KeyEncrypted:     secret,
		Weight:           1,
		IsActive:         true,
		Source:           "config",
	}
}

func TestUpsertConfigKeySkipsCredentialTheUserAlreadyManages(t *testing.T) {
	repo, mock := mockRepository(t)

	mock.ExpectExec("DELETE FROM provider_api_keys").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	key := configKeyFor("sk-user-secret")
	err := repo.UpsertConfigKey(context.Background(), key)
	if !errors.Is(err, ErrConfigKeyDuplicatesManual) {
		t.Fatalf("UpsertConfigKey() error = %v, want ErrConfigKeyDuplicatesManual", err)
	}
	if key.ID != "" {
		t.Fatalf("key.ID = %q, want no row written", key.ID)
	}
}

func TestUpsertConfigKeyStillSeedsAPlatformKey(t *testing.T) {
	repo, mock := mockRepository(t)

	mock.ExpectExec("DELETE FROM provider_api_keys").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("INSERT INTO provider_api_keys").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("22222222-2222-2222-2222-222222222222", nowForTest(), nowForTest()))

	key := configKeyFor("sk-platform-owned")
	if err := repo.UpsertConfigKey(context.Background(), key); err != nil {
		t.Fatalf("UpsertConfigKey() error = %v", err)
	}
	if key.Source != "config" {
		t.Fatalf("key.Source = %q, want config", key.Source)
	}
	if key.ID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("key.ID = %q, want the returned row id", key.ID)
	}
}

func nowForTest() time.Time { return time.Unix(0, 0).UTC() }

// The tenant-scoped methods carry the ownership predicate in SQL and report
// rows outside the tenant as ErrKeyNotFound.
func TestForOrgMethodsScopeByOwningOrganization(t *testing.T) {
	const (
		org   = "11111111-1111-1111-1111-11111111111a"
		cfgID = "22222222-2222-2222-2222-22222222222b"
		keyID = "33333333-3333-3333-3333-33333333333b"
	)
	ctx := context.Background()

	t.Run("delete", func(t *testing.T) {
		repo, mock := mockRepository(t)
		mock.ExpectExec(`DELETE FROM provider_api_keys AS k\s+USING provider_configurations AS c\s+WHERE k\.id = \$1 AND c\.id = k\.provider_config_id AND c\.organization_id = \$2`).
			WithArgs(keyID, org).
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := repo.DeleteForOrg(ctx, org, keyID); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("DeleteForOrg() error = %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		repo, mock := mockRepository(t)
		mock.ExpectQuery(`FROM provider_api_keys k\s+JOIN provider_configurations c ON c\.id = k\.provider_config_id\s+WHERE k\.provider_config_id = \$1 AND c\.organization_id = \$2`).
			WithArgs(cfgID, org).
			WillReturnRows(sqlmock.NewRows([]string{"id", "provider_config_id", "key_name", "key_encrypted", "weight", "is_active", "source", "rate_limit_tracking", "created_at", "updated_at"}))
		keys, err := repo.ListByProviderConfigForOrg(ctx, org, cfgID)
		if err != nil || len(keys) != 0 {
			t.Fatalf("ListByProviderConfigForOrg() = %v, %v; want empty", keys, err)
		}
	})

	t.Run("update", func(t *testing.T) {
		repo, mock := mockRepository(t)
		mock.ExpectExec(`UPDATE provider_api_keys AS k[\s\S]+FROM provider_configurations AS c\s+WHERE k\.id = \$1 AND c\.id = k\.provider_config_id AND c\.organization_id = \$8`).
			WithArgs(keyID, sqlmock.AnyArg(), sqlmock.AnyArg(), 3, true, sqlmock.AnyArg(), sqlmock.AnyArg(), org).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.UpdateForOrg(ctx, org, &ProviderAPIKey{ID: keyID, ProviderConfigID: cfgID, KeyName: "k", KeyEncrypted: "sk-x", Weight: 3, IsActive: true})
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("UpdateForOrg() error = %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("create", func(t *testing.T) {
		repo, mock := mockRepository(t)
		mock.ExpectExec(`INSERT INTO provider_api_keys[\s\S]+SELECT \$1::uuid, c\.id,[\s\S]+FROM provider_configurations c\s+WHERE c\.id = \$2::uuid AND c\.organization_id = \$11`).
			WithArgs(keyID, cfgID, "k", "sk-x", 1, true, "manual", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), org).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.CreateForOrg(ctx, org, &ProviderAPIKey{ID: keyID, ProviderConfigID: cfgID, KeyName: "k", KeyEncrypted: "sk-x", Weight: 1, IsActive: true})
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("CreateForOrg() error = %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("empty org is refused before SQL", func(t *testing.T) {
		repo, _ := mockRepository(t)
		if _, err := repo.GetByIDForOrg(ctx, "", keyID); err == nil {
			t.Fatal("GetByIDForOrg with empty org succeeded")
		}
		if err := repo.DeleteForOrg(ctx, "", keyID); err == nil {
			t.Fatal("DeleteForOrg with empty org succeeded")
		}
	})
}
