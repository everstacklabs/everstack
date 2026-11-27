package requesthost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Connect handlers only see request headers, and net/http moves Host out of
// the header map, so without this the private-host cookie relaxation could
// never fire on Connect login paths.
func TestMiddlewareCarriesHostToHandlers(t *testing.T) {
	var got *http.Request
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = Request(r.Context(), r.Header)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://100.64.0.1:8089/everstack.auth.v1.AuthService/Login", nil)
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-Forwarded-Proto", "https")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.Host != "100.64.0.1:8089" {
		t.Fatalf("Host = %q", got.Host)
	}
	if got.Header.Get("X-Forwarded-Proto") != "https" || got.Header.Get("Cookie") != "a=b" {
		t.Fatalf("headers not carried: %v", got.Header)
	}
}

// A client cannot choose the Host its own context records by sending a
// header; only the real request Host is used.
func TestRequestWithoutMiddlewareHasNoHost(t *testing.T) {
	header := http.Header{}
	header.Set("Host", "localhost")
	if got := Request(context.Background(), header); got.Host != "" {
		t.Fatalf("Host = %q, want empty without the middleware", got.Host)
	}
}

func TestRequestClonesHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-A", "1")
	got := Request(context.Background(), header)
	got.Header.Set("X-A", "2")
	if header.Get("X-A") != "1" {
		t.Fatal("Request must not alias the caller's header map")
	}
}
