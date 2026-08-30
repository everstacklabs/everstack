package provider_api_keys

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// PostgresRepository implements the Repository interface using PostgreSQL
type PostgresRepository struct {
	db *sqlx.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(db *sqlx.DB) Repository {
	return &PostgresRepository{db: db}
}

// dbProviderAPIKey is the database model
type dbProviderAPIKey struct {
	ID               string    `db:"id"`
	ProviderConfigID string    `db:"provider_config_id"`
	KeyName          string    `db:"key_name"`
	KeyEncrypted     string    `db:"key_encrypted"`
	Weight           int       `db:"weight"`
	IsActive         bool      `db:"is_active"`
	Source           string    `db:"source"`
	RateLimitData    []byte    `db:"rate_limit_tracking"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

// ListByProviderConfig returns all API keys for a provider configuration
func (r *PostgresRepository) ListByProviderConfig(ctx context.Context, providerConfigID string) ([]*ProviderAPIKey, error) {
	query := `
		SELECT id, provider_config_id, key_name, key_encrypted, weight, is_active, 
		       COALESCE(source, 'manual') as source, rate_limit_tracking, created_at, updated_at
		FROM provider_api_keys
		WHERE provider_config_id = $1
		ORDER BY weight DESC, created_at ASC
	`

	var dbKeys []dbProviderAPIKey
	if err := r.db.SelectContext(ctx, &dbKeys, query, providerConfigID); err != nil {
		if err == sql.ErrNoRows {
			return []*ProviderAPIKey{}, nil
		}
		return nil, fmt.Errorf("failed to list provider API keys: %w", err)
	}

	keys := make([]*ProviderAPIKey, len(dbKeys))
	for i, dbKey := range dbKeys {
		key, err := r.fromDB(&dbKey)
		if err != nil {
			return nil, fmt.Errorf("failed to convert key %s: %w", dbKey.ID, err)
		}
		keys[i] = key
	}

	return keys, nil
}

// Create creates a new API key
func (r *PostgresRepository) Create(ctx context.Context, key *ProviderAPIKey) error {
	dbKey, err := r.toDB(key)
	if err != nil {
		return fmt.Errorf("failed to convert key for insert: %w", err)
	}

	// Default source to 'manual' if not set
	if dbKey.Source == "" {
		dbKey.Source = "manual"
	}

	query := `
		INSERT INTO provider_api_keys 
		(id, provider_config_id, key_name, key_encrypted, weight, is_active, source, rate_limit_tracking, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err = r.db.ExecContext(ctx, query,
		dbKey.ID,
		dbKey.ProviderConfigID,
		dbKey.KeyName,
		dbKey.KeyEncrypted,
		dbKey.Weight,
		dbKey.IsActive,
		dbKey.Source,
		dbKey.RateLimitData,
		dbKey.CreatedAt,
		dbKey.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create provider API key: %w", err)
	}

	return nil
}

// Update updates an existing API key
func (r *PostgresRepository) Update(ctx context.Context, key *ProviderAPIKey) error {
	key.UpdatedAt = time.Now()
	dbKey, err := r.toDB(key)
	if err != nil {
		return fmt.Errorf("failed to convert key for update: %w", err)
	}

	query := `
		UPDATE provider_api_keys
		SET key_name = $2, key_encrypted = $3, weight = $4, is_active = $5, 
		    rate_limit_tracking = $6, updated_at = $7
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		dbKey.ID,
		dbKey.KeyName,
		dbKey.KeyEncrypted,
		dbKey.Weight,
		dbKey.IsActive,
		dbKey.RateLimitData,
		dbKey.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update provider API key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("provider API key not found: %s", key.ID)
	}

	return nil
}

// Delete deletes an API key by ID
func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM provider_api_keys WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete provider API key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("provider API key not found: %s", id)
	}

	return nil
}

// UpdateRateLimitData updates the rate limit tracking data for a key
func (r *PostgresRepository) UpdateRateLimitData(ctx context.Context, keyID string, data map[string]interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal rate limit data: %w", err)
	}

	query := `
		UPDATE provider_api_keys
		SET rate_limit_tracking = $2, updated_at = $3
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, keyID, jsonData, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update rate limit data: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("provider API key not found: %s", keyID)
	}

	return nil
}

// GetByID retrieves a single API key by ID
func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*ProviderAPIKey, error) {
	query := `
		SELECT id, provider_config_id, key_name, key_encrypted, weight, is_active, 
		       COALESCE(source, 'manual') as source, rate_limit_tracking, created_at, updated_at
		FROM provider_api_keys
		WHERE id = $1
	`

	var dbKey dbProviderAPIKey
	if err := r.db.GetContext(ctx, &dbKey, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider API key not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get provider API key: %w", err)
	}

	return r.fromDB(&dbKey)
}

// toDB converts domain model to database model
func (r *PostgresRepository) toDB(key *ProviderAPIKey) (*dbProviderAPIKey, error) {
	var rateLimitData []byte
	var err error

	if key.RateLimitData != nil {
		rateLimitData, err = json.Marshal(key.RateLimitData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal rate limit data: %w", err)
		}
	} else {
		rateLimitData = []byte("{}")
	}

	source := key.Source
	if source == "" {
		source = "manual"
	}

	return &dbProviderAPIKey{
		ID:               key.ID,
		ProviderConfigID: key.ProviderConfigID,
		KeyName:          key.KeyName,
		KeyEncrypted:     key.KeyEncrypted,
		Weight:           key.Weight,
		IsActive:         key.IsActive,
		Source:           source,
		RateLimitData:    rateLimitData,
		CreatedAt:        key.CreatedAt,
		UpdatedAt:        key.UpdatedAt,
	}, nil
}

// fromDB converts database model to domain model
func (r *PostgresRepository) fromDB(dbKey *dbProviderAPIKey) (*ProviderAPIKey, error) {
	var rateLimitData map[string]interface{}
	if len(dbKey.RateLimitData) > 0 {
		if err := json.Unmarshal(dbKey.RateLimitData, &rateLimitData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal rate limit data: %w", err)
		}
	}

	return &ProviderAPIKey{
		ID:               dbKey.ID,
		ProviderConfigID: dbKey.ProviderConfigID,
		KeyName:          dbKey.KeyName,
		KeyEncrypted:     dbKey.KeyEncrypted,
		Weight:           dbKey.Weight,
		IsActive:         dbKey.IsActive,
		Source:           dbKey.Source,
		RateLimitData:    rateLimitData,
		CreatedAt:        dbKey.CreatedAt,
		UpdatedAt:        dbKey.UpdatedAt,
	}, nil
}

// UpsertConfigKey upserts a config-sourced API key (special handling)
// configKeyDuplicatesManual reports whether a user-managed key already holds
// this exact credential for the provider config. `source` is not decoration:
// only "config" inference is metered against the wallet
// (inference_meter.platformKeySource), so a config row carrying a key the user
// supplied themselves both clutters the key list and bills BYOK traffic as
// platform traffic.
func (r *PostgresRepository) configKeyDuplicatesManual(ctx context.Context, key *ProviderAPIKey) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM provider_api_keys
			WHERE provider_config_id = $1
			  AND source = 'manual'
			  AND key_encrypted = $2
		)
	`
	var duplicate bool
	if err := r.db.GetContext(ctx, &duplicate, query, key.ProviderConfigID, key.KeyEncrypted); err != nil {
		return false, fmt.Errorf("failed to check for a duplicate manual key: %w", err)
	}
	return duplicate, nil
}

// dropDuplicateConfigKeys removes config rows for this provider config whose
// credential a manual row already holds. It covers the case where the duplicate
// was written before this guard existed, or where the user added their key
// manually after a config row had already been seeded with it.
func (r *PostgresRepository) dropDuplicateConfigKeys(ctx context.Context, providerConfigID string) error {
	const query = `
		DELETE FROM provider_api_keys AS config_key
		USING provider_api_keys AS manual_key
		WHERE config_key.provider_config_id = $1
		  AND manual_key.provider_config_id = config_key.provider_config_id
		  AND config_key.key_encrypted = manual_key.key_encrypted
		  AND config_key.source = 'config'
		  AND manual_key.source = 'manual'
	`
	if _, err := r.db.ExecContext(ctx, query, providerConfigID); err != nil {
		return fmt.Errorf("failed to drop duplicate config API keys: %w", err)
	}
	return nil
}

func (r *PostgresRepository) UpsertConfigKey(ctx context.Context, key *ProviderAPIKey) error {
	if err := r.dropDuplicateConfigKeys(ctx, key.ProviderConfigID); err != nil {
		return err
	}
	duplicate, err := r.configKeyDuplicatesManual(ctx, key)
	if err != nil {
		return err
	}
	if duplicate {
		// The user already manages this credential. Seeding a second,
		// platform-owned row for it would be the "Config API Key" that shows
		// up alongside every key someone adds through the UI.
		return ErrConfigKeyDuplicatesManual
	}

	// Upsert: update if exists with same provider_config_id and source='config', otherwise insert
	query := `
		INSERT INTO provider_api_keys 
		(id, provider_config_id, key_name, key_encrypted, weight, is_active, source, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'config', $7, $8)
		ON CONFLICT (provider_config_id, key_name) 
		DO UPDATE SET
			key_encrypted = EXCLUDED.key_encrypted,
			weight = EXCLUDED.weight,
			is_active = EXCLUDED.is_active,
			source = 'config',
			updated_at = EXCLUDED.updated_at
		WHERE provider_api_keys.source = 'config'
		   OR provider_api_keys.key_name = 'Config API Key'
		RETURNING id, created_at, updated_at
	`

	now := time.Now()
	if key.ID == "" {
		// Generate a proper UUID
		newID, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("failed to generate UUID: %w", err)
		}
		key.ID = newID.String()
	}

	var returned struct {
		ID        string    `db:"id"`
		CreatedAt time.Time `db:"created_at"`
		UpdatedAt time.Time `db:"updated_at"`
	}

	if err := r.db.GetContext(ctx, &returned, query,
		key.ID, key.ProviderConfigID, key.KeyName, key.KeyEncrypted,
		key.Weight, key.IsActive, now, now,
	); err != nil {
		return fmt.Errorf("failed to upsert config API key: %w", err)
	}

	key.ID = returned.ID
	key.CreatedAt = returned.CreatedAt
	key.UpdatedAt = returned.UpdatedAt
	key.Source = "config"

	return nil
}

// DeactivateConfigKeys marks all config-sourced keys as inactive for a provider
func (r *PostgresRepository) DeactivateConfigKeys(ctx context.Context, providerConfigID string) error {
	query := `
		UPDATE provider_api_keys
		SET is_active = false, updated_at = $1
		WHERE provider_config_id = $2 AND source = 'config'
	`

	_, err := r.db.ExecContext(ctx, query, time.Now(), providerConfigID)
	if err != nil {
		return fmt.Errorf("failed to deactivate config keys: %w", err)
	}

	return nil
}
