package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestMountGRPCGatewayPrefixesRoutesOutsideV1(t *testing.T) {
	router := mux.NewRouter()
	api := &API{
		router: router,
		grpcGateway: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/model-metrics/v1/report" {
				t.Fatalf("gateway received path %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	api.MountGRPCGatewayPrefixes("/api/model-metrics/v1")

	req := httptest.NewRequest(http.MethodGet, "/api/model-metrics/v1/report", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestMountGRPCGatewayPrefixesSkipsMissingGateway(t *testing.T) {
	api := &API{router: mux.NewRouter()}
	api.MountGRPCGatewayPrefixes("/api/model-metrics/v1")
}
