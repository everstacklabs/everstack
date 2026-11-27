package config

import (
	"os"
	"path/filepath"
	"testing"
)

// services/config/config.yaml ships secure: false for local HTTP development.
// Deployments turn it on with EVS_SERVICES_AUTH_SESSION_SECURE, so that
// variable has to win over the file, and has to work even if a trimmed
// config leaves the key out entirely.
func TestLoadBindsAuthSessionSecure(t *testing.T) {
	for name, body := range map[string]string{
		"file says false": "services:\n  auth:\n    session:\n      cookie_name: es_everstack_session\n      secure: false\n",
		"key absent":      "services:\n  auth:\n    session:\n      cookie_name: es_everstack_session\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("EVS_SERVICES_AUTH_SESSION_SECURE", "true")
			path := filepath.Join(t.TempDir(), "services.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if !cfg.Services.Auth.Session.Secure {
				t.Fatalf("EVS_SERVICES_AUTH_SESSION_SECURE=true did not set services.auth.session.secure")
			}
		})
	}
}
