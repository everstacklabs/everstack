package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/pkg/grpc/everstack/billing/v1/billingconnect"
)

func TestBillingServiceBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://everstack-services:8092/api/billing/usage/sandbox-snapshot": "http://everstack-services:8092",
		"https://billing.internal/api/billing/usage/snapshot":               "https://billing.internal",
		"http://svc/prefix/api/billing/usage/sandbox-snapshot":              "http://svc/prefix",
		"http://svc/other":    "",
		"":                    "",
		"/api/billing/usage/": "",
	} {
		if got := billingServiceBaseURL(in); got != want {
			t.Errorf("billingServiceBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The registrar posts RegisterApiKey to billing's Connect path on the host
// the usage URL names.
func TestBillingKeyRegistrarCallsRegisterApiKey(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg, err := newBillingKeyRegistrar(srv.URL+"/api/billing/usage/sandbox-snapshot", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterAPIKey(context.Background(), "org-a", "8d7c6b5a-4321-4abc-9def-0123456789ab"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if gotPath != billingconnect.BillingServiceRegisterApiKeyProcedure {
		t.Fatalf("path = %q, want %q", gotPath, billingconnect.BillingServiceRegisterApiKeyProcedure)
	}
	if err := reg.UnregisterAPIKey(context.Background(), "org-a", "8d7c6b5a-4321-4abc-9def-0123456789ab"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if gotPath != billingconnect.BillingServiceUnregisterApiKeyProcedure {
		t.Fatalf("path = %q, want %q", gotPath, billingconnect.BillingServiceUnregisterApiKeyProcedure)
	}
	if _, err := newBillingKeyRegistrar("http://svc/other", srv.Client()); err == nil {
		t.Fatal("unrecognised usage URL: want error")
	}
	if _, err := newBillingKeyRegistrar(srv.URL+"/api/billing/usage/snapshot", nil); err == nil {
		t.Fatal("no M2M client: want error")
	}
}
