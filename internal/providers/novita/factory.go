package novita

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://api.novita.ai/openai/v1"

// Novita AI documents its chat endpoint as
// https://api.novita.ai/openai/v1/chat/completions
// (https://docs.novita.ai/guides/llm-api), so the default root includes /v1.
// Model ids are vendor-prefixed and lowercase, e.g. "zai-org/glm-5.3".
func init() {
	factory.Default.Register("novita",
		openai_compatible.CreateOpenAICompatibleFactory(
			"novita",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
