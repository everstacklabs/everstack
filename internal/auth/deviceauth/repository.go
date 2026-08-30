package deviceauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type Status string

const (
	DefaultPollInterval = 5 * time.Second

	StatusPending    Status = "pending"
	StatusAuthorized Status = "authorized"
	StatusDenied     Status = "denied"
	StatusConsumed   Status = "consumed"
)

type Session struct {
	ID                  uuid.UUID `db:"id"`
	DeviceCode          string
	UserCode            string     `db:"user_code"`
	ClientID            string     `db:"client_id"`
	Scope               string     `db:"scope"`
	Status              Status     `db:"status"`
	UserID              *uuid.UUID `db:"user_id"`
	OrgID               *uuid.UUID `db:"org_id"`
	ExpiresAt           time.Time  `db:"expires_at"`
	CreatedAt           time.Time  `db:"created_at"`
	LastPolledAt        *time.Time `db:"last_polled_at"`
	PollIntervalSeconds int        `db:"poll_interval_seconds"`
}

var (
	ErrSessionNotFound      = errors.New("device authorization session not found")
	ErrAuthorizationPending = errors.New("device authorization pending")
	ErrAuthorizationDenied  = errors.New("device authorization denied")
	ErrSessionExpired       = errors.New("device authorization session expired")
	ErrSessionConsumed      = errors.New("device authorization session already consumed")
	ErrClientMismatch       = errors.New("device authorization client mismatch")
	ErrSlowDown             = errors.New("device authorization polling too quickly")
)

type Store interface {
	Create(ctx context.Context, clientID, scope string, ttl time.Duration) (*Session, error)
	Redeem(ctx context.Context, deviceCode, clientID string, issue func(*Session) error) error
	GetByUserCode(ctx context.Context, userCode string) (*Session, error)
	Approve(ctx context.Context, userCode string, userID, orgID uuid.UUID) error
	Deny(ctx context.Context, userCode string) error
	CleanupExpired(ctx context.Context) (int64, error)
}

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, clientID, scope string, ttl time.Duration) (*Session, error) {
	deviceCode, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate device code: %w", err)
	}
	userCode, err := generateUserCode()
	if err != nil {
		return nil, fmt.Errorf("generate user code: %w", err)
	}

	session := &Session{
		ID:         uuid.New(),
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ClientID:   clientID,
		Scope:      scope,
		Status:     StatusPending,
		ExpiresAt:  time.Now().UTC().Add(ttl),
	}
	err = r.db.QueryRowxContext(ctx, `
		INSERT INTO device_authorization_sessions (
			id, device_code, user_code, client_id, scope, status, expires_at, last_polled_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		RETURNING created_at, last_polled_at
	`, session.ID, deviceCodeDigest(session.DeviceCode), session.UserCode, session.ClientID, session.Scope, session.Status, session.ExpiresAt).
		Scan(&session.CreatedAt, &session.LastPolledAt)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// Redeem atomically exchanges an authorized device session. The row lock
// ensures that concurrent token requests cannot both exchange the same bearer
// code. The issue callback runs while the row is locked; returning an error
// rolls the transaction back so a transient identity or signing failure does
// not burn the code without delivering a token.
// The raw-code lookup remains temporarily supported for sessions created by a
// previous binary; new sessions are always stored as SHA-256 digests.
func (r *Repository) Redeem(ctx context.Context, deviceCode, clientID string, issue func(*Session) error) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var session Session
	err = tx.GetContext(ctx, &session, `
		SELECT id, user_code, client_id, scope, status, user_id, org_id, expires_at, created_at,
		       last_polled_at, poll_interval_seconds
		FROM device_authorization_sessions
		WHERE device_code = $1 OR device_code = $2
		FOR UPDATE
	`, deviceCodeDigest(deviceCode), deviceCode)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return err
	}
	if clientID != session.ClientID {
		return ErrClientMismatch
	}

	now := time.Now().UTC()
	if !now.Before(session.ExpiresAt) {
		return ErrSessionExpired
	}

	switch session.Status {
	case StatusPending:
		if session.pollingTooQuickly(now) {
			return ErrSlowDown
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE device_authorization_sessions
			SET last_polled_at = $1
			WHERE id = $2
		`, now, session.ID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrAuthorizationPending
	case StatusDenied:
		return ErrAuthorizationDenied
	case StatusConsumed:
		return ErrSessionConsumed
	case StatusAuthorized:
		if session.pollingTooQuickly(now) {
			return ErrSlowDown
		}
		if issue == nil {
			return errors.New("device authorization redemption requires an issuer")
		}
		if err := issue(&session); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE device_authorization_sessions
			SET status = 'consumed', last_polled_at = $1
			WHERE id = $2 AND status = 'authorized'
		`, now, session.ID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrSessionConsumed
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown device authorization status %q", session.Status)
	}
}

func (r *Repository) GetByUserCode(ctx context.Context, userCode string) (*Session, error) {
	var session Session
	err := r.db.GetContext(ctx, &session, `
		SELECT id, user_code, client_id, scope, status, user_id, org_id, expires_at, created_at,
		       last_polled_at, poll_interval_seconds
		FROM device_authorization_sessions
		WHERE REPLACE(UPPER(user_code), '-', '') = $1
	`, normalizeUserCode(userCode))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *Repository) Approve(ctx context.Context, userCode string, userID, orgID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE device_authorization_sessions
		SET status = 'authorized', user_id = $1, org_id = $2
		WHERE REPLACE(UPPER(user_code), '-', '') = $3
		  AND status = 'pending'
		  AND expires_at > NOW()
	`, userID, orgID, normalizeUserCode(userCode))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (r *Repository) Deny(ctx context.Context, userCode string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE device_authorization_sessions
		SET status = 'denied'
		WHERE REPLACE(UPPER(user_code), '-', '') = $1
		  AND status = 'pending'
	`, normalizeUserCode(userCode))
	return err
}

func (r *Repository) CleanupExpired(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM device_authorization_sessions
		WHERE expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func normalizeUserCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func deviceCodeDigest(code string) string {
	sum := sha256.Sum256([]byte(code))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *Session) pollingTooQuickly(now time.Time) bool {
	if s.LastPolledAt == nil {
		return false
	}
	interval := time.Duration(s.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return now.Before(s.LastPolledAt.Add(interval))
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateUserCode() (string, error) {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	code := make([]byte, 8)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		code[i] = chars[n.Int64()]
	}
	return fmt.Sprintf("%s-%s", string(code[:4]), string(code[4:])), nil
}
