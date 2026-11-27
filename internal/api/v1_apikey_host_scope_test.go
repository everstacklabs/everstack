package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
)

// A live key presented on another tenant's hostname gets exactly the
// OpenAI-shaped answer an unknown key gets, on /v1 and on /openai/v1, for the
// bearer form an SDK sends and for the native api-key header.

func (h *harness) doOnHost(t *testing.T, srv http.Handler, path string, hdr map[string]string, hostInstance string) *httptest.ResponseRecorder {
	t.Helper()
	*h.seen = gatewaySeen{}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5.5","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	bus := query.NewQueryBus()
	bus.RegisterHandler(h.store)
	ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), testHashSecret)
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
	if hostInstance != "" {
		ctx = contextkeys.WithHostSelectedTenant(ctx, contextkeys.RequestInstanceScope{InstanceID: hostInstance, OrganizationID: "org-of-" + hostInstance})
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestV1KeyOnAnotherTenantsHostIsRefusedLikeAnUnknownKey(t *testing.T) {
	const otherInstance = "inst-other"
	for _, tc := range []struct {
		name string
		path string
		srv  func(*harness) http.Handler
		hdr  func(key string) map[string]string
	}{
		{"/v1 bearer", "/v1/chat/completions", func(h *harness) http.Handler { return h.v1 }, bearer},
		{"/openai/v1 bearer", "/openai/v1/chat/completions", func(h *harness) http.Handler { return h.alias }, bearer},
		{"/v1 api-key header", "/v1/chat/completions", func(h *harness) http.Handler { return h.v1 },
			func(key string) map[string]string { return map[string]string{"x-evs-api-key": key} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			unknown := h.doOnHost(t, tc.srv(h), tc.path, tc.hdr("sk-mf-svcacct-api01-neverissued"), otherInstance)
			if unknown.Code != http.StatusUnauthorized {
				t.Fatalf("unknown key: status %d", unknown.Code)
			}

			for attempt := 0; attempt < 2; attempt++ { // cold, then cached
				rec := h.doOnHost(t, tc.srv(h), tc.path, tc.hdr(liveKey), otherInstance)
				if h.seen.called {
					t.Fatalf("attempt %d: key reached the gateway on another tenant's host", attempt)
				}
				if rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() ||
					rec.Header().Get("Content-Type") != unknown.Header().Get("Content-Type") {
					t.Fatalf("attempt %d: got %d %q %s, unknown key gets %d %q %s", attempt,
						rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(),
						unknown.Code, unknown.Header().Get("Content-Type"), unknown.Body.String())
				}
			}

			rec := h.doOnHost(t, tc.srv(h), tc.path, tc.hdr(liveKey), liveInstanceID)
			if rec.Code != http.StatusOK || !h.seen.called || h.seen.tenantID != liveInstanceID {
				t.Fatalf("own host: status %d, reached %t, tenant %q: %s", rec.Code, h.seen.called, h.seen.tenantID, rec.Body.String())
			}
		})
	}
}
