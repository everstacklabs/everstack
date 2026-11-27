package middleware

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func personCtx(role string) context.Context {
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	ctx = contextkeys.WithCloudUserID(ctx, "user-1")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	if role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	return ctx
}

func TestOrgAdminWriteAllowed(t *testing.T) {
	gated := []string{
		"/everstack.api_key.v1.ApiKeyService/CreateApiKey",
		"/everstack.api_key.v1.ApiKeyService/DeleteApiKey",
		"/everstack.providers.v1.ProvidersService/ConfigureProvider",
		"/everstack.providers.v1.ProvidersService/ToggleProvider",
		"/everstack.providers.v1.ProvidersService/DeleteProviderConfiguration",
		"/everstack.memory.v1.MemoryService/SetupPgVector",
		"/everstack.providers.v1.ProvidersService/AddProviderAPIKey",
		"/everstack.providers.v1.ProvidersService/UpdateAPIKeyWeight",
		"/everstack.providers.v1.ProvidersService/ToggleAPIKey",
		"/everstack.providers.v1.ProvidersService/DeleteProviderAPIKey",
		"/everstack.providers.v1.ProvidersService/SaveConfigYAML",
		"/everstack.providers.v1.ProvidersService/ReloadConfig",
	}
	denied := func(err error) bool { return connect.CodeOf(err) == connect.CodePermissionDenied }

	for _, proc := range gated {
		for _, role := range []string{"owner", "admin"} {
			if err := orgAdminWriteAllowed(personCtx(role), proc); err != nil {
				t.Errorf("%s: %s should pass, got %v", proc, role, err)
			}
		}
		for _, role := range []string{"member", "viewer", "Admin", "billing"} {
			if err := orgAdminWriteAllowed(personCtx(role), proc); !denied(err) {
				t.Errorf("%s: %s must be refused, got %v", proc, role, err)
			}
		}
		// An API key carries no role and is not a person.
		keyCtx := contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "hash", "key-1")
		if err := orgAdminWriteAllowed(keyCtx, proc); err != nil {
			t.Errorf("%s: API key callers must be unaffected, got %v", proc, err)
		}
		// A context with no principal at all is some other layer's problem;
		// this check must not turn into an authentication decision.
		if err := orgAdminWriteAllowed(context.Background(), proc); err != nil {
			t.Errorf("%s: unauthenticated context must pass through, got %v", proc, err)
		}
	}

	t.Run("unrecorded role: refused on managed, allowed on self-hosted", func(t *testing.T) {
		const proc = "/everstack.api_key.v1.ApiKeyService/CreateApiKey"
		if err := orgAdminWriteAllowed(personCtx(""), proc); err != nil {
			t.Fatalf("self-hosted member with no recorded role must keep access, got %v", err)
		}
		enterprise.SetManagedGateway(true)
		t.Cleanup(func() { enterprise.SetManagedGateway(false) })
		if err := orgAdminWriteAllowed(personCtx(""), proc); !denied(err) {
			t.Fatalf("managed member with no recorded role must be refused, got %v", err)
		}
	})

	t.Run("reads and other writes are not gated", func(t *testing.T) {
		for _, proc := range []string{
			"/everstack.api_key.v1.ApiKeyService/ListApiKeys",
			"/everstack.providers.v1.ProvidersService/ListConfiguredProviders",
			"/everstack.providers.v1.ProvidersService/ListProviderAPIKeys",
			"/everstack.providers.v1.ProvidersService/GetConfigYAML",
			"/everstack.memory.v1.MemoryService/CreateCollection",
			"/everstack.agents.v1.AgentsService/CreateAgent",
		} {
			if err := orgAdminWriteAllowed(personCtx("member"), proc); err != nil {
				t.Errorf("%s must not be role gated, got %v", proc, err)
			}
		}
	})
}
