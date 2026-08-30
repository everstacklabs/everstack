package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// NonceStore defines the interface for nonce storage and replay prevention
type NonceStore interface {
	// CheckAndStore attempts to store a nonce. Returns ErrNonceReused if the nonce
	// already exists (indicating a potential replay attack).
	CheckAndStore(ctx context.Context, nonce, clientID string, ttl time.Duration) error

	// Cleanup removes expired nonces from the store
	Cleanup(ctx context.Context) error
}

// PostgresNonceStore implements NonceStore using PostgreSQL for distributed replay protection.
// This ensures nonce uniqueness across multiple service instances.
type PostgresNonceStore struct {
	db  *sqlx.DB
	ttl time.Duration
}

// NewPostgresNonceStore creates a new PostgreSQL-backed nonce store
func NewPostgresNonceStore(db *sqlx.DB, defaultTTL time.Duration) *PostgresNonceStore {
	if defaultTTL <= 0 {
		defaultTTL = 10 * time.Minute
	}
	return &PostgresNonceStore{
		db:  db,
		ttl: defaultTTL,
	}
}

// CheckAndStore attempts to insert a nonce into the database.
// If the nonce already exists (unique constraint violation), it returns ErrNonceReused.
func (s *PostgresNonceStore) CheckAndStore(ctx context.Context, nonce, clientID string, ttl time.Duration) error {
	if s.db == nil {
		return errors.New("nonce store database not initialized")
	}

	if ttl <= 0 {
		ttl = s.ttl
	}

	// Use INSERT with conflict detection via unique constraint
	query := `
		INSERT INTO m2m_nonces (nonce, client_id, expires_at, created_at)
		VALUES ($1, $2, NOW() + $3::interval, NOW())
	`
	intervalStr := fmt.Sprintf("%d seconds", int(ttl.Seconds()))

	_, err := s.db.ExecContext(ctx, query, nonce, clientID, intervalStr)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrNonceReused
		}
		return fmt.Errorf("failed to store nonce: %w", err)
	}

	return nil
}

// Cleanup removes all expired nonces from the database
func (s *PostgresNonceStore) Cleanup(ctx context.Context) error {
	if s.db == nil {
		return errors.New("nonce store database not initialized")
	}

	query := `DELETE FROM m2m_nonces WHERE expires_at < NOW()`
	result, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired nonces: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		logger.Debugf("m2m_nonce: cleaned up %d expired nonces", rowsAffected)
	}

	return nil
}

// StartCleanupWorker starts a background goroutine that periodically cleans up expired nonces
func (s *PostgresNonceStore) StartCleanupWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Run initial cleanup
		if err := s.Cleanup(ctx); err != nil {
			logger.WithError(err).Warn("m2m_nonce: initial cleanup failed")
		}

		for {
			select {
			case <-ctx.Done():
				logger.Info("m2m_nonce: cleanup worker stopped")
				return
			case <-ticker.C:
				if err := s.Cleanup(context.Background()); err != nil {
					logger.WithError(err).Warn("m2m_nonce: periodic cleanup failed")
				}
			}
		}
	}()

	logger.Infof("m2m_nonce: cleanup worker started (interval: %v)", interval)
}

// isUniqueViolation checks if an error is a PostgreSQL unique constraint violation
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	// Check for PostgreSQL unique violation error code (23505)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}

	return false
}

// NoopNonceStore is a nonce store that does nothing (for testing or when replay protection is disabled)
type NoopNonceStore struct{}

func NewNoopNonceStore() *NoopNonceStore {
	return &NoopNonceStore{}
}

func (s *NoopNonceStore) CheckAndStore(ctx context.Context, nonce, clientID string, ttl time.Duration) error {
	return nil
}

func (s *NoopNonceStore) Cleanup(ctx context.Context) error {
	return nil
}

// EnsureNonceTableExists creates the m2m_nonces table if it doesn't exist.
// This is a fallback for when migrations haven't been run.
func EnsureNonceTableExists(ctx context.Context, db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS m2m_nonces (
			nonce TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_m2m_nonces_expires_at ON m2m_nonces(expires_at);
		CREATE INDEX IF NOT EXISTS idx_m2m_nonces_client_id ON m2m_nonces(client_id);
	`

	_, err := db.ExecContext(ctx, query)
	return err
}


