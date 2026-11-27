package sambanova

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://api.sambanova.ai/v1"

// SambaNova Cloud is OpenAI-compatible
// (https://docs.sambanova.ai/docs/en/get-started/api-keys-urls.md). It rejects
// request fields it does not know with HTTP 400 rather than ignoring them, and
// accepts only 0 for frequency_penalty and presence_penalty.
func init() {
	factory.Default.Register("sambanova",
		openai_compatible.CreateOpenAICompatibleFactory(
			"sambanova",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
