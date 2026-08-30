package otlp

import (
	"strconv"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// Candidate attribute keys, in priority order, for the fields needed to price
// an LLM span. Coding agents (Claude Code, Codex, Gemini CLI) and the various
// OTel GenAI / OpenInference SDKs each spell these differently, so we coalesce
// across the known spellings — mirroring the read-side coalesce in
// internal/query/handlers/traces/semconv.go.
var (
	// Keys the trace read path actually reads for cost (see costAttrs in
	// semconv.go). If a span already carries one, it was priced upstream (the
	// gateway proxy path, or a prior enrichment pass) and we leave it alone.
	costPresentKeys = []string{attributes.CostEstimatedUSD, "llm.cost.total"}
	modelKeys       = []string{"gen_ai.request.model", "model", "gen_ai.response.model", "llm.model_name", "llm.model"}
	providerKeys    = []string{"gen_ai.system", "llm.provider", "llm.system", "provider"}
	inputTokenKeys  = []string{"input_tokens", "gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens", "llm.token_count.prompt", "llm.usage.prompt_tokens"}
	outputTokenKeys = []string{"output_tokens", "gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens", "llm.token_count.completion", "llm.usage.completion_tokens"}
	cacheReadKeys   = []string{"cache_read_tokens", "gen_ai.usage.cache_read_input_tokens", "llm.tokens.cache_read", "llm.token_count.cache_read"}
)

// costFn prices a single model call in USD. It is a package var so tests can
// stub it; in production it is the global catalog-backed calculator, which
// normalizes variant ids and applies provider-aware cache pricing, returning
// zero for any model not in the catalog.
var costFn = func(provider, model string, inputTokens, outputTokens, cacheReadTokens int) float64 {
	return metrics.CalculateCost(provider, model, inputTokens, outputTokens, cacheReadTokens).EstimatedUSD
}

// enrichSpanCost computes and stamps cost.estimated_usd for LLM spans that
// arrive without a cost attribute.
//
// Coding-agent telemetry (e.g. Claude Code's beta tracing) reports per-call
// token counts but never a cost: the agent talks to the model provider
// directly, so Everstack only sees the post-hoc span and must price it from the
// model catalog at ingest. Without this, every such span coalesced to $0 in the
// trace list and dashboards.
//
// Only spans that look like a model call (a model id plus some token usage) and
// that carry no existing cost are touched, so non-LLM spans (tool, root
// interaction) and proxy-priced spans are left untouched — and because only the
// leaf llm_request spans carry model+tokens, the per-trace sum does not
// double-count.
func enrichSpanCost(attrs map[string]string) {
	if firstNonEmpty(attrs, costPresentKeys...) != "" {
		return
	}
	model := firstNonEmpty(attrs, modelKeys...)
	if model == "" {
		return
	}
	in := firstInt(attrs, inputTokenKeys...)
	out := firstInt(attrs, outputTokenKeys...)
	cacheRead := firstInt(attrs, cacheReadKeys...)
	if in == 0 && out == 0 && cacheRead == 0 {
		return
	}
	provider := firstNonEmpty(attrs, providerKeys...)
	if provider == "" {
		provider = providerFromModel(model)
	}
	// costFn normalizes variant ids (e.g. "claude-opus-4-8[1m]") and applies
	// Anthropic vs OpenAI cache-pricing semantics; it returns zero when the
	// model is not in the catalog, in which case we stamp nothing.
	if usd := costFn(provider, model, in, out, cacheRead); usd > 0 {
		attrs[attributes.CostEstimatedUSD] = strconv.FormatFloat(usd, 'f', -1, 64)
	}
}

// providerFromModel infers a catalog provider key from a model id when the span
// carries no explicit provider attribute. Only the common coding-agent
// providers need covering here; anything else falls through to "" and the
// catalog lookup decides.
func providerFromModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "openai"
	case strings.HasPrefix(m, "gemini"):
		return "google"
	}
	return ""
}

func firstNonEmpty(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

// firstInt returns the first key that parses as a non-negative integer token
// count. OTLP int attributes are stored as decimal strings (see
// anyValueToString); some SDKs emit them as floats, so we tolerate that too.
func firstInt(attrs map[string]string, keys ...string) int {
	for _, k := range keys {
		v := attrs[k]
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			if n < 0 {
				return 0
			}
			return n
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return int(f)
		}
	}
	return 0
}
