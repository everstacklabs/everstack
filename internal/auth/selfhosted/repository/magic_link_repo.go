package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// MagicLinkToken represents a magic link token for passwordless login
type MagicLinkToken struct {
	ID        uuid.UUID  `db:"id"`
	Email     string     `db:"email"`
	Token     string     `db:"token"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// MagicLinkRepository handles database operations for magic link tokens
type MagicLinkRepository struct {
	db *sqlx.DB
}

// NewMagicLinkRepository creates a new magic link repository
func NewMagicLinkRepository(db *sqlx.DB) *MagicLinkRepository {
	return &MagicLinkRepository{db: db}
}

// Create creates a new magic link token
func (r *MagicLinkRepository) Create(ctx context.Context, email string, expiresIn time.Duration) (*MagicLinkToken, error) {
	token, err := generateMagicToken()
	if err != nil {
		return nil, err
	}

	ml := &MagicLinkToken{
		ID:        uuid.New(),
		Email:     email,
		Token:     token,
		ExpiresAt: time.Now().Add(expiresIn),
		CreatedAt: time.Now(),
	}

	query := `
		INSERT INTO everstack.magic_link_tokens (id, email, token, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err = r.db.ExecContext(ctx, query, ml.ID, ml.Email, ml.Token, ml.ExpiresAt, ml.CreatedAt)
	if err != nil {
		return nil, err
	}

	return ml, nil
}

// GetByToken retrieves a magic link token
func (r *MagicLinkRepository) GetByToken(ctx context.Context, token string) (*MagicLinkToken, error) {
	query := `SELECT * FROM everstack.magic_link_tokens WHERE token = $1`
	var ml MagicLinkToken
	err := r.db.GetContext(ctx, &ml, query, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &ml, err
}

// GetValidByToken retrieves a valid (unused, not expired) magic link token
func (r *MagicLinkRepository) GetValidByToken(ctx context.Context, token string) (*MagicLinkToken, error) {
	query := `
		SELECT * FROM everstack.magic_link_tokens 
		WHERE token = $1 AND used_at IS NULL AND expires_at > NOW()
	`
	var ml MagicLinkToken
	err := r.db.GetContext(ctx, &ml, query, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &ml, err
}

// MarkUsed marks a magic link token as used
func (r *MagicLinkRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE everstack.magic_link_tokens SET used_at = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, time.Now())
	return err
}

// DeleteByEmail deletes all magic link tokens for an email
func (r *MagicLinkRepository) DeleteByEmail(ctx context.Context, email string) error {
	query := `DELETE FROM everstack.magic_link_tokens WHERE email = $1`
	_, err := r.db.ExecContext(ctx, query, email)
	return err
}

// DeleteExpired deletes all expired magic link tokens
func (r *MagicLinkRepository) DeleteExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM everstack.magic_link_tokens WHERE expires_at < $1`
	result, err := r.db.ExecContext(ctx, query, time.Now())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// generateMagicToken generates a secure random token for magic links
func generateMagicToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
