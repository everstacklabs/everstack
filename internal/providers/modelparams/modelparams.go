// Package modelparams drops request controls a model's upstream API rejects,
// as declared by the catalog's per-model unsupported_parameters list.
//
// Some newer models refuse fields the gateway otherwise forwards verbatim
// (for example temperature on reasoning-only models, or an explicit thinking
// switch on models whose thinking is always on). Sending them turns an
// otherwise valid call into an upstream 400. The filter runs once, at the
// provider middleware layer every adapter is wrapped in, so every chat path
// (unary, streaming, HTTP, Connect, agent runtime) gets the same treatment.
//
// Only controls whose removal leaves the request's meaning intact are
// handled. reasoning_effort and a forced tool_choice are deliberately left
// alone: dropping them would silently change behaviour, so the provider's own
// error is the better outcome.
package modelparams

import (
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// DropUnsupported clears the sampling controls the catalog marks as
// unsupported for (provider, req.Model) and returns the catalog keys it
// dropped, in a stable order. A nil cache, a model the catalog does not
// describe, or a model with no unsupported_parameters leaves the request
// untouched and returns nil.
//
// Only fields of req.Sampling are reassigned (pointers are set to nil, never
// written through), so a caller that shares pointer values between request
// copies, such as a fallback attempt for another model, is unaffected.
func DropUnsupported(cache *catalog.Cache, provider string, req *gw.ChatCompletionRequest) []string {
	if cache == nil || req == nil {
		return nil
	}
	definition, ok := resolveModel(cache, provider, req.Model)
	if !ok || definition == nil || len(definition.UnsupportedParameters) == 0 {
		return nil
	}

	sampling := &req.Sampling
	var dropped []string
	if definition.RejectsParameter(catalog.UnsupportedTemperature) &&
		(sampling.TemperatureConfigured || sampling.Temperature != 0) {
		sampling.Temperature = 0
		sampling.TemperatureConfigured = false
		dropped = append(dropped, catalog.UnsupportedTemperature)
	}
	if definition.RejectsParameter(catalog.UnsupportedTopP) &&
		(sampling.TopPConfigured || sampling.TopP != 0) {
		sampling.TopP = 0
		sampling.TopPConfigured = false
		dropped = append(dropped, catalog.UnsupportedTopP)
	}
	if definition.RejectsParameter(catalog.UnsupportedTopK) && sampling.TopK != nil {
		sampling.TopK = nil
		dropped = append(dropped, catalog.UnsupportedTopK)
	}
	if definition.RejectsParameter(catalog.UnsupportedThinkingToggle) && sampling.ReasoningEnabled != nil {
		sampling.ReasoningEnabled = nil
		dropped = append(dropped, catalog.UnsupportedThinkingToggle)
	}
	if definition.RejectsParameter(catalog.UnsupportedThinkingBudget) && sampling.ReasoningBudget != nil {
		sampling.ReasoningBudget = nil
		dropped = append(dropped, catalog.UnsupportedThinkingBudget)
	}

	if len(dropped) > 0 {
		// Names only: never the request content or any credential.
		logger.WithFields(
			"provider", provider,
			"model", req.Model,
			"dropped_parameters", strings.Join(dropped, ","),
		).Debug("gateway: dropped request parameters the model does not support")
	}
	return dropped
}

// providerKeyAliases maps an adapter's Name(), which telemetry depends on and
// must not change, to its catalog provider key where the two differ. The
// Gemini API adapter reports "gemini" but its catalog directory is "google".
var providerKeyAliases = map[string]string{
	"gemini": "google",
}

// CatalogProviderKey returns the catalog provider key for an adapter name.
func CatalogProviderKey(provider string) string {
	key := strings.ToLower(strings.TrimSpace(provider))
	if alias, ok := providerKeyAliases[key]; ok {
		return alias
	}
	return key
}

// bedrockRegionPrefixes are the geography segments AWS Bedrock puts in front
// of a model id to form a cross-region inference profile id
// ("us.anthropic.claude-fable-5-2" -> "anthropic.claude-fable-5-2").
var bedrockRegionPrefixes = []string{"us", "us-gov", "eu", "apac", "jp", "au", "ca", "global"}

// resolveModel finds the catalog entry whose unsupported_parameters apply to
// a request. It first runs the catalog's own ResolveModel (exact id, variant
// marker, dated snapshot) and only when that finds nothing tries the forms a
// hosting provider puts on the same model:
//
//   - aws-bedrock: a cross-region inference profile id, with one leading
//     region segment removed;
//   - vertex-ai: a bare or pinned Claude id, with any "@version" replaced by
//     "@default" (or "@default" appended when there is no "@").
//
// These rules live here rather than in catalog.ResolveModel on purpose: a
// regional Bedrock profile is priced differently from the base model, so
// cost calculation must not inherit this mapping. Dropping a parameter the
// model rejects is correct whatever the region, so the filter can.
func resolveModel(cache *catalog.Cache, provider, model string) (*catalog.ModelDefinition, bool) {
	provider = CatalogProviderKey(provider)
	if definition, ok := cache.ResolveModel(provider, model); ok {
		return definition, true
	}
	candidate, ok := hostedModelCandidate(provider, model)
	if !ok {
		return nil, false
	}
	return cache.ResolveModel(provider, candidate)
}

// hostedModelCandidate rewrites a hosted model id into the catalog's form for
// the providers that need it, reporting false when no rewrite applies.
func hostedModelCandidate(provider, model string) (string, bool) {
	model = strings.TrimSpace(model)
	switch provider {
	case "aws-bedrock":
		region, rest, found := strings.Cut(model, ".")
		if !found || rest == "" {
			return "", false
		}
		for _, prefix := range bedrockRegionPrefixes {
			if strings.EqualFold(region, prefix) {
				return rest, true
			}
		}
		return "", false
	case "vertex-ai":
		base := catalog.StripModelVariant(model)
		if base == "" {
			return "", false
		}
		if i := strings.IndexByte(base, '@'); i >= 0 {
			if i == 0 || strings.EqualFold(base[i:], "@default") {
				return "", false
			}
			base = base[:i]
		}
		return base + "@default", true
	}
	return "", false
}
