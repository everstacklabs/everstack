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
	Token     string     `db:"token"` // raw token from Create; the stored SHA-256 hex when read back
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
	_, err = r.db.ExecContext(ctx, query, ml.ID, ml.Email, hashToken(ml.Token), ml.ExpiresAt, ml.CreatedAt)
	if err != nil {
		return nil, err
	}

	return ml, nil
}

// GetByToken retrieves a magic link token
func (r *MagicLinkRepository) GetByToken(ctx context.Context, token string) (*MagicLinkToken, error) {
	query := `SELECT * FROM everstack.magic_link_tokens WHERE token = $1`
	var ml MagicLinkToken
	err := r.db.GetContext(ctx, &ml, query, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &ml, err
}

// Consume atomically marks a valid (unused, unexpired) magic link token as
// used and returns it, or returns nil when the token is unknown, expired or
// already used. It is a single UPDATE, so of any number of concurrent
// verifications of the same link exactly one gets the token; reading the
// token and marking it used in separate statements let two of them through.
func (r *MagicLinkRepository) Consume(ctx context.Context, token string) (*MagicLinkToken, error) {
	query := `
		UPDATE everstack.magic_link_tokens
		   SET used_at = NOW()
		 WHERE token = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING id, email, token, expires_at, used_at, created_at
	`
	var ml MagicLinkToken
	err := r.db.GetContext(ctx, &ml, query, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ml, nil
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
