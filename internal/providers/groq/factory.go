package groq

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("groq",
		openai_compatible.CreateOpenAICompatibleFactory(
			"groq",
			"https://api.groq.com/openai/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
