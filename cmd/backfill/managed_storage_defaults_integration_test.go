package backfill

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/dialect"
	"github.com/everstacklabs/everstack/internal/database/migrations"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestManagedStorageDefaultBackfillPostgresIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_POSTGRES_DSN to a disposable PostgreSQL database")
	}
	// Both migration loaders use repository-relative paths.
	t.Chdir("../..")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	conn, err := database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: dsn})
	if err != nil {
		t.Fatal("open managed storage backfill acceptance database")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if err := conn.RW.PingContext(ctx); err != nil {
		t.Fatal("ping managed storage backfill acceptance database")
	}
	if err := (dialect.Postgres{}).EnsureSchema(ctx, conn.RW); err != nil {
		t.Fatal("migrate gateway schema for managed storage backfill acceptance")
	}
	if err := migrations.EnsureForService(ctx, conn.RW, "postgres", "cloud"); err != nil {
		t.Fatal("migrate cloud tenant inventory for managed storage backfill acceptance")
	}

	tenantIDs := []string{uuid.NewString(), uuid.NewString()}
	for i, tenantID := range tenantIDs {
		_, err := conn.RW.ExecContext(ctx, `
			INSERT INTO everstack.tenant_config (
				instance_id, organization_id, workspace_id, slug, org_slug,
				schema_name, status, gateway_url
			) VALUES ($1, $2, $3, $4, $5, $6, 'active', $7)
		`,
			tenantID,
			uuid.NewString(),
			uuid.NewString(),
			"storage-backfill-"+tenantID[:8],
			"acceptance-"+tenantID[:8],
			"tenant_"+strings.ReplaceAll(tenantID, "-", "_"),
			"https://acceptance.invalid/"+tenantID,
		)
		if err != nil {
			t.Fatalf("insert managed tenant %d", i+1)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = database.RunWithBypass(cleanupCtx, conn.RW, func(tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(cleanupCtx,
				"DELETE FROM object_storage_configs WHERE tenant_id IN ($1, $2)",
				tenantIDs[0], tenantIDs[1],
			); err != nil {
				return err
			}
			_, err := tx.ExecContext(cleanupCtx,
				"DELETE FROM everstack.tenant_config WHERE instance_id IN ($1::uuid, $2::uuid)",
				tenantIDs[0], tenantIDs[1],
			)
			return err
		})
	})

	runBackfill := func() string {
		t.Helper()
		cmd := NewManagedStorageDefaultBackfillCommand()
		cmd.SilenceUsage = true
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{
			"--dsn", dsn,
			"--tenant-dsn", dsn,
			"--cell-id", "r2-eu-acceptance-001",
			"--batch", "1",
		})
		if err := cmd.ExecuteContext(ctx); err != nil {
			t.Fatal("execute managed storage default backfill")
		}
		return strings.TrimSpace(output.String())
	}

	const expectedReport = "tenants_scanned=2 defaults_ensured=2"
	if report := runBackfill(); report != expectedReport {
		t.Fatalf("first backfill report = %q, want %q", report, expectedReport)
	}
	if report := runBackfill(); report != expectedReport {
		t.Fatalf("second backfill report = %q, want %q", report, expectedReport)
	}

	type managedDefaultRow struct {
		ConfigID   string `db:"id"`
		TenantID   string `db:"tenant_id"`
		CellID     string `db:"managed_cell_id"`
		PathPrefix string `db:"managed_path_prefix"`
		IsDefault  bool   `db:"is_default"`
	}
	var defaults []managedDefaultRow
	if err := database.RunWithBypass(ctx, conn.RW, func(tx *sqlx.Tx) error {
		return tx.SelectContext(ctx, &defaults, `
			SELECT id, tenant_id, managed_cell_id, managed_path_prefix, is_default
			FROM object_storage_configs
			WHERE tenant_id IN ($1, $2) AND management_mode = 'system'
			ORDER BY tenant_id
		`, tenantIDs[0], tenantIDs[1])
	}); err != nil {
		t.Fatal("read managed defaults after backfill")
	}
	if len(defaults) != len(tenantIDs) {
		t.Fatalf("managed defaults = %d, want %d", len(defaults), len(tenantIDs))
	}
	for _, row := range defaults {
		if row.ConfigID != storagepkg.ManagedDefaultConfigID(row.TenantID) ||
			row.CellID != "r2-eu-acceptance-001" ||
			row.PathPrefix != storagepkg.ManagedTenantPrefix(row.TenantID) ||
			!row.IsDefault {
			t.Fatal("managed default was not stable after idempotent backfill")
		}
	}
}
