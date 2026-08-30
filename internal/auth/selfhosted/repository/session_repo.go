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
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
)

// SessionRepository handles database operations for sessions
type SessionRepository struct {
	db *sqlx.DB
}

// NewSessionRepository creates a new session repository
func NewSessionRepository(db *sqlx.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create creates a new session
func (r *SessionRepository) Create(ctx context.Context, userID uuid.UUID, expiresIn time.Duration, ipAddress, userAgent *string) (*domain.Session, error) {
	session := &domain.Session{
		ID:        uuid.New(),
		UserID:    userID,
		Token:     generateSessionToken(),
		ExpiresAt: time.Now().Add(expiresIn),
		CreatedAt: time.Now(),
		IPAddress: ipAddress,
		UserAgent: userAgent,
	}

	query := `
		INSERT INTO everstack.sessions (
			id, user_id, token, expires_at, created_at, ip_address, user_agent
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.Token, session.ExpiresAt,
		session.CreatedAt, session.IPAddress, session.UserAgent,
	)
	if err != nil {
		return nil, err
	}

	return session, nil
}

// GetByToken retrieves a session by token
func (r *SessionRepository) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	query := `SELECT * FROM everstack.sessions WHERE token = $1`
	var session domain.Session
	err := r.db.GetContext(ctx, &session, query, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &session, err
}

// Delete deletes a session
func (r *SessionRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM everstack.sessions WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// DeleteByToken deletes a session by token
func (r *SessionRepository) DeleteByToken(ctx context.Context, token string) error {
	query := `DELETE FROM everstack.sessions WHERE token = $1`
	_, err := r.db.ExecContext(ctx, query, token)
	return err
}

// DeleteByUserID deletes all sessions for a user
func (r *SessionRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM everstack.sessions WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// DeleteExpired deletes all expired sessions
func (r *SessionRepository) DeleteExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM everstack.sessions WHERE expires_at < $1`
	result, err := r.db.ExecContext(ctx, query, time.Now())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetByID retrieves a session by ID
func (r *SessionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Session, error) {
	query := `SELECT * FROM everstack.sessions WHERE id = $1`
	var session domain.Session
	err := r.db.GetContext(ctx, &session, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &session, err
}

// generateSessionToken generates a cryptographically secure session token
func generateSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
