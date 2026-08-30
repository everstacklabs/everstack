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
