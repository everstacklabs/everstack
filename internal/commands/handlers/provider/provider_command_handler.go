package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/commands/provider"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/events"
	providerEvents "github.com/everstacklabs/everstack/internal/events/provider"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// Handler handles provider-related commands
type Handler struct {
	repo       *provider_config.Repository
	apiKeyRepo provider_api_keys.Repository
	eventBus   events.Bus
}

func isMaskedKeyPlaceholder(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return false
	}
	// Treat masked UI placeholders as "no new key provided".
	// Real API keys should never contain '*' characters.
	return strings.Contains(trimmed, "*")
}

// NewHandler creates a new provider command handler
func NewHandler(
	repo *provider_config.Repository,
	apiKeyRepo provider_api_keys.Repository,
	eventBus events.Bus,
) *Handler {
	return &Handler{
		repo:       repo,
		apiKeyRepo: apiKeyRepo,
		eventBus:   eventBus,
	}
}

// HandleConfigureProvider handles the configure provider command. Every
// repo call is tenant-scoped via *ForOrg variants. The legacy unscoped
// Upsert/Get/List paths would write rows with organization_id NULL —
// post-migration the partial unique index ignores NULL, so two tenants
// configuring the same provider would write separate rows AND the
// gateway boot's List would surface those NULL rows back to whichever
// tenant's request triggered the bundle reload. That's the LLM-key
// cross-tenant leak the user reported.
func (h *Handler) HandleConfigureProvider(ctx context.Context, cmd provider.ConfigureProviderCommand) (*provider_config.Configuration, error) {
	if cmd.TenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if cmd.ProviderName == "" {
		return nil, fmt.Errorf("provider_name is required")
	}

	existingConfig, err := h.repo.GetForOrg(ctx, cmd.TenantID, cmd.ProviderName)
	if err != nil && !isNotFoundError(err) {
		return nil, fmt.Errorf("failed to get existing configuration: %w", err)
	}

	var apiKey string
	inputKey := strings.TrimSpace(cmd.APIKey)
	if inputKey != "" && !isMaskedKeyPlaceholder(inputKey) {
		apiKey = inputKey
	} else if existingConfig != nil {
		apiKey = existingConfig.APIKeyEncrypted
	} else {
		return nil, fmt.Errorf("api_key is required for initial configuration")
	}

	if cmd.CustomSettings == nil || cmd.CustomSettings["default"] != "true" {
		allConfigs, err := h.repo.ListForOrg(ctx, cmd.TenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing providers: %w", err)
		}
		configuredCount := 0
		for _, cfg := range allConfigs {
			if cfg.ProviderName != cmd.ProviderName && len(cfg.EnabledModels) > 0 {
				configuredCount++
			}
		}
		if configuredCount == 0 {
			if cmd.CustomSettings == nil {
				cmd.CustomSettings = make(map[string]string)
			}
			cmd.CustomSettings["default"] = "true"
			if err := h.unsetAllDefaultProviders(ctx, cmd.TenantID); err != nil {
				return nil, fmt.Errorf("failed to unset other default providers: %w", err)
			}
		}
	}

	if cmd.CustomSettings != nil && cmd.CustomSettings["default"] == "true" {
		if err := h.unsetAllDefaultProviders(ctx, cmd.TenantID); err != nil {
			return nil, fmt.Errorf("failed to unset other default providers: %w", err)
		}
	}

	config := &provider_config.Configuration{
		ProviderName:    cmd.ProviderName,
		APIKeyEncrypted: apiKey,
		APIKeySource:    "ui",
		EnabledModels:   cmd.EnabledModels,
		CustomBaseURL:   cmd.CustomBaseURL,
		CustomSettings:  cmd.CustomSettings,
		IsActive:        true,
	}

	if existingConfig != nil {
		config.ID = existingConfig.ID
		if strings.TrimSpace(cmd.APIKey) == "" {
			config.APIKeySource = existingConfig.APIKeySource
		}
	} else {
		config.ID = uuid.New().String()
	}

	if err := h.repo.UpsertForOrg(ctx, cmd.TenantID, config); err != nil {
		return nil, fmt.Errorf("failed to save configuration: %w", err)
	}

	if inputKey != "" && cmd.APIKeyName != "" {
		weight := cmd.APIKeyWeight
		if weight <= 0 {
			weight = 1
		}
		initialKey := &provider_api_keys.ProviderAPIKey{
			ID:               uuid.New().String(),
			ProviderConfigID: config.ID,
			KeyName:          strings.TrimSpace(cmd.APIKeyName),
			KeyEncrypted:     inputKey,
			Weight:           weight,
			IsActive:         true,
			Source:           "manual",
			RateLimitData:    make(map[string]interface{}),
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
		if err := h.apiKeyRepo.Create(ctx, initialKey); err != nil {
			return nil, fmt.Errorf("failed to save initial API key: %w", err)
		}
	} else if inputKey != "" {
		configKey := &provider_api_keys.ProviderAPIKey{
			ProviderConfigID: config.ID,
			KeyName:          "Config API Key",
			KeyEncrypted:     apiKey,
			Weight:           1,
			IsActive:         true,
			Source:           "config",
		}
		err := h.apiKeyRepo.UpsertConfigKey(ctx, configKey)
		if err != nil && !errors.Is(err, provider_api_keys.ErrConfigKeyDuplicatesManual) {
			return nil, fmt.Errorf("failed to sync configuration API key: %w", err)
		}
	}

	// Emit event
	event := providerEvents.ProviderConfiguredEvent{
		ProviderName:   config.ProviderName,
		ConfigID:       config.ID,
		EnabledModels:  config.EnabledModels,
		CustomBaseURL:  config.CustomBaseURL,
		CustomSettings: config.CustomSettings,
		UserID:         cmd.UserID,
		TraceID:        cmd.TraceID,
		Timestamp:      time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			// Log error but don't fail the command
			fmt.Printf("failed to publish ProviderConfiguredEvent: %v\n", err)
		}
	}

	return config, nil
}

// HandleAddProviderAPIKey handles the add provider API key command
func (h *Handler) HandleAddProviderAPIKey(ctx context.Context, cmd provider.AddProviderAPIKeyCommand) (*provider_api_keys.ProviderAPIKey, error) {
	// Validate command
	if cmd.ProviderConfigID == "" {
		return nil, fmt.Errorf("provider_config_id is required")
	}
	if cmd.KeyName == "" {
		return nil, fmt.Errorf("key_name is required")
	}
	inputKey := strings.TrimSpace(cmd.APIKey)
	if inputKey == "" || isMaskedKeyPlaceholder(inputKey) {
		return nil, fmt.Errorf("api_key is required")
	}
	if cmd.Weight <= 0 {
		return nil, fmt.Errorf("weight must be greater than 0")
	}

	// Create new API key (stored as plaintext)
	newKey := &provider_api_keys.ProviderAPIKey{
		ID:               uuid.New().String(),
		ProviderConfigID: cmd.ProviderConfigID,
		KeyName:          cmd.KeyName,
		KeyEncrypted:     inputKey, // Actually plaintext (field name is legacy)
		Weight:           cmd.Weight,
		IsActive:         true,
		RateLimitData:    make(map[string]interface{}),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := h.apiKeyRepo.Create(ctx, newKey); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, fmt.Errorf("API key with name '%s' already exists for this provider", cmd.KeyName)
		}
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}

	// Emit event
	event := providerEvents.ProviderAPIKeyAddedEvent{
		KeyID:            newKey.ID,
		ProviderConfigID: newKey.ProviderConfigID,
		KeyName:          newKey.KeyName,
		Weight:           newKey.Weight,
		IsActive:         newKey.IsActive,
		UserID:           cmd.UserID,
		TraceID:          cmd.TraceID,
		Timestamp:        time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderAPIKeyAddedEvent: %v\n", err)
		}
	}

	return newKey, nil
}

// HandleUpdateAPIKeyWeight handles the update API key weight command
func (h *Handler) HandleUpdateAPIKeyWeight(ctx context.Context, cmd provider.UpdateAPIKeyWeightCommand) (*provider_api_keys.ProviderAPIKey, error) {
	// Validate command
	if cmd.KeyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}
	if cmd.Weight <= 0 {
		return nil, fmt.Errorf("weight must be greater than 0")
	}

	// Get existing key
	existingKey, err := h.apiKeyRepo.GetByID(ctx, cmd.KeyID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("API key not found")
		}
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}

	// Prevent modification of config-sourced keys
	if existingKey.Source == "config" {
		return nil, fmt.Errorf("cannot modify config-sourced API key - update YAML config instead")
	}

	oldWeight := existingKey.Weight

	// Update weight
	existingKey.Weight = cmd.Weight
	existingKey.UpdatedAt = time.Now()

	if err := h.apiKeyRepo.Update(ctx, existingKey); err != nil {
		return nil, fmt.Errorf("failed to update API key weight: %w", err)
	}

	// Emit event
	event := providerEvents.ProviderAPIKeyWeightUpdatedEvent{
		KeyID:     existingKey.ID,
		OldWeight: oldWeight,
		NewWeight: cmd.Weight,
		UserID:    cmd.UserID,
		TraceID:   cmd.TraceID,
		Timestamp: time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderAPIKeyWeightUpdatedEvent: %v\n", err)
		}
	}

	return existingKey, nil
}

// HandleToggleAPIKey handles the toggle API key command
func (h *Handler) HandleToggleAPIKey(ctx context.Context, cmd provider.ToggleAPIKeyCommand) (*provider_api_keys.ProviderAPIKey, error) {
	// Validate command
	if cmd.KeyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}

	// Get existing key
	existingKey, err := h.apiKeyRepo.GetByID(ctx, cmd.KeyID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("API key not found")
		}
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}

	// Prevent modification of config-sourced keys
	if existingKey.Source == "config" {
		return nil, fmt.Errorf("cannot toggle config-sourced API key - update YAML config instead")
	}

	// Toggle active status
	existingKey.IsActive = cmd.IsActive
	existingKey.UpdatedAt = time.Now()

	if err := h.apiKeyRepo.Update(ctx, existingKey); err != nil {
		return nil, fmt.Errorf("failed to toggle API key: %w", err)
	}

	// Emit event
	event := providerEvents.ProviderAPIKeyToggledEvent{
		KeyID:     existingKey.ID,
		IsActive:  cmd.IsActive,
		UserID:    cmd.UserID,
		TraceID:   cmd.TraceID,
		Timestamp: time.Now(),
	}

	if h.eventBus != nil {
		fmt.Printf("[DEBUG] Publishing ProviderAPIKeyToggledEvent: keyID=%s, isActive=%v, eventType=%s\n",
			existingKey.ID, cmd.IsActive, event.Event())
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderAPIKeyToggledEvent: %v\n", err)
		} else {
			fmt.Printf("[DEBUG] ProviderAPIKeyToggledEvent published successfully\n")
		}
	} else {
		fmt.Printf("[WARNING] eventBus is nil, cannot publish ProviderAPIKeyToggledEvent\n")
	}

	return existingKey, nil
}

// HandleDeleteProviderAPIKey handles the delete provider API key command
func (h *Handler) HandleDeleteProviderAPIKey(ctx context.Context, cmd provider.DeleteProviderAPIKeyCommand) error {
	// Validate command
	if cmd.KeyID == "" {
		return fmt.Errorf("key_id is required")
	}

	// Get existing key for event data
	existingKey, err := h.apiKeyRepo.GetByID(ctx, cmd.KeyID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("API key not found")
		}
		return fmt.Errorf("failed to get API key: %w", err)
	}

	// Prevent deletion of config-sourced keys
	if existingKey.Source == "config" {
		return fmt.Errorf("cannot delete config-sourced API key - update YAML config instead")
	}

	// Delete the key
	if err := h.apiKeyRepo.Delete(ctx, cmd.KeyID); err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}

	// Emit event
	event := providerEvents.ProviderAPIKeyDeletedEvent{
		KeyID:            existingKey.ID,
		ProviderConfigID: existingKey.ProviderConfigID,
		KeyName:          existingKey.KeyName,
		UserID:           cmd.UserID,
		TraceID:          cmd.TraceID,
		Timestamp:        time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderAPIKeyDeletedEvent: %v\n", err)
		}
	}

	return nil
}

// HandleToggleProvider handles the toggle provider command
func (h *Handler) HandleToggleProvider(ctx context.Context, cmd provider.ToggleProviderCommand) (*provider_config.Configuration, error) {
	if cmd.TenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if cmd.ProviderName == "" {
		return nil, fmt.Errorf("provider_name is required")
	}

	existingConfig, err := h.repo.GetForOrg(ctx, cmd.TenantID, cmd.ProviderName)
	if err != nil {
		if isNotFoundError(err) {
			return nil, fmt.Errorf("provider configuration not found")
		}
		return nil, fmt.Errorf("failed to get provider configuration: %w", err)
	}

	isDefault := existingConfig.CustomSettings != nil && existingConfig.CustomSettings["default"] == "true"
	existingConfig.IsActive = cmd.IsActive

	if !cmd.IsActive && isDefault {
		if existingConfig.CustomSettings == nil {
			existingConfig.CustomSettings = make(map[string]string)
		}
		existingConfig.CustomSettings["default"] = "false"
	}

	if err := h.repo.UpsertForOrg(ctx, cmd.TenantID, existingConfig); err != nil {
		return nil, fmt.Errorf("failed to update provider configuration: %w", err)
	}

	if !cmd.IsActive && isDefault {
		if err := h.promoteNewDefaultProvider(ctx, cmd.TenantID); err != nil {
			logger.Warnf("failed to promote new default provider: %v", err)
		}
	}

	// Emit event
	event := providerEvents.ProviderToggledEvent{
		ProviderName: cmd.ProviderName,
		ConfigID:     existingConfig.ID,
		IsActive:     cmd.IsActive,
		UserID:       cmd.UserID,
		TraceID:      cmd.TraceID,
		Timestamp:    time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderToggledEvent: %v\n", err)
		}
	}

	return existingConfig, nil
}

// HandleDeleteProvider handles the delete provider command
func (h *Handler) HandleDeleteProvider(ctx context.Context, cmd provider.DeleteProviderCommand) error {
	if cmd.TenantID == "" {
		return fmt.Errorf("tenant id is required")
	}
	if cmd.ProviderName == "" {
		return fmt.Errorf("provider_name is required")
	}

	existingConfig, err := h.repo.GetForOrg(ctx, cmd.TenantID, cmd.ProviderName)
	if err != nil {
		if isNotFoundError(err) {
			return fmt.Errorf("provider configuration not found")
		}
		return fmt.Errorf("failed to get provider configuration: %w", err)
	}

	wasDefault := existingConfig.CustomSettings != nil && existingConfig.CustomSettings["default"] == "true"

	if err := h.repo.DeleteForOrg(ctx, cmd.TenantID, cmd.ProviderName); err != nil {
		return fmt.Errorf("failed to delete provider configuration: %w", err)
	}

	if wasDefault {
		if err := h.promoteNewDefaultProvider(ctx, cmd.TenantID); err != nil {
			logger.Warnf("failed to promote new default provider: %v", err)
		}
	}

	// Emit event
	event := providerEvents.ProviderDeletedEvent{
		ProviderName: cmd.ProviderName,
		ConfigID:     existingConfig.ID,
		UserID:       cmd.UserID,
		TraceID:      cmd.TraceID,
		Timestamp:    time.Now(),
	}

	if h.eventBus != nil {
		if err := h.eventBus.Publish(ctx, event); err != nil {
			fmt.Printf("failed to publish ProviderDeletedEvent: %v\n", err)
		}
	}

	return nil
}

// Helper function to check for not found errors
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "no rows") ||
		strings.Contains(errStr, "does not exist")
}

// unsetAllDefaultProviders removes the default flag from this tenant's
// providers. The earlier version called the unscoped List/Upsert pair
// which would mutate other tenants' rows when they happened to be marked
// default — and post-migration would fail outright because Upsert's
// ON CONFLICT (provider_name) target no longer exists.
func (h *Handler) unsetAllDefaultProviders(ctx context.Context, tenantID string) error {
	configs, err := h.repo.ListForOrg(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("failed to list configurations: %w", err)
	}
	for _, config := range configs {
		if config.CustomSettings != nil && config.CustomSettings["default"] == "true" {
			delete(config.CustomSettings, "default")
			if err := h.repo.UpsertForOrg(ctx, tenantID, config); err != nil {
				return fmt.Errorf("failed to update provider %s: %w", config.ProviderName, err)
			}
		}
	}
	return nil
}

// promoteNewDefaultProvider promotes the first available active provider
// to default within the caller's tenant.
func (h *Handler) promoteNewDefaultProvider(ctx context.Context, tenantID string) error {
	configs, err := h.repo.ListForOrg(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("failed to list configurations: %w", err)
	}
	var candidates []*provider_config.Configuration
	for _, config := range configs {
		if config.IsActive && len(config.EnabledModels) > 0 {
			candidates = append(candidates, config)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	newDefault := candidates[0]
	if newDefault.CustomSettings == nil {
		newDefault.CustomSettings = make(map[string]string)
	}
	newDefault.CustomSettings["default"] = "true"
	if err := h.repo.UpsertForOrg(ctx, tenantID, newDefault); err != nil {
		return fmt.Errorf("failed to promote %s to default: %w", newDefault.ProviderName, err)
	}
	logger.Infof("promoted provider '%s' to default after deletion", newDefault.ProviderName)
	return nil
}
