package modelidentity

import "testing"

func TestResolveCanonicalIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		provider  string
		model     string
		publisher string
		canonical string
	}{
		{
			name:      "direct provider",
			provider:  "openai",
			model:     "gpt-5.6",
			publisher: "openai",
			canonical: "openai/gpt-5.6",
		},
		{
			name:      "OpenRouter publisher prefix",
			provider:  "openrouter",
			model:     "anthropic/claude-opus-5",
			publisher: "anthropic",
			canonical: "anthropic/claude-opus-5",
		},
		{
			name:      "Hugging Face repository",
			provider:  "huggingface",
			model:     "Qwen/Qwen3-Next-80B-A3B-Instruct",
			publisher: "qwen",
			canonical: "qwen/qwen3-next-80b-a3b-instruct",
		},
		{
			name:      "Vertex Anthropic model",
			provider:  "vertex-ai",
			model:     "claude-opus-5@default",
			publisher: "anthropic",
			canonical: "anthropic/claude-opus-5",
		},
		{
			name:      "Azure OpenAI model",
			provider:  "azure-openai",
			model:     "gpt-5.6-terra",
			publisher: "openai",
			canonical: "openai/gpt-5.6-terra",
		},
		{
			name:      "Bedrock Anthropic model",
			provider:  "aws-bedrock",
			model:     "anthropic.claude-opus-5",
			publisher: "anthropic",
			canonical: "anthropic/claude-opus-5",
		},
		{
			name:      "Bedrock Amazon model",
			provider:  "aws-bedrock",
			model:     "amazon.nova-pro-v1:0",
			publisher: "amazon",
			canonical: "amazon/nova-pro-v1:0",
		},
		{
			name:      "NVIDIA hosted DeepSeek model",
			provider:  "nvidia-nim",
			model:     "deepseek-ai/deepseek-v4-pro",
			publisher: "deepseek",
			canonical: "deepseek/deepseek-v4-pro",
		},
		{
			name:      "Fireworks hosted Qwen model",
			provider:  "fireworks",
			model:     "accounts/fireworks/models/qwen3p7-plus",
			publisher: "qwen",
			canonical: "qwen/qwen3.7-plus",
		},
		{
			name:      "Fireworks hosted Kimi router",
			provider:  "fireworks",
			model:     "accounts/fireworks/routers/kimi-k2p7-code-fast",
			publisher: "moonshot",
			canonical: "moonshot/kimi-k2.7-code-fast",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Resolve(tt.provider, tt.model)
			if got.Publisher != tt.publisher {
				t.Fatalf("publisher = %q, want %q", got.Publisher, tt.publisher)
			}
			if got.CanonicalModelID != tt.canonical {
				t.Fatalf("canonical model ID = %q, want %q", got.CanonicalModelID, tt.canonical)
			}
		})
	}
}

func TestResolveUsesExplicitCatalogIdentity(t *testing.T) {
	t.Parallel()

	got := ResolveWithOverrides(
		"openrouter",
		"vendor/special-routing-name",
		"acme-ai",
		"acme-ai/foundation-7b",
	)
	if got.Publisher != "acme-ai" || got.CanonicalModelID != "acme-ai/foundation-7b" {
		t.Fatalf("ResolveWithOverrides() = %#v", got)
	}
}
