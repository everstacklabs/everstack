package v1

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestResolveAPIKeyContextKeepsBearerOrganizationSeparateFromInstanceTenant(t *testing.T) {
	manager, err := deviceauth.NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		InstanceID:     "instance-a",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	ctx := contextkeys.WithUserID(context.Background(), "user-1")
	ctx = contextkeys.WithTenantID(ctx, "instance-a")

	got, err := NewCLIServer(nil, manager).resolveAPIKeyContext(ctx, header)
	if err != nil {
		t.Fatalf("resolveAPIKeyContext() error = %v", err)
	}
	if got.UserID != "user-1" || got.OrgID != "org-1" {
		t.Fatalf("resolveAPIKeyContext() = %#v, want user-1/org-1", got)
	}
}

func TestResolveJWTContextRejectsTokenSignedByDifferentKey(t *testing.T) {
	t.Parallel()

	trusted, err := deviceauth.NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(trusted) error = %v", err)
	}
	untrusted, err := deviceauth.NewTokenManager([]byte(strings.Repeat("b", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(untrusted) error = %v", err)
	}
	token, err := untrusted.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	server := NewCLIServer(nil, trusted)
	if _, err := server.resolveJWTContext(context.Background(), token); err == nil {
		t.Fatal("resolveJWTContext() accepted a token signed by an untrusted key")
	}
}

func TestResolveJWTContextRejectsPKCETokenForDifferentInstance(t *testing.T) {
	t.Parallel()

	manager, err := deviceauth.NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		InstanceID:     "instance-a",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	ctx := contextkeys.WithTenantID(context.Background(), "instance-b")
	if _, err := NewCLIServer(nil, manager).resolveJWTContext(ctx, token); err == nil {
		t.Fatal("resolveJWTContext() accepted an instance-a token on instance-b")
	}
}

func TestResolveJWTContextAcceptsPKCETokenForCurrentInstance(t *testing.T) {
	t.Parallel()

	manager, err := deviceauth.NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		InstanceID:     "instance-a",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	ctx := contextkeys.WithTenantID(context.Background(), "instance-a")
	if _, err := NewCLIServer(nil, manager).resolveJWTContext(ctx, token); err != nil {
		t.Fatalf("resolveJWTContext() error = %v", err)
	}
}

func TestResolveJWTContextAcceptsTrustedDeviceToken(t *testing.T) {
	t.Parallel()

	manager, err := deviceauth.NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := manager.Issue(deviceauth.Identity{
		UserID:           "user-1",
		OrganizationID:   "org-1",
		OrganizationSlug: "example-org",
		ClientID:         "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	got, err := NewCLIServer(nil, manager).resolveJWTContext(context.Background(), token)
	if err != nil {
		t.Fatalf("resolveJWTContext() error = %v", err)
	}
	if got.UserID != "user-1" || got.OrgID != "org-1" {
		t.Fatalf("resolveJWTContext() = %#v, want user-1/org-1", got)
	}
}
