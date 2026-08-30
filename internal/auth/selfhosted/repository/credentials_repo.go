package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// UserCredential represents stored password credentials for a user
type UserCredential struct {
	UserID       uuid.UUID `db:"user_id"`
	PasswordHash string    `db:"password_hash"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// CredentialsRepository handles database operations for user credentials
type CredentialsRepository struct {
	db *sqlx.DB
}

// NewCredentialsRepository creates a new credentials repository
func NewCredentialsRepository(db *sqlx.DB) *CredentialsRepository {
	return &CredentialsRepository{db: db}
}

// Create creates credentials for a user
func (r *CredentialsRepository) Create(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	now := time.Now()
	query := `
		INSERT INTO everstack.user_credentials (user_id, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
	`
	_, err := r.db.ExecContext(ctx, query, userID, passwordHash, now, now)
	return err
}

// GetByUserID retrieves credentials for a user
func (r *CredentialsRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*UserCredential, error) {
	query := `SELECT * FROM everstack.user_credentials WHERE user_id = $1`
	var cred UserCredential
	err := r.db.GetContext(ctx, &cred, query, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &cred, err
}

// Update updates the password hash for a user
func (r *CredentialsRepository) Update(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	query := `
		UPDATE everstack.user_credentials
		SET password_hash = $2, updated_at = $3
		WHERE user_id = $1
	`
	_, err := r.db.ExecContext(ctx, query, userID, passwordHash, time.Now())
	return err
}

// Delete deletes credentials for a user
func (r *CredentialsRepository) Delete(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM everstack.user_credentials WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// Exists checks if credentials exist for a user
func (r *CredentialsRepository) Exists(ctx context.Context, userID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM everstack.user_credentials WHERE user_id = $1)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, userID)
	return exists, err
}
