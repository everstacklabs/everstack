package huggingface

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("huggingface",
		openai_compatible.CreateOpenAICompatibleFactory(
			"huggingface",
			"https://router.huggingface.co/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
