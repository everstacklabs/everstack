package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
)

func TestServeSubdomainSignedPreviewStripsTokenBeforeProxy(t *testing.T) {
	signer := newTestPreviewSigner(t)
	token := signTestPreviewToken(t, signer, previewtoken.Claims{
		SandboxID: "sbx-a",
		Subdomain: "abc-3000",
		TenantID:  "tenant-a",
		Port:      3000,
	})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get(previewtoken.QueryParam); got != "" {
			http.Error(w, "preview token leaked upstream", http.StatusTeapot)
			return
		}
		if got := r.URL.Query().Get("keep"); got != "1" {
			http.Error(w, "query parameter was not preserved", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	h.SetPreviewSigner(signer)

	q := url.Values{}
	q.Set("keep", "1")
	q.Set(previewtoken.QueryParam, token)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app?"+q.Encode(), nil)
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != previewtoken.CookiePrefix+"abc-3000" || cookies[0].Value != token {
		t.Fatalf("expected signed preview cookie, got %+v", cookies)
	}
}

func TestServeSubdomainSignedPreviewCookieIsVerified(t *testing.T) {
	signer := newTestPreviewSigner(t)
	token := signTestPreviewToken(t, signer, previewtoken.Claims{
		SandboxID: "sbx-a",
		Subdomain: "abc-3000",
		TenantID:  "tenant-a",
		Port:      3000,
	})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	h.SetPreviewSigner(signer)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app", nil)
	req.AddCookie(&http.Cookie{Name: previewtoken.CookiePrefix + "abc-3000", Value: token})
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeSubdomainRequirePreviewTokenRejectsUnsigned(t *testing.T) {
	h := newTestProxyHandler(t, &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: "127.0.0.1:1",
		Status:        "active",
	})
	h.SetPreviewSigner(newTestPreviewSigner(t))
	h.SetRequirePreviewToken(true)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app", nil)
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestServeSubdomainRequirePreviewTokenAcceptsSigned(t *testing.T) {
	signer := newTestPreviewSigner(t)
	token := signTestPreviewToken(t, signer, previewtoken.Claims{
		SandboxID: "sbx-a",
		Subdomain: "abc-3000",
		TenantID:  "tenant-a",
		Port:      3000,
	})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	h.SetPreviewSigner(signer)
	h.SetRequirePreviewToken(true)
	q := url.Values{}
	q.Set(previewtoken.QueryParam, token)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app?"+q.Encode(), nil)
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeSubdomainRequirePreviewTokenFailsClosedWithoutSigner(t *testing.T) {
	h := newTestProxyHandler(t, &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: "127.0.0.1:1",
		Status:        "active",
	})
	h.SetRequirePreviewToken(true)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app", nil)
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestServeSubdomainSignedPreviewRejectsScopeMismatch(t *testing.T) {
	signer := newTestPreviewSigner(t)
	token := signTestPreviewToken(t, signer, previewtoken.Claims{
		SandboxID: "sbx-other",
		Subdomain: "abc-3000",
		TenantID:  "tenant-a",
		Port:      3000,
	})
	h := newTestProxyHandler(t, &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: "127.0.0.1:1",
		Status:        "active",
	})
	h.SetPreviewSigner(signer)

	q := url.Values{}
	q.Set(previewtoken.QueryParam, token)
	req := httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/app?"+q.Encode(), nil)
	rec := httptest.NewRecorder()

	h.ServeSubdomain(rec, req, "abc-3000")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServeByIDLocalPathSetsSignedRouteCookieAndFallbackVerifiesIt(t *testing.T) {
	signer := newTestPreviewSigner(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app" && r.URL.Path != "/asset.js" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	h.SetPreviewSigner(signer)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8443/_sandbox/sbx-a/port/3000/app", nil)
	rec := httptest.NewRecorder()

	h.ServeByID(rec, req, "sbx-a", "3000", "/app")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != routeCookieName {
		t.Fatalf("expected signed route cookie, got %+v", cookies)
	}
	claims, err := signer.Verify(cookies[0].Value)
	if err != nil {
		t.Fatalf("route cookie should be signed: %v", err)
	}
	if claims.SandboxID != "sbx-a" || claims.Subdomain != "abc-3000" || claims.TenantID != "tenant-a" || claims.Port != 3000 {
		t.Fatalf("unexpected route cookie claims: %+v", claims)
	}

	fallbackReq := httptest.NewRequest(http.MethodGet, "http://localhost:8443/asset.js", nil)
	fallbackReq.AddCookie(cookies[0])
	fallbackRec := httptest.NewRecorder()
	if !h.ServeCookieFallback(fallbackRec, fallbackReq) {
		t.Fatal("expected cookie fallback to route signed cookie")
	}
	if fallbackRec.Code != http.StatusNoContent {
		t.Fatalf("fallback status = %d, body=%s", fallbackRec.Code, fallbackRec.Body.String())
	}
}

func TestServeCookieFallbackRejectsForgedRouteCookieWhenSignerConfigured(t *testing.T) {
	h := newTestProxyHandler(t, &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: "127.0.0.1:1",
		Status:        "active",
	})
	h.SetPreviewSigner(newTestPreviewSigner(t))
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8443/asset.js", nil)
	req.AddCookie(&http.Cookie{Name: routeCookieName, Value: "sbx-a:3000"})
	rec := httptest.NewRecorder()

	if h.ServeCookieFallback(rec, req) {
		t.Fatal("forged legacy route cookie should not route when signer is configured")
	}
}

func TestServeByIDNonLocalPathRequiresSignedRouteCookie(t *testing.T) {
	h := newTestProxyHandler(t, &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: "127.0.0.1:1",
		Status:        "active",
	})
	h.SetPreviewSigner(newTestPreviewSigner(t))
	req := httptest.NewRequest(http.MethodGet, "https://preview.evs.run/_sandbox/sbx-a/port/3000/app", nil)
	rec := httptest.NewRecorder()

	h.ServeByID(rec, req, "sbx-a", "3000", "/app")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServeRefererFallbackRequiresSignedRouteCookieWhenSignerConfigured(t *testing.T) {
	signer := newTestPreviewSigner(t)
	token := signTestPreviewToken(t, signer, previewtoken.Claims{
		SandboxID: "sbx-a",
		Subdomain: "abc-3000",
		TenantID:  "tenant-a",
		Port:      3000,
	})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	h.SetPreviewSigner(signer)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8443/asset.js", nil)
	req.Header.Set("Referer", "http://localhost:8443/_sandbox/sbx-a/port/3000/app")
	rec := httptest.NewRecorder()
	if h.ServeRefererFallback(rec, req) {
		t.Fatal("referer fallback should not route without signed route cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost:8443/asset.js", nil)
	req.Header.Set("Referer", "http://localhost:8443/_sandbox/sbx-a/port/3000/app")
	req.AddCookie(&http.Cookie{Name: routeCookieName, Value: token})
	rec = httptest.NewRecorder()
	if !h.ServeRefererFallback(rec, req) {
		t.Fatal("referer fallback should route with signed route cookie")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

type fakePreviewLookup struct {
	mapping *sandbox.PortMapping
}

func (f *fakePreviewLookup) LookupPortMapping(_ context.Context, subdomain string) (*sandbox.PortMapping, error) {
	if f.mapping != nil && f.mapping.Subdomain == subdomain {
		return f.mapping, nil
	}
	return nil, fmt.Errorf("port mapping not found")
}

func (f *fakePreviewLookup) LookupPortMappingByID(_ context.Context, sandboxID string, port int) (*sandbox.PortMapping, error) {
	if f.mapping != nil && f.mapping.SandboxID == sandboxID && f.mapping.Port == port {
		return f.mapping, nil
	}
	return nil, fmt.Errorf("port mapping not found")
}

func newTestProxyHandler(t *testing.T, mapping *sandbox.PortMapping) *ProxyHandler {
	t.Helper()
	transport, err := NewTransportPool(MTLSConfig{})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	t.Cleanup(transport.Close)
	return NewProxyHandler(&fakePreviewLookup{mapping: mapping}, transport)
}

func testPreviewMapping(backendURL string) *sandbox.PortMapping {
	return &sandbox.PortMapping{
		SandboxID:     "sbx-a",
		TenantID:      "tenant-a",
		Port:          3000,
		Subdomain:     "abc-3000",
		BackendTarget: strings.TrimPrefix(backendURL, "http://"),
		Status:        "active",
	}
}

func newTestPreviewSigner(t *testing.T) *previewtoken.Signer {
	t.Helper()
	signer, err := previewtoken.NewSigner([]byte("test-preview-secret-32-bytes-long"))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

func signTestPreviewToken(t *testing.T, signer *previewtoken.Signer, claims previewtoken.Claims) string {
	t.Helper()
	token, err := signer.Sign(claims, time.Hour)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}
