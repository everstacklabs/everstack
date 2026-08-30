package voyage

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// Voyage AI serves retrieval embeddings from an OpenAI-compatible /embeddings
// endpoint, so the shared client covers it.
//
// Voyage's rerankers (rerank-2.5, rerank-2.5-lite) are deliberately absent
// from the catalog: the shared OpenAI-compatible client implements Chat and
// Embed but not gateway.RerankProvider, so a reranker listed here would
// resolve to a provider that cannot serve it. Adding them needs a dedicated
// adapter.
func init() {
	factory.Default.Register("voyage",
		openai_compatible.CreateOpenAICompatibleFactory(
			"voyage",
			"https://api.voyageai.com/v1",
			"Authorization",
			"Bearer {api_key}",
		),
	)
}
