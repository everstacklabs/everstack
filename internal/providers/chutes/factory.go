package chutes

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://llm.chutes.ai/v1"

// Chutes routes OpenAI-compatible inference through one host
// (https://chutes.ai/agents). Keys start with cpk_ and must be sent as a
// bearer token; X-API-Key is not accepted for inference.
func init() {
	factory.Default.Register("chutes",
		openai_compatible.CreateOpenAICompatibleFactory(
			"chutes",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
