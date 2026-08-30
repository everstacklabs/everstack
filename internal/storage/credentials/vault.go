package credentials

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
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	vaultCredentialBackend = "vault"
	defaultVaultMountPath  = "secret"
	defaultVaultPathPrefix = "everstack/storage-credentials"
	maxVaultResponseBytes  = 1 << 20
)

// VaultConfig selects a Vault KV v2 mount for dynamic credential values.
// PostgreSQL retains only the opaque reference registry used by foreign keys,
// tenant checks, ordering, and revocation state.
type VaultConfig struct {
	Address    string
	Token      string
	Namespace  string
	MountPath  string
	PathPrefix string
}

type VaultStore struct {
	db         *sqlx.DB
	baseURL    *url.URL
	token      string
	namespace  string
	mountPath  string
	pathPrefix string
	client     *http.Client
	fallback   Store
}

func (s *VaultStore) SetFallback(store Store) { s.fallback = store }

func NewVaultStore(db *sqlx.DB, config VaultConfig, client *http.Client) (*VaultStore, error) {
	if db == nil {
		return nil, ErrStoreNotConfigured
	}
	address := strings.TrimSpace(config.Address)
	token := strings.TrimSpace(config.Token)
	if address == "" || token == "" {
		return nil, fmt.Errorf("vault storage credential address and token are required")
	}
	baseURL, err := url.Parse(address)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" {
		return nil, fmt.Errorf("vault storage credential address is invalid")
	}
	mountPath := strings.Trim(strings.TrimSpace(config.MountPath), "/")
	if mountPath == "" {
		mountPath = defaultVaultMountPath
	}
	pathPrefix := strings.Trim(strings.TrimSpace(config.PathPrefix), "/")
	if pathPrefix == "" {
		pathPrefix = defaultVaultPathPrefix
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &VaultStore{
		db: db, baseURL: baseURL, token: token,
		namespace: strings.TrimSpace(config.Namespace),
		mountPath: mountPath, pathPrefix: pathPrefix, client: &clientCopy,
	}, nil
}

func (s *VaultStore) Put(ctx context.Context, tenantID string, credentials ProviderCredentials) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", fmt.Errorf("storage credential tenant is required")
	}
	if err := credentials.Validate(); err != nil {
		return "", err
	}
	reference := "storagecred_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	body, err := json.Marshal(map[string]interface{}{"data": credentials})
	if err != nil {
		return "", fmt.Errorf("encode storage credentials")
	}
	if err := s.request(ctx, http.MethodPost, s.dataPath(tenantID, reference), body, http.StatusOK, http.StatusNoContent); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO object_storage_credentials (id, tenant_id, backend, ciphertext, key_id, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`, reference, tenantID, vaultCredentialBackend, []byte{}, vaultCredentialBackend); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.request(cleanupCtx, http.MethodDelete, s.metadataPath(tenantID, reference), nil, http.StatusNoContent, http.StatusNotFound)
		return "", fmt.Errorf("persist storage credential reference: %w", err)
	}
	return reference, nil
}

func (s *VaultStore) Resolve(ctx context.Context, tenantID, reference string) (ProviderCredentials, error) {
	tenantID = strings.TrimSpace(tenantID)
	reference = strings.TrimSpace(reference)
	var backend string
	if err := s.db.GetContext(ctx, &backend, `
		SELECT backend FROM object_storage_credentials
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, reference, tenantID); errors.Is(err, sql.ErrNoRows) {
		return ProviderCredentials{}, ErrCredentialNotFound
	} else if err != nil {
		return ProviderCredentials{}, fmt.Errorf("resolve storage credential reference: %w", err)
	}
	if backend != vaultCredentialBackend {
		if s.fallback != nil {
			return s.fallback.Resolve(ctx, tenantID, reference)
		}
		return ProviderCredentials{}, ErrCredentialNotFound
	}

	responseBody, err := s.requestBody(ctx, http.MethodGet, s.dataPath(tenantID, reference), nil, http.StatusOK)
	if err != nil {
		return ProviderCredentials{}, err
	}
	var response struct {
		Data struct {
			Data ProviderCredentials `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return ProviderCredentials{}, fmt.Errorf("decode vault storage credential response")
	}
	if err := response.Data.Data.Validate(); err != nil {
		return ProviderCredentials{}, fmt.Errorf("stored credential payload is invalid")
	}
	return response.Data.Data, nil
}

func (s *VaultStore) Revoke(ctx context.Context, tenantID, reference string) error {
	tenantID = strings.TrimSpace(tenantID)
	reference = strings.TrimSpace(reference)
	var selectedBackend string
	if err := s.db.GetContext(ctx, &selectedBackend, `
		SELECT backend FROM object_storage_credentials
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, reference, tenantID); errors.Is(err, sql.ErrNoRows) {
		return ErrCredentialNotFound
	} else if err != nil {
		return fmt.Errorf("resolve storage credential backend: %w", err)
	}
	if selectedBackend != vaultCredentialBackend {
		if s.fallback != nil {
			return s.fallback.Revoke(ctx, tenantID, reference)
		}
		return ErrCredentialNotFound
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage credential revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var backend string
	if err := tx.GetContext(ctx, &backend, `
		SELECT backend FROM object_storage_credentials
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM object_storage_configs WHERE credential_ref = $1 AND tenant_id = $2)
		  AND NOT EXISTS (SELECT 1 FROM tenant_volume_buckets WHERE credential_ref = $1 AND tenant_id = $2)
		FOR UPDATE
	`, reference, tenantID); errors.Is(err, sql.ErrNoRows) {
		return ErrCredentialNotFound
	} else if err != nil {
		return fmt.Errorf("lock storage credential reference: %w", err)
	}
	if backend != vaultCredentialBackend {
		return ErrCredentialNotFound
	}
	if err := s.request(ctx, http.MethodDelete, s.metadataPath(tenantID, reference), nil, http.StatusNoContent, http.StatusNotFound); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE object_storage_credentials
		SET revoked_at = COALESCE(revoked_at, NOW()), ciphertext = '\x'::bytea, key_id = 'revoked'
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, reference, tenantID)
	if err != nil {
		return fmt.Errorf("revoke storage credential reference: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return ErrCredentialNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage credential revocation: %w", err)
	}
	return nil
}

func (s *VaultStore) CredentialCutoverEnabled(ctx context.Context) (bool, error) {
	return CredentialCutoverEnabledForDB(ctx, s.db)
}

func (s *VaultStore) ResolveConfigCredentials(ctx context.Context, tenantID, configID, reference string) (ProviderCredentials, string, error) {
	if reference = strings.TrimSpace(reference); reference != "" {
		credentials, err := s.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	enabled, err := s.CredentialCutoverEnabled(ctx)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	if enabled {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	var credentials ProviderCredentials
	if err := s.db.GetContext(ctx, &credentials, `
		SELECT access_key_id, secret_access_key FROM object_storage_configs
		WHERE id = $1 AND tenant_id = $2
	`, strings.TrimSpace(configID), strings.TrimSpace(tenantID)); errors.Is(err, sql.ErrNoRows) {
		return ProviderCredentials{}, "", ErrCredentialNotFound
	} else if err != nil {
		return ProviderCredentials{}, "", fmt.Errorf("resolve legacy storage config credentials: %w", err)
	}
	if err := credentials.Validate(); err != nil {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	return credentials, "", nil
}

func (s *VaultStore) ResolveVolumeCredentials(ctx context.Context, tenantID, reference string) (ProviderCredentials, string, error) {
	if reference = strings.TrimSpace(reference); reference != "" {
		credentials, err := s.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	enabled, err := s.CredentialCutoverEnabled(ctx)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	if enabled {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	var credentials ProviderCredentials
	if err := s.db.GetContext(ctx, &credentials, `
		SELECT access_key_id, secret_access_key, cf_token_id AS provider_token_id
		FROM tenant_volume_buckets
		WHERE tenant_id = $1
	`, strings.TrimSpace(tenantID)); errors.Is(err, sql.ErrNoRows) {
		return ProviderCredentials{}, "", ErrCredentialNotFound
	} else if err != nil {
		return ProviderCredentials{}, "", fmt.Errorf("resolve legacy volume credentials: %w", err)
	}
	if err := credentials.Validate(); err != nil {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	if strings.TrimSpace(credentials.ProviderTokenID) == "" {
		credentials.ProviderTokenID = credentials.AccessKeyID
	}
	return credentials, "", nil
}

func (s *VaultStore) request(ctx context.Context, method, requestPath string, body []byte, accepted ...int) error {
	_, err := s.requestBody(ctx, method, requestPath, body, accepted...)
	return err
}

func (s *VaultStore) requestBody(ctx context.Context, method, requestPath string, body []byte, accepted ...int) ([]byte, error) {
	endpoint := *s.baseURL
	endpoint.Path = path.Join(endpoint.Path, "v1", s.mountPath, requestPath)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("create vault storage credential request")
	}
	req.Header.Set("X-Vault-Token", s.token)
	if s.namespace != "" {
		req.Header.Set("X-Vault-Namespace", s.namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault storage credential request failed")
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxVaultResponseBytes))
	if readErr != nil {
		return nil, fmt.Errorf("read vault storage credential response")
	}
	for _, status := range accepted {
		if resp.StatusCode == status {
			return responseBody, nil
		}
	}
	return nil, fmt.Errorf("vault storage credential operation failed with status %d", resp.StatusCode)
}

func (s *VaultStore) dataPath(tenantID, reference string) string {
	return path.Join("data", s.pathPrefix, vaultTenantKey(tenantID), reference)
}

func (s *VaultStore) metadataPath(tenantID, reference string) string {
	return path.Join("metadata", s.pathPrefix, vaultTenantKey(tenantID), reference)
}

func vaultTenantKey(tenantID string) string {
	digest := sha256.Sum256([]byte(tenantID))
	return hex.EncodeToString(digest[:])
}

var _ Store = (*VaultStore)(nil)
var _ CutoverGate = (*VaultStore)(nil)
var _ ConfigResolver = (*VaultStore)(nil)
var _ VolumeResolver = (*VaultStore)(nil)
