package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	keyserver "github.com/everstacklabs/everstack/internal/api/grpc/api_key/v1"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"github.com/gorilla/mux"
)

func TestDirectConnectEdgeStripsClientIdentityInEmbeddedGateway(t *testing.T) {
	api := &API{router: mux.NewRouter(), baseCtx: context.Background(), connectServices: map[string][]string{}}
	api.registerConnectServer(keyserver.CreateServer())
	procedure := "/everstack.api_key.v1.ApiKeyService/ListApiKeys"
	envelope := internalauth.SignGatewayIdentity(internalauth.GatewayIdentity{TenantID: "tenant", Hash: "digest", Surfaces: []string{"mcp"}})
	for _, prefixed := range []bool{false, true} {
		r := httptest.NewRequest("POST", procedure, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		name := internalauth.GatewayIdentityHeader
		if prefixed {
			name = "Grpc-Metadata-" + name
		}
		r.Header.Set(name, envelope)
		w := httptest.NewRecorder()
		api.router.ServeHTTP(w, r)
		if w.Code != 401 || !strings.Contains(w.Body.String(), "unauthenticated") {
			t.Fatalf("client identity reached interceptor: %d %s", w.Code, w.Body.String())
		}
	}
	// The gateway-owned hop still reaches the exact surface check, not an
	// unrestricted internal bypass. The MCP-only key cannot list API keys.
	r := httptest.NewRequest("POST", procedure, strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(internalauth.GatewayIdentityHeader, envelope)
	internalauth.SetGatewayTransportHeader(r.Header)
	w := httptest.NewRecorder()
	api.router.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "this API key cannot call this API") {
		t.Fatalf("owned hop lost surface check: %d %s", w.Code, w.Body.String())
	}
}
