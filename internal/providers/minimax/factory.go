package minimax

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("minimax",
		openai_compatible.CreateOpenAICompatibleFactory(
			"minimax",
			"https://api.minimax.io/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
