package zai

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// Z.AI serves the GLM family from an OpenAI-compatible endpoint. Until now GLM
// was only reachable by reselling through Fireworks, Together, OpenRouter or
// NVIDIA NIM, each of which adds a markup and its own rate limits.
func init() {
	factory.Default.Register("zai",
		openai_compatible.CreateOpenAICompatibleFactory(
			"zai",
			"https://api.z.ai/api/paas/v4",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
