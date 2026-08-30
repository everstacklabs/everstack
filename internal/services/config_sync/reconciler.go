package config_sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/events"
	providerEvents "github.com/everstacklabs/everstack/internal/events/provider"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Reconciler handles synchronization between YAML and database on startup
type Reconciler struct {
	repo       *provider_config.Repository
	configPath string
	eventBus   events.Bus
}

// NewReconciler creates a new reconciler
func NewReconciler(repo *provider_config.Repository, configPath string, eventBus events.Bus) *Reconciler {
	return &Reconciler{
		repo:       repo,
		configPath: configPath,
		eventBus:   eventBus,
	}
}

// ReconcileOnStartup compares YAML vs DB timestamps and syncs accordingly
func (r *Reconciler) ReconcileOnStartup(ctx context.Context) error {

	// Check if YAML file exists
	yamlStat, err := os.Stat(r.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat YAML file: %w", err)
	}

	yamlModTime := yamlStat.ModTime()

	// Get all configurations from database
	dbConfigs, err := r.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list database configurations: %w", err)
	}

	// Determine the newest sync time from database
	var newestDBSync time.Time
	for _, config := range dbConfigs {
		if config.SyncedToYAMLAt != nil && config.SyncedToYAMLAt.After(newestDBSync) {
			newestDBSync = *config.SyncedToYAMLAt
		}
	}

	// Decision logic:
	// - If YAML is newer than newest DB sync → load YAML into DB
	// - Otherwise → use DB as source of truth (already in DB)

	logger.Debugf("config_sync: YAML mod time: %v, newest DB sync: %v", yamlModTime, newestDBSync)

	if newestDBSync.IsZero() || yamlModTime.After(newestDBSync) {
		logger.Info("config_sync: YAML is newer or no DB sync found, loading from YAML")
		return r.loadYAMLToDatabase(ctx)
	}

	logger.Info("config_sync: DB is up-to-date, skipping YAML sync")
	return nil
}

// loadYAMLToDatabase loads configurations from YAML and updates database
func (r *Reconciler) loadYAMLToDatabase(ctx context.Context) error {
	// Load configurations from YAML
	yamlConfigs, err := LoadFromYAML(r.configPath)
	if err != nil {
		return fmt.Errorf("failed to load from YAML: %w", err)
	}

	logger.Infof("config_sync: loaded %d provider configurations from YAML", len(yamlConfigs))

	// Create API key repository
	apiKeyRepo := provider_api_keys.NewPostgresRepository(r.repo.GetDB())

	// Upsert each configuration into database
	for _, config := range yamlConfigs {
		hasKey := config.APIKeyEncrypted != ""
		logger.Debugf("config_sync: processing provider %s, has API key: %v", config.ProviderName, hasKey)

		if err := r.repo.Upsert(ctx, config); err != nil {
			return fmt.Errorf("failed to upsert provider %s: %w", config.ProviderName, err)
		}

		// Also sync API key to provider_api_keys table
		if config.APIKeyEncrypted != "" {
			logger.Infof("config_sync: syncing API key for provider %s (config ID: %s)", config.ProviderName, config.ID)

			configKey := &provider_api_keys.ProviderAPIKey{
				ProviderConfigID: config.ID,
				KeyName:          "Config API Key",
				KeyEncrypted:     config.APIKeyEncrypted,
				Weight:           1,
				IsActive:         true,
				Source:           "config",
			}

			err := apiKeyRepo.UpsertConfigKey(ctx, configKey)
			switch {
			case errors.Is(err, provider_api_keys.ErrConfigKeyDuplicatesManual):
				logger.Infof("config_sync: provider %s already has this credential as a user-managed key, not seeding a config key", config.ProviderName)
			case err != nil:
				logger.Errorf("config_sync: failed to sync config API key for %s: %v", config.ProviderName, err)
			default:
				logger.Infof("config_sync: successfully synced API key %s for provider %s", configKey.ID, config.ProviderName)
				// Publish event on successful sync
				if r.eventBus != nil {
					r.eventBus.Publish(ctx, providerEvents.ProviderConfigAPIKeySyncedEvent{
						KeyID:            configKey.ID,
						ProviderConfigID: config.ID,
						ProviderName:     config.ProviderName,
						KeyName:          configKey.KeyName,
						Weight:           configKey.Weight,
						IsActive:         configKey.IsActive,
						UserID:           "system",
						TraceID:          "",
						Timestamp:        time.Now(),
					})
				}
			}
		} else {
			// API key removed from YAML - mark any existing config keys as inactive
			if err := apiKeyRepo.DeactivateConfigKeys(ctx, config.ID); err != nil {
				logger.Warnf("config_sync: failed to deactivate config keys for %s: %v", config.ProviderName, err)
			}
		}

		// Mark as synced
		if err := r.repo.UpdateSyncTime(ctx, config.ProviderName); err != nil {
			logger.Warnf("config_sync: failed to update sync time for %s: %v", config.ProviderName, err)
		}
	}

	return nil
}

// ForceSyncFromYAML forces a sync from YAML to database (for manual operations)
func (r *Reconciler) ForceSyncFromYAML(ctx context.Context) error {
	return r.loadYAMLToDatabase(ctx)
}

// ForceSyncToYAML forces a sync from database to YAML (for manual operations)
func (r *Reconciler) ForceSyncToYAML(ctx context.Context) error {

	// Get all configurations from database
	configs, err := r.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list configurations: %w", err)
	}

	// Write to YAML
	if err := SyncToYAML(r.configPath, configs); err != nil {
		return fmt.Errorf("failed to sync to YAML: %w", err)
	}

	// Update sync times
	for _, config := range configs {
		if err := r.repo.UpdateSyncTime(ctx, config.ProviderName); err != nil {
			logger.Warnf("config_sync: failed to update sync time for %s: %v", config.ProviderName, err)
		}
	}

	return nil
}
