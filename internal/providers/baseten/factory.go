package baseten

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://inference.baseten.co/v1"

// Baseten Model APIs expose OpenAI-compatible chat completions
// (https://docs.baseten.co/inference/model-apis/overview). Dedicated Baseten
// deployments use per-model hosts and are reached by overriding base_url.
func init() {
	factory.Default.Register("baseten",
		openai_compatible.CreateOpenAICompatibleFactory(
			"baseten",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
