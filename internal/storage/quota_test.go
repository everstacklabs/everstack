package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

func TestQuotaEnforcerAuthorizesTenantBeforeDatabaseAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quota := NewQuotaEnforcer(sqlx.NewDb(db, "sqlmock"))

	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "get usage",
			call: func(ctx context.Context) error {
				_, err := quota.GetUsage(ctx, "tenant-1")
				return err
			},
		},
		{
			name: "check unlimited quota",
			call: func(ctx context.Context) error {
				return quota.CheckQuota(ctx, "tenant-1", 1, 0)
			},
		},
		{
			name: "increment usage",
			call: func(ctx context.Context) error {
				return quota.IncrementUsage(ctx, "tenant-1", 1)
			},
		},
		{
			name: "decrement usage",
			call: func(ctx context.Context) error {
				return quota.DecrementUsage(ctx, "tenant-1", 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" unauthenticated", func(t *testing.T) {
			if err := tt.call(context.Background()); !errors.Is(err, storageauth.ErrUnauthenticated) {
				t.Fatalf("operation error = %v, want unauthenticated", err)
			}
		})
		t.Run(tt.name+" cross tenant", func(t *testing.T) {
			ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-2", "verified-key-hash")
			if err := tt.call(ctx); !errors.Is(err, storageauth.ErrPermissionDenied) {
				t.Fatalf("operation error = %v, want permission denied", err)
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("authorization should run before SQL: %v", err)
	}
}

func TestQuotaEnforcerAllowsAuthenticatedTenant(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quota := NewQuotaEnforcer(sqlx.NewDb(db, "sqlmock"))
	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-1", "verified-key-hash")

	mock.ExpectQuery("SELECT tenant_id, total_bytes, object_count, reserved_bytes, reserved_object_count FROM object_storage_usage").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"tenant_id", "total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow("tenant-1", 12, 2, 4, 1))

	usage, err := quota.GetUsage(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.TotalBytes != 12 || usage.ObjectCount != 2 || usage.ReservedBytes != 4 || usage.ReservedObjectCount != 1 {
		t.Fatalf("GetUsage() = %+v, want committed and reserved usage", usage)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaEnforcerCountsReservationsAgainstLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quota := NewQuotaEnforcer(sqlx.NewDb(db, "sqlmock"))
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")

	mock.ExpectQuery("SELECT tenant_id, total_bytes, object_count, reserved_bytes, reserved_object_count FROM object_storage_usage").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"tenant_id", "total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow("tenant-1", 60, 1, 30, 1))

	err = quota.CheckQuota(ctx, "tenant-1", 11, 100)
	if err == nil {
		t.Fatal("CheckQuota() error = nil, want reserved capacity to exhaust the limit")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
