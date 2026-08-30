package credentials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var (
	ErrCredentialNotFound = errors.New("storage credential reference not found")
	ErrStoreNotConfigured = errors.New("storage credential store is not configured")
)

// ProviderCredentials is the secret material required by the current
// S3-compatible adapters. It is deliberately separate from connection
// metadata and must never be serialized into CQRS events or API responses.
type ProviderCredentials struct {
	AccessKeyID     string `db:"access_key_id" json:"access_key_id"`
	SecretAccessKey string `db:"secret_access_key" json:"secret_access_key"`
	ProviderTokenID string `db:"provider_token_id" json:"provider_token_id,omitempty"`
}

func (c ProviderCredentials) Validate() error {
	if strings.TrimSpace(c.AccessKeyID) == "" {
		return fmt.Errorf("storage access key id is required")
	}
	if strings.TrimSpace(c.SecretAccessKey) == "" {
		return fmt.Errorf("storage secret access key is required")
	}
	return nil
}

// Store persists provider credentials behind opaque, tenant-scoped references.
type Store interface {
	Put(ctx context.Context, tenantID string, credentials ProviderCredentials) (reference string, err error)
	Resolve(ctx context.Context, tenantID, reference string) (ProviderCredentials, error)
	Revoke(ctx context.Context, tenantID, reference string) error
}

// PostgresStore is the default secret backend. PostgreSQL stores only
// authenticated ciphertext and a key id; encryption keys stay outside the DB.
type PostgresStore struct {
	db     *sqlx.DB
	cipher *EnvelopeCipher
}

func NewPostgresStore(db *sqlx.DB, cipher *EnvelopeCipher) (*PostgresStore, error) {
	if db == nil || cipher == nil {
		return nil, ErrStoreNotConfigured
	}
	return &PostgresStore{db: db, cipher: cipher}, nil
}

func NewConfiguredPostgresStore(db *sqlx.DB) (*PostgresStore, error) {
	config, err := LoadKeyringConfig()
	if err != nil {
		return nil, err
	}
	cipher, err := NewEnvelopeCipherFromConfig(config)
	if err != nil {
		return nil, err
	}
	return NewPostgresStore(db, cipher)
}

func (s *PostgresStore) Put(ctx context.Context, tenantID string, credentials ProviderCredentials) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", fmt.Errorf("storage credential tenant is required")
	}
	reference := "storagecred_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ciphertext, keyID, err := s.seal(tenantID, reference, credentials)
	if err != nil {
		return "", err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO object_storage_credentials (id, tenant_id, backend, ciphertext, key_id, created_at)
		VALUES ($1, $2, 'postgres', $3, $4, NOW())
	`, reference, tenantID, ciphertext, keyID)
	if err != nil {
		return "", fmt.Errorf("persist storage credential reference: %w", err)
	}
	return reference, nil
}

func (s *PostgresStore) seal(tenantID, reference string, credentials ProviderCredentials) ([]byte, string, error) {
	if err := credentials.Validate(); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(credentials)
	if err != nil {
		return nil, "", fmt.Errorf("encode storage credentials: %w", err)
	}
	return s.cipher.Seal(tenantID, reference, payload)
}

func (s *PostgresStore) Resolve(ctx context.Context, tenantID, reference string) (ProviderCredentials, error) {
	tenantID = strings.TrimSpace(tenantID)
	reference = strings.TrimSpace(reference)
	var record struct {
		Ciphertext []byte `db:"ciphertext"`
		KeyID      string `db:"key_id"`
	}
	err := s.db.GetContext(ctx, &record, `
		SELECT ciphertext, key_id FROM object_storage_credentials
		WHERE id = $1 AND tenant_id = $2 AND backend = 'postgres' AND revoked_at IS NULL
	`, reference, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderCredentials{}, ErrCredentialNotFound
	}
	if err != nil {
		return ProviderCredentials{}, fmt.Errorf("resolve storage credential reference: %w", err)
	}

	payload, err := s.cipher.Open(tenantID, reference, record.KeyID, record.Ciphertext)
	if err != nil {
		return ProviderCredentials{}, err
	}
	var credentials ProviderCredentials
	if err := json.Unmarshal(payload, &credentials); err != nil {
		return ProviderCredentials{}, fmt.Errorf("decode storage credential payload")
	}
	if err := credentials.Validate(); err != nil {
		return ProviderCredentials{}, fmt.Errorf("stored credential payload is invalid")
	}
	return credentials, nil
}

func (s *PostgresStore) Revoke(ctx context.Context, tenantID, reference string) error {
	tenantID = strings.TrimSpace(tenantID)
	reference = strings.TrimSpace(reference)
	result, err := s.db.ExecContext(ctx, `
		UPDATE object_storage_credentials
		SET revoked_at = COALESCE(revoked_at, NOW()), ciphertext = '\x'::bytea, key_id = 'revoked'
		WHERE id = $1 AND tenant_id = $2 AND backend = 'postgres'
		  AND NOT EXISTS (
			SELECT 1 FROM object_storage_configs
			WHERE credential_ref = $1 AND tenant_id = $2
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM tenant_volume_buckets
			WHERE credential_ref = $1 AND tenant_id = $2
		  )
	`, reference, tenantID)
	if err != nil {
		return fmt.Errorf("revoke storage credential reference: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("verify storage credential revocation: %w", err)
	}
	if rows == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

var _ Store = (*PostgresStore)(nil)
