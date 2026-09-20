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

func TestLoadBindsRedisSentinelSettings(t *testing.T) {
	t.Setenv("EVS_SERVICES_BILLING_REDIS_SENTINEL_MASTER_NAME", "everstack")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_SENTINEL_ADDRESSES", "redis-a:26379,redis-b:26379")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_PASSWORD", "billing-password")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_REQUIRED", "true")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_TLS_ENABLED", "true")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_TLS_CA_FILE", "/redis/ca.crt")
	t.Setenv("EVS_SERVICES_BILLING_REDIS_TLS_SERVER_NAME", "ha-data-redis")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_TYPE", "redis")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_SENTINEL_MASTER_NAME", "everstack")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_SENTINEL_ADDRESSES", "redis-a:26379,redis-b:26379")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_PASSWORD", "license-password")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_REQUIRED", "true")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_TLS_ENABLED", "true")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_TLS_CA_FILE", "/redis/ca.crt")
	t.Setenv("EVS_SERVICES_LICENSE_RATE_LIMIT_REDIS_TLS_SERVER_NAME", "ha-data-redis")

	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Services.Billing.Redis.SentinelMasterName; got != "everstack" {
		t.Fatalf("billing Sentinel master = %q", got)
	}
	if got := cfg.Services.Billing.Redis.SentinelAddresses; len(got) != 2 || got[1] != "redis-b:26379" {
		t.Fatalf("billing Sentinel addresses = %#v", got)
	}
	if got := cfg.Services.Billing.Redis.Password; got != "billing-password" {
		t.Fatalf("billing Redis password = %q", got)
	}
	if !cfg.Services.Billing.Redis.Required {
		t.Fatal("billing Redis should be required")
	}
	if !cfg.Services.Billing.Redis.TLSEnabled || cfg.Services.Billing.Redis.TLSCAFile != "/redis/ca.crt" || cfg.Services.Billing.Redis.TLSServerName != "ha-data-redis" {
		t.Fatalf("billing Redis TLS = %#v", cfg.Services.Billing.Redis)
	}
	if got := cfg.Services.License.RateLimit.Type; got != "redis" {
		t.Fatalf("license rate-limit type = %q", got)
	}
	if got := cfg.Services.License.RateLimit.Redis.SentinelAddresses; len(got) != 2 || got[0] != "redis-a:26379" {
		t.Fatalf("license Sentinel addresses = %#v", got)
	}
	if got := cfg.Services.License.RateLimit.Redis.Password; got != "license-password" {
		t.Fatalf("license Redis password = %q", got)
	}
	if !cfg.Services.License.RateLimit.Redis.Required {
		t.Fatal("license Redis should be required")
	}
	if !cfg.Services.License.RateLimit.Redis.TLSEnabled || cfg.Services.License.RateLimit.Redis.TLSCAFile != "/redis/ca.crt" || cfg.Services.License.RateLimit.Redis.TLSServerName != "ha-data-redis" {
		t.Fatalf("license Redis TLS = %#v", cfg.Services.License.RateLimit.Redis)
	}
}
