package metrics

import (
	"math"
	"testing"

	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// Rates are per 1k tokens. cache_read_cost_per_1k values here are the real
// published ratios: Anthropic and OpenAI price cache reads at 0.1x input,
// DeepSeek at roughly 0.008x. The pre-existing code multiplied the input rate
// by a constant 0.5x for every non-Anthropic provider, which is why those two
// providers were over-charged.
const testCatalogYAML = `
providers:
  anthropic:
    name: anthropic
    models:
      - name: claude-opus-5
        display_name: Claude Opus 5
        input_cost_per_1k: 0.005
        output_cost_per_1k: 0.025
        cache_read_cost_per_1k: 0.0005
        cache_write_cost_per_1k: 0.00625
        long_context_threshold_tokens: 2000
        long_context_input_multiplier: 2
        long_context_cache_multiplier: 2
        long_context_output_multiplier: 1.5
  openai:
    name: openai
    models:
      - name: gpt-5.6-luna
        display_name: GPT-5.6 Luna
        input_cost_per_1k: 0.0002
        output_cost_per_1k: 0.0012
        cache_read_cost_per_1k: 0.00002
      - name: gpt-6-astra
        display_name: GPT-6 Astra
        input_cost_per_1k: 0.01
        output_cost_per_1k: 0.05
        cache_read_cost_per_1k: 0.001
        cache_write_cost_per_1k: 0.0125
        long_context_threshold_tokens: 272000
        long_context_input_multiplier: 2
        long_context_cache_multiplier: 2
        long_context_output_multiplier: 1.5
  deepseek:
    name: deepseek
    models:
      - name: deepseek-v4-pro
        display_name: DeepSeek V4 Pro
        input_cost_per_1k: 0.000435
        output_cost_per_1k: 0.00087
        cache_read_cost_per_1k: 0.000003625
  groq:
    name: groq
    models:
      - name: llama-3.3-70b-versatile
        display_name: Llama 3.3 70B
        input_cost_per_1k: 0.00059
        output_cost_per_1k: 0.00079
`

const testProvidersYAML = `
providers:
  anthropic:
    name: anthropic
  openai:
    name: openai
  deepseek:
    name: deepseek
  groq:
    name: groq
`

func newTestCalculator(t *testing.T) *CostCalculator {
	t.Helper()
	cache := catalog.NewCache()
	if err := cache.Load([]byte(testCatalogYAML), []byte(testProvidersYAML)); err != nil {
		t.Fatalf("load test catalog: %v", err)
	}
	return NewCostCalculatorFromCache(cache)
}

func closeEnough(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}

func TestCalculateCostUsesPublishedCacheRates(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	tests := []struct {
		name     string
		provider string
		model    string
		in       int
		out      int
		cached   int
		want     float64
		why      string
	}{
		{
			name: "legacy anthropic calls keep fresh input separate from cache reads",
			// CalculateCost preserves its original Anthropic convention for
			// historical trace and OTLP records: input is fresh-only and the
			// cache-read bucket is supplied separately.
			provider: "anthropic", model: "claude-opus-5",
			in: 1000, out: 100, cached: 500,
			want: (1000 * 0.005 / 1000) + (500 * 0.0005 / 1000) + (100 * 0.025 / 1000),
			why:  "fresh input + cache reads + output",
		},
		{
			name: "openai prices cache reads as a subset of input",
			// 200 of the 1000 prompt tokens were cache hits, so 800 are
			// charged at the input rate. At the published 0.1x rate this is
			// materially cheaper than the old hardcoded 0.5x.
			provider: "openai", model: "gpt-5.6-luna",
			in: 1000, out: 100, cached: 200,
			want: (800 * 0.0002 / 1000) + (200 * 0.00002 / 1000) + (100 * 0.0012 / 1000),
			why:  "non-cached input + cache reads at catalog rate + output",
		},
		{
			name:     "deepseek's very low cache rate is honoured",
			provider: "deepseek", model: "deepseek-v4-pro",
			in: 10000, out: 500, cached: 9000,
			want: (1000 * 0.000435 / 1000) + (9000 * 0.000003625 / 1000) + (500 * 0.00087 / 1000),
			why:  "the old 0.5x constant over-charged this by roughly 25x",
		},
		{
			name:     "no cached tokens is unaffected by cache pricing",
			provider: "openai", model: "gpt-5.6-luna",
			in: 1000, out: 100, cached: 0,
			want: (1000 * 0.0002 / 1000) + (100 * 0.0012 / 1000),
			why:  "plain input + output",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := calc.CalculateCost(tc.provider, tc.model, tc.in, tc.out, tc.cached)
			if !closeEnough(got.EstimatedUSD, tc.want) {
				t.Fatalf("cost = %.12f, want %.12f (%s)", got.EstimatedUSD, tc.want, tc.why)
			}
			if !closeEnough(got.InputCost+got.CachedCost+got.OutputCost, got.EstimatedUSD) {
				t.Fatalf("cost split %.12f+%.12f+%.12f does not sum to total %.12f",
					got.InputCost, got.CachedCost, got.OutputCost, got.EstimatedUSD)
			}
		})
	}
}

func TestCalculateCostPreservesLegacyAnthropicThresholdConvention(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	// Legacy Anthropic records store 1900 fresh tokens plus 200 cache reads.
	// The old API evaluated the 2000-token threshold against the fresh input,
	// so this must remain at standard rates even though the normalized total
	// would be 2100.
	got := calc.CalculateCost("anthropic", "claude-opus-5", 1900, 100, 200)
	want := (1900 * 0.005 / 1000) + (200 * 0.0005 / 1000) + (100 * 0.025 / 1000)
	if !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("legacy cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}
	if got.InputTokens != 1900 {
		t.Fatalf("legacy InputTokens = %d, want 1900", got.InputTokens)
	}
}

func TestCalculateCostResolvesUniqueOrQualifiedModelWithoutProvider(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	unique := calc.CalculateCostWithCacheTokens("", "gpt-6-astra", 1000, 100, 100, 50)
	qualified := calc.CalculateCostWithCacheTokens("", "@openai/gpt-6-astra", 1000, 100, 100, 50)
	if unique.EstimatedUSD == 0 {
		t.Fatal("unique unqualified model should resolve without a persisted provider")
	}
	if !closeEnough(unique.EstimatedUSD, qualified.EstimatedUSD) {
		t.Fatalf("unqualified cost %.12f != qualified cost %.12f", unique.EstimatedUSD, qualified.EstimatedUSD)
	}
}

func TestCalculateCostPricesCacheWrites(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	tests := []struct {
		name       string
		provider   string
		model      string
		input      int
		output     int
		cacheRead  int
		cacheWrite int
		want       float64
	}{
		{
			name:       "anthropic cache creation uses the catalog write rate",
			provider:   "anthropic",
			model:      "claude-opus-5",
			input:      1700,
			output:     100,
			cacheRead:  500,
			cacheWrite: 200,
			want: (1000 * 0.005 / 1000) +
				(500 * 0.0005 / 1000) +
				(200 * 0.00625 / 1000) +
				(100 * 0.025 / 1000),
		},
		{
			name:       "astra long context multiplies fresh read write and output rates",
			provider:   "openai",
			model:      "gpt-6-astra",
			input:      300000,
			output:     1000,
			cacheRead:  60000,
			cacheWrite: 40000,
			want: (200000 * 0.01 * 2 / 1000) +
				(60000 * 0.001 * 2 / 1000) +
				(40000 * 0.0125 * 2 / 1000) +
				(1000 * 0.05 * 1.5 / 1000),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := calc.CalculateCostWithCacheTokens(
				tc.provider,
				tc.model,
				tc.input,
				tc.output,
				tc.cacheRead,
				tc.cacheWrite,
			)
			if !closeEnough(got.EstimatedUSD, tc.want) {
				t.Fatalf("cost = %.12f, want %.12f", got.EstimatedUSD, tc.want)
			}
			if got.CachedTokens != tc.cacheRead+tc.cacheWrite {
				t.Fatalf("cached tokens = %d, want %d", got.CachedTokens, tc.cacheRead+tc.cacheWrite)
			}
		})
	}
}

// A model with no published cache rate must keep the previous behaviour rather
// than pricing cache reads at zero. Catalogs synced before cache rates were
// carried through hit this path.
func TestCalculateCostFallsBackWhenCacheRateMissing(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	got := calc.CalculateCost("groq", "llama-3.3-70b-versatile", 1000, 100, 400)

	const inputRate = 0.00059 / 1000
	want := (600 * inputRate) + (400 * inputRate * 0.5) + (100 * 0.00079 / 1000)
	if !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("fallback cost = %.12f, want %.12f (0.5x of input rate)", got.EstimatedUSD, want)
	}
}

func TestCalculateCostAppliesLongContextPricingToWholeRequest(t *testing.T) {
	t.Parallel()
	calc := newTestCalculator(t)

	tests := []struct {
		name   string
		input  int
		output int
		cached int
		want   float64
	}{
		{
			name:   "threshold itself uses standard rates",
			input:  272000,
			output: 1000,
			cached: 72000,
			want:   (200000 * 0.01 / 1000) + (72000 * 0.001 / 1000) + (1000 * 0.05 / 1000),
		},
		{
			name:   "above threshold multiplies input cache and output rates",
			input:  272001,
			output: 1000,
			cached: 72000,
			want:   (200001 * 0.01 * 2 / 1000) + (72000 * 0.001 * 2 / 1000) + (1000 * 0.05 * 1.5 / 1000),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calc.CalculateCost("openai", "gpt-6-astra", tc.input, tc.output, tc.cached)
			if !closeEnough(got.EstimatedUSD, tc.want) {
				t.Fatalf("cost = %.12f, want %.12f", got.EstimatedUSD, tc.want)
			}
		})
	}
}

// The tracing middleware and the billing projection used to run different
// calculators, so the cost shown on a trace and the cost metered for the same
// request disagreed whenever caching was involved. They now share this one
// calculator; this pins that they agree.
func TestTraceAndBillingCostsAgree(t *testing.T) {
	// This test exercises the process-wide calculator used by billing. Keep it
	// serial so parallel tests cannot observe the temporary global instance.
	calc := newTestCalculator(t)
	SetGlobalCalculator(calc)
	t.Cleanup(func() { SetGlobalCalculator(nil) })

	const (
		provider   = "openai"
		model      = "gpt-5.6-luna"
		in         = 4000
		out        = 250
		cacheRead  = 2500
		cacheWrite = 500
	)

	// What the tracing middleware records on the span.
	traceSide := calc.CalculateCostWithCacheTokens(provider, model, in, out, cacheRead, cacheWrite)
	// What the billing projection writes to billing_usage_records.
	billingSide := CalculateCostWithCacheTokens(provider, model, in, out, cacheRead, cacheWrite)

	if !closeEnough(traceSide.EstimatedUSD, billingSide.EstimatedUSD) {
		t.Fatalf("trace cost %.12f != billing cost %.12f",
			traceSide.EstimatedUSD, billingSide.EstimatedUSD)
	}
	if traceSide.EstimatedUSD == 0 {
		t.Fatal("expected a non-zero cost, so the comparison is meaningful")
	}
}
