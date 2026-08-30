package moonshot

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("moonshot",
		openai_compatible.CreateOpenAICompatibleFactory(
			"moonshot",
			"https://api.moonshot.ai/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
