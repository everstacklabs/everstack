package deepinfra

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestDeepInfraDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://api.deepinfra.com/v1/openai" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestDeepInfraRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "deepinfra", "deepseek-ai/DeepSeek-V4-Pro")
}

func TestDeepInfraSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "deepinfra", "deepseek-ai/DeepSeek-V4-Pro", "/v1/openai")
}
