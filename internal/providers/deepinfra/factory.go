package deepinfra

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://api.deepinfra.com/v1/openai"

// DeepInfra serves open-weight models behind an OpenAI-compatible API
// (https://docs.deepinfra.com/chat/overview). Model ids are repository-style,
// e.g. "deepseek-ai/DeepSeek-V4-Pro".
func init() {
	factory.Default.Register("deepinfra",
		openai_compatible.CreateOpenAICompatibleFactory(
			"deepinfra",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
