package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/internalauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestGatewayMetadataCarriesOnlySignedVerifiedSurfaces(t *testing.T) {
	for _, surfaces := range [][]string{nil, {}, {"mcp"}, {"inference", "mcp", "telemetry", "a2a"}} {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.Header.Set(internalauth.GatewayIdentityHeader, "client-forgery")
		plain := gatewayRequestMetadata(req.Context(), req)
		if len(plain.Get(internalauth.GatewayIdentityHeader)) != 0 {
			t.Fatal("client header forwarded")
		}
		ctx := contextkeys.WithTenantID(req.Context(), "tenant")
		if len(gatewayRequestMetadata(ctx, req.WithContext(ctx)).Get(internalauth.GatewayIdentityHeader)) != 0 {
			t.Fatal("host selection signed")
		}
		ctx = contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant", "digest", "key-id"), surfaces)
		md := gatewayRequestMetadata(ctx, req.WithContext(ctx))
		h := http.Header{}
		for name, values := range md {
			for _, value := range values {
				h.Add(name, value)
			}
		}
		identity, ok := internalauth.GatewayKeyIdentity(h)
		if !ok || identity.TenantID != "tenant" || identity.KeyID != "key-id" || (identity.Surfaces == nil) != (surfaces == nil) || len(identity.Surfaces) != len(surfaces) {
			t.Fatal("verified identity did not survive hop")
		}
		if internalauth.IsInternalHeader(h) {
			t.Fatal("identity granted process-wide internal bypass")
		}
		// Changing mcp to unrestricted NULL without the process secret must fail.
		parts := strings.Split(h.Get(internalauth.GatewayIdentityHeader), ".")
		identity.Surfaces = nil
		identity.TenantID = "victim"
		data, _ := json.Marshal(identity)
		parts[0] = base64.RawURLEncoding.EncodeToString(data)
		h.Set(internalauth.GatewayIdentityHeader, strings.Join(parts, "."))
		if _, ok := internalauth.GatewayKeyIdentity(h); ok {
			t.Fatal("tampered forwarding marker verified")
		}
	}
}

func TestGatewayEdgeStripsClientEnvelopeBeforeMiddleware(t *testing.T) {
	for _, prefixed := range []bool{false, true} {
		request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		name := internalauth.GatewayIdentityHeader
		if prefixed {
			name = "Grpc-Metadata-" + name
		}
		request.Header.Set(name, internalauth.SignGatewayIdentity(internalauth.GatewayIdentity{TenantID: "forged-tenant", Hash: "digest"}))
		request.Header.Set(internalauth.GatewayTransportHeader, "client-supplied")
		request.Header.Set("Grpc-Metadata-"+internalauth.GatewayTransportHeader, "client-supplied")
		called := false
		StripClientGatewayIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Header.Get(internalauth.GatewayIdentityHeader) != "" || r.Header.Get("Grpc-Metadata-"+internalauth.GatewayIdentityHeader) != "" || r.Header.Get(internalauth.GatewayTransportHeader) != "" || r.Header.Get("Grpc-Metadata-"+internalauth.GatewayTransportHeader) != "" {
				t.Fatal("client envelope reached middleware")
			}
			md := gatewayRequestMetadata(r.Context(), r)
			if len(md.Get(internalauth.GatewayIdentityHeader)) != 0 {
				t.Fatal("client identity forwarded")
			}
		})).ServeHTTP(httptest.NewRecorder(), request)
		if !called {
			t.Fatal("next handler not reached")
		}
	}
}
func TestGatewayEdgePreservesOnlyOwnedLoopbackEnvelope(t *testing.T) {
	request := httptest.NewRequest("POST", "/everstack.gateway.v1.GatewayService/ListModels", nil)
	request.Header.Set(internalauth.GatewayIdentityHeader, internalauth.SignGatewayIdentity(internalauth.GatewayIdentity{TenantID: "tenant", Hash: "digest", Surfaces: []string{"inference"}}))
	internalauth.SetGatewayTransportHeader(request.Header)
	request.Header.Set("Grpc-Metadata-"+internalauth.GatewayIdentityHeader, "client-copy")
	handler := StripClientGatewayIdentity(StripClientGatewayIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := internalauth.GatewayKeyIdentity(r.Header); !ok {
			t.Fatal("owned envelope removed")
		}
		if r.Header.Get("Grpc-Metadata-"+internalauth.GatewayIdentityHeader) != "" {
			t.Fatal("prefixed copy survived")
		}
		if internalauth.IsInternalHeader(r.Header) {
			t.Fatal("transport nonce granted general internal authentication")
		}
	})))
	handler.ServeHTTP(httptest.NewRecorder(), request)
}
