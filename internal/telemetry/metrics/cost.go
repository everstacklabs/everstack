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
	CachedTokens     int // Number of tokens served from cache

	// Cost split, in USD. Traces report input and output separately, so this
	// calculator has to expose the breakdown it already computes rather than
	// only the total. CachedCost is the cache-read portion; for OpenAI-shaped
	// providers those tokens are a subset of the input count, so InputCost
	// covers only the non-cached remainder.
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

// CalculateCost calculates the cost for a request using the model catalog.
// The catalog is the single source of truth for pricing. If the model is not
// found in the catalog, cost is returned as zero rather than using hardcoded
// estimates — accurate data is better than wrong data.
func (cc *CostCalculator) CalculateCost(provider, model string, inputTokens, outputTokens, cachedTokens int) CostDetails {
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
	modelDef, found := models.GetModel(provider, model)
	if !found {
		modelDef, found = models.GetModelByPrefix(provider, model)
	}
	if !found {
		if norm := normalizeModelID(model); norm != model {
			if modelDef, found = models.GetModel(provider, norm); !found {
				modelDef, found = models.GetModelByPrefix(provider, norm)
			}
		}
	}
	if !found {
		// Model not in catalog — return zero cost. Do not use hardcoded pricing.
		return CostDetails{
			PricingModel: "unknown",
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CachedTokens: cachedTokens,
		}
	}

	// Calculate costs
	// Prices in catalog are per 1k tokens, convert to per-token
	inputPricePerToken := modelDef.InputCostPer1k / 1000.0
	outputPricePerToken := modelDef.OutputCostPer1k / 1000.0

	// Cache pricing differs by provider along two independent axes.
	//
	// COUNTING. For Anthropic, cache-read tokens are a SEPARATE count from
	// input_tokens (not a subset). For OpenAI-style providers, cached tokens
	// are a SUBSET of the prompt/input tokens. This is a difference in what
	// providers report, not in what they charge, and it is unchanged here.
	//
	// PRICE. Previously both branches multiplied the input rate by a constant
	// (0.1x Anthropic, 0.5x otherwise). The catalog publishes an exact
	// cache_read rate per model, so prefer it. The constants survive only as a
	// fallback for models with no published rate, which keeps behaviour
	// identical for catalogs synced before cache rates were carried through.
	// The old 0.5x constant was right for Groq and wrong elsewhere, over-
	// charging cached tokens 5x on OpenAI and Google and 25x on DeepSeek.
	anthropicShaped := usesAnthropicCachePricing(provider, model)

	fallbackMultiplier := 0.5
	if anthropicShaped {
		fallbackMultiplier = 0.1
	}
	cachedPricePerToken := inputPricePerToken * fallbackMultiplier
	if modelDef.CacheReadCostPer1k > 0 {
		cachedPricePerToken = modelDef.CacheReadCostPer1k / 1000.0
	}

	var inputCost, cachedCost float64
	if anthropicShaped {
		inputCost = float64(inputTokens) * inputPricePerToken
		cachedCost = float64(cachedTokens) * cachedPricePerToken
	} else {
		nonCachedInputTokens := inputTokens - cachedTokens
		if nonCachedInputTokens < 0 {
			nonCachedInputTokens = 0
		}
		inputCost = float64(nonCachedInputTokens) * inputPricePerToken
		cachedCost = float64(cachedTokens) * cachedPricePerToken
	}
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

// normalizeModelID strips a trailing context-window / variant marker such as
// the "[1m]" in "claude-opus-4-8[1m]" so the base model resolves in the
// catalog. Returns the input unchanged when there is no bracket suffix.
func normalizeModelID(model string) string {
	if i := strings.IndexByte(model, '['); i > 0 {
		return strings.TrimRight(model[:i], "-")
	}
	return model
}

// usesAnthropicCachePricing reports whether a request should be priced with
// Anthropic prompt-caching semantics — cache-read tokens are a separate count
// billed at ~0.1x input — rather than the OpenAI model where cached tokens are
// a subset of input billed at ~0.5x. Covers Claude served via any route.
func usesAnthropicCachePricing(provider, model string) bool {
	if strings.Contains(strings.ToLower(provider), "anthropic") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(model), "claude")
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
