package together

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("together",
		openai_compatible.CreateOpenAICompatibleFactory(
			"together",
			"https://api.together.xyz/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
