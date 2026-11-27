package tencent

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// DefaultBaseURL is the documented OpenAI-compatible root; the adapter appends
// /chat/completions to it.
const DefaultBaseURL = "https://tokenhub-intl.tencentcloudmaas.com/v1"

// Tencent TokenHub serves the Hy (Hunyuan) models and resold third-party
// models over an OpenAI-compatible API. The default is the Singapore (global)
// host; the Guangzhou and Silicon Valley hosts are documented at
// https://www.tencentcloud.com/document/product/1300/80632 and are reached by
// overriding base_url. A model must be activated in the console first, or the
// upstream answers HTTP 402.
func init() {
	factory.Default.Register("tencent",
		openai_compatible.CreateOpenAICompatibleFactory(
			"tencent",
			DefaultBaseURL,
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
