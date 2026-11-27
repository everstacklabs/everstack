package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/gorilla/mux"
)

// Each of these used to log a warning and start the gateway with no
// authentication at all. The operator asked for authentication, so the
// gateway must refuse to start instead.
func TestAuthModeThatCannotAuthenticateRefusesToStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		mode             string
		injectedProvider bool
		primaryDB        bool
		want             error
	}{
		{name: "oidc is not implemented", mode: "oidc", primaryDB: true, want: errOIDCAuthNotImplemented},
		{name: "oidc without issuer", mode: "oidc", want: errOIDCAuthNotImplemented},
		{name: "oidc any case", mode: " OIDC ", primaryDB: true, want: errOIDCAuthNotImplemented},
		{name: "builtin without a database", mode: "builtin", want: errBuiltinAuthNeedsDatabase},
	}
	for _, tt := range tests {
		err := authModeStartupError(tt.mode, tt.injectedProvider, tt.primaryDB)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: authModeStartupError(%q) = %v, want %v", tt.name, tt.mode, err, tt.want)
		}
	}
	if msg := errOIDCAuthNotImplemented.Error(); msg != "oidc auth mode is not implemented yet; use builtin or none" {
		t.Errorf("oidc message = %q", msg)
	}
	if msg := errBuiltinAuthNeedsDatabase.Error(); !strings.Contains(msg, "database") || !strings.Contains(msg, "none") {
		t.Errorf("builtin message %q should name the missing database and the explicit alternative", msg)
	}
}

func TestAuthModesThatCanStart(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		mode             string
		injectedProvider bool
		primaryDB        bool
	}{
		{mode: "none"},
		{mode: ""},
		{mode: "builtin", primaryDB: true},
		// The cloud control plane injects its own provider and needs no
		// gateway database for it.
		{mode: "builtin", injectedProvider: true},
	} {
		if err := authModeStartupError(tt.mode, tt.injectedProvider, tt.primaryDB); err != nil {
			t.Errorf("authModeStartupError(%q, injected=%v, db=%v) = %v, want nil", tt.mode, tt.injectedProvider, tt.primaryDB, err)
		}
	}
}

// startAPI rejects oidc before it opens databases or registers anything.
func TestStartAPIRefusesOIDCAuthMode(t *testing.T) {
	cfg := &validator.Config{Auth: &validator.AuthConfig{Mode: "oidc", OIDC: validator.AuthOIDCConfig{IssuerURL: "https://idp.example.test", ClientID: "everstack"}}}
	_, err := startAPI(context.Background(), cfg, "", nil, mux.NewRouter(), &GatewayAPIRuntime{})
	if !errors.Is(err, errOIDCAuthNotImplemented) {
		t.Fatalf("startAPI(auth.mode=oidc) error = %v, want %v", err, errOIDCAuthNotImplemented)
	}
}
