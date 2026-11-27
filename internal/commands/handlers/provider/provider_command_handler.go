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

// ErrNotFound reports that the provider configuration or API key does not
// exist for the caller's tenant. It is returned for rows owned by another
// tenant and for platform-owned rows too, so a caller cannot probe ids.
var ErrNotFound = errors.New("not found")

// ErrBaseURLChangeRequiresKey rejects a base URL change that would keep the
// stored credential: the stored key would then be sent to the new host.
var ErrBaseURLChangeRequiresKey = errors.New("re-enter the API key when changing the base URL")

// ErrBaseURLChangeWithStoredKeys rejects a base URL change while the
// configuration still holds API keys this request does not replace: every
// stored key is served against the configuration's base URL.
var ErrBaseURLChangeWithStoredKeys = errors.New("remove this provider's other API keys before changing the base URL")

// ErrProviderNameCollision rejects a new configuration whose provider name
// matches an existing one of the same tenant except for case or surrounding
// space. The gateway would merge the two and serve one row's keys against the
// other's base URL.
var ErrProviderNameCollision = errors.New("this provider is already configured under a different spelling")

// sameProviderGroup reports whether two provider names land in the same
// group when the gateway builds a tenant's provider bundle.
func sameProviderGroup(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// configStore is the slice of the provider configuration repository the
// handler uses. Every method is tenant-scoped.
type configStore interface {
	GetForOrg(ctx context.Context, orgID, providerName string) (*provider_config.Configuration, error)
	GetByIDForOrg(ctx context.Context, orgID, id string) (*provider_config.Configuration, error)
	ListForOrg(ctx context.Context, orgID string) ([]*provider_config.Configuration, error)
	UpsertForOrg(ctx context.Context, orgID string, config *provider_config.Configuration) error
	DeleteForOrg(ctx context.Context, orgID, providerName string) error
}

// Handler handles provider-related commands
type Handler struct {
	repo       configStore
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
	repo configStore,
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

	// The gateway groups a tenant's rows by lowercased provider name when it
	// builds the provider bundle, and one row's base URL then serves every
	// key in the group. A second row whose name differs only by case or
	// surrounding space ("OpenAI" beside "openai") would therefore redirect
	// the first row's stored keys to its own base URL without ever meeting
	// the base URL check below, which only sees the exact-name row.
	if existingConfig == nil {
		siblings, err := h.repo.ListForOrg(ctx, cmd.TenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing providers: %w", err)
		}
		for _, cfg := range siblings {
			if cfg.ProviderName != cmd.ProviderName && sameProviderGroup(cfg.ProviderName, cmd.ProviderName) {
				return nil, fmt.Errorf("%w: use %q", ErrProviderNameCollision, cfg.ProviderName)
			}
		}
	}

	var apiKey string
	inputKey := strings.TrimSpace(cmd.APIKey)
	freshKey := inputKey != "" && !isMaskedKeyPlaceholder(inputKey)
	// named decides both the base URL check and which key row the write
	// below touches, so the two can never disagree (e.g. on a blank name).
	named := strings.TrimSpace(cmd.APIKeyName) != ""
	if freshKey {
		apiKey = inputKey
	} else if existingConfig != nil {
		apiKey = existingConfig.APIKeyEncrypted
	} else {
		return nil, fmt.Errorf("api_key is required for initial configuration")
	}

	if existingConfig != nil && provider_config.BaseURLChanged(existingConfig.CustomBaseURL, cmd.CustomBaseURL, cmd.DefaultBaseURL) {
		if err := h.checkBaseURLChange(ctx, cmd.TenantID, existingConfig.ID, freshKey, named); err != nil {
			return nil, err
		}
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

	if inputKey != "" && named {
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
		if err := h.apiKeyRepo.CreateForOrg(ctx, cmd.TenantID, initialKey); err != nil {
			return nil, fmt.Errorf("failed to save initial API key: %w", err)
		}
	} else if inputKey != "" {
		// An unnamed key is stored as the config's single replace-in-place
		// "Config API Key" row. Its source='config' label drives that upsert
		// and the modify/delete guards below, not billing: this handler is
		// always tenant-scoped, so the row is tenant-owned and the gateway
		// serves it as BYOK (provider_api_keys.BillingSource).
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

// checkBaseURLChange is called when a configure request would change the
// upstream host of an existing configuration. Every key stored on the
// configuration (its legacy api_key_encrypted column and every
// provider_api_keys row) is served against that host, so the change is
// allowed only when this request supplies a fresh key and no stored key row
// survives the write. An unnamed fresh key replaces the "Config API Key" row
// in place; a named fresh key adds a row and replaces nothing.
func (h *Handler) checkBaseURLChange(ctx context.Context, tenantID, configID string, freshKey, named bool) error {
	if !freshKey {
		return ErrBaseURLChangeRequiresKey
	}
	keys, err := h.apiKeyRepo.ListByProviderConfigForOrg(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("failed to check stored API keys: %w", err)
	}
	for _, k := range keys {
		replacedInPlace := !named && k.Source == "config" && k.KeyName == configAPIKeyName
		if !replacedInPlace {
			return ErrBaseURLChangeWithStoredKeys
		}
	}
	return nil
}

// configAPIKeyName is the name of a configuration's single replace-in-place
// key row (see UpsertConfigKey).
const configAPIKeyName = "Config API Key"

// validID reports whether id can name a row. Ids are UUIDs; anything else
// cannot exist, so it is reported as not found rather than letting Postgres
// fail the cast (which would surface as an internal error).
func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

// ownedKey loads a key whose configuration the tenant owns. A key that is
// missing, malformed, or owned by another tenant or the platform is
// ErrNotFound; any other failure is returned as is.
func (h *Handler) ownedKey(ctx context.Context, tenantID, keyID string) (*provider_api_keys.ProviderAPIKey, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}
	if !validID(keyID) {
		return nil, fmt.Errorf("API key %w", ErrNotFound)
	}
	key, err := h.apiKeyRepo.GetByIDForOrg(ctx, tenantID, keyID)
	if err != nil {
		if errors.Is(err, provider_api_keys.ErrKeyNotFound) {
			return nil, fmt.Errorf("API key %w", ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}
	return key, nil
}

// keyWriteErr maps a scoped repository write error: a row that vanished or
// left the tenant's scope between read and write is ErrNotFound.
func keyWriteErr(op string, err error) error {
	if errors.Is(err, provider_api_keys.ErrKeyNotFound) {
		return fmt.Errorf("API key %w", ErrNotFound)
	}
	return fmt.Errorf("failed to %s API key: %w", op, err)
}

// HandleAddProviderAPIKey handles the add provider API key command. The key
// is written only to a configuration the caller's tenant owns.
func (h *Handler) HandleAddProviderAPIKey(ctx context.Context, cmd provider.AddProviderAPIKeyCommand) (*provider_api_keys.ProviderAPIKey, error) {
	// Validate command
	if cmd.TenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
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
	if !validID(cmd.ProviderConfigID) {
		return nil, fmt.Errorf("provider configuration %w", ErrNotFound)
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

	// CreateForOrg checks ownership in the same statement, before any
	// unique-name violation can fire, so "already exists" is only ever
	// reported for the caller's own configuration.
	if err := h.apiKeyRepo.CreateForOrg(ctx, cmd.TenantID, newKey); err != nil {
		if errors.Is(err, provider_api_keys.ErrKeyNotFound) {
			return nil, fmt.Errorf("provider configuration %w", ErrNotFound)
		}
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
	if cmd.Weight <= 0 {
		return nil, fmt.Errorf("weight must be greater than 0")
	}

	// Ownership first, so the config-sourced guard below never confirms
	// that another tenant's key exists.
	existingKey, err := h.ownedKey(ctx, cmd.TenantID, cmd.KeyID)
	if err != nil {
		return nil, err
	}

	// Prevent modification of config-sourced keys
	if existingKey.Source == "config" {
		return nil, fmt.Errorf("cannot modify config-sourced API key - update YAML config instead")
	}

	oldWeight := existingKey.Weight

	// Update weight
	existingKey.Weight = cmd.Weight
	existingKey.UpdatedAt = time.Now()

	if err := h.apiKeyRepo.UpdateForOrg(ctx, cmd.TenantID, existingKey); err != nil {
		return nil, keyWriteErr("update", err)
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
	existingKey, err := h.ownedKey(ctx, cmd.TenantID, cmd.KeyID)
	if err != nil {
		return nil, err
	}

	// Prevent modification of config-sourced keys
	if existingKey.Source == "config" {
		return nil, fmt.Errorf("cannot toggle config-sourced API key - update YAML config instead")
	}

	// Toggle active status
	existingKey.IsActive = cmd.IsActive
	existingKey.UpdatedAt = time.Now()

	if err := h.apiKeyRepo.UpdateForOrg(ctx, cmd.TenantID, existingKey); err != nil {
		return nil, keyWriteErr("toggle", err)
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
		if err := h.eventBus.Publish(ctx, event); err != nil {
			logger.Warnf("failed to publish ProviderAPIKeyToggledEvent: %v", err)
		}
	}

	return existingKey, nil
}

// HandleDeleteProviderAPIKey handles the delete provider API key command
func (h *Handler) HandleDeleteProviderAPIKey(ctx context.Context, cmd provider.DeleteProviderAPIKeyCommand) error {
	existingKey, err := h.ownedKey(ctx, cmd.TenantID, cmd.KeyID)
	if err != nil {
		return err
	}

	// Prevent deletion of config-sourced keys
	if existingKey.Source == "config" {
		return fmt.Errorf("cannot delete config-sourced API key - update YAML config instead")
	}

	// Delete the key
	if err := h.apiKeyRepo.DeleteForOrg(ctx, cmd.TenantID, existingKey.ID); err != nil {
		return keyWriteErr("delete", err)
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

// HandleListProviderAPIKeys lists the API keys of one provider configuration
// the caller's tenant owns. Another tenant's or a platform configuration is
// ErrNotFound, not an empty list.
func (h *Handler) HandleListProviderAPIKeys(ctx context.Context, q provider.ListProviderAPIKeysQuery) ([]*provider_api_keys.ProviderAPIKey, error) {
	if q.TenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if q.ProviderConfigID == "" {
		return nil, fmt.Errorf("provider_config_id is required")
	}
	if !validID(q.ProviderConfigID) {
		return nil, fmt.Errorf("provider configuration %w", ErrNotFound)
	}
	if _, err := h.repo.GetByIDForOrg(ctx, q.TenantID, q.ProviderConfigID); err != nil {
		if errors.Is(err, provider_config.ErrNotFound) {
			return nil, fmt.Errorf("provider configuration %w", ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get provider configuration: %w", err)
	}
	keys, err := h.apiKeyRepo.ListByProviderConfigForOrg(ctx, q.TenantID, q.ProviderConfigID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	return keys, nil
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
