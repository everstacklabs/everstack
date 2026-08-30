package backfill

import (
	"fmt"
	"os"
	"strings"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// NewStorageCredentialBackfillCommand returns the maintenance command for inventorying and
// remediating plaintext external storage credentials. Output contains counts
// only and never credential values.
func NewStorageCredentialBackfillCommand() *cobra.Command {
	logger.SetFormatter(logger.NewDefaultBracketFormatter())
	logger.SetGlobal()

	var dsn string
	var batchSize int
	var inventoryOnly bool
	cmd := &cobra.Command{
		Use:    "backfill-storage-credentials",
		Short:  "Encrypt legacy external storage credentials and redact PostgreSQL events",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved := resolvePostgresDSN(dsn)
			if resolved == "" {
				return fmt.Errorf("no DSN found; set database.postgres.dsn in config or pass --dsn")
			}
			ctx := cmd.Context()
			conn, err := database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: resolved})
			if err != nil {
				return err
			}
			defer conn.Close(ctx)

			inventory, err := storagecredentials.InventoryLegacyCredentials(ctx, conn.RW)
			if err != nil {
				return err
			}
			cutoverEnabled, err := storagecredentials.CredentialCutoverEnabledForDB(ctx, conn.RW)
			if err != nil {
				return err
			}
			if inventoryOnly {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "plaintext_configs=%d incomplete_configs=%d plaintext_volume_buckets=%d incomplete_volume_buckets=%d postgres_events=%d cutover_enabled=%t\n",
					inventory.PlaintextConfigs, inventory.IncompleteConfigs, inventory.PlaintextVolumeBuckets,
					inventory.IncompleteVolumeBuckets, inventory.PostgresEvents, cutoverEnabled)
				return err
			}

			backend := strings.ToLower(strings.TrimSpace(os.Getenv("EVS_STORAGE_CREDENTIAL_BACKEND")))
			if backend == "" {
				backend = strings.ToLower(strings.TrimSpace(viper.GetString("secret_manager.storage_credentials.backend")))
			}
			if backend == "" || backend == "inherit" {
				managerType := strings.ToLower(strings.TrimSpace(os.Getenv("EVS_SECRET_MANAGER_TYPE")))
				if managerType == "" {
					managerType = strings.ToLower(strings.TrimSpace(viper.GetString("secret_manager.type")))
				}
				if managerType == "vault" {
					backend = "vault"
				} else {
					backend = "postgres"
				}
			}
			livePostgresCredentials, err := storagecredentials.CountLivePostgresCredentials(ctx, conn.RW)
			if err != nil {
				return err
			}
			if backend == "vault" && livePostgresCredentials == 0 && legacyInventoryIsClean(inventory) {
				if err := storagecredentials.EnableCredentialCutoverIfClean(ctx, conn.RW); err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(),
					"configs_remediated=0 volume_buckets_remediated=0 postgres_events_redacted=0 credentials_rewrapped=0 remaining_plaintext_configs=0 remaining_plaintext_volume_buckets=0 remaining_postgres_events=0 cutover_enabled=true\n")
				return err
			}

			store, err := storagecredentials.NewConfiguredPostgresStore(conn.RW)
			if err != nil {
				return fmt.Errorf("configure storage credential backend: %w", err)
			}

			report, err := store.BackfillLegacyCredentials(ctx, batchSize)
			if err != nil {
				return fmt.Errorf("storage credential remediation failed after %d configs and %d volume buckets: %w",
					report.ConfigsRemediated, report.VolumeBucketsRemediated, err)
			}
			rewrapped, err := store.RewrapCredentials(ctx, batchSize)
			if err != nil {
				return fmt.Errorf("storage credential key rotation failed after %d records: %w", rewrapped, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"configs_remediated=%d volume_buckets_remediated=%d postgres_events_redacted=%d credentials_rewrapped=%d remaining_plaintext_configs=%d remaining_plaintext_volume_buckets=%d remaining_postgres_events=%d cutover_enabled=%t\n",
				report.ConfigsRemediated, report.VolumeBucketsRemediated, report.PostgresEventsRedacted, rewrapped,
				report.After.PlaintextConfigs, report.After.PlaintextVolumeBuckets, report.After.PostgresEvents, report.CutoverEnabled)
			return err
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "override connection string (defaults to database.postgres.dsn)")
	cmd.Flags().IntVar(&batchSize, "batch", 100, "configs per scan batch")
	cmd.Flags().BoolVar(&inventoryOnly, "inventory-only", false, "report aggregate exposure counts without changing data")
	return cmd
}

func legacyInventoryIsClean(inventory storagecredentials.LegacyInventory) bool {
	return inventory.PlaintextConfigs == 0 && inventory.IncompleteConfigs == 0 &&
		inventory.PlaintextVolumeBuckets == 0 && inventory.IncompleteVolumeBuckets == 0 && inventory.PostgresEvents == 0
}
