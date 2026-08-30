package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/ssh"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// UserSSHKey represents a stored user SSH public key.
type UserSSHKey struct {
	ID          int64   `db:"id"          json:"id"`
	UserID      string  `db:"user_id"     json:"userId"`
	TenantID    string  `db:"tenant_id"   json:"tenantId"`
	Name        string  `db:"name"        json:"name"`
	PublicKey   string  `db:"public_key"  json:"publicKey"`
	Fingerprint string  `db:"fingerprint" json:"fingerprint"`
	KeyType     string  `db:"key_type"    json:"keyType"`
	LastUsedAt  *string `db:"last_used_at" json:"lastUsedAt,omitempty"`
	CreatedAt   string  `db:"created_at"  json:"createdAt"`
}

// SSHAccess represents a per-sandbox SSH access grant.
type SSHAccess struct {
	ID        int64  `db:"id"         json:"id"`
	SandboxID string `db:"sandbox_id" json:"sandboxId"`
	UserID    string `db:"user_id"    json:"userId"`
	TenantID  string `db:"tenant_id"  json:"tenantId"`
	GrantedBy string `db:"granted_by" json:"grantedBy"`
	CreatedAt string `db:"created_at" json:"createdAt"`
}

// KeyStore manages SSH key and access persistence.
type KeyStore struct {
	db *sqlx.DB
}

// NewKeyStore creates a new KeyStore.
func NewKeyStore(db *sqlx.DB) *KeyStore {
	return &KeyStore{db: db}
}

// ParsePublicKey parses a raw SSH public key and returns its type and fingerprint.
func ParsePublicKey(raw string) (keyType string, fingerprint string, err error) {
	pubKey, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(raw))
	if parseErr != nil {
		return "", "", fmt.Errorf("invalid SSH public key: %w", parseErr)
	}
	return pubKey.Type(), ssh.FingerprintSHA256(pubKey), nil
}

// GenerateGatewayHostKey generates an Ed25519 host key at the given path
// if it doesn't already exist. Returns the signer.
func GenerateGatewayHostKey(path string) (ssh.Signer, error) {
	if _, err := os.Stat(path); err == nil {
		return LoadGatewayHostKey(path)
	}

	// Generate new key
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("failed to create host key dir: %w", err)
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	pemBytes := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to write host key: %w", err)
	}

	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}

	logger.WithFields("path", path).Info("ssh: generated new gateway host key")
	return signer, nil
}

// LoadGatewayHostKey loads an existing gateway host key without generating a
// replacement. Use this when a stable key is mounted from a Kubernetes Secret.
func LoadGatewayHostKey(path string) (ssh.Signer, error) {
	keyBytes, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read host key: %w", readErr)
	}
	signer, signErr := ssh.ParsePrivateKey(keyBytes)
	if signErr != nil {
		return nil, fmt.Errorf("failed to parse host key: %w", signErr)
	}
	logger.Info("ssh: loaded existing gateway host key")
	return signer, nil
}

// ─── Key CRUD ──────────────────────────────────────────────────────────

// AddKey stores a new user SSH key.
func (s *KeyStore) AddKey(ctx context.Context, userID, tenantID, name, publicKey string) (*UserSSHKey, error) {
	keyType, fingerprint, err := ParsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}

	var key UserSSHKey
	err = s.db.QueryRowxContext(ctx, `
		INSERT INTO user_ssh_keys (user_id, tenant_id, name, public_key, fingerprint, key_type)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, tenant_id, name, public_key, fingerprint, key_type, last_used_at, created_at`,
		userID, tenantID, name, publicKey, fingerprint, keyType,
	).StructScan(&key)
	if err != nil {
		return nil, fmt.Errorf("failed to add SSH key: %w", err)
	}
	return &key, nil
}

// ListKeys returns all SSH keys for a user within a tenant.
//
// The same cloud user can belong to multiple orgs/tenants and add SSH keys
// in each; without the tenant filter this would return the user's keys
// across every tenant they're a member of, leaking sandbox-access metadata
// (key names, fingerprints, last_used timestamps) between tenants.
func (s *KeyStore) ListKeys(ctx context.Context, userID, tenantID string) ([]UserSSHKey, error) {
	var keys []UserSSHKey
	err := s.db.SelectContext(ctx, &keys, `
		SELECT id, user_id, tenant_id, name, public_key, fingerprint, key_type, last_used_at, created_at
		FROM user_ssh_keys
		WHERE user_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC`, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list SSH keys: %w", err)
	}
	return keys, nil
}

// ListKeysByTenant returns all SSH keys within a tenant regardless of user.
//
// The WHERE tenant_id = $1 filter is belt-and-suspenders: the user_ssh_keys
// table is per-tenant-schema (the migration is not in tenantExcludedPrefixes)
// and the tenant-aware DB pool sets search_path from request context, so the
// unqualified table name resolves into the calling tenant's schema. The
// explicit filter makes the safety property survive a context misconfiguration
// or a future caller using a non-tenant-aware DB handle.
func (s *KeyStore) ListKeysByTenant(ctx context.Context, tenantID string) ([]UserSSHKey, error) {
	var keys []UserSSHKey
	err := s.db.SelectContext(ctx, &keys, `
		SELECT id, user_id, tenant_id, name, public_key, fingerprint, key_type, last_used_at, created_at
		FROM user_ssh_keys
		WHERE tenant_id = $1
		ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list SSH keys by tenant: %w", err)
	}
	return keys, nil
}

// DeleteKey removes an SSH key by ID, scoped to tenant and owner. Both
// scopes are checked in SQL: id-only or user-only would let one tenant
// delete another tenant's key by id collision, or let an admin-style
// caller cross tenants by passing the right user_id.
func (s *KeyStore) DeleteKey(ctx context.Context, keyID int64, tenantID, userID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM user_ssh_keys
		WHERE id = $1 AND user_id = $2 AND tenant_id = $3`, keyID, userID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete SSH key: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("SSH key not found")
	}
	return nil
}

// DeleteKeyByTenant removes an SSH key by ID scoped to tenant only.
// Any authenticated user within the tenant can delete any key in that tenant.
//
// Same defensive note as ListKeysByTenant: per-tenant schema isolation is the
// primary safety, the explicit WHERE clause is the backup.
func (s *KeyStore) DeleteKeyByTenant(ctx context.Context, keyID int64, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM user_ssh_keys
		WHERE id = $1 AND tenant_id = $2`, keyID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete SSH key: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("SSH key not found")
	}
	return nil
}

// LookupKeyByFingerprint finds a key by fingerprint within a tenant. The
// (tenant_id, fingerprint) unique index makes this an exact-match lookup
// scoped to the calling tenant; without the tenant filter, a fingerprint
// collision between tenants — or a malicious caller probing for known
// fingerprints — would return another tenant's key row, leaking user_id and
// public_key plus enabling cross-tenant authentication on shared SSH
// surfaces.
func (s *KeyStore) LookupKeyByFingerprint(ctx context.Context, tenantID, fingerprint string) (*UserSSHKey, error) {
	var key UserSSHKey
	err := s.db.QueryRowxContext(ctx, `
		SELECT id, user_id, tenant_id, name, public_key, fingerprint, key_type, last_used_at, created_at
		FROM user_ssh_keys
		WHERE tenant_id = $1 AND fingerprint = $2`, tenantID, fingerprint).StructScan(&key)
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// LookupKeysByFingerprint returns every tenant-scoped key row matching a public
// key fingerprint. SSH proxy public-key auth uses this before sandbox lookup so
// it can resolve username collisions inside each candidate tenant instead of
// performing an unscoped sandbox lookup first.
func (s *KeyStore) LookupKeysByFingerprint(ctx context.Context, fingerprint string) ([]UserSSHKey, error) {
	var keys []UserSSHKey
	err := s.db.SelectContext(ctx, &keys, `
		SELECT id, user_id, tenant_id, name, public_key, fingerprint, key_type, last_used_at, created_at
		FROM user_ssh_keys
		WHERE fingerprint = $1
		ORDER BY created_at DESC`, fingerprint)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// TouchKeyLastUsed updates the last_used_at timestamp for a key.
//
// Caller must have validated tenant ownership before invoking — the id-only
// WHERE is safe because (a) ids are tenant-schema-scoped (BIGSERIAL per
// tenant schema) and (b) any caller path here has already passed
// LookupKeyByFingerprint or similar tenant-scoped resolution.
func (s *KeyStore) TouchKeyLastUsed(ctx context.Context, keyID int64) {
	_, _ = s.db.ExecContext(ctx, `
		UPDATE user_ssh_keys SET last_used_at = NOW() WHERE id = $1`, keyID)
}

// ─── Access CRUD ────────────────────────────────────────────────────────

// GrantAccess grants SSH access to a sandbox for a user.
func (s *KeyStore) GrantAccess(ctx context.Context, sandboxID, userID, tenantID, grantedBy string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sandbox_ssh_access (sandbox_id, user_id, tenant_id, granted_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (sandbox_id, user_id) DO NOTHING`,
		sandboxID, userID, tenantID, grantedBy)
	if err != nil {
		return fmt.Errorf("failed to grant SSH access: %w", err)
	}
	return nil
}

// RevokeAccess revokes SSH access from a user for a sandbox.
func (s *KeyStore) RevokeAccess(ctx context.Context, sandboxID, userID, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM sandbox_ssh_access
		WHERE sandbox_id = $1 AND user_id = $2 AND tenant_id = $3`,
		sandboxID, userID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to revoke SSH access: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("access grant not found")
	}
	return nil
}

// CheckAccess checks if a user has SSH access to a sandbox. This is the
// authoritative gate at SSH-connection time — without the tenant filter, a
// matching (sandbox_id, user_id) pair from a different tenant would grant
// access. Sandbox IDs are unique enough that cross-tenant collision is
// unlikely in practice, but unlikely is not the same as impossible, and
// "auth says yes when it shouldn't" is the worst class of bug.
func (s *KeyStore) CheckAccess(ctx context.Context, sandboxID, userID, tenantID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sandbox_ssh_access
		WHERE sandbox_id = $1 AND user_id = $2 AND tenant_id = $3`,
		sandboxID, userID, tenantID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListAccessBySandbox returns all access grants for a sandbox.
// The tenant filter prevents leaking grant rows (user_id, granted_by) from
// a sandbox in another tenant that happens to share an id.
func (s *KeyStore) ListAccessBySandbox(ctx context.Context, sandboxID, tenantID string) ([]SSHAccess, error) {
	var grants []SSHAccess
	err := s.db.SelectContext(ctx, &grants, `
		SELECT id, sandbox_id, user_id, tenant_id, granted_by, created_at
		FROM sandbox_ssh_access
		WHERE sandbox_id = $1 AND tenant_id = $2
		ORDER BY created_at`, sandboxID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list access grants: %w", err)
	}
	return grants, nil
}
