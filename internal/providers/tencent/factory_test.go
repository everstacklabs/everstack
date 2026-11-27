package tencent

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/providertest"
)

// The documented root is pinned so a typo or a dropped path segment fails here
// rather than as a 404 on a customer's first request.
func TestTencentDefaultBaseURL(t *testing.T) {
	t.Parallel()

	if DefaultBaseURL != "https://tokenhub-intl.tencentcloudmaas.com/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestTencentRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	providertest.AssertRegistered(t, "tencent", "hy4")
}

func TestTencentSendsChatAndStreamWithBearerKey(t *testing.T) {
	t.Parallel()

	providertest.AssertBearerChatAndStream(t, "tencent", "hy4", "/v1")
}
