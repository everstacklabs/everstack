package serve

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	billingv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/billing/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/billing/v1/billingconnect"
)

// billingKeyRegistrar reports each gateway API key a managed gateway creates
// to billing (BillingService.RegisterApiKey), so a spend limit can be set on
// a key before it has made a request (docs/design/spend-limits-v2.md 4.5).
// It rides the same M2M client and billing host as the usage reporters;
// billing admits the call only from the gateway's own client.
type billingKeyRegistrar struct {
	client billingconnect.BillingServiceClient
}

func (r *billingKeyRegistrar) RegisterAPIKey(ctx context.Context, tenantID, apiKeyID string) error {
	_, err := r.client.RegisterApiKey(ctx, connect.NewRequest(&billingv1.RegisterApiKeyRequest{
		TenantId: tenantID,
		ApiKeyId: apiKeyID,
	}))
	return err
}

// UnregisterAPIKey tells billing a key this gateway created was deleted, so
// its ownership record goes. Billing removes only the row owned by the
// organization tenantID resolves to.
func (r *billingKeyRegistrar) UnregisterAPIKey(ctx context.Context, tenantID, apiKeyID string) error {
	_, err := r.client.UnregisterApiKey(ctx, connect.NewRequest(&billingv1.UnregisterApiKeyRequest{
		TenantId: tenantID,
		ApiKeyId: apiKeyID,
	}))
	return err
}

// billingServiceBaseURL derives billing's base URL from the configured usage
// endpoint (".../api/billing/usage/sandbox-snapshot" or a sibling), which is
// served by the same billing HTTP server as its Connect procedures. Any other
// shape yields "" rather than a guess.
func billingServiceBaseURL(usageURL string) string {
	usageURL = strings.TrimSpace(usageURL)
	const marker = "/api/billing/"
	i := strings.Index(usageURL, marker)
	if i <= 0 {
		return ""
	}
	return usageURL[:i]
}

// newBillingKeyRegistrar builds the registrar from the managed billing usage
// URL and an M2M-authenticated client. A nil registrar and an error mean key
// registration is off; billing then accepts key limits only for keys with
// traffic.
func newBillingKeyRegistrar(usageURL string, httpClient *http.Client) (*billingKeyRegistrar, error) {
	base := billingServiceBaseURL(usageURL)
	if base == "" {
		return nil, fmt.Errorf("billing usage URL %q has no billing base", usageURL)
	}
	if httpClient == nil {
		return nil, fmt.Errorf("no M2M client for billing")
	}
	return &billingKeyRegistrar{client: billingconnect.NewBillingServiceClient(httpClient, base)}, nil
}
