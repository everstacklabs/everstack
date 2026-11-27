package novita

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestNovitaDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://api.novita.ai/openai/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestNovitaRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "novita", "zai-org/glm-5.3")
}

func TestNovitaSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "novita", "zai-org/glm-5.3", "/openai/v1")
}
