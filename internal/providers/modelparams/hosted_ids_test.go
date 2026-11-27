package modelparams

import (
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

const hostedFixtureProviders = `providers:
  aws-bedrock:
    name: aws-bedrock
  vertex-ai:
    name: vertex-ai
  google:
    name: google
  anthropic:
    name: anthropic
`

const hostedFixtureModels = `providers:
  aws-bedrock:
    name: aws-bedrock
    models:
      - name: anthropic.claude-fable-5-2
        unsupported_parameters: [temperature]
      - name: anthropic.claude-haiku-5-5
        unsupported_parameters: [top_p]
      - name: meta.llama3-3-70b-instruct-v1:0
  vertex-ai:
    name: vertex-ai
    models:
      - name: claude-fable-5-2@default
        unsupported_parameters: [temperature]
      - name: gemini-4-argon
  google:
    name: google
    models:
      - name: gemini-4-argon
        unsupported_parameters: [top_k]
  anthropic:
    name: anthropic
    models:
      - name: claude-fable-5-2
        unsupported_parameters: [temperature]
`

func hostedFixtureCache(t *testing.T) *catalog.Cache {
	t.Helper()
	cache := catalog.NewCache()
	if err := cache.Load([]byte(hostedFixtureModels), []byte(hostedFixtureProviders)); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cache
}

func TestResolveModelHostedIDs(t *testing.T) {
	cache := hostedFixtureCache(t)
	tests := []struct {
		provider string
		model    string
		want     string // catalog entry name, "" for no match
	}{
		// Bedrock cross-region inference profiles.
		{"aws-bedrock", "us.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "eu.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "jp.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "au.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "apac.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "global.anthropic.claude-haiku-5-5", "anthropic.claude-haiku-5-5"},
		{"aws-bedrock", "US.anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		{"aws-bedrock", "anthropic.claude-fable-5-2", "anthropic.claude-fable-5-2"},
		// Negatives: another publisher stays on its own entry, unknown
		// regions and models find nothing, and only one segment is removed.
		{"aws-bedrock", "us.meta.llama3-3-70b-instruct-v1:0", "meta.llama3-3-70b-instruct-v1:0"},
		{"aws-bedrock", "us.meta.llama4-maverick-17b-instruct-v1:0", ""},
		{"aws-bedrock", "xx.anthropic.claude-fable-5-2", ""},
		{"aws-bedrock", "us.eu.anthropic.claude-fable-5-2", ""},
		{"aws-bedrock", "us.anthropic.claude-unknown-9", ""},
		// Vertex Claude ids.
		{"vertex-ai", "claude-fable-5-2", "claude-fable-5-2@default"},
		{"vertex-ai", "claude-fable-5-2@20261109", "claude-fable-5-2@default"},
		{"vertex-ai", "claude-fable-5-2@default", "claude-fable-5-2@default"},
		{"vertex-ai", "claude-unknown-9@20261109", ""},
		// The rules are scoped to their provider.
		{"anthropic", "us.anthropic.claude-fable-5-2", ""},
		{"anthropic", "claude-fable-5-2@20261109", ""},
		{"vertex-ai", "us.anthropic.claude-fable-5-2", ""},
		{"aws-bedrock", "claude-fable-5-2", ""},
		// The Gemini API adapter reports "gemini"; its catalog key is "google".
		{"gemini", "gemini-4-argon", "gemini-4-argon"},
	}
	for _, tt := range tests {
		definition, ok := resolveModel(cache, tt.provider, tt.model)
		got := ""
		if ok && definition != nil {
			got = definition.Name
		}
		if got != tt.want {
			t.Errorf("resolveModel(%s, %s) = %q, want %q", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestDropUnsupportedHostedIDs(t *testing.T) {
	cache := hostedFixtureCache(t)
	req := gw.ChatCompletionRequest{Model: "us.anthropic.claude-fable-5-2", Sampling: fullSampling()}
	if dropped := DropUnsupported(cache, "aws-bedrock", &req); len(dropped) != 1 || dropped[0] != catalog.UnsupportedTemperature {
		t.Fatalf("bedrock regional dropped = %v, want [temperature]", dropped)
	}
	if req.Model != "us.anthropic.claude-fable-5-2" {
		t.Fatalf("model rewritten to %q, want the hosted id kept", req.Model)
	}

	req = gw.ChatCompletionRequest{Model: "claude-fable-5-2@20261109", Sampling: fullSampling()}
	if dropped := DropUnsupported(cache, "vertex-ai", &req); len(dropped) != 1 || dropped[0] != catalog.UnsupportedTemperature {
		t.Fatalf("vertex pinned dropped = %v, want [temperature]", dropped)
	}

	req = gw.ChatCompletionRequest{Model: "gemini-4-argon", Sampling: fullSampling()}
	if dropped := DropUnsupported(cache, "gemini", &req); len(dropped) != 1 || dropped[0] != catalog.UnsupportedTopK {
		t.Fatalf("gemini dropped = %v, want [top_k]", dropped)
	}

	req = gw.ChatCompletionRequest{Model: "us.anthropic.claude-unknown-9", Sampling: fullSampling()}
	if dropped := DropUnsupported(cache, "aws-bedrock", &req); dropped != nil || !req.Sampling.TemperatureConfigured {
		t.Fatalf("unknown model dropped = %v, want the request untouched", dropped)
	}
}
