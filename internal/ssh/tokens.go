package ssh

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	sshTokenPrefix         = "ssht_"
	sshTokenIDPrefix       = "sshtok_"
	sshTokenRandBytes      = 24
	defaultSSHTokenMinutes = 60
	maxSSHTokenMinutes     = 24 * 60
)

// SandboxSSHToken is a stored, non-secret view of a short-lived SSH token.
// The raw token is never persisted; token_hash stores sha256(raw_token).
type SandboxSSHToken struct {
	ID             string         `db:"id" json:"id"`
	OrganizationID string         `db:"organization_id" json:"organizationId"`
	TenantID       string         `db:"tenant_id" json:"tenantId"`
	InstanceID     string         `db:"instance_id" json:"instanceId"`
	SandboxID      string         `db:"sandbox_id" json:"sandboxId"`
	TokenPrefix    string         `db:"token_prefix" json:"tokenPrefix"`
	CreatedBy      string         `db:"created_by" json:"createdBy"`
	ExpiresAt      time.Time      `db:"expires_at" json:"expiresAt"`
	RevokedAt      sql.NullTime   `db:"revoked_at" json:"revokedAt,omitempty"`
	LastUsedAt     sql.NullTime   `db:"last_used_at" json:"lastUsedAt,omitempty"`
	LastUsedIP     sql.NullString `db:"last_used_ip" json:"lastUsedIp,omitempty"`
	CreatedAt      time.Time      `db:"created_at" json:"createdAt"`
}

type SandboxSSHTokenScope struct {
	OrganizationID string
	TenantID       string
	InstanceID     string
	SandboxID      string
}

// IsSandboxSSHTokenValue reports whether a string looks like an SSH bearer token.
func IsSandboxSSHTokenValue(v string) bool {
	return strings.HasPrefix(strings.TrimSpace(v), sshTokenPrefix)
}

// NormalizeSSHTokenMinutes applies product limits to a requested token lifetime.
func NormalizeSSHTokenMinutes(minutes int32) int32 {
	if minutes <= 0 {
		return defaultSSHTokenMinutes
	}
	if minutes > maxSSHTokenMinutes {
		return maxSSHTokenMinutes
	}
	return minutes
}

// CreateSandboxSSHToken creates a short-lived bearer token and returns the raw
// token exactly once alongside the stored metadata.
func (s *KeyStore) CreateSandboxSSHToken(ctx context.Context, scope SandboxSSHTokenScope, createdBy string, expiresInMinutes int32) (*SandboxSSHToken, string, error) {
	minutes := NormalizeSSHTokenMinutes(expiresInMinutes)
	rawToken, err := generateOpaqueToken(sshTokenPrefix)
	if err != nil {
		return nil, "", err
	}
	id, err := generateOpaqueToken(sshTokenIDPrefix)
	if err != nil {
		return nil, "", err
	}
	expiresAt := time.Now().UTC().Add(time.Duration(minutes) * time.Minute)

	var token SandboxSSHToken
	err = s.db.QueryRowxContext(ctx, `
		INSERT INTO sandbox_ssh_tokens (id, organization_id, tenant_id, instance_id, sandbox_id, token_hash, token_prefix, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, organization_id, tenant_id, instance_id, sandbox_id, token_prefix, created_by, expires_at, revoked_at, last_used_at, last_used_ip, created_at`,
		id, scope.OrganizationID, scope.TenantID, scope.InstanceID, scope.SandboxID, hashSSHToken(rawToken), tokenDisplayPrefix(rawToken), createdBy, expiresAt,
	).StructScan(&token)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create SSH token: %w", err)
	}
	return &token, rawToken, nil
}

// ListSandboxSSHTokens lists non-expired token records for a sandbox. Revoked
// rows are included so the UI can show recent revoke state without raw secrets.
func (s *KeyStore) ListSandboxSSHTokens(ctx context.Context, scope SandboxSSHTokenScope) ([]SandboxSSHToken, error) {
	var tokens []SandboxSSHToken
	err := s.db.SelectContext(ctx, &tokens, `
		SELECT id, organization_id, tenant_id, instance_id, sandbox_id, token_prefix, created_by, expires_at, revoked_at, last_used_at, last_used_ip, created_at
		FROM sandbox_ssh_tokens
		WHERE organization_id = $1 AND tenant_id = $2 AND instance_id = $3 AND sandbox_id = $4 AND expires_at > NOW()
		ORDER BY created_at DESC`, scope.OrganizationID, scope.TenantID, scope.InstanceID, scope.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to list SSH tokens: %w", err)
	}
	return tokens, nil
}

// RevokeSandboxSSHToken revokes one token for a sandbox.
func (s *KeyStore) RevokeSandboxSSHToken(ctx context.Context, scope SandboxSSHTokenScope, tokenID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sandbox_ssh_tokens
		SET revoked_at = COALESCE(revoked_at, NOW())
		WHERE id = $1 AND organization_id = $2 AND tenant_id = $3 AND instance_id = $4 AND sandbox_id = $5`,
		tokenID, scope.OrganizationID, scope.TenantID, scope.InstanceID, scope.SandboxID)
	if err != nil {
		return fmt.Errorf("failed to revoke SSH token: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("SSH token not found")
	}
	return nil
}

// LookupActiveSandboxSSHToken validates a raw token and returns its metadata.
func (s *KeyStore) LookupActiveSandboxSSHToken(ctx context.Context, rawToken string) (*SandboxSSHToken, error) {
	if !IsSandboxSSHTokenValue(rawToken) {
		return nil, fmt.Errorf("invalid SSH token")
	}
	var token SandboxSSHToken
	err := s.db.QueryRowxContext(ctx, `
		SELECT id, organization_id, tenant_id, instance_id, sandbox_id, token_prefix, created_by, expires_at, revoked_at, last_used_at, last_used_ip, created_at
		FROM sandbox_ssh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()`, hashSSHToken(rawToken)).StructScan(&token)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// TouchSandboxSSHToken records token use after a successful SSH handshake.
func (s *KeyStore) TouchSandboxSSHToken(ctx context.Context, tokenID, remoteIP string) {
	_, _ = s.db.ExecContext(ctx, `
		UPDATE sandbox_ssh_tokens
		SET last_used_at = NOW(), last_used_ip = $2
		WHERE id = $1`, tokenID, remoteIP)
}

func generateOpaqueToken(prefix string) (string, error) {
	b := make([]byte, sshTokenRandBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func hashSSHToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func tokenDisplayPrefix(rawToken string) string {
	if len(rawToken) <= 16 {
		return rawToken
	}
	return rawToken[:16]
}
