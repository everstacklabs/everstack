package nebius

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestNebiusDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://api.tokenfactory.nebius.com/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestNebiusRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "nebius", "moonshotai/Kimi-K3.1-Pro")
}

func TestNebiusSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "nebius", "moonshotai/Kimi-K3.1-Pro", "/v1")
}
