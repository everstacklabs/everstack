package otlp

import "testing"

// costCall records the arguments a stubbed costFn was invoked with.
type costCall struct {
	called             bool
	provider, model    string
	in, out, cacheRead int
}

// withStubCost swaps the package-level pricing function for the duration of a
// test, recording the arguments it was called with.
func withStubCost(t *testing.T, usd float64) *costCall {
	t.Helper()
	rec := &costCall{}
	prev := costFn
	costFn = func(provider, model string, in, out, cacheRead int) float64 {
		rec.called = true
		rec.provider, rec.model, rec.in, rec.out, rec.cacheRead = provider, model, in, out, cacheRead
		return usd
	}
	t.Cleanup(func() { costFn = prev })
	return rec
}

func TestEnrichSpanCost_ClaudeCodeSpan(t *testing.T) {
	rec := withStubCost(t, 0.0425)
	attrs := map[string]string{
		"model":                 "claude-opus-4-8[1m]",
		"gen_ai.request.model":  "claude-opus-4-8[1m]",
		"gen_ai.system":         "anthropic",
		"input_tokens":          "2",
		"output_tokens":         "497",
		"cache_read_tokens":     "68095",
		"cache_creation_tokens": "588",
	}
	enrichSpanCost(attrs)

	if !rec.called {
		t.Fatal("expected costFn to be called for an LLM span")
	}
	if rec.provider != "anthropic" || rec.model != "claude-opus-4-8[1m]" {
		t.Fatalf("wrong provider/model: %q / %q", rec.provider, rec.model)
	}
	if rec.in != 2 || rec.out != 497 || rec.cacheRead != 68095 {
		t.Fatalf("wrong token counts: in=%d out=%d cacheRead=%d", rec.in, rec.out, rec.cacheRead)
	}
	if got := attrs["cost.estimated_usd"]; got != "0.0425" {
		t.Fatalf("cost.estimated_usd = %q, want %q", got, "0.0425")
	}
}

func TestEnrichSpanCost_ProviderInferredFromModel(t *testing.T) {
	rec := withStubCost(t, 0.01)
	// No gen_ai.system / provider attribute — must infer "anthropic" from model.
	attrs := map[string]string{
		"model":        "claude-opus-4-8[1m]",
		"input_tokens": "100",
	}
	enrichSpanCost(attrs)
	if rec.provider != "anthropic" {
		t.Fatalf("inferred provider = %q, want anthropic", rec.provider)
	}
}

func TestEnrichSpanCost_NoStampWhenGuardsFail(t *testing.T) {
	cases := map[string]map[string]string{
		"already priced (cost.estimated_usd)": {"model": "claude-opus-4-8", "input_tokens": "10", "cost.estimated_usd": "0.5"},
		"already priced (llm.cost.total)":     {"model": "claude-opus-4-8", "input_tokens": "10", "llm.cost.total": "0.5"},
		"no model":                            {"input_tokens": "10", "output_tokens": "20"},
		"no tokens":                           {"model": "claude-opus-4-8"},
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			rec := withStubCost(t, 1.23)
			before := attrs["cost.estimated_usd"]
			enrichSpanCost(attrs)
			if rec.called {
				t.Fatal("costFn should not be called when a guard short-circuits")
			}
			if attrs["cost.estimated_usd"] != before {
				t.Fatalf("cost.estimated_usd mutated: %q -> %q", before, attrs["cost.estimated_usd"])
			}
		})
	}
}

func TestEnrichSpanCost_NoStampWhenCatalogMiss(t *testing.T) {
	withStubCost(t, 0) // model not in catalog -> zero
	attrs := map[string]string{"model": "some-unknown-model", "input_tokens": "100"}
	enrichSpanCost(attrs)
	if _, ok := attrs["cost.estimated_usd"]; ok {
		t.Fatal("must not stamp a zero cost")
	}
}

func TestProviderFromModel(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8[1m]": "anthropic",
		"gpt-4o":              "openai",
		"o3-mini":             "openai",
		"gemini-2.0-flash":    "google",
		"llama-3":             "",
	}
	for model, want := range cases {
		if got := providerFromModel(model); got != want {
			t.Errorf("providerFromModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestFirstInt(t *testing.T) {
	attrs := map[string]string{"a": "", "b": "42", "c": "3.9", "d": "-5"}
	if got := firstInt(attrs, "a", "b"); got != 42 {
		t.Errorf("skip-empty then parse int: got %d, want 42", got)
	}
	if got := firstInt(attrs, "c"); got != 3 {
		t.Errorf("float tolerated (truncated): got %d, want 3", got)
	}
	if got := firstInt(attrs, "d"); got != 0 {
		t.Errorf("negative clamped: got %d, want 0", got)
	}
	if got := firstInt(attrs, "missing"); got != 0 {
		t.Errorf("missing key: got %d, want 0", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	attrs := map[string]string{"a": "", "b": "x"}
	if got := firstNonEmpty(attrs, "a", "b"); got != "x" {
		t.Errorf("got %q, want x", got)
	}
	if got := firstNonEmpty(attrs, "missing"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
