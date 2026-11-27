package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	httputil "github.com/everstacklabs/everstack/internal/api/http"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// The gateway holds no key that could verify an x-evs-service-token /
// x-evs-signature request: those credentials belong to the license service's
// M2M validator. So on the gateway these headers must never authenticate,
// whether forged or produced by the real signing client.

const (
	procGetGateway          = "/everstack.gateway.v1.GatewayService/GetGateway"
	procListResponses       = "/everstack.gateway.v1.GatewayService/ListResponses"
	procGetUpgradeCallback  = "/everstack.gateway.v1.GatewayService/GetUpgradeCallbackSecret"
	procActivationCallback  = "/everstack.gateway.v1.GatewayService/ActivationCallback"
	procSubscriptionCallbck = "/everstack.gateway.v1.GatewayService/SubscriptionStatusCallback"
)

type captureTransport struct{ hdr http.Header }

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.hdr = req.Header.Clone()
	return nil, errors.New("captured")
}

// signedHeaders returns the headers the real billing callback client
// (httputil.ServiceM2MHTTPClient) attaches to a call to proc.
func signedHeaders(t *testing.T, proc string) http.Header {
	t.Helper()
	capture := &captureTransport{}
	transport := httputil.NewServiceM2MTransport(capture, "billing", "svc-token", []byte("0123456789abcdef0123456789abcdef"))
	req, err := http.NewRequest(http.MethodPost, "https://tenant.example.com"+proc, strings.NewReader(`{"id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = transport.RoundTrip(req)
	if capture.hdr.Get(HeaderSignature) == "" || capture.hdr.Get(HeaderServiceToken) == "" {
		t.Fatalf("real client did not emit M2M headers: %v", capture.hdr)
	}
	return capture.hdr
}

func forgedHeaders() http.Header {
	h := http.Header{}
	h.Set(HeaderServiceToken, "forged")
	h.Set(HeaderSignature, "forged")
	return h
}

func requireUnauthenticated(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("request authenticated, want CodeUnauthenticated")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want %v (err=%v)", got, connect.CodeUnauthenticated, err)
	}
}

func TestValidateAPIKey_ForgedM2MHeadersDoNotAuthenticate(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())
	for _, proc := range []string{procGetGateway, procListResponses} {
		t.Run(proc, func(t *testing.T) {
			_, err := interceptor.validateAPIKey(context.Background(), forgedHeaders(), proc, "corr-test")
			requireUnauthenticated(t, err)
		})
	}
}

func TestValidateAPIKey_LegacyForgedM2MHeadersDoNotAuthenticate(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())
	h := http.Header{}
	h.Set(legacyHeaderServiceToken, "forged")
	h.Set(legacyHeaderSignature, "forged")
	h.Set(HeaderInstanceID, "forged")
	_, err := interceptor.validateAPIKey(context.Background(), h, procGetGateway, "corr-test")
	requireUnauthenticated(t, err)
}

// A request signed by the real client is still not a gateway credential: the
// gateway has no key for it. Fresh, stale and body-tampered variants are all
// rejected. (The client signs SHA256(empty), not the body, so body tampering
// would not be detectable by any verifier of this scheme either.)
func TestValidateAPIKey_RealSignedM2MRequestDoesNotAuthenticate(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())

	t.Run("fresh", func(t *testing.T) {
		_, err := interceptor.validateAPIKey(context.Background(), signedHeaders(t, procGetGateway), procGetGateway, "corr-test")
		requireUnauthenticated(t, err)
	})
	t.Run("stale timestamp", func(t *testing.T) {
		h := signedHeaders(t, procListResponses)
		stale := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
		h.Set(HeaderTimestamp, stale)
		h.Set(legacyHeaderTimestamp, stale)
		_, err := interceptor.validateAPIKey(context.Background(), h, procListResponses, "corr-test")
		requireUnauthenticated(t, err)
	})
	t.Run("body tampered after signing", func(t *testing.T) {
		// Headers are body-independent, so replaying them with any body is
		// the same request as far as the interceptor can see.
		_, err := interceptor.validateAPIKey(context.Background(), signedHeaders(t, procListResponses), procListResponses, "corr-test")
		requireUnauthenticated(t, err)
	})
}

// A valid API key keeps authenticating, with or without stray M2M headers.
func TestValidateAPIKey_APIKeyUnaffectedByM2MChange(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())
	const key = "gateway-api-key"
	hash, ok := apikeylib.HashWithSecret(key, "test-hash-secret")
	if !ok {
		t.Fatal("failed to hash test API key")
	}
	interceptor.cache.Store(hash, cacheEntry{valid: true, orgID: "tenant-1", expiresAt: time.Now().Add(time.Minute)})

	for name, h := range map[string]http.Header{"no m2m headers": {}, "forged m2m headers": forgedHeaders()} {
		t.Run(name, func(t *testing.T) {
			h.Set(common.EverstackApiKey, key)
			ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), "test-hash-secret")
			ctx, err := interceptor.validateAPIKey(ctx, h, procGetGateway, "corr-test")
			if err != nil {
				t.Fatalf("validateAPIKey() error = %v", err)
			}
			if got := contextkeys.GetTenantID(ctx); got != "tenant-1" {
				t.Fatalf("tenant = %q, want tenant-1 (must come from the key, not headers)", got)
			}
		})
	}
}

// The procedures billing/license call on the gateway authenticate themselves
// (Ed25519-signed JWT payloads, or possession of the callback secret), so the
// API-key interceptor must let them through on policy, not on header presence.
func TestValidateAPIKey_SelfAuthenticatingCallbacksBypassAPIKey(t *testing.T) {
	interceptor := NewAPIKeyInterceptorWithPolicy(false, apilic.NewDefaultPolicy())
	for _, proc := range []string{procGetUpgradeCallback, procActivationCallback, procSubscriptionCallbck} {
		t.Run(proc, func(t *testing.T) {
			if _, err := interceptor.validateAPIKey(context.Background(), http.Header{}, proc, "corr-test"); err != nil {
				t.Fatalf("without M2M headers: %v", err)
			}
			if _, err := interceptor.validateAPIKey(context.Background(), signedHeaders(t, proc), proc, "corr-test"); err != nil {
				t.Fatalf("with real signed M2M headers: %v", err)
			}
		})
	}
}
