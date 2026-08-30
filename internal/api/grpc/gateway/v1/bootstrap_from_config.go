package v1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	catalogsvc "github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/services/config_sync"
)

// bootstrapFromConfig builds providers and router from gateway.yaml config.
func (s *Server) bootstrapFromConfig() {
	reg := gw.NewRegistry()
	modelMap := map[string]string{}
	modelRoutes := map[string]gw.ModelRoute{}

	// Load whitelisted models from catalog service
	var allowed map[string]map[string]struct{}
	var catalogCache *catalogsvc.Cache
	if catalogSvcAny := s.ctx.Value(contextkeys.CatalogService); catalogSvcAny != nil {
		if catalogSvc, ok := catalogSvcAny.(*catalogsvc.Service); ok {
			allowed = catalogSvc.GetModelWhitelist()
			catalogCache = catalogSvc.GetCache()
		}
	}

	if s.cfg != nil {
		aggregated := make(map[string]*factory.AggregatedInput)
		for _, mc := range s.cfg.Models {
			provider := strings.ToLower(mc.Provider)
			// Filter models using whitelist if available
			var filtered []string
			for _, model := range mc.Model {
				if allowed == nil {
					filtered = append(filtered, model)
					continue
				}
				if set, ok := allowed[provider]; ok {
					if _, ok := set[strings.ToLower(model)]; ok {
						filtered = append(filtered, model)
					}
				}
			}
			if len(filtered) == 0 {
				continue
			}
			for _, model := range filtered {
				alias := strings.ToLower(model)
				modelMap[alias] = provider
				modelRoutes[alias] = gw.ModelRoute{ProviderName: provider, ModelName: model}
			}

			agg := aggregated[provider]
			if agg == nil {
				agg = &factory.AggregatedInput{Provider: provider}
				aggregated[provider] = agg
			}

			// Handle multiple API keys from YAML
			if len(mc.APIKeys) > 0 && len(agg.APIKeys) == 0 {
				agg.APIKeys = make([]factory.APIKeyInput, len(mc.APIKeys))
				for i, yamlKey := range mc.APIKeys {
					key := yamlKey.Key
					// Expand env vars if needed
					if strings.Contains(key, "${") {
						key = os.ExpandEnv(key)
					}
					agg.APIKeys[i] = factory.APIKeyInput{
						ID:           provider + "-key-" + strings.ToLower(yamlKey.Name), // Generate temp ID
						KeyName:      yamlKey.Name,
						KeyEncrypted: key, // Plaintext key (field name is legacy)
						Weight:       yamlKey.Weight,
						IsActive:     true,
						Source:       "config",
					}
				}
			} else {
				// Fallback to legacy single key
				if agg.APIKey == "" && mc.APIKey != "" {
					key := mc.APIKey
					// Expand env vars if needed
					if strings.Contains(key, "${") {
						key = os.ExpandEnv(key)
					}
					agg.APIKey = key // Plaintext key (no encryption)
				}
			}

			if agg.BaseURL == "" && mc.BaseURL != "" {
				agg.BaseURL = mc.BaseURL
			}
			agg.Models = append(agg.Models, filtered...)
			agg.Raw = append(agg.Raw, mc)
		}

		// Extract sticky key from context for consistent API key selection
		ctx := s.ctx
		stickyKey := s.hashKeyFromContext(ctx)

		// Compose compaction once for the whole batch — same config
		// across every factory call.
		compactCfg := compactConfigFromFeatures(s.feat)
		summarizerResolve := factory.SummarizerResolver(s.makeCompactSummarizerResolver())

		for prov, in := range aggregated {
			in.StickyKey = stickyKey                 // Set sticky key for each provider
			in.CatalogCache = catalogCache           // Pass catalog for cost calculation
			in.CompactConfig = compactCfg            // Same compact policy for every provider
			in.SummarizerResolve = summarizerResolve // Lazy resolver — captures the live router
			if f, ok := factory.Default.Get(prov); ok {
				if p, err := f(*in); err == nil && p != nil {
					reg.Register(p)
				}
			}
		}
	}

	router := gw.NewRouter(reg, modelMap)
	router.SetModelRoutes(modelRoutes)
	// YAML config is the boot-time / single-tenant path: the resulting
	// bundle becomes the process-wide default. Shared multi-tenant mode
	// will overwrite it per-tenant via bootstrapFromDatabase as requests
	// arrive.
	s.installBundle("", &tenantBundle{reg: reg, router: router})
}

func (s *Server) requestDefaultsForModel(ctx context.Context, model string) modelRequestDefaults {
	// First, check model-scoped DB defaults for the resolved provider/model.
	// In shared multi-tenant mode the per-tenant bundle owns the sampling
	// overrides so two tenants can have different defaults.
	if router := s.routerFor(ctx); router != nil {
		_, route, err := router.Resolve(model)
		if err == nil {
			providerDefaults, hasProvider := s.providerDefaultsFor(ctx, route.ProviderName)
			modelDefaults, hasModel := s.modelDefaultsFor(ctx, route.ProviderName, route.ModelName)
			// The tiers compose: the model's values win control by control,
			// anything it leaves unset falls back to the provider's, and the
			// provider's only reach controls this model actually accepts.
			if hasModel || hasProvider {
				providerDefaults = providerDefaults.restrictedTo(
					s.modelParameterSupport(route.ProviderName, route.ModelName),
				)
				return modelDefaults.overlay(providerDefaults)
			}
		}
	}

	// Fallback to YAML config if no DB config found
	if s.cfg == nil {
		return modelRequestDefaults{}
	}
	for _, mc := range s.cfg.Models {
		for _, m := range mc.Model {
			if strings.EqualFold(m, model) {
				providerDefaults := defaultsFromLegacySampling(gw.SamplingParams{
					Temperature:      mc.Temperature,
					TopP:             mc.TopP,
					MaxTokens:        mc.MaxTokens,
					FrequencyPenalty: mc.FrequencyPenalty,
					PresencePenalty:  mc.PresencePenalty,
				})
				if values, ok := modelParametersForName(mc.ModelParameters, m); ok {
					defaults, err := parseOneModelRequestDefaults(values)
					if err != nil {
						logger.Warnf("gateway: ignoring invalid model parameters for %s/%s: %v", mc.Provider, m, err)
					} else {
						return defaults.overlay(providerDefaults)
					}
				}
				return providerDefaults
			}
		}
	}
	return modelRequestDefaults{}
}

// modelParameterSupport reports which request controls one model accepts,
// from the catalog. A model the catalog does not describe - a custom or
// runtime-discovered one - reports every control as supported: there is no
// basis to filter it, and the values were set deliberately.
func (s *Server) modelParameterSupport(providerName, modelName string) func(string) bool {
	supportsEverything := func(string) bool { return true }
	if s.ctx == nil {
		return supportsEverything
	}
	catalogSvcAny := s.ctx.Value(contextkeys.CatalogService)
	if catalogSvcAny == nil {
		return supportsEverything
	}
	catalogSvc, ok := catalogSvcAny.(*catalogsvc.Service)
	if !ok || catalogSvc == nil {
		return supportsEverything
	}
	cache := catalogSvc.GetCache()
	if cache == nil {
		return supportsEverything
	}
	model, found := cache.GetModel(providerName, modelName)
	if !found || model == nil || len(model.Parameters) == 0 {
		return supportsEverything
	}
	return model.SupportsParameter
}

func defaultsFromLegacySampling(sampling gw.SamplingParams) modelRequestDefaults {
	var defaults modelRequestDefaults
	if sampling.MaxTokens > 0 {
		defaults.MaxOutputTokens = &sampling.MaxTokens
	}
	if sampling.Temperature != 0 {
		defaults.Temperature = &sampling.Temperature
	}
	if sampling.TopP != 0 {
		defaults.TopP = &sampling.TopP
	}
	if sampling.FrequencyPenalty != 0 {
		defaults.FrequencyPenalty = &sampling.FrequencyPenalty
	}
	if sampling.PresencePenalty != 0 {
		defaults.PresencePenalty = &sampling.PresencePenalty
	}
	return defaults
}

// bootstrapFromDatabase loads provider configurations for the request's
// tenant and returns a fully built bundle. The caller decides what to do
// with the bundle — install it as the process-wide default (single-tenant
// boot) or cache it under the tenant key (shared multi-tenant request).
// Mutating Server pointers in here was what gave us the cross-tenant race;
// now the function is purely a builder.
func (s *Server) bootstrapFromDatabase(ctx context.Context, providerRepo *provider_config.Repository, apiKeyRepo provider_api_keys.Repository) (*tenantBundle, error) {
	// Scoped to ctx's tenant when one is set. The unscoped repo.List used
	// to be safe under schema-per-tenant; once that plumbing was removed
	// the same call started returning every tenant's rows, and the
	// per-tenant bundle keyed by ctx would happily cache them under one
	// tenant's key — the leak path.
	configs, err := listProviderConfigs(ctx, providerRepo)
	if err != nil {
		return nil, err
	}

	reg := gw.NewRegistry()
	modelMap := map[string]string{}
	modelRoutes := map[string]gw.ModelRoute{}
	aggregated := make(map[string]*factory.AggregatedInput)
	dbProviderDefaults := make(map[string]modelRequestDefaults)
	dbModelDefaults := make(map[string]modelRequestDefaults)

	// Load whitelisted models from catalog service for optional validation.
	// In shared/cloud mode, DB provider configuration is the runtime source of truth,
	// so we do NOT gate enabled_models by catalog whitelist.
	//
	// Both of these are server-scoped values and must be resolved the way
	// isSharedMode() already resolves shared mode: from the server context,
	// falling back from the request context. See catalogServiceFor.
	var allowed map[string]map[string]struct{}
	var catalogCache *catalogsvc.Cache
	enforceCatalogWhitelist := !s.isSharedMode()
	if catalogSvc := s.catalogServiceFor(ctx); catalogSvc != nil {
		allowed = catalogSvc.GetModelWhitelist()
		catalogCache = catalogSvc.GetCache()
	}

	// Build aggregated inputs from database configs
	for _, config := range configs {
		if !config.IsActive || len(config.EnabledModels) == 0 {
			continue
		}

		provider := strings.ToLower(config.ProviderName)

		// Filter models only when whitelist enforcement is enabled.
		// Shared/cloud mode uses DB-configured models directly.
		var filtered []string
		for _, model := range config.EnabledModels {
			if !enforceCatalogWhitelist || allowed == nil {
				filtered = append(filtered, model)
				continue
			}
			if set, ok := allowed[provider]; ok {
				if _, ok := set[strings.ToLower(model)]; ok {
					filtered = append(filtered, model)
				}
			}
		}

		if len(filtered) == 0 {
			continue
		}

		// Add to model maps
		for _, model := range filtered {
			alias := strings.ToLower(model)
			modelMap[alias] = provider
			modelRoutes[alias] = gw.ModelRoute{ProviderName: provider, ModelName: model}
		}

		// Create or get aggregated input
		agg := aggregated[provider]
		if agg == nil {
			agg = &factory.AggregatedInput{Provider: provider}
			aggregated[provider] = agg
		}

		// Set custom base URL if provided
		if config.CustomBaseURL != nil && *config.CustomBaseURL != "" {
			agg.BaseURL = *config.CustomBaseURL
		}

		// Add models
		agg.Models = append(agg.Models, filtered...)

		// Provider-wide defaults: one set of values that applies to every model
		// under this provider unless the model overrides it. Parsed by the same
		// code as the model tier so both understand the same controls.
		if len(config.CustomSettings) > 0 {
			providerDefaults, err := parseProviderRequestDefaults(config.CustomSettings)
			if err != nil {
				logger.Warnf("gateway: ignoring invalid provider defaults for %s: %v", provider, err)
			} else if providerDefaults != (modelRequestDefaults{}) {
				dbProviderDefaults[provider] = providerDefaults
				logger.Debugf("gateway: loaded provider defaults for %s", provider)
			}

			if rawModelDefaults, ok := config.CustomSettings[modelParametersSetting]; ok {
				parsed, err := parseModelRequestDefaults(provider, rawModelDefaults)
				if err != nil {
					logger.Warnf("gateway: ignoring invalid model defaults for provider %s: %v", provider, err)
				} else {
					for key, defaults := range parsed {
						dbModelDefaults[key] = defaults
					}
				}
			}
		}

		// Fetch API keys for this provider from provider_api_keys table
		apiKeys, err := apiKeyRepo.ListByProviderConfig(ctx, config.ID)
		if err != nil {
			logger.Warnf("gateway: failed to load API keys for provider %s: %v", config.ProviderName, err)
			continue
		}

		logger.Debugf("gateway: provider %s has %d API keys in database", config.ProviderName, len(apiKeys))

		// Load API keys (stored as plaintext, no decryption needed)
		if len(apiKeys) > 0 {
			agg.APIKeys = make([]factory.APIKeyInput, 0, len(apiKeys))
			for _, key := range apiKeys {
				if !key.IsActive {
					logger.Debugf("gateway: skipping inactive key %s for provider %s", key.KeyName, config.ProviderName)
					continue
				}

				logger.Debugf("gateway: adding active key %s for provider %s", key.KeyName, config.ProviderName)
				agg.APIKeys = append(agg.APIKeys, factory.APIKeyInput{
					ID:           key.ID,
					KeyName:      key.KeyName,
					KeyEncrypted: key.KeyEncrypted, // Actually plaintext (field name is legacy)
					Weight:       key.Weight,
					IsActive:     key.IsActive,
					Source:       key.Source,
				})
			}
		}

		// Fallback: If no API keys found in provider_api_keys table, use the one from provider_config (legacy)
		if len(agg.APIKeys) == 0 && config.APIKeyEncrypted != "" {
			logger.Debugf("gateway: using legacy API key for provider %s", config.ProviderName)
			agg.APIKey = config.APIKeyEncrypted // Actually plaintext (field name is legacy)
		}

		// Skip provider if no active keys available
		if len(agg.APIKeys) == 0 && agg.APIKey == "" {
			logger.Warnf("gateway: skipping provider %s - no active API keys available", config.ProviderName)
			delete(aggregated, provider)
			continue
		}

		logger.Debugf("gateway: provider %s will be loaded with %d active keys", config.ProviderName, len(agg.APIKeys))
	}

	// Extract sticky key from context for consistent API key selection
	stickyKey := s.hashKeyFromContext(ctx)

	compactCfg := compactConfigFromFeatures(s.feat)
	summarizerResolve := factory.SummarizerResolver(s.makeCompactSummarizerResolver())

	// Build providers from aggregated inputs
	for prov, in := range aggregated {
		in.StickyKey = stickyKey
		in.CatalogCache = catalogCache // Pass catalog for cost calculation
		in.CompactConfig = compactCfg
		in.SummarizerResolve = summarizerResolve
		if f, ok := factory.Default.Get(prov); ok {
			if p, err := f(*in); err == nil && p != nil {
				reg.Register(p)
			} else if err != nil {
				logger.Warnf("gateway: failed to create provider %s: %v", prov, err)
			}
		}
	}

	router := gw.NewRouter(reg, modelMap)
	router.SetModelRoutes(modelRoutes)

	// Clear default model cache since provider configs changed. The cache
	// is process-wide (model name string) and changing tenants can change
	// what model "default" means; clearing on every load keeps it honest
	// at minor cost.
	s.invalidateDefaultModelCache()

	providerNames := make([]string, 0, len(aggregated))
	for provName := range aggregated {
		providerNames = append(providerNames, provName)
	}
	logger.Infof("gateway: loaded %d providers from database: %v", len(aggregated), providerNames)
	for model, prov := range modelMap {
		logger.Debugf("gateway: model '%s' mapped to provider '%s'", model, prov)
	}

	return &tenantBundle{
		reg:              reg,
		router:           router,
		providerDefaults: dbProviderDefaults,
		modelDefaults:    dbModelDefaults,
	}, nil
}

// RefreshProviders reloads providers from the database (for hot-reload). In
// shared mode the bundle is cached under the request's tenant key; in
// single-tenant mode it replaces the process-wide default.
func (s *Server) RefreshProviders(ctx context.Context, providerRepo *provider_config.Repository, apiKeyRepo provider_api_keys.Repository) error {
	logger.Info("gateway: refreshing providers from database")
	bundle, err := s.bootstrapFromDatabase(ctx, providerRepo, apiKeyRepo)
	if err != nil {
		return err
	}
	s.installBundle(tenantKeyFromContext(ctx), bundle)
	s.invalidateDefaultModelCache()
	return nil
}

// seedDatabaseFromYAML loads provider configs from YAML and saves to database (one-time seeding)
func (s *Server) seedDatabaseFromYAML(ctx context.Context, providerRepo *provider_config.Repository, apiKeyRepo provider_api_keys.Repository) error {
	if s.cfg == nil {
		return fmt.Errorf("no gateway config available to seed")
	}

	// Shared gateway runs with a minimal in-memory config (gateway.models is empty).
	// In that mode, build seed models from the loaded catalog + provider env vars.
	if len(s.cfg.Models) == 0 {
		sharedMode, _ := s.ctx.Value(contextkeys.SharedGatewayMode).(bool)
		if sharedMode {
			if seeded := s.buildSharedSeedModelsFromCatalog(); len(seeded) > 0 {
				s.cfg.Models = seeded
				logger.Infof("gateway: shared mode built %d provider seed entries from catalog", len(seeded))
			}
		}
	}

	if len(s.cfg.Models) == 0 {
		return fmt.Errorf("no models in gateway config to seed")
	}

	// Use existing config_sync logic to load and save
	yamlConfigs, err := config_sync.LoadFromGatewayConfig(s.cfg)
	if err != nil {
		return fmt.Errorf("failed to load from YAML: %w", err)
	}

	logger.Infof("gateway: seeding %d provider configurations to database", len(yamlConfigs))

	for _, config := range yamlConfigs {
		if err := providerRepo.Upsert(ctx, config); err != nil {
			logger.Errorf("gateway: failed to seed provider %s: %v", config.ProviderName, err)
			continue
		}

		// Seed API key if present
		if config.APIKeyEncrypted != "" {
			apiKey := &provider_api_keys.ProviderAPIKey{
				ProviderConfigID: config.ID,
				KeyName:          "Config API Key",
				KeyEncrypted:     config.APIKeyEncrypted,
				Weight:           1,
				IsActive:         true,
				Source:           "config",
			}
			err := apiKeyRepo.UpsertConfigKey(ctx, apiKey)
			if errors.Is(err, provider_api_keys.ErrConfigKeyDuplicatesManual) {
				logger.Infof("gateway: provider %s already has this credential as a user-managed key, not seeding a config key", config.ProviderName)
			} else if err != nil {
				logger.Errorf("gateway: failed to seed API key for %s: %v", config.ProviderName, err)
			}
		}
	}

	return nil
}

// buildSharedSeedModelsFromCatalog builds gateway.models entries for shared mode
// from the loaded catalog. Only providers with a configured API key env var are
// included (except ollama, which can run without an API key).
func (s *Server) buildSharedSeedModelsFromCatalog() []validator.ModelConfig {
	catalogSvcAny := s.ctx.Value(contextkeys.CatalogService)
	if catalogSvcAny == nil {
		logger.Warn("gateway: shared seed skipped, catalog service not available")
		return nil
	}
	catalogSvc, ok := catalogSvcAny.(*catalogsvc.Service)
	if !ok || catalogSvc == nil {
		logger.Warn("gateway: shared seed skipped, invalid catalog service in context")
		return nil
	}

	cache := catalogSvc.GetCache()
	if cache == nil {
		logger.Warn("gateway: shared seed skipped, catalog cache not available")
		return nil
	}

	whitelist := cache.GetModelWhitelist()
	if len(whitelist) == 0 {
		logger.Warn("gateway: shared seed skipped, catalog whitelist is empty")
		return nil
	}

	providers := make([]string, 0, len(whitelist))
	for provider := range whitelist {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	out := make([]validator.ModelConfig, 0, len(providers))
	for _, provider := range providers {
		modelDefs, ok := cache.GetAllModels(provider)
		if !ok || len(modelDefs) == 0 {
			continue
		}

		models := make([]string, 0, len(modelDefs))
		for _, m := range modelDefs {
			if m == nil || m.Name == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(m.Status), "deprecated") {
				continue
			}
			models = append(models, m.Name)
		}
		if len(models) == 0 {
			continue
		}

		pd, _ := cache.GetProvider(provider)
		envVar := ""
		baseURL := ""
		if pd != nil {
			envVar = strings.TrimSpace(pd.Auth.EnvVar)
			baseURL = strings.TrimSpace(pd.BaseURL)
		}

		apiKey := ""
		if envVar != "" {
			apiKey = strings.TrimSpace(os.Getenv(envVar))
		}

		// Most providers require an API key; Ollama can run keyless locally.
		if apiKey == "" && provider != "ollama" {
			continue
		}

		out = append(out, validator.ModelConfig{
			Provider: provider,
			Model:    models,
			APIKey:   apiKey,
			BaseURL:  baseURL,
			// Pick one stable default model if multiple providers are available.
			Default: len(out) == 0,
		})
	}

	return out
}

// markFailedKey marks an API key as temporarily failed (due to auth error)
func (s *Server) markFailedKey(keyID string) {
	s.failedKeyMutex.Lock()
	defer s.failedKeyMutex.Unlock()
	s.failedKeys[keyID] = true
	logger.Debugf("gateway: marked key %s as failed", keyID)
}

// clearFailedKey removes the failed marker from an API key (after successful use)
func (s *Server) clearFailedKey(keyID string) {
	s.failedKeyMutex.Lock()
	defer s.failedKeyMutex.Unlock()
	delete(s.failedKeys, keyID)
}

// isKeyFailed checks if an API key is marked as failed
func (s *Server) isKeyFailed(keyID string) bool {
	s.failedKeyMutex.RLock()
	defer s.failedKeyMutex.RUnlock()
	return s.failedKeys[keyID]
}

// attemptKeyRotation tries to re-instantiate a provider with a different API key
// Returns true if rotation succeeded, false if no more keys available
func (s *Server) attemptKeyRotation(ctx context.Context, modelAlias string) (rotated bool, currentKeyID string) {
	// Get repositories from context
	providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo)
	apiKeyRepoAny := s.ctx.Value(contextkeys.APIKeyRepo)
	if providerRepoAny == nil || apiKeyRepoAny == nil {
		logger.Debug("gateway: key rotation not available (no repos in context)")
		return false, ""
	}

	providerRepo := providerRepoAny.(*provider_config.Repository)
	apiKeyRepo := apiKeyRepoAny.(provider_api_keys.Repository)

	// Find which provider supports this model. Resolve via the per-tenant
	// router so a stale-key rotation in tenant A doesn't reach into tenant
	// B's registry.
	router := s.routerFor(ctx)
	if router == nil {
		return false, ""
	}
	provider, route, err := router.ResolveWithContext(ctx, modelAlias)

	if err != nil || provider == nil {
		logger.Debugf("gateway: could not find provider for model %s", modelAlias)
		return false, ""
	}

	// Get provider name from route
	providerName := route.ProviderName

	// Tenant-scoped — rotating a failed key for tenant A must not search
	// or mutate tenant B's rows.
	configs, err := listProviderConfigs(ctx, providerRepo)
	if err != nil {
		logger.Warnf("gateway: failed to get provider configs for key rotation: %v", err)
		return false, ""
	}

	var matchingConfig *provider_config.Configuration
	for _, cfg := range configs {
		if strings.EqualFold(cfg.ProviderName, providerName) {
			matchingConfig = cfg
			break
		}
	}

	if matchingConfig == nil {
		logger.Debugf("gateway: no provider config found for provider %s (model: %s)", providerName, modelAlias)
		return false, ""
	}

	// Get all API keys for this provider
	allKeys, err := apiKeyRepo.ListByProviderConfig(ctx, matchingConfig.ID)
	if err != nil {
		logger.Warnf("gateway: failed to get API keys for key rotation: %v", err)
		return false, ""
	}

	// Filter to active keys that haven't failed
	var availableKeys []*provider_api_keys.ProviderAPIKey
	for _, key := range allKeys {
		if key.IsActive && !s.isKeyFailed(key.ID) {
			availableKeys = append(availableKeys, key)
		}
	}

	if len(availableKeys) == 0 {
		logger.Infof("gateway: no more API keys available for provider %s", matchingConfig.ProviderName)
		return false, ""
	}

	logger.Infof("gateway: attempting key rotation for provider %s (model: %s), %d keys available",
		matchingConfig.ProviderName, modelAlias, len(availableKeys))

	// Build API keys input
	apiKeys := make([]factory.APIKeyInput, 0, len(availableKeys))
	for _, k := range availableKeys {
		apiKeys = append(apiKeys, factory.APIKeyInput{
			ID:           k.ID,
			KeyName:      k.KeyName,
			KeyEncrypted: k.KeyEncrypted, // Plaintext now
			Weight:       k.Weight,
			IsActive:     k.IsActive,
			Source:       k.Source,
		})
	}

	stickyKey := s.hashKeyFromContext(ctx)
	if stickyKey == "" {
		stickyKey = correlation.GetCorrelationID(ctx)
	}

	baseURL := ""
	if matchingConfig.CustomBaseURL != nil {
		baseURL = *matchingConfig.CustomBaseURL
	}

	// The rotated provider is a full replacement for the tenant's entry, so
	// it needs every input the bootstrap paths pass -- including the catalog.
	// Omitting it here silently dropped cost calculation and canonical model
	// identity for the rest of the bundle's life after any key rotation.
	var rotationCatalog *catalogsvc.Cache
	if catalogSvc := s.catalogServiceFor(ctx); catalogSvc != nil {
		rotationCatalog = catalogSvc.GetCache()
	}

	aggInput := factory.AggregatedInput{
		Provider:          matchingConfig.ProviderName,
		Models:            matchingConfig.EnabledModels,
		BaseURL:           baseURL,
		APIKey:            "", // Not used with multi-key path
		APIKeys:           apiKeys,
		StickyKey:         stickyKey,
		CatalogCache:      rotationCatalog,
		CompactConfig:     compactConfigFromFeatures(s.feat),
		SummarizerResolve: factory.SummarizerResolver(s.makeCompactSummarizerResolver()),
	}

	// Re-initialize this provider
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get factory for this provider
	factoryFn, ok := factory.Default.Get(matchingConfig.ProviderName)
	if !ok {
		logger.Warnf("gateway: no factory found for provider %s", matchingConfig.ProviderName)
		return false, ""
	}

	// Create new provider instance
	newProvider, err := factoryFn(aggInput)
	if err != nil {
		logger.Warnf("gateway: failed to re-create provider %s: %v", matchingConfig.ProviderName, err)
		return false, ""
	}

	// Build a fresh bundle for the request's tenant: copy every provider
	// except the one being rotated, then add the rotated provider. The
	// previous version mutated the global s.reg/s.router which raced with
	// other tenants' requests; the new bundle replaces only this tenant's
	// cached entry.
	currentReg := s.regFor(ctx)
	newReg := gw.NewRegistry()
	if currentReg != nil {
		for name, prov := range currentReg.All() {
			if !strings.EqualFold(name, matchingConfig.ProviderName) {
				newReg.Register(prov)
			}
		}
	}
	newReg.Register(newProvider)

	modelToProvider := make(map[string]string)
	for _, model := range matchingConfig.EnabledModels {
		modelToProvider[strings.ToLower(model)] = matchingConfig.ProviderName
	}
	newRouter := gw.NewRouter(newReg, modelToProvider)
	rotatedBundle := &tenantBundle{reg: newReg, router: newRouter}
	if current := s.providersFor(ctx); current != nil {
		rotatedBundle.providerDefaults = current.providerDefaults
		rotatedBundle.modelDefaults = current.modelDefaults
	}
	s.installBundle(tenantKeyFromContext(ctx), rotatedBundle)

	logger.Infof("gateway: successfully rotated keys for provider %s", matchingConfig.ProviderName)

	// Return the first available key ID so it can be marked as failed if it also fails
	if len(availableKeys) > 0 {
		return true, availableKeys[0].ID
	}
	return true, ""
}

// catalogServiceFor resolves the model catalog for a provider bootstrap.
//
// The catalog service is installed once on the server context at startup
// (cmd/serve/start_api.go). Request contexts do NOT inherit it -- gRPC and
// Connect build each request context from scratch, which is exactly why the
// chat processor has to copy contextkeys.ProviderRepo across by hand.
//
// In shared multi-tenant mode EnsureProvidersForRequest rebuilds a tenant's
// provider bundle from the REQUEST context, so a ctx-only lookup found no
// catalog and every per-tenant bundle was built with CatalogCache = nil.
// TracingMiddleware only constructs a CostCalculator when that cache is
// non-nil, so provider spans for those tenants carried no llm.cost.total at
// all and the public catalog reported a flat zero cost for them --
// Anthropic's entire production footprint, in practice. The same nil cache
// also forced setModelMetricsIdentity onto its derived publisher/canonical
// fallback instead of the catalog's own identity.
//
// Resolving from the request context first keeps a caller's explicit override
// working (the boot path passes the server context itself); the s.ctx
// fallback is what makes per-request bootstraps correct.
func (s *Server) catalogServiceFor(ctx context.Context) *catalogsvc.Service {
	if ctx != nil {
		if svc, ok := ctx.Value(contextkeys.CatalogService).(*catalogsvc.Service); ok && svc != nil {
			return svc
		}
	}
	if s != nil && s.ctx != nil {
		if svc, ok := s.ctx.Value(contextkeys.CatalogService).(*catalogsvc.Service); ok && svc != nil {
			return svc
		}
	}
	return nil
}
