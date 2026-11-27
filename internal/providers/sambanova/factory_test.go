package sambanova

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestSambaNovaDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://api.sambanova.ai/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestSambaNovaRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "sambanova", "GLM-5.3")
}

func TestSambaNovaSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "sambanova", "GLM-5.3", "/v1")
}
