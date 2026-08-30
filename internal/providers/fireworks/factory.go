package fireworks

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("fireworks",
		openai_compatible.CreateOpenAICompatibleFactory(
			"fireworks",
			"https://api.fireworks.ai/inference/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
