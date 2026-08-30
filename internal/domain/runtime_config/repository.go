package runtime_config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"gopkg.in/yaml.v3"
)

// Repository handles database operations for runtime configuration.
//
// All read/write operations are scoped by tenant_id. Empty tenant_id
// means the self-hosted single-tenant deployment (or any case where
// the auth context didn't carry a tenant). Hosted deployments pass a
// real tenant_id so customers can't see or overwrite each other's
// settings — see UNIQUE(tenant_id, section) on the table.
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a new runtime configuration repository
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// GetDB returns the underlying database connection
func (r *Repository) GetDB() *sqlx.DB {
	return r.db
}

// Get returns a configuration section by tenant + name.
func (r *Repository) Get(ctx context.Context, tenantID, section string) (*RuntimeConfigSection, error) {
	if !IsValidSection(section) {
		return nil, ErrInvalidSection
	}

	query := `
		SELECT id, tenant_id, section, config, version, updated_by, updated_at, created_at
		FROM runtime_config
		WHERE tenant_id = $1 AND section = $2
	`

	var config RuntimeConfigSection
	if err := r.db.GetContext(ctx, &config, query, tenantID, section); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSectionNotFound
		}
		return nil, fmt.Errorf("failed to get runtime config section: %w", err)
	}

	return &config, nil
}

// GetAll returns all configuration sections for a tenant.
func (r *Repository) GetAll(ctx context.Context, tenantID string) ([]*RuntimeConfigSection, error) {
	query := `
		SELECT id, tenant_id, section, config, version, updated_by, updated_at, created_at
		FROM runtime_config
		WHERE tenant_id = $1
		ORDER BY section ASC
	`

	var sections []RuntimeConfigSection
	if err := r.db.SelectContext(ctx, &sections, query, tenantID); err != nil {
		return nil, fmt.Errorf("failed to list runtime config sections: %w", err)
	}

	result := make([]*RuntimeConfigSection, len(sections))
	for i := range sections {
		result[i] = &sections[i]
	}

	return result, nil
}

// Update updates a configuration section for a tenant. Inserts on conflict.
func (r *Repository) Update(ctx context.Context, tenantID, section string, config json.RawMessage, updatedBy *string) (*RuntimeConfigSection, error) {
	if !IsValidSection(section) {
		return nil, ErrInvalidSection
	}

	query := `
		UPDATE runtime_config
		SET config = $1,
		    version = version + 1,
		    updated_by = $2,
		    updated_at = $3
		WHERE tenant_id = $4 AND section = $5
		RETURNING id, tenant_id, section, config, version, updated_by, updated_at, created_at
	`

	now := time.Now()
	var result RuntimeConfigSection
	err := r.db.GetContext(ctx, &result, query, config, updatedBy, now, tenantID, section)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r.Create(ctx, tenantID, section, config, updatedBy)
		}
		return nil, fmt.Errorf("failed to update runtime config section: %w", err)
	}

	return &result, nil
}

// UpdateWithVersion updates a configuration section with optimistic locking.
func (r *Repository) UpdateWithVersion(ctx context.Context, tenantID, section string, config json.RawMessage, expectedVersion int, updatedBy *string) (*RuntimeConfigSection, error) {
	if !IsValidSection(section) {
		return nil, ErrInvalidSection
	}

	query := `
		UPDATE runtime_config
		SET config = $1,
		    version = version + 1,
		    updated_by = $2,
		    updated_at = $3
		WHERE tenant_id = $4 AND section = $5 AND version = $6
		RETURNING id, tenant_id, section, config, version, updated_by, updated_at, created_at
	`

	now := time.Now()
	var result RuntimeConfigSection
	err := r.db.GetContext(ctx, &result, query, config, updatedBy, now, tenantID, section, expectedVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := r.Get(ctx, tenantID, section)
			if getErr != nil {
				if errors.Is(getErr, ErrSectionNotFound) {
					return r.Create(ctx, tenantID, section, config, updatedBy)
				}
				return nil, getErr
			}
			if existing.Version != expectedVersion {
				return nil, ErrVersionMismatch
			}
			return nil, ErrSectionNotFound
		}
		return nil, fmt.Errorf("failed to update runtime config section: %w", err)
	}

	return &result, nil
}

// Create creates a new configuration section for a tenant.
func (r *Repository) Create(ctx context.Context, tenantID, section string, config json.RawMessage, updatedBy *string) (*RuntimeConfigSection, error) {
	if !IsValidSection(section) {
		return nil, ErrInvalidSection
	}

	query := `
		INSERT INTO runtime_config (id, tenant_id, section, config, version, updated_by, updated_at, created_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $6)
		ON CONFLICT (tenant_id, section) DO UPDATE SET
			config = EXCLUDED.config,
			version = runtime_config.version + 1,
			updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at
		RETURNING id, tenant_id, section, config, version, updated_by, updated_at, created_at
	`

	now := time.Now()
	var result RuntimeConfigSection
	err := r.db.GetContext(ctx, &result, query, uuid.New().String(), tenantID, section, config, updatedBy, now)
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime config section: %w", err)
	}

	return &result, nil
}

// Reset resets a configuration section to its default values.
func (r *Repository) Reset(ctx context.Context, tenantID, section string, updatedBy *string) (*RuntimeConfigSection, error) {
	if !IsValidSection(section) {
		return nil, ErrInvalidSection
	}

	defaultConfig, err := GetDefaultConfig(SectionName(section))
	if err != nil {
		return nil, err
	}

	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal default config: %w", err)
	}

	return r.Update(ctx, tenantID, section, configBytes, updatedBy)
}

// GetFullConfig returns the complete runtime configuration for a tenant.
// Missing sections fall back to defaults so callers always get a usable
// FullRuntimeConfig even before the tenant has overridden anything.
func (r *Repository) GetFullConfig(ctx context.Context, tenantID string) (*FullRuntimeConfig, error) {
	sections, err := r.GetAll(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	config := &FullRuntimeConfig{
		RateLimit:    DefaultRateLimitConfig(),
		LoadBalancer: DefaultLoadBalancerConfig(),
		Features:     DefaultFeaturesConfig(),
		Cache:        DefaultCacheConfig(),
		Telemetry:    DefaultTelemetryConfig(),
		CORS:         DefaultCORSConfig(),
		Version:      0,
	}

	var latestUpdate time.Time
	for _, section := range sections {
		if section.UpdatedAt.After(latestUpdate) {
			latestUpdate = section.UpdatedAt
		}
		if section.Version > config.Version {
			config.Version = section.Version
		}

		switch SectionName(section.Section) {
		case SectionRateLimit:
			if err := json.Unmarshal(section.Config, &config.RateLimit); err != nil {
				return nil, fmt.Errorf("failed to unmarshal rate_limit config: %w", err)
			}
		case SectionLoadBalancer:
			if err := json.Unmarshal(section.Config, &config.LoadBalancer); err != nil {
				return nil, fmt.Errorf("failed to unmarshal load_balancer config: %w", err)
			}
		case SectionFeatures:
			if err := json.Unmarshal(section.Config, &config.Features); err != nil {
				return nil, fmt.Errorf("failed to unmarshal features config: %w", err)
			}
		case SectionCache:
			if err := json.Unmarshal(section.Config, &config.Cache); err != nil {
				return nil, fmt.Errorf("failed to unmarshal cache config: %w", err)
			}
		case SectionTelemetry:
			if err := json.Unmarshal(section.Config, &config.Telemetry); err != nil {
				return nil, fmt.Errorf("failed to unmarshal telemetry config: %w", err)
			}
		case SectionCORS:
			if err := json.Unmarshal(section.Config, &config.CORS); err != nil {
				return nil, fmt.Errorf("failed to unmarshal cors config: %w", err)
			}
		}
	}

	config.UpdatedAt = latestUpdate
	return config, nil
}

// ConfigToYAML converts a config section to YAML string
func ConfigToYAML(config json.RawMessage) (string, error) {
	var data interface{}
	if err := json.Unmarshal(config, &data); err != nil {
		return "", fmt.Errorf("failed to unmarshal config: %w", err)
	}

	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal to YAML: %w", err)
	}

	return string(yamlBytes), nil
}

// YAMLToConfig converts a YAML string to JSON config
func YAMLToConfig(yamlContent string) (json.RawMessage, error) {
	var data interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &data); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to convert YAML to JSON: %w", err)
	}

	return jsonBytes, nil
}

// ValidateConfig validates a configuration section against its schema
func ValidateConfig(section SectionName, config json.RawMessage) error {
	switch section {
	case SectionRateLimit:
		var cfg RateLimitConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid rate_limit config: %v", ErrInvalidConfig, err)
		}
		if cfg.Enabled && cfg.RequestsPerMinute <= 0 {
			return fmt.Errorf("%w: requests_per_minute must be > 0 when enabled", ErrInvalidConfig)
		}
		if cfg.Enabled && cfg.Burst <= 0 {
			return fmt.Errorf("%w: burst must be > 0 when enabled", ErrInvalidConfig)
		}
	case SectionLoadBalancer:
		var cfg LoadBalancerConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid load_balancer config: %v", ErrInvalidConfig, err)
		}
		if cfg.Enabled && cfg.Strategy == "" {
			return fmt.Errorf("%w: strategy is required when enabled", ErrInvalidConfig)
		}
	case SectionFeatures:
		var cfg FeaturesConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid features config: %v", ErrInvalidConfig, err)
		}
	case SectionCache:
		var cfg CacheConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid cache config: %v", ErrInvalidConfig, err)
		}
		if cfg.Enabled && cfg.Type == "" {
			return fmt.Errorf("%w: type is required when enabled", ErrInvalidConfig)
		}
	case SectionTelemetry:
		var cfg TelemetryConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid telemetry config: %v", ErrInvalidConfig, err)
		}
		if cfg.SamplingRate < 0 || cfg.SamplingRate > 1 {
			return fmt.Errorf("%w: sampling_rate must be between 0 and 1", ErrInvalidConfig)
		}
	case SectionCORS:
		var cfg CORSConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("%w: invalid cors config: %v", ErrInvalidConfig, err)
		}
		if cfg.Enabled && len(cfg.AllowedOrigins) == 0 {
			return fmt.Errorf("%w: allowed_origins is required when enabled", ErrInvalidConfig)
		}
	default:
		return ErrInvalidSection
	}

	return nil
}
