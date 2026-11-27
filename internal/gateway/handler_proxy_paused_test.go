package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProxyRefusesPausedTenant(t *testing.T) {
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	h := newTestProxyHandler(t, testPreviewMapping(backend.URL))
	var asked atomic.Value
	h.SetTenantActiveFn(func(_ context.Context, tenantID string) bool {
		asked.Store(tenantID)
		return tenantID != "tenant-a"
	})

	rec := httptest.NewRecorder()
	h.ServeSubdomain(rec, httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/", nil), "abc-3000")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("backend was called %d times for a paused tenant", hits.Load())
	}
	if got, _ := asked.Load().(string); got != "tenant-a" {
		t.Fatalf("gate asked about %q, want tenant-a", got)
	}
	var body struct {
		Error struct {
			Type, Code, Message string
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if body.Error.Type != "instance_paused" || body.Error.Code != "instance_paused" || body.Error.Message != "This instance is paused." {
		t.Fatalf("body = %+v", body)
	}

	// Resumed (or gate cleared): traffic flows again.
	h.SetTenantActiveFn(nil)
	rec = httptest.NewRecorder()
	h.ServeSubdomain(rec, httptest.NewRequest(http.MethodGet, "http://abc-3000.preview.test/", nil), "abc-3000")
	if rec.Code != http.StatusNoContent || hits.Load() != 1 {
		t.Fatalf("active tenant: status = %d hits = %d", rec.Code, hits.Load())
	}
}
