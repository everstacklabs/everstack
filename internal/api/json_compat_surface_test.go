package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONConnectCompatibilityRequiresExactProcedure(t *testing.T) {
	for _, tc := range []struct {
		path   string
		mapped bool
	}{
		{"/everstack.gateway.v1.GatewayService/ChatCompletion", true},
		{"/everstack.gateway.v1.GatewayService/Embeddings", true},
		{"/everstack.gateway.v1.GatewayService/Anything/ChatCompletion", false},
		{"/everstack.gateway.v1.GatewayService/XEmbeddings", false},
		{"/everstack.gateway.v1.GatewayService/ChatCompletion/", false},
		{"/everstack.agents.v1.AgentsService/ChatCompletion", false},
	} {
		mapped := false
		handler := jsonCompatForConnectPaths(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mapped = true; w.WriteHeader(204) }))
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if mapped != tc.mapped {
			t.Fatalf("%s mapped=%v", tc.path, mapped)
		}
	}
}
