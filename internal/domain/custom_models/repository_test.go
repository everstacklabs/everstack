package custom_models

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func newMockRepository(t *testing.T, managed bool) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return NewRepository(sqlx.NewDb(raw, "sqlmock"), managed), mock
}

func customModelRows(tenantID string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "provider_name", "model_name", "display_name",
		"model_metadata", "source", "is_active", "created_at", "updated_at",
	}).AddRow("model-id", tenantID, "ollama", "model-a", "Model A", []byte(`{}`), "manual", true, now, now)
}

func TestManagedRepositoryScopesTenantOperations(t *testing.T) {
	repo, mock := newMockRepository(t, true)
	ctx := context.Background()

	mock.ExpectQuery(`(?s)FROM custom_models\s+WHERE tenant_id = \$1`).
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "provider_name", "model_name", "display_name",
			"model_metadata", "source", "is_active", "created_at", "updated_at",
		}))
	models, err := repo.List(ctx, "tenant-a")
	if err != nil || len(models) != 0 {
		t.Fatalf("List tenant A = %d models, %v", len(models), err)
	}

	mock.ExpectQuery(`(?s)WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs("tenant-b-model", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "provider_name", "model_name", "display_name",
			"model_metadata", "source", "is_active", "created_at", "updated_at",
		}))
	if _, err := repo.GetByID(ctx, "tenant-a", "tenant-b-model"); err == nil {
		t.Fatal("tenant A unexpectedly read tenant B model")
	}

	mock.ExpectQuery(`(?s)WHERE id = \$5 AND tenant_id = \$6`).
		WithArgs("Model B", sqlmock.AnyArg(), true, sqlmock.AnyArg(), "tenant-b-model", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}))
	if err := repo.Update(ctx, "tenant-a", &CustomModel{
		ID: "tenant-b-model", DisplayName: "Model B", ModelMetadata: map[string]interface{}{}, IsActive: true,
	}); err == nil {
		t.Fatal("tenant A unexpectedly updated tenant B model")
	}

	mock.ExpectExec(`(?s)DELETE FROM custom_models\s+WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs("tenant-b-model", "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.Delete(ctx, "tenant-a", "tenant-b-model"); err == nil {
		t.Fatal("tenant A unexpectedly deleted tenant B model")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerlessRowsVisibleOnlyWhenNotManaged(t *testing.T) {
	t.Run("self-hosted", func(t *testing.T) {
		repo, mock := newMockRepository(t, false)
		mock.ExpectQuery(`(?s)WHERE id = \$1 AND \(tenant_id = \$2 OR tenant_id = ''\)`).
			WithArgs("model-id", "local-tenant").
			WillReturnRows(customModelRows(""))

		model, err := repo.GetByID(context.Background(), "local-tenant", "model-id")
		if err != nil {
			t.Fatal(err)
		}
		if model.TenantID != "" {
			t.Fatalf("ownerless model tenant = %q", model.TenantID)
		}
		mock.ExpectQuery(`(?s)WHERE id = \$5 AND \(tenant_id = \$6 OR tenant_id = ''\)`).
			WithArgs("Model A", []byte(`{}`), true, sqlmock.AnyArg(), "model-id", "local-tenant").
			WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
		if err := repo.Update(context.Background(), "local-tenant", model); err != nil {
			t.Fatalf("update ownerless model: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("managed", func(t *testing.T) {
		repo, mock := newMockRepository(t, true)
		mock.ExpectQuery(`(?s)WHERE id = \$1 AND tenant_id = \$2`).
			WithArgs("model-id", "tenant-a").
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "tenant_id", "provider_name", "model_name", "display_name",
				"model_metadata", "source", "is_active", "created_at", "updated_at",
			}))
		if _, err := repo.GetByID(context.Background(), "tenant-a", "model-id"); err == nil {
			t.Fatal("managed tenant unexpectedly read ownerless model")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestManagedRepositoryEmptyTenantFailsClosed(t *testing.T) {
	repo, mock := newMockRepository(t, true)
	ctx := context.Background()

	models, err := repo.List(ctx, "")
	if err != nil || len(models) != 0 {
		t.Fatalf("List empty tenant = %d models, %v", len(models), err)
	}
	if exists, err := repo.Exists(ctx, "", "ollama", "model-a"); err != nil || exists {
		t.Fatalf("Exists empty tenant = %t, %v", exists, err)
	}
	if _, err := repo.GetByID(ctx, "", "model-id"); err == nil {
		t.Fatal("GetByID empty tenant unexpectedly succeeded")
	}

	model := &CustomModel{ID: "model-id", ModelMetadata: map[string]interface{}{}}
	writeErrors := []error{
		repo.Create(ctx, "", model),
		repo.Update(ctx, "", model),
		repo.Delete(ctx, "", "model-id"),
		repo.DeleteByProviderAndModel(ctx, "", "ollama", "model-a"),
		repo.SetActive(ctx, "", "model-id", true),
	}
	for i, err := range writeErrors {
		if !errors.Is(err, ErrTenantRequired) {
			t.Fatalf("write %d error = %v, want ErrTenantRequired", i, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWritesCallerTenant(t *testing.T) {
	repo, mock := newMockRepository(t, true)
	now := time.Now()
	mock.ExpectQuery(`(?s)INSERT INTO custom_models.*\(id, tenant_id, provider_name`).
		WithArgs(
			"model-id", "tenant-a", "ollama", "model-a", "Model A", []byte(`{}`),
			ModelSourceManual, true, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("model-id", now, now))

	model := &CustomModel{
		ID:            "model-id",
		ProviderName:  "ollama",
		ModelName:     "model-a",
		DisplayName:   "Model A",
		ModelMetadata: map[string]interface{}{},
		Source:        ModelSourceManual,
		IsActive:      true,
	}
	if err := repo.Create(context.Background(), "tenant-a", model); err != nil {
		t.Fatal(err)
	}
	if model.TenantID != "tenant-a" {
		t.Fatalf("model tenant = %q", model.TenantID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
