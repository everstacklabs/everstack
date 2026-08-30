package xai

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("xai",
		openai_compatible.CreateOpenAICompatibleFactory(
			"xai",
			"https://api.x.ai/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
