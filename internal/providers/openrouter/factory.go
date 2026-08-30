package openrouter

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("openrouter",
		openai_compatible.CreateOpenAICompatibleFactory(
			"openrouter",
			"https://openrouter.ai/api/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
