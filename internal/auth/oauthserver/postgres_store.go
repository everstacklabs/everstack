package oauthserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const refreshTokenTTL = 30 * 24 * time.Hour

// PostgresStore persists OAuth grants without storing raw codes or refresh tokens.
type PostgresStore struct {
	db  *sqlx.DB
	now func() time.Time
}

// NewPostgresStore creates an OAuth grant store backed by PostgreSQL.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db, now: time.Now}
}

// CreateAuthorizationCode stores a digest of a new one-time authorization code.
func (s *PostgresStore) CreateAuthorizationCode(ctx context.Context, grant AuthorizationGrant, ttl time.Duration) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("oauth authorization code store is not configured")
	}
	if err := s.pruneExpired(ctx); err != nil {
		return "", fmt.Errorf("prune expired OAuth grants: %w", err)
	}
	code, err := randomOpaqueToken(32)
	if err != nil {
		return "", fmt.Errorf("generate authorization code: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_codes (
			id, code_hash, client_id, redirect_uri, scope, code_challenge,
			user_id, user_email, org_id, org_slug, instance_id, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`,
		uuid.New(),
		tokenDigest(code),
		grant.ClientID,
		grant.RedirectURI,
		grant.Scope,
		grant.CodeChallenge,
		grant.Identity.UserID,
		grant.Identity.Email,
		grant.Identity.OrganizationID,
		grant.Identity.OrganizationSlug,
		grant.Identity.InstanceID,
		s.now().UTC().Add(ttl),
	)
	if err != nil {
		return "", err
	}
	return code, nil
}

type authorizationCodeRecord struct {
	ID            uuid.UUID  `db:"id"`
	ClientID      string     `db:"client_id"`
	RedirectURI   string     `db:"redirect_uri"`
	Scope         string     `db:"scope"`
	CodeChallenge string     `db:"code_challenge"`
	UserID        string     `db:"user_id"`
	UserEmail     string     `db:"user_email"`
	OrgID         string     `db:"org_id"`
	OrgSlug       string     `db:"org_slug"`
	InstanceID    string     `db:"instance_id"`
	ExpiresAt     time.Time  `db:"expires_at"`
	ConsumedAt    *time.Time `db:"consumed_at"`
}

// RedeemAuthorizationCode atomically consumes a code and creates a refresh family.
func (s *PostgresStore) RedeemAuthorizationCode(
	ctx context.Context,
	code string,
	clientID string,
	redirectURI string,
	verifier string,
	instanceID string,
	issue IssueAccessToken,
) (*TokenSet, error) {
	if s == nil || s.db == nil || issue == nil {
		return nil, errors.New("oauth authorization code redemption is not configured")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var record authorizationCodeRecord
	err = tx.GetContext(ctx, &record, `
		SELECT id, client_id, redirect_uri, scope, code_challenge,
		       user_id, user_email, org_id, org_slug, instance_id, expires_at, consumed_at
		FROM oauth_authorization_codes
		WHERE code_hash = $1
		FOR UPDATE
	`, tokenDigest(code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	if record.ConsumedAt != nil || !now.Before(record.ExpiresAt) ||
		record.ClientID != clientID || record.RedirectURI != redirectURI ||
		record.InstanceID != instanceID ||
		!matchesPKCEChallenge(verifier, record.CodeChallenge) {
		return nil, ErrInvalidGrant
	}

	access, err := issue(Identity{
		UserID:           record.UserID,
		Email:            record.UserEmail,
		OrganizationID:   record.OrgID,
		OrganizationSlug: record.OrgSlug,
		InstanceID:       record.InstanceID,
	}, record.ClientID)
	if err != nil {
		return nil, err
	}

	refresh, err := s.insertRefreshToken(ctx, tx, refreshTokenRecord{
		FamilyID: uuid.New(),
		ClientID: record.ClientID,
		Scope:    record.Scope,
		Identity: Identity{
			UserID:           record.UserID,
			Email:            record.UserEmail,
			OrganizationID:   record.OrgID,
			OrganizationSlug: record.OrgSlug,
			InstanceID:       record.InstanceID,
		},
		ExpiresAt: now.Add(refreshTokenTTL),
	})
	if err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE oauth_authorization_codes
		SET consumed_at = NOW()
		WHERE id = $1 AND consumed_at IS NULL
	`, record.ID)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ErrInvalidGrant
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TokenSet{
		AccessToken:  access.Token,
		RefreshToken: refresh,
		ExpiresAt:    access.ExpiresAt,
		Scope:        record.Scope,
	}, nil
}

type refreshTokenRecord struct {
	ID         uuid.UUID  `db:"id"`
	FamilyID   uuid.UUID  `db:"family_id"`
	ClientID   string     `db:"client_id"`
	Scope      string     `db:"scope"`
	UserID     string     `db:"user_id"`
	UserEmail  string     `db:"user_email"`
	OrgID      string     `db:"org_id"`
	OrgSlug    string     `db:"org_slug"`
	InstanceID string     `db:"instance_id"`
	ExpiresAt  time.Time  `db:"expires_at"`
	RotatedAt  *time.Time `db:"rotated_at"`
	RevokedAt  *time.Time `db:"revoked_at"`
	ReplacedBy *string    `db:"replaced_by_hash"`
	Identity   Identity
}

// RotateRefreshToken atomically replaces a refresh token and detects replay.
func (s *PostgresStore) RotateRefreshToken(
	ctx context.Context,
	rawToken string,
	clientID string,
	instanceID string,
	authorize AuthorizeRefresh,
	issue IssueAccessToken,
) (*TokenSet, error) {
	if s == nil || s.db == nil || authorize == nil || issue == nil {
		return nil, errors.New("oauth refresh token rotation is not configured")
	}
	var preliminary refreshTokenRecord
	err := s.db.GetContext(ctx, &preliminary, `
		SELECT id, family_id, client_id, scope, user_id, user_email, org_id, org_slug, instance_id,
		       expires_at, rotated_at, revoked_at, replaced_by_hash
		FROM oauth_refresh_tokens
		WHERE token_hash = $1
	`, tokenDigest(rawToken))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	if preliminary.RotatedAt != nil || preliminary.RevokedAt != nil {
		if err := s.revokeFamilyByID(ctx, preliminary.FamilyID); err != nil {
			return nil, err
		}
		return nil, ErrInvalidGrant
	}
	if !now.Before(preliminary.ExpiresAt) || preliminary.ClientID != clientID ||
		preliminary.InstanceID != instanceID {
		return nil, ErrInvalidGrant
	}
	identity := Identity{
		UserID:           preliminary.UserID,
		Email:            preliminary.UserEmail,
		OrganizationID:   preliminary.OrgID,
		OrganizationSlug: preliminary.OrgSlug,
		InstanceID:       preliminary.InstanceID,
	}
	// Refresh authorization may query the same database pool. Run it before
	// opening the rotation transaction so a single-connection pool cannot
	// deadlock waiting for itself.
	if err := authorize(ctx, identity); err != nil {
		if !errors.Is(err, ErrAccessDenied) {
			return nil, err
		}
		if err := s.revokeFamilyByID(ctx, preliminary.FamilyID); err != nil {
			return nil, err
		}
		return nil, ErrInvalidGrant
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockRefreshFamily(ctx, tx, preliminary.FamilyID); err != nil {
		return nil, err
	}

	var record refreshTokenRecord
	err = tx.GetContext(ctx, &record, `
		SELECT id, family_id, client_id, scope, user_id, user_email, org_id, org_slug, instance_id,
		       expires_at, rotated_at, revoked_at, replaced_by_hash
		FROM oauth_refresh_tokens
		WHERE token_hash = $1
		FOR UPDATE
	`, tokenDigest(rawToken))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}

	if record.RotatedAt != nil || record.RevokedAt != nil {
		if err := s.revokeFamily(ctx, tx, record.FamilyID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrInvalidGrant
	}
	if !now.Before(record.ExpiresAt) || record.ClientID != clientID ||
		record.InstanceID != instanceID {
		return nil, ErrInvalidGrant
	}

	identity = Identity{
		UserID:           record.UserID,
		Email:            record.UserEmail,
		OrganizationID:   record.OrgID,
		OrganizationSlug: record.OrgSlug,
		InstanceID:       record.InstanceID,
	}
	access, err := issue(identity, record.ClientID)
	if err != nil {
		return nil, err
	}
	refresh, err := s.insertRefreshToken(ctx, tx, refreshTokenRecord{
		FamilyID:  record.FamilyID,
		ClientID:  record.ClientID,
		Scope:     record.Scope,
		Identity:  identity,
		ExpiresAt: record.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE oauth_refresh_tokens
		SET rotated_at = NOW(), replaced_by_hash = $1
		WHERE id = $2 AND rotated_at IS NULL AND revoked_at IS NULL
	`, tokenDigest(refresh), record.ID)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ErrInvalidGrant
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TokenSet{
		AccessToken:  access.Token,
		RefreshToken: refresh,
		ExpiresAt:    access.ExpiresAt,
		Scope:        record.Scope,
	}, nil
}

func (s *PostgresStore) insertRefreshToken(ctx context.Context, tx *sqlx.Tx, record refreshTokenRecord) (string, error) {
	token, err := randomOpaqueToken(48)
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO oauth_refresh_tokens (
			id, token_hash, family_id, client_id, scope,
			user_id, user_email, org_id, org_slug, instance_id, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		record.ID,
		tokenDigest(token),
		record.FamilyID,
		record.ClientID,
		record.Scope,
		record.Identity.UserID,
		record.Identity.Email,
		record.Identity.OrganizationID,
		record.Identity.OrganizationSlug,
		record.Identity.InstanceID,
		record.ExpiresAt,
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// RevokeRefreshToken revokes the entire family containing the supplied token.
// Unknown and cross-instance tokens are deliberately treated as success.
func (s *PostgresStore) RevokeRefreshToken(
	ctx context.Context,
	rawToken string,
	clientID string,
	instanceID string,
) error {
	if s == nil || s.db == nil {
		return errors.New("oauth refresh token revocation is not configured")
	}
	var familyID uuid.UUID
	err := s.db.GetContext(ctx, &familyID, `
		SELECT family_id
		FROM oauth_refresh_tokens
		WHERE token_hash = $1 AND client_id = $2 AND instance_id = $3
		LIMIT 1
	`, tokenDigest(rawToken), clientID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.revokeFamilyByID(ctx, familyID)
}

func (s *PostgresStore) revokeFamily(ctx context.Context, tx *sqlx.Tx, familyID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE oauth_refresh_tokens
		SET revoked_at = COALESCE(revoked_at, NOW())
		WHERE family_id = $1
	`, familyID)
	return err
}

func (s *PostgresStore) revokeFamilyByID(ctx context.Context, familyID uuid.UUID) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockRefreshFamily(ctx, tx, familyID); err != nil {
		return err
	}
	if err := s.revokeFamily(ctx, tx, familyID); err != nil {
		return err
	}
	return tx.Commit()
}

func lockRefreshFamily(ctx context.Context, tx *sqlx.Tx, familyID uuid.UUID) error {
	_, err := tx.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		familyID.String(),
	)
	return err
}

func (s *PostgresStore) pruneExpired(ctx context.Context) error {
	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM oauth_authorization_codes WHERE expires_at < NOW()`,
	); err != nil {
		return err
	}
	_, err := s.db.ExecContext(
		ctx,
		`DELETE FROM oauth_refresh_tokens WHERE expires_at < NOW()`,
	)
	return err
}

func matchesPKCEChallenge(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return len(got) == len(challenge) &&
		subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomOpaqueToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
