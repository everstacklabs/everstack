package meta

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestMetaDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://api.meta.ai/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestMetaRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "meta", "muse-spark-1.3")
}

func TestMetaSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "meta", "muse-spark-1.3", "/v1")
}
