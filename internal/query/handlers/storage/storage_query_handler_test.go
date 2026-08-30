package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

func TestStorageQueriesRequireTenant(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
	}{
		{
			name:     "get storage config",
			validate: func() error { return NewGetStorageConfigQuery("config-1", "").Validate() },
		},
		{
			name:     "list storage configs",
			validate: func() error { return NewListStorageConfigsQuery("").Validate() },
		},
		{
			name:     "list storage objects",
			validate: func() error { return NewListObjectsQuery("", "", "", "", 0, 0).Validate() },
		},
		{
			name:     "get storage usage",
			validate: func() error { return NewGetStorageUsageQuery("").Validate() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.validate(); err == nil {
				t.Fatal("Validate() error = nil, want missing tenant error")
			}
		})
	}
}

func TestStorageQueryHandlersAuthorizeTenantBeforeDatabaseAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "get config",
			call: func(ctx context.Context) error {
				_, err := NewGetStorageConfigHandler(xdb).Handle(ctx, NewGetStorageConfigQuery("config-1", "tenant-1"))
				return err
			},
		},
		{
			name: "list configs",
			call: func(ctx context.Context) error {
				_, err := NewListStorageConfigsHandler(xdb).Handle(ctx, NewListStorageConfigsQuery("tenant-1"))
				return err
			},
		},
		{
			name: "list objects",
			call: func(ctx context.Context) error {
				_, err := NewListStorageObjectsHandler(xdb).Handle(ctx, NewListObjectsQuery("tenant-1", "", "", "", 0, 0))
				return err
			},
		},
		{
			name: "get usage",
			call: func(ctx context.Context) error {
				_, err := NewGetStorageUsageHandler(xdb).Handle(ctx, NewGetStorageUsageQuery("tenant-1"))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" unauthenticated", func(t *testing.T) {
			if err := tt.call(context.Background()); !errors.Is(err, storageauth.ErrUnauthenticated) {
				t.Fatalf("Handle() error = %v, want unauthenticated", err)
			}
		})
		t.Run(tt.name+" cross tenant", func(t *testing.T) {
			ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-2", "verified-key-hash")
			if err := tt.call(ctx); !errors.Is(err, storageauth.ErrPermissionDenied) {
				t.Fatalf("Handle() error = %v, want permission denied", err)
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("authorization should run before SQL: %v", err)
	}
}

var _ query.Query = (*GetStorageConfigQuery)(nil)

func TestStorageConfigReadModelCarriesInternalPlacementWithoutSerializingIt(t *testing.T) {
	rm := StorageConfigReadModel{
		ID: "config-1", TenantID: "tenant-1", CredentialRef: "storagecred_opaque",
		ManagementMode: "system", ManagedCellID: "r2-eu-production", ManagedPathPrefix: "tenants/opaque",
	}
	encoded, err := json.Marshal(rm)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || string(encoded) == "null" {
		t.Fatal("read model did not serialize")
	}
	for _, forbidden := range []string{"storagecred_opaque", "system", "r2-eu-production", "tenants/opaque"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("serialized read model exposes %q: %s", forbidden, encoded)
		}
	}
	if json.Valid(encoded) && string(encoded) != "" {
		var body map[string]interface{}
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		if _, exists := body["credential_ref"]; exists {
			t.Fatalf("serialized read model exposes credential_ref: %s", encoded)
		}
		for _, field := range []string{"management_mode", "system_managed", "managed_cell_id", "managed_path_prefix"} {
			if _, exists := body[field]; exists {
				t.Fatalf("serialized read model exposes %s: %s", field, encoded)
			}
		}
	}

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	now := time.Now()
	mock.ExpectQuery("SELECT id, tenant_id, provider, endpoint, region, bucket, COALESCE\\(credential_ref, ''\\) AS credential_ref").
		WithArgs("config-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "provider", "endpoint", "region", "bucket", "credential_ref",
			"path_prefix", "is_default", "enabled", "created_at", "updated_at",
			"management_mode", "managed_cell_id", "managed_path_prefix",
		}).AddRow("config-1", "tenant-1", "s3", "https://storage.example", "us-east-1", "bucket", "storagecred_opaque", "", true, true, now, now, "customer", "", ""))

	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-1", "verified-key-hash")
	result, err := NewGetStorageConfigHandler(db).Handle(ctx, NewGetStorageConfigQuery("config-1", "tenant-1"))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := result.(*StorageConfigReadModel)
	if got.CredentialRef != "storagecred_opaque" {
		t.Fatalf("CredentialRef = %q, want storagecred_opaque", got.CredentialRef)
	}
	if got.ManagementMode != "customer" {
		t.Fatalf("ManagementMode = %q, want customer", got.ManagementMode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
