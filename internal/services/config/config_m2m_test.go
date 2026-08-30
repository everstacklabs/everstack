package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBindsLicenseOIDCClientCredentials(t *testing.T) {
	t.Setenv("EVS_M2M_OIDC_LICENSE_CLIENT_ID", "license-client")
	t.Setenv("EVS_M2M_OIDC_LICENSE_CLIENT_SECRET", "license-secret")

	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte(`
security:
  m2m:
    enabled: true
    provider: oidc
    oidc_clients:
      license:
        client_id: ${EVS_M2M_OIDC_LICENSE_CLIENT_ID}
        client_secret: ${EVS_M2M_OIDC_LICENSE_CLIENT_SECRET}
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	license := cfg.Security.M2M.OIDCClients["license"]
	if license.ClientID != "license-client" || license.ClientSecret != "license-secret" {
		t.Fatalf("license OIDC credentials = %#v", license)
	}
}

func TestLoadBindsLicenseBillingServiceURL(t *testing.T) {
	t.Setenv("EVS_SERVICES_LICENSE_BILLING_SERVICE_URL", "https://billing.internal")

	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte("services:\n  license:\n    billing_service_url: \"\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got, want := cfg.Services.License.BillingServiceURL, "https://billing.internal"; got != want {
		t.Fatalf("billing service URL = %q, want %q", got, want)
	}
}
