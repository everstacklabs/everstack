// Package backfill hosts server-side data backfill commands. These need a
// real database connection (DSN resolution mirrors cmd/migrate), unlike the
// remote Connect-RPC CLI under cmd/everstack-eval.
package backfill

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/eval_runner"
)

// New returns the hidden `backfill-eval-hashes` maintenance command, which
// populates eval_run_items.input_canonical/input_hash for rows created before
// the hash-substrate migration. Idempotent (NULL-hash rows only), batched,
// tenant-agnostic.
func New() *cobra.Command {
	// Match the server/CLI log formatting used by cmd/migrate.
	logger.SetFormatter(logger.NewDefaultBracketFormatter())
	logger.SetGlobal()

	var dsn string
	var batchSize int

	cmd := &cobra.Command{
		Use:    "backfill-eval-hashes",
		Short:  "Populate input_canonical/input_hash on pre-migration eval_run_items rows",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			updated, err := eval_runner.BackfillInputHashes(ctx, conn.RW, batchSize)
			if err != nil {
				return fmt.Errorf("backfill failed after updating %d rows: %w", updated, err)
			}
			fmt.Printf("backfill-eval-hashes: %d rows updated\n", updated)
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "override connection string (defaults to database.postgres.dsn)")
	cmd.Flags().IntVar(&batchSize, "batch", 500, "rows per transaction")
	return cmd
}

// resolvePostgresDSN mirrors cmd/migrate's resolution order:
// explicit flag, then EVS_DATABASE_POSTGRES_DSN, then viper config.
func resolvePostgresDSN(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if env := os.Getenv("EVS_DATABASE_POSTGRES_DSN"); strings.TrimSpace(env) != "" {
		return env
	}
	return viper.GetString("database.postgres.dsn")
}
