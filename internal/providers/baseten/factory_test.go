package baseten

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestBasetenDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://inference.baseten.co/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestBasetenRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "baseten", "zai-org/GLM-5.3")
}

func TestBasetenSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "baseten", "zai-org/GLM-5.3", "/v1")
}
