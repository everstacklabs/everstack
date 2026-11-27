package meta

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://api.meta.ai/v1"

// Meta's Model API serves the Muse family. Its Chat Completions endpoint is
// OpenAI-compatible (https://dev.meta.ai/docs/protocols/chat-completions) but
// rejects stop, n > 1 and logprobs, and does not support frequency_penalty or
// presence_penalty.
func init() {
	factory.Default.Register("meta",
		openai_compatible.CreateOpenAICompatibleFactory(
			"meta",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
