package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CutoverGate reports whether every deployment replica is expected to
// understand encrypted credential references.
type CutoverGate interface {
	CredentialCutoverEnabled(ctx context.Context) (bool, error)
}

// ConfigResolver resolves either an encrypted reference or a legacy config
// during the expand-and-contract rollout window.
type ConfigResolver interface {
	ResolveConfigCredentials(ctx context.Context, tenantID, configID, reference string) (ProviderCredentials, string, error)
}

// VolumeResolver resolves encrypted or legacy bucket-scoped sandbox-volume
// credentials during the same fleet cutover window.
type VolumeResolver interface {
	ResolveVolumeCredentials(ctx context.Context, tenantID, reference string) (ProviderCredentials, string, error)
}

// CredentialCutoverEnabled defaults to true for custom stores that do not need
// the PostgreSQL mixed-version rollout gate.
func CredentialCutoverEnabled(ctx context.Context, store Store) (bool, error) {
	if store == nil {
		return false, ErrStoreNotConfigured
	}
	gate, ok := store.(CutoverGate)
	if !ok {
		return true, nil
	}
	return gate.CredentialCutoverEnabled(ctx)
}

// ResolveConfigCredentials centralizes reference resolution for storage RPCs
// and agent artifact tools.
func ResolveConfigCredentials(ctx context.Context, store Store, tenantID, configID, reference string) (ProviderCredentials, string, error) {
	if store == nil {
		return ProviderCredentials{}, "", ErrStoreNotConfigured
	}
	reference = strings.TrimSpace(reference)
	if reference != "" {
		credentials, err := store.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	if resolver, ok := store.(ConfigResolver); ok {
		return resolver.ResolveConfigCredentials(ctx, tenantID, configID, reference)
	}
	migrator, ok := store.(LegacyMigrator)
	if !ok {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	reference, _, err := migrator.MigrateLegacyConfig(ctx, tenantID, configID)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	credentials, err := store.Resolve(ctx, tenantID, reference)
	return credentials, reference, err
}

// ResolveVolumeCredentials centralizes reference resolution for the sandbox
// volume provisioner without exposing legacy plaintext outside the backend.
func ResolveVolumeCredentials(ctx context.Context, store Store, tenantID, reference string) (ProviderCredentials, string, error) {
	if store == nil {
		return ProviderCredentials{}, "", ErrStoreNotConfigured
	}
	reference = strings.TrimSpace(reference)
	if reference != "" {
		credentials, err := store.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	resolver, ok := store.(VolumeResolver)
	if !ok {
		return ProviderCredentials{}, "", ErrLegacyCredentialIncomplete
	}
	return resolver.ResolveVolumeCredentials(ctx, tenantID, reference)
}

func (s *PostgresStore) CredentialCutoverEnabled(ctx context.Context) (bool, error) {
	return CredentialCutoverEnabledForDB(ctx, s.db)
}

func (s *PostgresStore) ResolveConfigCredentials(ctx context.Context, tenantID, configID, reference string) (ProviderCredentials, string, error) {
	if reference = strings.TrimSpace(reference); reference != "" {
		credentials, err := s.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	enabled, err := s.CredentialCutoverEnabled(ctx)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	if !enabled {
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

	reference, _, err = s.MigrateLegacyConfig(ctx, tenantID, configID)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	credentials, err := s.Resolve(ctx, tenantID, reference)
	return credentials, reference, err
}

func (s *PostgresStore) ResolveVolumeCredentials(ctx context.Context, tenantID, reference string) (ProviderCredentials, string, error) {
	if reference = strings.TrimSpace(reference); reference != "" {
		credentials, err := s.Resolve(ctx, tenantID, reference)
		return credentials, reference, err
	}
	enabled, err := s.CredentialCutoverEnabled(ctx)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	if !enabled {
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

	reference, _, err = s.MigrateLegacyVolumeBucket(ctx, tenantID)
	if err != nil {
		return ProviderCredentials{}, "", err
	}
	credentials, err := s.Resolve(ctx, tenantID, reference)
	return credentials, reference, err
}

var _ CutoverGate = (*PostgresStore)(nil)
var _ ConfigResolver = (*PostgresStore)(nil)
var _ VolumeResolver = (*PostgresStore)(nil)
