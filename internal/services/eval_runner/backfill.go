package eval_runner

import (
	"context"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// BackfillInputHashes populates input_canonical/input_hash on eval_run_items
// rows created before the hash-substrate migration, by re-joining the current
// dataset_items input. It is an admin/maintenance operation over ALL tenants
// and is idempotent: it only touches rows where input_hash IS NULL.
//
// Note the freeze semantics caveat: backfilled rows hash the CURRENT dataset
// item input, not the input as it was when the run executed. That is the best
// available approximation for pre-migration rows (the historical input was
// never stored) and is exactly why new rows freeze the canonical at INSERT.
//
// Rows whose input fails to canonicalize are logged and left NULL. Keyset
// pagination on id (VARCHAR) guarantees such rows are skipped rather than
// re-fetched forever.
func BackfillInputHashes(ctx context.Context, db *sqlx.DB, batchSize int) (updated int, err error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	type itemRow struct {
		ID    string `db:"id"`
		Input []byte `db:"input"`
	}

	lastID := ""
	for {
		var rows []itemRow
		if err := db.SelectContext(ctx, &rows, `
			SELECT eri.id, di.input
			FROM eval_run_items eri
			JOIN dataset_items di ON di.id = eri.dataset_item_id
			WHERE eri.input_hash IS NULL AND eri.id > $1
			ORDER BY eri.id ASC
			LIMIT $2
		`, lastID, batchSize); err != nil {
			return updated, err
		}
		if len(rows) == 0 {
			logger.Infof("backfill-eval-hashes: done, %d rows updated", updated)
			return updated, nil
		}

		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return updated, err
		}
		batchUpdated := 0
		for _, row := range rows {
			lastID = row.ID
			canonical, hash, cerr := CanonicalizeInput(row.Input)
			if cerr != nil {
				logger.Warnf("backfill-eval-hashes: canonicalize input for eval_run_item %s failed, leaving NULL: %v", row.ID, cerr)
				continue
			}
			res, err := tx.ExecContext(ctx, `
				UPDATE eval_run_items
				SET input_canonical = $2, input_hash = $3, updated_at = NOW()
				WHERE id = $1 AND input_hash IS NULL
			`, row.ID, canonical, hash)
			if err != nil {
				_ = tx.Rollback()
				return updated, err
			}
			if n, err := res.RowsAffected(); err == nil {
				batchUpdated += int(n)
			}
		}
		if err := tx.Commit(); err != nil {
			return updated, err
		}
		updated += batchUpdated
		logger.Infof("backfill-eval-hashes: batch of %d scanned, %d updated (total %d)", len(rows), batchUpdated, updated)
	}
}
