package traces

import (
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// costFn prices token usage for a (provider, model). It is a package var so
// tests can stub it without standing up the model catalog. In the running
// gateway it delegates to the same calculator the billing/spend-limit paths use
// (metrics.CalculateCost, backed by the model-catalog pricing), so a trace's
// computed cost matches what the tenant is actually billed.
var costFn = func(provider, model string, inputTokens, outputTokens, cachedTokens int) float64 {
	return metrics.CalculateCost(provider, model, inputTokens, outputTokens, cachedTokens).EstimatedUSD
}

// recomputeTraceCostIfMissing fills in TotalCost for traces that carry token
// counts but no cost attribute. Coding agents other than Claude Code (Gemini
// CLI, Codex, GLM, Kimi) emit tokens but never a cost, so without this their
// traces show $0. Cost is derived from model-catalog pricing at read time
// (non-destructive — the stored span attributes are untouched).
//
// Trace-level approximation: tokens are summed across the trace's spans and
// priced at the trace's primary model. Correct for the common single-model
// trace; multi-model traces are approximate (per-span pricing is a future
// refinement).
func recomputeTraceCostIfMissing(t *query.TraceReadModel) {
	if t == nil || t.TotalCost > 0 {
		return
	}
	if t.LLMModel == "" || (t.InputTokens == 0 && t.OutputTokens == 0) {
		return
	}
	t.TotalCost = priceTraceTokens(t.LLMModel, t.Provider, t.InputTokens, t.OutputTokens, t.CachedTokens)
}

// priceTraceTokens resolves the provider needed for the catalog lookup and
// prices the usage. The model name is the reliable pricing signal, so when the
// attribute provider yields no price (e.g. Claude Code reports
// gen_ai.system=anthropic while actually calling a glm-* model) we retry with
// the provider inferred from the model name.
func priceTraceTokens(model, provider string, in, out, cached int64) float64 {
	price := func(p string) float64 {
		if p == "" {
			return 0
		}
		return costFn(p, model, int(in), int(out), int(cached))
	}
	if c := price(provider); c > 0 {
		return c
	}
	if inferred := InferProviderFromModel(model); inferred != "" && inferred != provider {
		if c := price(inferred); c > 0 {
			return c
		}
	}
	return 0
}
