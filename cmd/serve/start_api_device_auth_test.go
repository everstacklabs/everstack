package serve

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
)

func TestNewCLIDeviceTokenManagerPrefersM2MKey(t *testing.T) {
	m2mKey := []byte(strings.Repeat("m", 32))
	sessionKey := strings.Repeat("s", 32)
	t.Setenv("EVS_M2M_SIGNING_KEY", base64.StdEncoding.EncodeToString(m2mKey))

	manager := newCLIDeviceTokenManager(&validator.Config{
		Auth: &validator.AuthConfig{
			Mode: "builtin",
			Builtin: validator.AuthBuiltinConfig{
				SessionSecret: sessionKey,
			},
		},
	}, false)
	if manager == nil {
		t.Fatal("newCLIDeviceTokenManager() = nil, want configured verifier")
	}

	m2mIssuer, err := deviceauth.NewTokenManager(m2mKey, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(m2m) error = %v", err)
	}
	token, err := m2mIssuer.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := manager.Verify(token); err != nil {
		t.Fatalf("gateway verifier rejected token signed from M2M key: %v", err)
	}

	sessionIssuer, err := deviceauth.NewTokenManager([]byte(sessionKey), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(session) error = %v", err)
	}
	sessionToken, err := sessionIssuer.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue(session) error = %v", err)
	}
	if _, err := manager.Verify(sessionToken); err == nil {
		t.Fatal("gateway verifier unexpectedly accepted the session-secret token")
	}
}

func TestNewCLIDeviceTokenManagerFallsBackToBuiltinSessionSecret(t *testing.T) {
	t.Setenv("EVS_M2M_SIGNING_KEY", "")
	secret := strings.Repeat("a", 32)
	manager := newCLIDeviceTokenManager(&validator.Config{
		Auth: &validator.AuthConfig{
			Mode: "builtin",
			Builtin: validator.AuthBuiltinConfig{
				SessionSecret: secret,
			},
		},
	}, false)
	if manager == nil {
		t.Fatal("newCLIDeviceTokenManager() = nil, want configured verifier")
	}
	issuer, err := deviceauth.NewTokenManager([]byte(secret), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := issuer.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := manager.Verify(token); err != nil {
		t.Fatalf("gateway verifier rejected builtin token: %v", err)
	}
}

func TestNewCLIDeviceTokenManagerRejectsShortBuiltinSecret(t *testing.T) {
	t.Setenv("EVS_M2M_SIGNING_KEY", "")
	manager := newCLIDeviceTokenManager(&validator.Config{
		Auth: &validator.AuthConfig{
			Mode: "builtin",
			Builtin: validator.AuthBuiltinConfig{
				SessionSecret: "short",
			},
		},
	}, false)
	if manager != nil {
		t.Fatal("newCLIDeviceTokenManager() accepted a short signing secret")
	}
}
