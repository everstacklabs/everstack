package middleware

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
)

// Forged M2M identity/signature headers must not stand in for credentials on
// StoreUpgradeCallbackSecret: it registers the nonce ActivationCallback
// accepts before rebinding the gateway's license.
func TestStoreUpgradeCallbackSecretRejectsForgedM2MHeaders(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())
	const proc = "/everstack.gateway.v1.GatewayService/StoreUpgradeCallbackSecret"

	headers := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	for name, hdr := range map[string]http.Header{
		"service token":        headers(HeaderServiceToken, "forged", HeaderSignature, "forged"),
		"instance id":          headers(HeaderInstanceID, "forged", HeaderSignature, "forged"),
		"legacy service token": headers(legacyHeaderServiceToken, "forged", legacyHeaderSignature, "forged"),
		"no headers":           {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := interceptor.validateAPIKey(context.Background(), hdr, proc, "corr-test")
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("want Unauthenticated, got %v", err)
			}
		})
	}
}
