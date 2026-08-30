// Package volstore provisions per-tenant Cloudflare R2 buckets + long-lived,
// bucket-scoped S3 credentials that back sandbox volumes (issue #317, slice 2).
//
// Model: one R2 bucket per tenant; a volume is a prefix (volumes/{id}/) within
// it. Isolation is enforced at the S3 layer — the credential stored for a
// tenant is scoped to that one bucket, so a token leaked from a (root) sandbox
// reaches only that tenant's own data. Long-lived (no TTL) to avoid the FUSE
// credential-refresh problem (s3fs/mount-s3 don't re-mint; temp creds 403
// mid-session). Persistent rows contain only an opaque reference to the
// tenant-bound encrypted credential payload.
//
// Cloudflare API contract (verified against developers.cloudflare.com):
//   - create bucket:  POST /accounts/{acct}/r2/buckets
//   - perm groups:    GET  /accounts/{acct}/iam/permission_groups
//   - create token:   POST /accounts/{acct}/tokens  (resources scoped to the bucket)
//   - delete token:   DELETE /accounts/{acct}/tokens/{id}
//   - S3 creds:       AccessKeyID = token.id; SecretAccessKey = sha256hex(token.value)
//   - S3 endpoint:    https://{acct}.r2.cloudflarestorage.com  (region "auto")
package volstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/jmoiron/sqlx"
)

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// TenantBucket is a tenant's resolved volume storage location + credentials.
type TenantBucket struct {
	TenantID        string `db:"tenant_id"`
	BucketName      string `db:"bucket_name"`
	Endpoint        string `db:"endpoint"`
	AccessKeyID     string `db:"access_key_id"`
	SecretAccessKey string `db:"secret_access_key"`
	CFTokenID       string `db:"cf_token_id"`
	CredentialRef   string `db:"credential_ref"`
}

// Config holds the Cloudflare account + parent credentials used to provision.
type Config struct {
	AccountID string // Cloudflare account ID
	APIToken  string // parent CF API token (account R2 admin + token create/list)
	// Endpoint is the R2 S3 endpoint; defaults to https://{AccountID}.r2.cloudflarestorage.com.
	Endpoint string
}

// Provisioner lazily provisions + caches per-tenant R2 buckets and tokens.
type Provisioner struct {
	db          *sqlx.DB
	http        *http.Client
	cfg         Config
	credentials storagecredentials.Store

	mu        sync.Mutex
	readPGID  string
	writePGID string
}

// New returns a Provisioner, or nil when Cloudflare config is incomplete
// (self-hosted / R2 not configured). Callers treat nil as "volumes not
// mountable" and skip the everstack-volume rewrite.
func New(db *sqlx.DB, cfg Config, credentialStores ...storagecredentials.Store) *Provisioner {
	if db == nil || cfg.AccountID == "" || cfg.APIToken == "" {
		return nil
	}
	var credentialStore storagecredentials.Store
	if len(credentialStores) > 0 {
		credentialStore = credentialStores[0]
	} else {
		configured, err := storagecredentials.NewConfiguredPostgresStore(db)
		if err != nil {
			return nil
		}
		credentialStore = configured
	}
	if credentialStore == nil {
		return nil
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}
	return &Provisioner{
		db:          db,
		http:        &http.Client{Timeout: 30 * time.Second},
		cfg:         cfg,
		credentials: credentialStore,
	}
}

// Resolve returns the tenant's bucket + credentials, provisioning them on first
// use. Idempotent: a stored row short-circuits the Cloudflare calls.
func (p *Provisioner) Resolve(ctx context.Context, tenantID string) (*TenantBucket, error) {
	if p == nil {
		return nil, fmt.Errorf("volstore: provisioner not configured")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("volstore: empty tenantID")
	}

	if existing, err := p.lookup(ctx, tenantID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	enabled, err := storagecredentials.CredentialCutoverEnabled(ctx, p.credentials)
	if err != nil {
		return nil, fmt.Errorf("volstore: read credential cutover: %w", err)
	}
	if !enabled {
		return nil, fmt.Errorf("volstore: storage credential cutover is not enabled")
	}

	bucket := BucketName(tenantID)
	if err := p.createBucket(ctx, bucket); err != nil {
		return nil, fmt.Errorf("volstore: create bucket: %w", err)
	}
	tokenID, secret, err := p.createBucketToken(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("volstore: create token: %w", err)
	}

	tb := &TenantBucket{
		TenantID:        tenantID,
		BucketName:      bucket,
		Endpoint:        p.cfg.Endpoint,
		AccessKeyID:     tokenID,
		SecretAccessKey: secret,
		CFTokenID:       tokenID,
	}
	if err := p.store(ctx, tb); err != nil {
		// The token was minted before the database write. Revoke it on every
		// failed persistence path so retries cannot accumulate live orphans.
		_ = p.DeleteToken(ctx, tokenID)
		// Row may have been created concurrently; prefer the stored copy.
		if existing, lerr := p.lookup(ctx, tenantID); lerr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("volstore: persist: %w", err)
	}
	return tb, nil
}

func (p *Provisioner) lookup(ctx context.Context, tenantID string) (*TenantBucket, error) {
	var tb TenantBucket
	err := p.db.GetContext(ctx, &tb,
		`SELECT tenant_id, bucket_name, endpoint, COALESCE(credential_ref, '') AS credential_ref
		   FROM tenant_volume_buckets WHERE tenant_id = $1`, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	credentials, reference, err := storagecredentials.ResolveVolumeCredentials(ctx, p.credentials, tenantID, tb.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("volstore: resolve credentials: %w", err)
	}
	tb.CredentialRef = reference
	tb.AccessKeyID = credentials.AccessKeyID
	tb.SecretAccessKey = credentials.SecretAccessKey
	tb.CFTokenID = credentials.ProviderTokenID
	if tb.CFTokenID == "" {
		tb.CFTokenID = tb.AccessKeyID
	}
	return &tb, nil
}

func (p *Provisioner) store(ctx context.Context, tb *TenantBucket) error {
	reference, err := p.credentials.Put(ctx, tb.TenantID, storagecredentials.ProviderCredentials{
		AccessKeyID: tb.AccessKeyID, SecretAccessKey: tb.SecretAccessKey, ProviderTokenID: tb.CFTokenID,
	})
	if err != nil {
		return fmt.Errorf("store encrypted credentials: %w", err)
	}
	tb.CredentialRef = reference
	result, err := p.db.ExecContext(ctx,
		`INSERT INTO tenant_volume_buckets
		   (tenant_id, bucket_name, endpoint, access_key_id, secret_access_key, cf_token_id, credential_ref, created_at, updated_at)
		 VALUES ($1,$2,$3,'','','',$4,NOW(),NOW())
		 ON CONFLICT (tenant_id) DO NOTHING`,
		tb.TenantID, tb.BucketName, tb.Endpoint, reference)
	if err != nil {
		_ = p.credentials.Revoke(ctx, tb.TenantID, reference)
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		_ = p.credentials.Revoke(ctx, tb.TenantID, reference)
		return fmt.Errorf("tenant bucket already exists")
	}
	return nil
}

// BucketName derives a deterministic, R2-valid bucket name from a tenant id
// (3-63 chars, lowercase alphanumeric + hyphens). Hash-based so arbitrary
// tenant ids (UUIDs, instance ids) always yield a legal, collision-resistant name.
func BucketName(tenantID string) string {
	sum := sha256.Sum256([]byte(tenantID))
	return "evs-vol-" + hex.EncodeToString(sum[:])[:24]
}

// deriveSecretAccessKey is the R2 rule: Secret Access Key = SHA-256 hex of the
// token's value.
func deriveSecretAccessKey(tokenValue string) string {
	sum := sha256.Sum256([]byte(tokenValue))
	return hex.EncodeToString(sum[:])
}

// --- Cloudflare API calls ---

func (p *Provisioner) createBucket(ctx context.Context, bucket string) error {
	body := map[string]string{"name": bucket}
	var resp struct {
		Success bool         `json:"success"`
		Errors  []cfAPIError `json:"errors"`
	}
	status, err := p.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/accounts/%s/r2/buckets", p.cfg.AccountID), body, &resp)
	if err != nil {
		return err
	}
	// 409 / "already exists" is fine — provisioning is idempotent.
	if resp.Success || status == http.StatusConflict || cfErrorsContain(resp.Errors, "already exists", 10004) {
		return nil
	}
	return fmt.Errorf("status %d: %s", status, cfErrorString(resp.Errors))
}

func (p *Provisioner) createBucketToken(ctx context.Context, bucket string) (tokenID, secret string, err error) {
	readPG, writePG, err := p.permissionGroups(ctx)
	if err != nil {
		return "", "", err
	}
	resourceKey := fmt.Sprintf("com.cloudflare.edge.r2.bucket.%s_default_%s", p.cfg.AccountID, bucket)
	body := map[string]any{
		"name": "everstack-vol-" + bucket,
		"policies": []map[string]any{{
			"effect": "allow",
			"permission_groups": []map[string]string{
				{"id": readPG},
				{"id": writePG},
			},
			"resources": map[string]string{resourceKey: "*"},
		}},
	}
	var resp struct {
		Success bool         `json:"success"`
		Errors  []cfAPIError `json:"errors"`
		Result  struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"result"`
	}
	status, err := p.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/accounts/%s/tokens", p.cfg.AccountID), body, &resp)
	if err != nil {
		return "", "", err
	}
	if !resp.Success || resp.Result.ID == "" || resp.Result.Value == "" {
		return "", "", fmt.Errorf("status %d: %s", status, cfErrorString(resp.Errors))
	}
	return resp.Result.ID, deriveSecretAccessKey(resp.Result.Value), nil
}

// permissionGroups resolves + caches the R2 bucket-item read/write permission
// group IDs (their values aren't stable across accounts, so we look them up).
func (p *Provisioner) permissionGroups(ctx context.Context) (read, write string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readPGID != "" && p.writePGID != "" {
		return p.readPGID, p.writePGID, nil
	}
	var resp struct {
		Success bool         `json:"success"`
		Errors  []cfAPIError `json:"errors"`
		Result  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if _, err = p.doJSON(ctx, http.MethodGet,
		fmt.Sprintf("/accounts/%s/iam/permission_groups", p.cfg.AccountID), nil, &resp); err != nil {
		return "", "", err
	}
	for _, g := range resp.Result {
		switch g.Name {
		case "Workers R2 Storage Bucket Item Read":
			p.readPGID = g.ID
		case "Workers R2 Storage Bucket Item Write":
			p.writePGID = g.ID
		}
	}
	if p.readPGID == "" || p.writePGID == "" {
		return "", "", fmt.Errorf("volstore: R2 bucket-item permission groups not found")
	}
	return p.readPGID, p.writePGID, nil
}

// DeleteToken revokes a tenant's R2 token (used on teardown). Best-effort.
func (p *Provisioner) DeleteToken(ctx context.Context, tokenID string) error {
	if p == nil || tokenID == "" {
		return nil
	}
	_, err := p.doJSON(ctx, http.MethodDelete,
		fmt.Sprintf("/accounts/%s/tokens/%s", p.cfg.AccountID, tokenID), nil, &struct{}{})
	return err
}

func (p *Provisioner) doJSON(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func cfErrorString(errs []cfAPIError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%d: %s", e.Code, e.Message))
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}

func cfErrorsContain(errs []cfAPIError, substr string, code int) bool {
	for _, e := range errs {
		if e.Code == code || (substr != "" && strings.Contains(strings.ToLower(e.Message), substr)) {
			return true
		}
	}
	return false
}
