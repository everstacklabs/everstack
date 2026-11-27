package metrics

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// newEmbeddedCatalogCalculator prices against the generated embedded catalog
// the gateway ships with, so these tests break when a published price or the
// lookup order changes, not only when a fixture does.
func newEmbeddedCatalogCalculator(t *testing.T) *CostCalculator {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "cmd", "config", "gateway", "defaults")
	modelsYAML, err := os.ReadFile(filepath.Join(dir, "models.yaml"))
	if err != nil {
		t.Fatalf("read embedded models.yaml: %v", err)
	}
	providersYAML, err := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if err != nil {
		t.Fatalf("read embedded providers.yaml: %v", err)
	}
	cache := catalog.NewCache()
	if err := cache.Load(modelsYAML, providersYAML); err != nil {
		t.Fatalf("load embedded catalog: %v", err)
	}
	return NewCostCalculatorFromCache(cache)
}

// Per-token USD rates from the 2.11.0 model files.
const (
	fable52Input  = 8.0 / 1e6
	fable52Output = 40.0 / 1e6
	fable5Input   = 10.0 / 1e6
	fable5Output  = 50.0 / 1e6
	haiku55Input  = 0.10 / 1e6
	haiku55Output = 0.50 / 1e6
	haiku55Read   = 0.01 / 1e6
	gpt62SolIn    = 2.0 / 1e6
	gpt62SolOut   = 10.0 / 1e6
)

func TestEmbeddedCatalogVariantSuffixPricesItsOwnModel(t *testing.T) {
	t.Parallel()
	calc := newEmbeddedCatalogCalculator(t)

	want := 1000*fable52Input + 100*fable52Output
	shorterSibling := 1000*fable5Input + 100*fable5Output
	for _, model := range []string{"claude-fable-5-2", "claude-fable-5-2[1m]"} {
		got := calc.CalculateCostWithCacheTokens("anthropic", model, 1000, 100, 0, 0)
		if !got.Priceable {
			t.Fatalf("%s: not priceable", model)
		}
		if closeEnough(got.EstimatedUSD, shorterSibling) {
			t.Fatalf("%s priced as claude-fable-5 (%.12f)", model, got.EstimatedUSD)
		}
		if !closeEnough(got.EstimatedUSD, want) {
			t.Fatalf("%s cost = %.12f, want %.12f", model, got.EstimatedUSD, want)
		}
	}

	// A dated snapshot still resolves to the longest catalog id it extends.
	got := calc.CalculateCostWithCacheTokens("anthropic", "claude-fable-5-2-20261109", 1000, 100, 0, 0)
	if !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("snapshot cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}
}

func TestEmbeddedCatalogHaiku55LongContextTier(t *testing.T) {
	t.Parallel()
	calc := newEmbeddedCatalogCalculator(t)

	// 50K prompt tokens: standard rates.
	got := calc.CalculateCostWithCacheTokens("anthropic", "claude-haiku-5-5", 50_000, 1_000, 0, 0)
	if want := 50_000*haiku55Input + 1_000*haiku55Output; !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("50K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}

	// 150K prompt tokens: the whole request bills at 5x every rate.
	got = calc.CalculateCostWithCacheTokens("anthropic", "claude-haiku-5-5", 150_000, 1_000, 0, 0)
	if want := 5 * (150_000*haiku55Input + 1_000*haiku55Output); !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("150K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}

	// Inclusive input (the live Usage path): 60K fresh + 60K cache reads is a
	// 120K prompt, so cache reads count toward the 100K threshold.
	got = calc.CalculateCostWithCacheTokens("anthropic", "claude-haiku-5-5", 120_000, 1_000, 60_000, 0)
	if want := 5 * (60_000*haiku55Input + 60_000*haiku55Read + 1_000*haiku55Output); !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("inclusive 120K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}

	// Legacy CalculateCost (fresh-only Anthropic input, cache separate) keeps
	// its historical convention: the threshold is checked against fresh input
	// only, so the same 60K + 60K request stays at standard rates there.
	got = calc.CalculateCost("anthropic", "claude-haiku-5-5", 60_000, 1_000, 60_000)
	if want := 60_000*haiku55Input + 60_000*haiku55Read + 1_000*haiku55Output; !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("legacy 60K+60K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}
}

func TestEmbeddedCatalogGPT62SolLongContextTier(t *testing.T) {
	t.Parallel()
	calc := newEmbeddedCatalogCalculator(t)

	got := calc.CalculateCostWithCacheTokens("openai", "gpt-6.2-sol", 100_000, 1_000, 0, 0)
	if want := 100_000*gpt62SolIn + 1_000*gpt62SolOut; !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("100K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}

	// Over 272K: 2x input and 1.5x output for the whole request.
	got = calc.CalculateCostWithCacheTokens("openai", "gpt-6.2-sol", 300_000, 1_000, 0, 0)
	if want := 300_000*gpt62SolIn*2 + 1_000*gpt62SolOut*1.5; !closeEnough(got.EstimatedUSD, want) {
		t.Fatalf("300K cost = %.12f, want %.12f", got.EstimatedUSD, want)
	}
}

// A Bedrock cross-region inference profile id is never mapped to the base
// entry, because a regional profile can carry a premium over the global one.
// A profile with its own catalog entry is priced at that entry's rate. One
// without an entry resolves to nothing: zero cost and not priceable, so
// billing fails closed on a platform key rather than debiting zero.
func TestEmbeddedCatalogBedrockRegionalProfilePricing(t *testing.T) {
	t.Parallel()
	calc := newEmbeddedCatalogCalculator(t)

	base := calc.CalculateCostWithCacheTokens("aws-bedrock", "anthropic.claude-fable-5-2", 1000, 100, 0, 0)
	if want := 1000*0.0088/1000 + 100*0.044/1000; !base.Priceable || !closeEnough(base.EstimatedUSD, want) {
		t.Fatalf("base Bedrock cost = %.12f (priceable %v), want %.12f", base.EstimatedUSD, base.Priceable, want)
	}

	us := calc.CalculateCostWithCacheTokens("aws-bedrock", "us.anthropic.claude-fable-5-2", 1000, 100, 0, 0)
	if want := 1000*0.0088/1000 + 100*0.044/1000; !us.Priceable || !closeEnough(us.EstimatedUSD, want) {
		t.Fatalf("US profile cost = %.12f (priceable %v), want %.12f", us.EstimatedUSD, us.Priceable, want)
	}

	// The global profile has its own, lower rate; it must not inherit the base.
	global := calc.CalculateCostWithCacheTokens("aws-bedrock", "global.anthropic.claude-fable-5-2", 1000, 100, 0, 0)
	if want := 1000*0.008/1000 + 100*0.04/1000; !global.Priceable || !closeEnough(global.EstimatedUSD, want) {
		t.Fatalf("global profile cost = %.12f (priceable %v), want %.12f", global.EstimatedUSD, global.Priceable, want)
	}

	// No catalog entry for this profile, so it stays unpriced.
	missing := calc.CalculateCostWithCacheTokens("aws-bedrock", "eu.anthropic.claude-fable-5-2", 1000, 100, 0, 0)
	if missing.Priceable || missing.EstimatedUSD != 0 || missing.PricingModel != "unknown" {
		t.Fatalf("profile without an entry = %+v, want unpriced", missing)
	}
	if calc.IsPriced("aws-bedrock", "eu.anthropic.claude-fable-5-2") {
		t.Fatal("IsPriced(profile without an entry) = true, want false")
	}
}

// Bedrock serves the OpenAI GPT-6 models through inference profiles, each
// with its own rate and the same over-272K tier as first-party.
func TestEmbeddedCatalogBedrockOpenAIProfilePricing(t *testing.T) {
	t.Parallel()
	calc := newEmbeddedCatalogCalculator(t)

	us := calc.CalculateCostWithCacheTokens("aws-bedrock", "us.openai.gpt-6.2-sol", 100_000, 1_000, 0, 0)
	if want := 100_000*2.2/1e6 + 1_000*11.0/1e6; !us.Priceable || !closeEnough(us.EstimatedUSD, want) {
		t.Fatalf("US profile cost = %.12f (priceable %v), want %.12f", us.EstimatedUSD, us.Priceable, want)
	}

	global := calc.CalculateCostWithCacheTokens("aws-bedrock", "global.openai.gpt-6.2-sol", 100_000, 1_000, 0, 0)
	if want := 100_000*2.0/1e6 + 1_000*10.0/1e6; !global.Priceable || !closeEnough(global.EstimatedUSD, want) {
		t.Fatalf("global profile cost = %.12f (priceable %v), want %.12f", global.EstimatedUSD, global.Priceable, want)
	}

	// Over 272K: 2x input and 1.5x output for the whole request.
	long := calc.CalculateCostWithCacheTokens("aws-bedrock", "us.openai.gpt-6.2-sol", 300_000, 1_000, 0, 0)
	if want := 300_000*4.4/1e6 + 1_000*16.5/1e6; !closeEnough(long.EstimatedUSD, want) {
		t.Fatalf("long-prompt cost = %.12f, want %.12f", long.EstimatedUSD, want)
	}

	// The base id answers only on an endpoint the adapter does not call, so
	// it has no entry and stays unpriced.
	if calc.IsPriced("aws-bedrock", "openai.gpt-6.2-sol") {
		t.Fatal("IsPriced(base Bedrock OpenAI id) = true, want false")
	}
}

func TestStripDateSuffix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"gpt-4o-mini-2024-07-18": "gpt-4o-mini",
		"gpt-5-2025-08-07":       "gpt-5",
		"claude-opus-4-20250514": "claude-opus-4",
		"gpt-4o":                 "gpt-4o",
		"gpt-4.1-nano":           "gpt-4.1-nano",
	}
	for in, want := range cases {
		if got := stripDateSuffix(in); got != want {
			t.Errorf("stripDateSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDatedSnapshotFallsBackToPricedBase(t *testing.T) {
	t.Parallel()
	cache := catalog.NewCache()
	yaml := `
providers:
  openai:
    name: openai
    models:
      - name: gpt-4o-mini
        input_cost_per_1k: 0.00015
        output_cost_per_1k: 0.0006
      - name: gpt-4o-mini-2024-07-18
  other:
    name: other
    models:
      - name: gpt-4o-mini-2024-07-18
`
	if err := cache.Load([]byte(yaml), []byte("providers:\n  openai:\n    name: openai\n  other:\n    name: other\n")); err != nil {
		t.Fatal(err)
	}
	calc := NewCostCalculatorFromCache(cache)
	got := calc.CalculateCostWithCacheTokens("openai", "gpt-4o-mini-2024-07-18", 1000, 1000, 0, 0)
	if !got.Priceable || !closeEnough(got.EstimatedUSD, 0.00075) {
		t.Fatalf("openai snapshot: %+v", got)
	}
	if !calc.IsPriced("openai", "gpt-4o-mini-2024-07-18") {
		t.Fatal("IsPriced false for priced base")
	}
	// Never cross providers.
	other := calc.CalculateCostWithCacheTokens("other", "gpt-4o-mini-2024-07-18", 1000, 1000, 0, 0)
	if other.Priceable || other.EstimatedUSD != 0 {
		t.Fatalf("other provider must stay unpriced: %+v", other)
	}
}

// Lists every chat-capable model in the embedded catalog that still cannot be
// priced after the fallback. Run with -v to see the list.
func TestEmbeddedCatalogUnpriceableChatModels(t *testing.T) {
	calc := newEmbeddedCatalogCalculator(t)
	cache := calc.catalogCache
	var unpriced []string
	for provider := range cache.GetModelWhitelist() {
		defs, _ := cache.GetAllModels(provider)
		for _, def := range defs {
			chat := false
			for _, c := range def.Capabilities {
				if c == "chat" {
					chat = true
				}
			}
			if chat && !calc.IsPriced(provider, def.Name) {
				unpriced = append(unpriced, provider+"/"+def.Name)
			}
		}
	}
	sort.Strings(unpriced)
	t.Logf("%d unpriceable chat models:", len(unpriced))
	for _, n := range unpriced {
		t.Log(n)
	}
	for _, n := range unpriced {
		for _, snap := range []string{"openai/gpt-4o-mini-2024-07-18", "openai/gpt-4.1-2025-04-14", "openai/gpt-5-2025-08-07", "openai/o3-2025-04-16"} {
			if n == snap {
				t.Errorf("%s is still unpriceable", n)
			}
		}
	}
}
