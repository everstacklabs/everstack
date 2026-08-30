package perplexity

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("perplexity",
		openai_compatible.CreateOpenAICompatibleFactory(
			"perplexity",
			"https://api.perplexity.ai",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
