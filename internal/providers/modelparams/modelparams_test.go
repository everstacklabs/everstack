package modelparams

import (
	"reflect"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

const fixtureProviders = `providers:
  anthropic:
    name: anthropic
  openai:
    name: openai
`

const fixtureModels = `providers:
  anthropic:
    name: anthropic
    models:
      - name: no-temperature
        unsupported_parameters: [temperature]
      - name: no-top-p
        unsupported_parameters: [top_p]
      - name: no-top-k
        unsupported_parameters: [top_k]
      - name: no-thinking-toggle
        unsupported_parameters: [thinking_toggle]
      - name: no-thinking-budget
        unsupported_parameters: [thinking_budget]
      - name: unknown-key-only
        unsupported_parameters: [logit_bias, " "]
      - name: claude-opus-4-8
        unsupported_parameters: [temperature, top_p]
      - name: claude-fable-5
      - name: claude-fable-5-2
        unsupported_parameters: [temperature, thinking_toggle]
      - name: plain-model
  openai:
    name: openai
    models:
      - name: gpt-6.2-sol
        unsupported_parameters: [Temperature, TOP_P]
`

func fixtureCache(t *testing.T) *catalog.Cache {
	t.Helper()
	cache := catalog.NewCache()
	if err := cache.Load([]byte(fixtureModels), []byte(fixtureProviders)); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cache
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

// fullSampling sets every control the filter can drop, plus ones it must
// never touch.
func fullSampling() gw.SamplingParams {
	return gw.SamplingParams{
		Temperature:           0.7,
		TemperatureConfigured: true,
		TopP:                  0.9,
		TopPConfigured:        true,
		TopK:                  intPtr(40),
		ReasoningEnabled:      boolPtr(true),
		ReasoningBudget:       intPtr(2048),
		ReasoningEffort:       "high",
		MaxTokens:             512,
		Stop:                  []string{"END"},
	}
}

func TestDropUnsupported(t *testing.T) {
	cache := fixtureCache(t)

	tests := []struct {
		name        string
		provider    string
		model       string
		wantDropped []string
		mutate      func(*gw.SamplingParams) // the expected result, applied to fullSampling()
	}{
		{
			name: "temperature", provider: "anthropic", model: "no-temperature",
			wantDropped: []string{"temperature"},
			mutate: func(s *gw.SamplingParams) {
				s.Temperature, s.TemperatureConfigured = 0, false
			},
		},
		{
			name: "top_p", provider: "anthropic", model: "no-top-p",
			wantDropped: []string{"top_p"},
			mutate: func(s *gw.SamplingParams) {
				s.TopP, s.TopPConfigured = 0, false
			},
		},
		{
			name: "top_k", provider: "anthropic", model: "no-top-k",
			wantDropped: []string{"top_k"},
			mutate:      func(s *gw.SamplingParams) { s.TopK = nil },
		},
		{
			name: "thinking_toggle", provider: "anthropic", model: "no-thinking-toggle",
			wantDropped: []string{"thinking_toggle"},
			mutate:      func(s *gw.SamplingParams) { s.ReasoningEnabled = nil },
		},
		{
			name: "thinking_budget", provider: "anthropic", model: "no-thinking-budget",
			wantDropped: []string{"thinking_budget"},
			mutate:      func(s *gw.SamplingParams) { s.ReasoningBudget = nil },
		},
		{
			name: "unknown key ignored", provider: "anthropic", model: "unknown-key-only",
			mutate: func(*gw.SamplingParams) {},
		},
		{
			name: "model without the field untouched", provider: "anthropic", model: "plain-model",
			mutate: func(*gw.SamplingParams) {},
		},
		{
			name: "model not in catalog untouched", provider: "anthropic", model: "claude-unknown-9",
			mutate: func(*gw.SamplingParams) {},
		},
		{
			name: "provider not in catalog untouched", provider: "mistral", model: "no-temperature",
			mutate: func(*gw.SamplingParams) {},
		},
		{
			name: "variant suffix resolves to base model", provider: "anthropic", model: "claude-opus-4-8[1m]",
			wantDropped: []string{"temperature", "top_p"},
			mutate: func(s *gw.SamplingParams) {
				s.Temperature, s.TemperatureConfigured = 0, false
				s.TopP, s.TopPConfigured = 0, false
			},
		},
		{
			// claude-fable-5 is a shorter sibling with no constraints; the
			// variant must resolve to claude-fable-5-2, not to it.
			name: "variant suffix beats shorter sibling prefix", provider: "anthropic", model: "claude-fable-5-2[1m]",
			wantDropped: []string{"temperature", "thinking_toggle"},
			mutate: func(s *gw.SamplingParams) {
				s.Temperature, s.TemperatureConfigured = 0, false
				s.ReasoningEnabled = nil
			},
		},
		{
			name: "dated snapshot resolves to base model", provider: "openai", model: "gpt-6.2-sol-2026-11-01",
			wantDropped: []string{"temperature", "top_p"},
			mutate: func(s *gw.SamplingParams) {
				s.Temperature, s.TemperatureConfigured = 0, false
				s.TopP, s.TopPConfigured = 0, false
			},
		},
		{
			name: "keys and lookup ignore case", provider: "OpenAI", model: "GPT-6.2-SOL",
			wantDropped: []string{"temperature", "top_p"},
			mutate: func(s *gw.SamplingParams) {
				s.Temperature, s.TemperatureConfigured = 0, false
				s.TopP, s.TopPConfigured = 0, false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := gw.ChatCompletionRequest{Model: tt.model, Sampling: fullSampling()}
			want := fullSampling()
			tt.mutate(&want)

			got := DropUnsupported(cache, tt.provider, &req)
			if !reflect.DeepEqual(got, tt.wantDropped) {
				t.Fatalf("dropped = %v, want %v", got, tt.wantDropped)
			}
			if !reflect.DeepEqual(req.Sampling, want) {
				t.Fatalf("sampling = %+v, want %+v", req.Sampling, want)
			}
			if req.Model != tt.model {
				t.Fatalf("model rewritten to %q", req.Model)
			}
		})
	}
}

func TestDropUnsupportedReportsOnlyFieldsThatWereSet(t *testing.T) {
	cache := fixtureCache(t)
	req := gw.ChatCompletionRequest{Model: "claude-opus-4-8"}
	if got := DropUnsupported(cache, "anthropic", &req); got != nil {
		t.Fatalf("dropped = %v, want nil for a request that set nothing", got)
	}

	// A configured zero is still an explicit value the upstream would see.
	req.Sampling.TemperatureConfigured = true
	if got := DropUnsupported(cache, "anthropic", &req); !reflect.DeepEqual(got, []string{"temperature"}) {
		t.Fatalf("dropped = %v, want [temperature] for a configured zero", got)
	}
	if req.Sampling.TemperatureConfigured {
		t.Fatal("TemperatureConfigured still set after drop")
	}
}

func TestDropUnsupportedDoesNotWriteThroughSharedPointers(t *testing.T) {
	cache := fixtureCache(t)
	budget, enabled, topK := 1024, true, 5
	original := gw.ChatCompletionRequest{
		Model: "no-thinking-budget",
		Sampling: gw.SamplingParams{
			ReasoningBudget:  &budget,
			ReasoningEnabled: &enabled,
			TopK:             &topK,
		},
	}
	attempt := original // fallback attempts copy the request by value
	DropUnsupported(cache, "anthropic", &attempt)

	if attempt.Sampling.ReasoningBudget != nil {
		t.Fatal("attempt kept the reasoning budget")
	}
	if original.Sampling.ReasoningBudget == nil || *original.Sampling.ReasoningBudget != 1024 {
		t.Fatal("dropping on a copy changed the original request")
	}
}

func TestDropUnsupportedNilSafe(t *testing.T) {
	req := gw.ChatCompletionRequest{Model: "no-temperature", Sampling: fullSampling()}
	if got := DropUnsupported(nil, "anthropic", &req); got != nil {
		t.Fatalf("dropped = %v with nil cache, want nil", got)
	}
	if !reflect.DeepEqual(req.Sampling, fullSampling()) {
		t.Fatal("nil cache changed the request")
	}
	if got := DropUnsupported(fixtureCache(t), "anthropic", nil); got != nil {
		t.Fatalf("dropped = %v with nil request, want nil", got)
	}
}
