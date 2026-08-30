package nvidia_nim

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

func init() {
	factory.Default.Register("nvidia-nim",
		openai_compatible.CreateOpenAICompatibleFactory(
			"nvidia-nim",
			"https://integrate.api.nvidia.com/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
