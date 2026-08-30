package cerebras

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("cerebras",
		openai_compatible.CreateOpenAICompatibleFactory(
			"cerebras",
			"https://api.cerebras.ai/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
