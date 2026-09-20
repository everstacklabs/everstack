package metrics

import (
	"strings"

	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// CostDetails contains comprehensive cost information
type CostDetails struct {
	EstimatedUSD     float64
	ActualUSD        float64
	SavingsUSD       float64
	PricingModel     string
	CarbonSavedGrams float64
	InputTokens      int
	OutputTokens     int
	CachedTokens     int // CacheReadTokens + CacheWriteTokens

	// Cost split, in USD. Traces report input and output separately, so this
	// calculator has to expose the breakdown it already computes rather than
	// only the total. CachedCost includes cache reads and writes; InputCost
	// covers only the fresh, non-cached remainder.
	InputCost  float64
	CachedCost float64
	OutputCost float64
}

// CostCalculator provides cost calculation using the model catalog
type CostCalculator struct {
	catalogService *catalog.Service
	catalogCache   *catalog.Cache
}

// NewCostCalculator creates a new cost calculator
func NewCostCalculator(catalogService *catalog.Service) *CostCalculator {
	return &CostCalculator{
		catalogService: catalogService,
	}
}

// NewCostCalculatorFromCache creates a calculator over an already-resolved
// catalog cache. Provider middleware is constructed with a cache rather than
// the catalog service, and previously had to use a separate calculator that
// knew nothing about cache pricing — so traces and billing disagreed on any
// cached request.
func NewCostCalculatorFromCache(catalogCache *catalog.Cache) *CostCalculator {
	return &CostCalculator{
		catalogCache: catalogCache,
	}
}

// models resolves the model lookup, from whichever source this calculator was
// built with. Returns nil when neither is available.
func (cc *CostCalculator) models() *catalog.Cache {
	if cc.catalogCache != nil {
		return cc.catalogCache
	}
	if cc.catalogService != nil {
		return cc.catalogService.GetModels()
	}
	return nil
}

// CalculateCost preserves the original API's provider-specific token-counting
// contract: Anthropic inputTokens is fresh-only and cachedTokens is separate;
// other providers report cachedTokens as a subset of inputTokens. New live
// Usage call sites should use CalculateCostWithCacheTokens, whose input is
// provider-neutral and inclusive.
func (cc *CostCalculator) CalculateCost(provider, model string, inputTokens, outputTokens, cachedTokens int) CostDetails {
	inclusiveInputTokens := inputTokens
	if cachedTokens > 0 && usesAnthropicCachePricing(provider, model) {
		inclusiveInputTokens += cachedTokens
	}
	result := cc.calculateCostWithCacheTokens(
		provider,
		model,
		inclusiveInputTokens,
		outputTokens,
		cachedTokens,
		0,
		inputTokens,
	)
	// Preserve the legacy result field as well as its input convention.
	result.InputTokens = inputTokens
	return result
}

// CalculateCostWithCacheTokens calculates request cost from the catalog using
// the gateway usage invariant: inputTokens is inclusive, while cache reads and
// writes are non-overlapping subsets of it. If the model is not found, cost is
// returned as zero rather than using hardcoded estimates.
func (cc *CostCalculator) CalculateCostWithCacheTokens(provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) CostDetails {
	return cc.calculateCostWithCacheTokens(
		provider,
		model,
		inputTokens,
		outputTokens,
		cacheReadTokens,
		cacheWriteTokens,
		inputTokens,
	)
}

func (cc *CostCalculator) calculateCostWithCacheTokens(provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, thresholdInputTokens int) CostDetails {
	if cacheReadTokens < 0 {
		cacheReadTokens = 0
	}
	if cacheWriteTokens < 0 {
		cacheWriteTokens = 0
	}
	cachedTokens := cacheReadTokens + cacheWriteTokens

	// Get model from catalog. Try the exact id, then a prefix match for
	// date-versioned models ("gpt-4o-2024-08-06" → "gpt-4o"), then both again
	// against a variant-normalized id so context-window markers like
	// "claude-opus-4-8[1m]" resolve to their base model instead of costing zero
	// (GetModelByPrefix only matches on a "-" boundary, so "[1m]" never matched).
	models := cc.models()
	if models == nil {
		return CostDetails{
			PricingModel: "unknown",
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CachedTokens: cachedTokens,
		}
	}
	modelDef, resolvedProvider, found := resolveCatalogModel(models, provider, model)
	if !found {
		// Model not in catalog — return zero cost. Do not use hardcoded pricing.
		return CostDetails{
			PricingModel: "unknown",
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CachedTokens: cachedTokens,
		}
	}

	// Prices in the catalog are per 1k tokens; convert to per-token rates.
	baseInputPricePerToken := modelDef.InputCostPer1k / 1000.0
	baseOutputPricePerToken := modelDef.OutputCostPer1k / 1000.0
	inputMultiplier, cacheMultiplier, outputMultiplier := 1.0, 1.0, 1.0
	if modelDef.LongContextThresholdTokens > 0 && thresholdInputTokens > modelDef.LongContextThresholdTokens {
		if modelDef.LongContextInputMultiplier > 0 {
			inputMultiplier = modelDef.LongContextInputMultiplier
		}
		if modelDef.LongContextCacheMultiplier > 0 {
			cacheMultiplier = modelDef.LongContextCacheMultiplier
		}
		if modelDef.LongContextOutputMultiplier > 0 {
			outputMultiplier = modelDef.LongContextOutputMultiplier
		}
	}
	inputPricePerToken := baseInputPricePerToken * inputMultiplier
	outputPricePerToken := baseOutputPricePerToken * outputMultiplier

	// Prefer exact catalog rates. The historical provider-shaped cache-read
	// multipliers remain only for older catalogs without those fields. Cache
	// writes default to the common 1.25x input rate for the same compatibility
	// reason; current OpenAI and Anthropic models publish an exact write rate.
	fallbackMultiplier := 0.5
	if usesAnthropicCachePricing(resolvedProvider, model) {
		fallbackMultiplier = 0.1
	}
	cacheReadPricePerToken := baseInputPricePerToken * fallbackMultiplier
	if modelDef.CacheReadCostPer1k > 0 {
		cacheReadPricePerToken = modelDef.CacheReadCostPer1k / 1000.0
	}
	cacheReadPricePerToken *= cacheMultiplier

	cacheWritePricePerToken := baseInputPricePerToken * 1.25
	if modelDef.CacheWriteCostPer1k > 0 {
		cacheWritePricePerToken = modelDef.CacheWriteCostPer1k / 1000.0
	}
	cacheWritePricePerToken *= cacheMultiplier

	freshInputTokens := inputTokens - cachedTokens
	if freshInputTokens < 0 {
		freshInputTokens = 0
	}
	inputCost := float64(freshInputTokens) * inputPricePerToken
	cacheReadCost := float64(cacheReadTokens) * cacheReadPricePerToken
	cacheWriteCost := float64(cacheWriteTokens) * cacheWritePricePerToken
	cachedCost := cacheReadCost + cacheWriteCost
	outputCost := float64(outputTokens) * outputPricePerToken
	totalCost := inputCost + cachedCost + outputCost

	return CostDetails{
		EstimatedUSD:     totalCost,
		ActualUSD:        totalCost, // Would be updated from provider billing
		SavingsUSD:       0,
		PricingModel:     "pay_per_token",
		CarbonSavedGrams: 0, // Not saved yet, only calculated for cache hits
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CachedTokens:     cachedTokens,
		InputCost:        inputCost,
		CachedCost:       cachedCost,
		OutputCost:       outputCost,
	}
}

func resolveCatalogModel(models *catalog.Cache, provider, model string) (*catalog.ModelDefinition, string, bool) {
	model = strings.TrimSpace(model)
	provider = strings.TrimSpace(provider)
	if strings.HasPrefix(model, "@") {
		if parts := strings.SplitN(strings.TrimPrefix(model, "@"), "/", 2); len(parts) == 2 {
			if provider == "" {
				provider = parts[0]
			}
			model = parts[1]
		}
	}

	lookup := func(providerName string) (*catalog.ModelDefinition, bool) {
		definition, found := models.GetModel(providerName, model)
		if !found {
			definition, found = models.GetModelByPrefix(providerName, model)
		}
		if !found {
			if normalized := normalizeModelID(model); normalized != model {
				if definition, found = models.GetModel(providerName, normalized); !found {
					definition, found = models.GetModelByPrefix(providerName, normalized)
				}
			}
		}
		return definition, found
	}

	if provider != "" {
		definition, found := lookup(provider)
		return definition, provider, found
	}

	// Historical agent events did not persist a provider. Resolve an
	// unqualified model only when the catalog match is unique; ambiguous model
	// names remain unpriced rather than being attributed to the wrong vendor.
	var matchedDefinition *catalog.ModelDefinition
	matchedProvider := ""
	for providerName := range models.GetModelWhitelist() {
		definition, found := lookup(providerName)
		if !found {
			continue
		}
		if matchedDefinition != nil {
			return nil, "", false
		}
		matchedDefinition = definition
		matchedProvider = providerName
	}
	return matchedDefinition, matchedProvider, matchedDefinition != nil
}

// normalizeModelID strips a trailing context-window / variant marker such as
// the "[1m]" in "claude-opus-4-8[1m]" so the base model resolves in the
// catalog. Returns the input unchanged when there is no bracket suffix.
func normalizeModelID(model string) string {
	if i := strings.IndexByte(model, '['); i > 0 {
		return strings.TrimRight(model[:i], "-")
	}
	return model
}

// usesAnthropicCachePricing selects the historical 0.1x cache-read fallback
// for catalogs that do not publish an exact rate. It covers Claude served via
// any route; token counting itself is provider-neutral and always follows the
// inclusive gateway Usage contract.
func usesAnthropicCachePricing(provider, model string) bool {
	if strings.Contains(strings.ToLower(provider), "anthropic") {
		return true
	}
	model = strings.ToLower(model)
	return strings.HasPrefix(model, "claude") || strings.HasPrefix(model, "@anthropic/claude")
}

// CalculateCacheSavings calculates cost savings from cache hit
// For a complete cache hit, the entire response is served from cache
func (cc *CostCalculator) CalculateCacheSavings(provider, model string, inputTokens, outputTokens int) CostDetails {
	// Calculate what the cost would have been without cache
	fullCost := cc.CalculateCost(provider, model, inputTokens, outputTokens, 0)

	// For cache hits, actual cost is zero (or minimal retrieval cost)
	// Savings is the full estimated cost
	fullCost.ActualUSD = 0
	fullCost.SavingsUSD = fullCost.EstimatedUSD

	// Calculate carbon savings
	carbonIntensity := getCarbonIntensity(provider)
	totalTokens := inputTokens + outputTokens
	fullCost.CarbonSavedGrams = (float64(totalTokens) / 1000) * carbonIntensity

	return fullCost
}

// CalculatePartialCacheSavings calculates savings when some tokens are cached
// This is useful for prompt caching where part of the input is reused
func (cc *CostCalculator) CalculatePartialCacheSavings(provider, model string, inputTokens, outputTokens, cachedTokens int) CostDetails {
	// Calculate cost with cached tokens
	costWithCache := cc.CalculateCost(provider, model, inputTokens, outputTokens, cachedTokens)

	// Calculate what cost would have been without cache
	costWithoutCache := cc.CalculateCost(provider, model, inputTokens, outputTokens, 0)

	// Savings is the difference
	costWithCache.SavingsUSD = costWithoutCache.EstimatedUSD - costWithCache.EstimatedUSD

	// Calculate carbon savings for the cached portion
	carbonIntensity := getCarbonIntensity(provider)
	costWithCache.CarbonSavedGrams = (float64(cachedTokens) / 1000) * carbonIntensity

	return costWithCache
}

// getCarbonIntensity returns carbon intensity for a provider (grams CO2 per 1000 tokens)
// Based on provider data center locations and energy mix
func getCarbonIntensity(provider string) float64 {
	providerLower := strings.ToLower(provider)

	carbonMap := map[string]float64{
		"openai":    0.5,  // US-based, mixed energy
		"anthropic": 0.4,  // AWS regions with renewable energy
		"cohere":    0.45, // GCP with renewable energy
		"google":    0.3,  // GCP with high renewable percentage
		"meta":      0.5,  // Varies by hosting provider
		"mistral":   0.4,  // EU-based with renewable energy
	}

	if intensity, ok := carbonMap[providerLower]; ok {
		return intensity
	}
	return 0.5 // Default
}

// EstimateCarbonSavings estimates carbon savings from time saved
// Assumes average data center carbon intensity and GPU power consumption
func EstimateCarbonSavings(timeSavedMs int64, provider string) float64 {
	if timeSavedMs <= 0 {
		return 0
	}

	// GPU power consumption estimate
	// - Average GPU power: 300W for inference
	// - Average data center carbon intensity: 400g CO2/kWh
	// - Convert ms to hours, multiply by power, multiply by carbon intensity
	timeSavedHours := float64(timeSavedMs) / (1000 * 60 * 60)
	powerKWh := 0.3 * timeSavedHours // 300W = 0.3kW
	carbonGrams := powerKWh * 400    // 400g CO2/kWh

	return carbonGrams
}

// CalculateSpeedupFactor calculates how much faster a cached response is
func CalculateSpeedupFactor(cachedLatencyMs, normalLatencyMs float64) int {
	if cachedLatencyMs <= 0 || normalLatencyMs <= 0 {
		return 0
	}
	return int(normalLatencyMs / cachedLatencyMs)
}

// CalculateCacheEfficiency calculates cache efficiency percentage
func CalculateCacheEfficiency(cachedLatencyMs, normalLatencyMs float64) float64 {
	if normalLatencyMs <= 0 {
		return 0
	}
	timeSaved := normalLatencyMs - cachedLatencyMs
	efficiency := (timeSaved / normalLatencyMs) * 100

	if efficiency < 0 {
		return 0
	}
	if efficiency > 100 {
		return 100
	}

	return efficiency
}

// Global calculator instance (will be initialized with catalog service)
var globalCalculator *CostCalculator

// SetGlobalCalculator sets the global cost calculator instance
func SetGlobalCalculator(calculator *CostCalculator) {
	globalCalculator = calculator
}

// CalculateCost is a convenience function that uses the global calculator.
// Returns zero cost if the global calculator is not set — the catalog is the
// single source of truth and we never fall back to hardcoded pricing.
func CalculateCost(provider, model string, inputTokens, outputTokens, cachedTokens int) CostDetails {
	if globalCalculator != nil {
		return globalCalculator.CalculateCost(provider, model, inputTokens, outputTokens, cachedTokens)
	}
	// No catalog available — return zero cost rather than wrong cost
	return CostDetails{
		PricingModel: "unknown",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CachedTokens: cachedTokens,
	}
}

// CalculateCostWithCacheTokens is the full cache-aware global cost helper.
// It keeps cache reads and writes separate so each catalog rate is applied to
// the correct subset of the inclusive prompt token count.
func CalculateCostWithCacheTokens(provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) CostDetails {
	if globalCalculator != nil {
		return globalCalculator.CalculateCostWithCacheTokens(
			provider,
			model,
			inputTokens,
			outputTokens,
			cacheReadTokens,
			cacheWriteTokens,
		)
	}
	return CostDetails{
		PricingModel: "unknown",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CachedTokens: cacheReadTokens + cacheWriteTokens,
	}
}

// CalculateCacheSavings is a convenience function that uses the global calculator.
// Returns zero savings if the global calculator is not set.
func CalculateCacheSavings(provider, model string, inputTokens, outputTokens int) CostDetails {
	if globalCalculator != nil {
		return globalCalculator.CalculateCacheSavings(provider, model, inputTokens, outputTokens)
	}
	return CostDetails{
		PricingModel: "unknown",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
}
