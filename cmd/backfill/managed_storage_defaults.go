package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"
)

type postgresManagedTenantSource struct {
	db *sqlx.DB
}

func (s *postgresManagedTenantSource) ListManagedTenantIDs(ctx context.Context, after string, limit int) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("managed storage tenant inventory database is required")
	}
	var tenantIDs []string
	err := database.RunWithBypass(ctx, s.db, func(tx *sqlx.Tx) error {
		return tx.SelectContext(ctx, &tenantIDs, `
			SELECT instance_id::text
			FROM everstack.tenant_config
			WHERE instance_id > COALESCE(
				NULLIF($1, '')::uuid,
				'00000000-0000-0000-0000-000000000000'::uuid
			)
			ORDER BY instance_id
			LIMIT $2
		`, strings.TrimSpace(after), limit)
	})
	if err != nil {
		return nil, fmt.Errorf("read cloud tenant inventory: %w", err)
	}
	return tenantIDs, nil
}

// NewManagedStorageDefaultBackfillCommand creates the hidden maintenance
// command that reconciles one system-managed default for every cloud tenant.
func NewManagedStorageDefaultBackfillCommand() *cobra.Command {
	logger.SetFormatter(logger.NewDefaultBracketFormatter())
	logger.SetGlobal()

	var targetDSN string
	var tenantDSN string
	var cellID string
	var batchSize int
	cmd := &cobra.Command{
		Use:    "backfill-managed-storage-defaults",
		Short:  "Create or repair the Everstack Storage default for every cloud tenant",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedTargetDSN := resolveManagedStorageTargetDSN(targetDSN)
			if resolvedTargetDSN == "" {
				return errors.New("no gateway DSN found; pass --dsn or set EVS_POSTGRES_DSN")
			}
			resolvedTenantDSN := resolveManagedStorageTenantDSN(tenantDSN, resolvedTargetDSN)
			resolvedCellID := strings.TrimSpace(cellID)
			if resolvedCellID == "" {
				resolvedCellID = strings.TrimSpace(os.Getenv("EVS_MANAGED_STORAGE_CELL_ID"))
			}
			if resolvedCellID == "" {
				return errors.New("managed storage cell ID is required; pass --cell-id or set EVS_MANAGED_STORAGE_CELL_ID")
			}

			ctx := cmd.Context()
			targetConn, err := database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: resolvedTargetDSN})
			if err != nil {
				return fmt.Errorf("open managed storage database: %w", err)
			}
			defer targetConn.Close(ctx)

			tenantDB := targetConn.RW
			if resolvedTenantDSN != resolvedTargetDSN {
				tenantConn, err := database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: resolvedTenantDSN})
				if err != nil {
					return fmt.Errorf("open cloud tenant inventory database: %w", err)
				}
				defer tenantConn.Close(ctx)
				tenantDB = tenantConn.RW
			}

			defaults, err := storagepkg.NewPostgresManagedDefaults(targetConn.RW, resolvedCellID)
			if err != nil {
				return err
			}
			report, err := storagepkg.BackfillManagedDefaults(
				ctx,
				&postgresManagedTenantSource{db: tenantDB},
				defaults,
				batchSize,
			)
			if err != nil {
				return fmt.Errorf(
					"managed storage default backfill failed after scanning %d tenants and ensuring %d defaults: %w",
					report.TenantsScanned,
					report.DefaultsEnsured,
					err,
				)
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"tenants_scanned=%d defaults_ensured=%d\n",
				report.TenantsScanned,
				report.DefaultsEnsured,
			)
			return err
		},
	}
	cmd.Flags().StringVar(&targetDSN, "dsn", "", "gateway PostgreSQL connection string (defaults to EVS_POSTGRES_DSN)")
	cmd.Flags().StringVar(&tenantDSN, "tenant-dsn", "", "cloud tenant inventory PostgreSQL connection string (defaults to EVS_PLATFORM_DSN or --dsn)")
	cmd.Flags().StringVar(&cellID, "cell-id", "", "managed storage cell ID (defaults to EVS_MANAGED_STORAGE_CELL_ID)")
	cmd.Flags().IntVar(&batchSize, "batch", 100, "tenant IDs per inventory page")
	return cmd
}

func resolveManagedStorageTargetDSN(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("EVS_POSTGRES_DSN")); value != "" {
		return value
	}
	return strings.TrimSpace(resolvePostgresDSN(""))
}

func resolveManagedStorageTenantDSN(flagValue, targetDSN string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("EVS_PLATFORM_DSN")); value != "" {
		return value
	}
	return strings.TrimSpace(targetDSN)
}
