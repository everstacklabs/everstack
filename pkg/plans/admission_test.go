package plans_test

import (
	"testing"

	"github.com/everstacklabs/everstack/pkg/plans"
)

func TestEmbeddedFreeInferenceAllowlist(t *testing.T) {
	cfg, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	if len(cfg.FreeInferenceAllowlist) == 0 {
		t.Fatal("embedded plans have no free_inference_allowlist")
	}
	for _, tc := range []struct {
		provider, model string
		want            bool
	}{
		{"openai", "gpt-4o-mini", true},
		{"openai", "gpt-4o-mini-2024-07-18", true},
		{"OpenAI", "GPT-4o-mini", true},
		{"anthropic", "claude-haiku-4-5", true},
		{"google", "gemini-2.5-flash", true},
		{"openai", "gpt-4o", false},
		{"openai", "o3", false},
		{"anthropic", "claude-opus-4-8", false},
		{"azure_openai", "gpt-4o-mini", false}, // patterns are provider-scoped
		{"", "gpt-4o-mini", false},
	} {
		if got := cfg.FreeInferenceAllowed(tc.provider, tc.model); got != tc.want {
			t.Errorf("FreeInferenceAllowed(%q, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestFreeInferenceAllowedPatterns(t *testing.T) {
	cfg, err := plans.Parse([]byte(`{"plans":{},"free_inference_allowlist":["*/cheap-*","acme/exact", "bad[pattern"]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.FreeInferenceAllowed("anyone", "cheap-1") {
		t.Error("provider wildcard did not match")
	}
	if !cfg.FreeInferenceAllowed("acme", "exact") || cfg.FreeInferenceAllowed("acme", "exact-2") {
		t.Error("exact pattern matched wrongly")
	}
	if cfg.FreeInferenceAllowed("bad", "pattern") {
		t.Error("malformed pattern must never match")
	}
	var nilCfg *plans.PlansConfig
	if nilCfg.FreeInferenceAllowed("openai", "gpt-4o-mini") {
		t.Error("nil config must allow nothing")
	}
	empty, _ := plans.Parse([]byte(`{"plans":{}}`))
	if empty.FreeInferenceAllowed("openai", "gpt-4o-mini") {
		t.Error("absent allowlist must allow nothing (fail closed)")
	}
}

func TestEmbeddedMaxInflightPlatformRequests(t *testing.T) {
	cfg, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	if got := cfg.MaxInflightPlatformRequests(); got <= 0 {
		t.Fatalf("MaxInflightPlatformRequests() = %d, want > 0", got)
	}
	empty, _ := plans.Parse([]byte(`{"plans":{}}`))
	if got := empty.MaxInflightPlatformRequests(); got != 0 {
		t.Fatalf("absent guardrail = %d, want 0 (no cap)", got)
	}
}
