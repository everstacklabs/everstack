package credentials

import (
	"context"
	"fmt"
)

// RewrapCredentials decrypts live records with their recorded key and
// re-encrypts them with the active key. References and authenticated tenant
// binding remain stable, so callers can rotate the master key without downtime.
func (s *PostgresStore) RewrapCredentials(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	activeKeyID := s.cipher.activeKeyID
	updated := 0
	for {
		var records []struct {
			ID         string `db:"id"`
			TenantID   string `db:"tenant_id"`
			Ciphertext []byte `db:"ciphertext"`
			KeyID      string `db:"key_id"`
		}
		if err := s.db.SelectContext(ctx, &records, `
			SELECT id, tenant_id, ciphertext, key_id
			FROM object_storage_credentials
			WHERE backend = 'postgres' AND revoked_at IS NULL AND key_id <> $1
			ORDER BY tenant_id, id
			LIMIT $2
		`, activeKeyID, batchSize); err != nil {
			return updated, fmt.Errorf("list storage credentials for key rotation: %w", err)
		}
		if len(records) == 0 {
			return updated, nil
		}

		for _, record := range records {
			plaintext, err := s.cipher.Open(record.TenantID, record.ID, record.KeyID, record.Ciphertext)
			if err != nil {
				return updated, fmt.Errorf("open storage credential during key rotation: %w", err)
			}
			ciphertext, keyID, err := s.cipher.Seal(record.TenantID, record.ID, plaintext)
			if err != nil {
				return updated, fmt.Errorf("seal storage credential during key rotation: %w", err)
			}
			result, err := s.db.ExecContext(ctx, `
				UPDATE object_storage_credentials
				SET ciphertext = $1, key_id = $2, rotated_at = NOW()
				WHERE id = $3 AND tenant_id = $4 AND backend = 'postgres' AND key_id = $5 AND revoked_at IS NULL
			`, ciphertext, keyID, record.ID, record.TenantID, record.KeyID)
			if err != nil {
				return updated, fmt.Errorf("persist rotated storage credential: %w", err)
			}
			if rows, err := result.RowsAffected(); err == nil {
				updated += int(rows)
			}
		}
	}
}
