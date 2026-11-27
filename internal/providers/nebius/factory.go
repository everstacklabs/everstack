package nebius

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://api.tokenfactory.nebius.com/v1"

// Nebius Token Factory is OpenAI-compatible
// (https://docs.tokenfactory.nebius.com/). The documented root carries a
// trailing slash; it is dropped here because the adapter appends the path.
func init() {
	factory.Default.Register("nebius",
		openai_compatible.CreateOpenAICompatibleFactory(
			"nebius",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
