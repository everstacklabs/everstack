package chutes

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestChutesDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://llm.chutes.ai/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestChutesRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "chutes", "zai-org/GLM-5.3")
}

func TestChutesSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "chutes", "zai-org/GLM-5.3", "/v1")
}
